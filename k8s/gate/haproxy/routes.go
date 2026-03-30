// Copyright 2025 HAProxy Technologies LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package haproxy

import (
	"context"
	"log/slog"
	"regexp"
	"strings"

	"github.com/haproxytech/haproxy-unified-gateway/hug/reload"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/storage/maps"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	k8stypes "k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func (b *RouteMgrImpl) processRoutes() error {
	// var err error

	b.fillMaps()
	var errs utils.Errors
	errs.Add(b.writeMaps())

	if !b.topManager.params.RuntimeUpdateHaproxy {
		return nil
	}
	err := b.runtimeMapSync()
	if err != nil {
		reload.Instance().SetReload("runtime map update failed")
		errs.Add(err)
	}
	b.ResetMapFiles()
	return errs.Result()
}

func (b *RouteMgrImpl) ResetMapFiles() {
	mapsStorage := b.topManager.params.mapsStorageEx
	for _, mapDir := range mapsStorage.GetMaps() {
		if mapDir == nil {
			continue
		}
		for _, mapFile := range mapDir {
			mapFile.Reset()
		}
	}
}

type RouteMgrImpl struct {
	topManager *HaproxyConfMgrImpl
}

func (b *RouteMgrImpl) onUpsertedHTTPRoute(routeKey k8stypes.NamespacedName, route *tree.HTTPRoute,
	mapExact, mapPrefix, mapRegex, mapDomainWPathExact *maps.MapFileState, acceptedHostnamesForRoute []string,
) error {
	if route.Valid {
		return b.onValidHTTPRouteUpserted(routeKey, route, mapExact, mapPrefix, mapRegex, mapDomainWPathExact, acceptedHostnamesForRoute)
	}
	return b.onInvalidHTTPRouteUpserted(routeKey, route, mapExact, mapPrefix, mapRegex, mapDomainWPathExact)
}

func (b *RouteMgrImpl) onUpsertedTLSRoute(routeKey k8stypes.NamespacedName, route *tree.TLSRoute,
	mapSNI, mapSNIDomainWildcardMap *maps.MapFileState, acceptedHostnamesForRoute []string,
) error {
	if route.Valid {
		return b.onValidTLSRouteUpserted(routeKey, route, mapSNI, mapSNIDomainWildcardMap, acceptedHostnamesForRoute)
	}
	return b.onInvalidTLSRouteUpserted(routeKey, route, mapSNI, mapSNIDomainWildcardMap)
}

func (b *RouteMgrImpl) onValidTLSRouteUpserted(namespacedName k8stypes.NamespacedName,
	tlsRoute *tree.TLSRoute, mapSNI, mapSNIDomainWildcardMap *maps.MapFileState, acceptedHostnamesForRoute []string,
) error {
	desiredBackendsBySNI := map[maps.EntryKey]map[string]*maps.WeightedBackend{}
	desiredBackendsBySNIWildcard := map[maps.EntryKey]map[string]*maps.WeightedBackend{}

	for _, tlsRouteRule := range tlsRoute.Rules {
		if !tlsRouteRule.Valid {
			continue
		}
		for _, backend := range tlsRouteRule.K8sResource.BackendRefs {
			checkResult, ok := tlsRouteRule.CheckBackendRef.Get(backend.BackendObjectReference)
			if !ok || !checkResult.Valid {
				b.topManager.logger.LogAttrs(context.Background(), slog.LevelDebug, "Processing TLSRoute [map update] - backend not valid",
					logging.LogAttrBackendName(string(backend.Name)),
				)
				continue
			}

			// backend := rule.K8sResource.BackendRefs[index]
			svckey := k8stypes.NamespacedName{
				Name: string(backend.Name),
			}
			if backend.Namespace == nil {
				svckey.Namespace = tlsRoute.K8sResource.Namespace
			} else {
				svckey.Namespace = string(*backend.Namespace)
			}
			svcPort := int32(0)
			if backend.Port != nil {
				svcPort = int32(*backend.Port)
			}

			backendName, err := b.topManager.getBackendName(svckey, int32(svcPort), "")
			if err != nil {
				b.topManager.logger.LogAttrs(context.Background(), slog.LevelError, "Processing TLSRoute [map update]",
					logging.LogAttrError(err),
				)
				continue
			}
			// For each accepted SNI ...
			for _, hostname := range acceptedHostnamesForRoute {
				mapDesiredBackends := desiredBackendsBySNI
				if isDomainWildcard(string(hostname)) {
					mapDesiredBackends = desiredBackendsBySNIWildcard
				}
				entryKey := maps.EntryKey{
					Hostname: hostname,
				}
				desiredBackendsByPath := mapDesiredBackends[entryKey]
				if desiredBackendsByPath == nil {
					desiredBackendsByPath = map[string]*maps.WeightedBackend{}
					mapDesiredBackends[entryKey] = desiredBackendsByPath
				}
				// ... and we set the weighted backend
				desiredBackendsByPath[backendName] = &maps.WeightedBackend{
					BackendName: backendName,
					Weight:      backend.Weight,
				}
			}
		}
	}

	mapSNI.ApplyRoute(
		maps.ResourceOrigin{
			Namespace: namespacedName.Namespace,
			Name:      namespacedName.Name,
		},
		desiredBackendsBySNI)
	mapSNIDomainWildcardMap.ApplyRoute(
		maps.ResourceOrigin{
			Namespace: namespacedName.Namespace,
			Name:      namespacedName.Name,
		},
		desiredBackendsBySNIWildcard)
	return nil
}

func (RouteMgrImpl) onInvalidTLSRouteUpserted(namespacedName k8stypes.NamespacedName, _ *tree.TLSRoute, mapSNI, mapSNIDomainWildcardMap *maps.MapFileState) error {
	mapSNI.ApplyRoute(
		maps.ResourceOrigin{
			Namespace: namespacedName.Namespace,
			Name:      namespacedName.Name,
		},
		map[maps.EntryKey]map[string]*maps.WeightedBackend{})
	mapSNIDomainWildcardMap.ApplyRoute(
		maps.ResourceOrigin{
			Namespace: namespacedName.Namespace,
			Name:      namespacedName.Name,
		},
		map[maps.EntryKey]map[string]*maps.WeightedBackend{})
	return nil
}

func (b *RouteMgrImpl) onDeletedTLSRoute(namespacedName k8stypes.NamespacedName, route *tree.TLSRoute,
	mapSNI *maps.MapFileState, mapSNIDomainWildcard *maps.MapFileState,
) error {
	return b.onInvalidTLSRouteUpserted(namespacedName, route, mapSNI, mapSNIDomainWildcard)
}

func (b *RouteMgrImpl) onDeletedHTTPRoute(namespacedName k8stypes.NamespacedName, route *tree.HTTPRoute,
	mapExact, mapPrefix, mapRegex, mapDomainWPathExact *maps.MapFileState,
) error {
	return b.onInvalidHTTPRouteUpserted(namespacedName, route, mapExact, mapPrefix, mapRegex, mapDomainWPathExact)
}

func (b *RouteMgrImpl) onValidHTTPRouteUpserted(_ k8stypes.NamespacedName, route *tree.HTTPRoute,
	mapExact, mapPrefix, mapRegex, mapDomainWildcardPathExact *maps.MapFileState, acceptedHostnamesForRoute []string,
) error { //revive:disable:function-length,cognitive-complexity
	// Hostname + Path -> backend name -> weighted backend
	desiredBackendsPathExactByHostnameAndPath := map[maps.EntryKey]map[string]*maps.WeightedBackend{}
	desiredBackendsPathPrefixByHostnameAndPath := map[maps.EntryKey]map[string]*maps.WeightedBackend{}
	desiredBackendsRegexByHostnameAndPath := map[maps.EntryKey]map[string]*maps.WeightedBackend{}
	desiredBackendsDomainWildcardByHostnameAndPath := map[maps.EntryKey]map[string]*maps.WeightedBackend{}
	associatedMapFilesAndDesiredBackends := []struct {
		mapFile         *maps.MapFileState
		desiredBackends map[maps.EntryKey]map[string]*maps.WeightedBackend
	}{
		{
			mapFile:         mapExact,
			desiredBackends: desiredBackendsPathExactByHostnameAndPath,
		},
		{
			mapFile:         mapPrefix,
			desiredBackends: desiredBackendsPathPrefixByHostnameAndPath,
		},
		{
			mapFile:         mapRegex,
			desiredBackends: desiredBackendsRegexByHostnameAndPath,
		},
		{
			mapFile:         mapDomainWildcardPathExact,
			desiredBackends: desiredBackendsDomainWildcardByHostnameAndPath,
		},
	}

	for _, rule := range route.Rules {
		// Rules with a RequestRedirect filter map directly to a redirect pseudo-backend.
		// Handle this before the rule.Valid check since redirect rules may have no
		// backendRefs (which causes rule.Valid to be false).
		if hasRedirectFilter(rule.K8sResource.Filters) {
			redirectBeName := b.topManager.getRedirectBackendName(rule.K8sResource.Filters)
			for _, hostname := range acceptedHostnamesForRoute {
				for _, match := range rule.K8sResource.Matches {
					path := "/"
					if match.Path != nil && match.Path.Value != nil {
						path = *match.Path.Value
					}
					var pathType gatewayv1.PathMatchType
					if match.Path == nil || match.Path.Type == nil {
						pathType = gatewayv1.PathMatchPathPrefix
					} else {
						pathType = *match.Path.Type
					}
					originalHostname := hostname
					hostname = sanitizeHostname(hostname, pathType)
					mapDesiredBackends := desiredBackendsPathExactByHostnameAndPath
					switch pathType {
					case gatewayv1.PathMatchExact:
						if isDomainWildcard(string(originalHostname)) {
							mapDesiredBackends = desiredBackendsDomainWildcardByHostnameAndPath
						}
					case gatewayv1.PathMatchPathPrefix:
						if isDomainWildcard(originalHostname) {
							mapDesiredBackends = desiredBackendsRegexByHostnameAndPath
							path += ".*"
							path = sanitizeRegexp(path)
							hostname = sanitizeRegexp(hostname)
						} else {
							mapDesiredBackends = desiredBackendsPathPrefixByHostnameAndPath
						}
					case gatewayv1.PathMatchRegularExpression:
						mapDesiredBackends = desiredBackendsRegexByHostnameAndPath
						path = strings.TrimPrefix(path, "^")
						path = sanitizeRegexp(path)
						hostname = sanitizeRegexp(hostname)
					}
					entryKey := maps.EntryKey{Hostname: hostname, Path: path}
					desiredBackendsByPath := mapDesiredBackends[entryKey]
					if desiredBackendsByPath == nil {
						desiredBackendsByPath = map[string]*maps.WeightedBackend{}
						mapDesiredBackends[entryKey] = desiredBackendsByPath
					}
					desiredBackendsByPath[redirectBeName] = &maps.WeightedBackend{BackendName: redirectBeName}
				}
			}
			continue
		}

		if !rule.Valid {
			continue
		}

		for _, backend := range rule.K8sResource.BackendRefs {
			checkResult, ok := rule.CheckBackendRef.Get(backend.BackendObjectReference)
			if !ok || !checkResult.Valid {
				b.topManager.logger.LogAttrs(context.Background(), slog.LevelDebug, "Processing HTTPRoute [map update] - backend not valid",
					logging.LogAttrBackendName(string(backend.Name)),
				)
				continue
			}

			svckey := k8stypes.NamespacedName{
				Name: string(backend.Name),
			}
			if backend.Namespace == nil {
				svckey.Namespace = route.K8sResource.Namespace
			} else {
				svckey.Namespace = string(*backend.Namespace)
			}
			svcPort := int32(0)
			if backend.Port != nil {
				svcPort = int32(*backend.Port)
			}
			filterHash := getFilterHash(rule.K8sResource.Filters, backend.Filters)
			backendName, err := b.topManager.getBackendName(svckey, int32(svcPort), filterHash)
			if err != nil {
				b.topManager.logger.LogAttrs(context.Background(), slog.LevelError, "Processing HTTPRoute [map update]",
					logging.LogAttrError(err),
				)
				continue
			}
			// For each accepted hostname ...
			for _, hostname := range acceptedHostnamesForRoute {
				for _, match := range rule.K8sResource.Matches {
					// ... we collect each path ...
					path := "/"
					if match.Path.Value != nil {
						path = *match.Path.Value
					}
					var pathType gatewayv1.PathMatchType
					// ... and their path type ...
					if match.Path.Type == nil {
						pathType = gatewayv1.PathMatchPathPrefix
					} else {
						pathType = *match.Path.Type
					}
					mapDesiredBackends := desiredBackendsPathExactByHostnameAndPath
					originalHostname := hostname
					hostname = sanitizeHostname(hostname, pathType)
					switch pathType {
					case gatewayv1.PathMatchExact:
						if isDomainWildcard(string(originalHostname)) {
							mapDesiredBackends = desiredBackendsDomainWildcardByHostnameAndPath
						}
					case gatewayv1.PathMatchPathPrefix:
						if isDomainWildcard(originalHostname) {
							mapDesiredBackends = desiredBackendsRegexByHostnameAndPath
							path += ".*"
							path = sanitizeRegexp(path)
							hostname = sanitizeRegexp(hostname)
						} else {
							mapDesiredBackends = desiredBackendsPathPrefixByHostnameAndPath
						}
					case gatewayv1.PathMatchRegularExpression:
						mapDesiredBackends = desiredBackendsRegexByHostnameAndPath
						path = strings.TrimPrefix(path, "^")
						path = sanitizeRegexp(path)
						hostname = sanitizeRegexp(hostname)
					}

					entryKey := maps.EntryKey{
						Hostname: hostname,
						Path:     path,
					}
					desiredBackendsByPath := mapDesiredBackends[entryKey]
					if desiredBackendsByPath == nil {
						desiredBackendsByPath = map[string]*maps.WeightedBackend{}
						mapDesiredBackends[entryKey] = desiredBackendsByPath
					}
					// ... and we set the weighted backend
					existing := desiredBackendsByPath[backendName]
					if existing == nil {
						existing = &maps.WeightedBackend{
							BackendName: backendName,
							Weight:      backend.Weight,
						}
						desiredBackendsByPath[backendName] = existing
						continue
					}
					newWeight := utils.PointerDefaultValueIfNil(existing.Weight) +
						utils.PointerDefaultValueIfNil(backend.Weight)
					existing.Weight = &newWeight
				}
			}
		}
	}

	for _, associatedMapFileAndDesiredBackends := range associatedMapFilesAndDesiredBackends {
		associatedMapFileAndDesiredBackends.mapFile.ApplyRoute(
			maps.ResourceOrigin{
				Namespace: route.K8sResource.Namespace,
				Name:      route.K8sResource.Name,
			},
			associatedMapFileAndDesiredBackends.desiredBackends)
	}
	return nil
}

func (RouteMgrImpl) onInvalidHTTPRouteUpserted(namespacedName k8stypes.NamespacedName, _ *tree.HTTPRoute,
	mapExact, mapPrefix, mapRegex, mapDomainWildcardPathExact *maps.MapFileState,
) error {
	mapExact.ApplyRoute(
		maps.ResourceOrigin{
			Namespace: namespacedName.Namespace,
			Name:      namespacedName.Name,
		},
		map[maps.EntryKey]map[string]*maps.WeightedBackend{})
	mapPrefix.ApplyRoute(
		maps.ResourceOrigin{
			Namespace: namespacedName.Namespace,
			Name:      namespacedName.Name,
		},
		map[maps.EntryKey]map[string]*maps.WeightedBackend{})
	mapRegex.ApplyRoute(
		maps.ResourceOrigin{
			Namespace: namespacedName.Namespace,
			Name:      namespacedName.Name,
		},
		map[maps.EntryKey]map[string]*maps.WeightedBackend{})
	mapDomainWildcardPathExact.ApplyRoute(
		maps.ResourceOrigin{
			Namespace: namespacedName.Namespace,
			Name:      namespacedName.Name,
		},
		map[maps.EntryKey]map[string]*maps.WeightedBackend{})

	return nil
}

func sanitizeHostname(hostname string, pathType gatewayv1.PathMatchType) string {
	var result string
	switch pathType {
	case gatewayv1.PathMatchExact, gatewayv1.PathMatchPathPrefix:
		if isDomainWildcard(hostname) {
			result = removeDomainWildcard(hostname)
		} else {
			result = hostname
		}
	case gatewayv1.PathMatchRegularExpression:
		if !isDomainWildcard(hostname) {
			result = "^" + hostname
		} else {
			result = removeDomainWildcard(hostname)
		}
	}
	return result
}

func sanitizeRegexp(s string) string {
	// This regex finds either ".*" OR a "."
	re := regexp.MustCompile(`(\.\*)|(\.)`)

	result := re.ReplaceAllStringFunc(s, func(match string) string {
		// If the match is ".*", return it unchanged
		if match == ".*" {
			return match
		}
		// Otherwise, it's a single ".", so escape it
		return `\.`
	})
	return result
}
