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
	mapsStorage := b.topManager.params.mapsStorage
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

func (b *RouteMgrImpl) onUpsertedHTTPRoute(origin maps.ResourceOrigin, routeValueName string, route *tree.HTTPRoute,
	mapExact, mapPrefix, mapRegex *maps.MapFileState,
) error {
	if route.Valid {
		return b.onValidHTTPRouteUpserted(origin, routeValueName, route, mapExact, mapPrefix, mapRegex)
	}
	return b.onInvalidHTTPRouteUpserted(origin, route, mapExact, mapPrefix, mapRegex)
}

func (b *RouteMgrImpl) onUpsertedTLSRoute(routeOrigin maps.ResourceOrigin, routeValueName string, route *tree.TLSRoute,
	mapSNI *maps.MapFileState,
) error {
	if route.Valid {
		return b.onValidTLSRouteUpserted(routeOrigin, routeValueName, route, mapSNI)
	}
	return b.onInvalidTLSRouteUpserted(routeOrigin, route, mapSNI)
}

func (b *RouteMgrImpl) onValidTLSRouteUpserted(routeOrigin maps.ResourceOrigin, routeValueName string,
	tlsRoute *tree.TLSRoute, mapSNI *maps.MapFileState,
) error {
	desiredBackends := map[maps.EntryKey]map[string]*maps.WeightedValue{}
	entryKey := maps.EntryKey{Hostname: routeValueName}
	backends := map[string]*maps.WeightedValue{}

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
			backends[backendName] = &maps.WeightedValue{ValueName: backendName, Weight: backend.Weight}
		}
	}

	if len(backends) > 0 {
		desiredBackends[entryKey] = backends
	}

	mapSNI.ApplyRoute(routeOrigin, desiredBackends)
	return nil
}

func (RouteMgrImpl) onInvalidTLSRouteUpserted(routeOrigin maps.ResourceOrigin, _ *tree.TLSRoute, mapSNI *maps.MapFileState) error {
	mapSNI.ApplyRoute(routeOrigin, map[maps.EntryKey]map[string]*maps.WeightedValue{})
	return nil
}

func (b *RouteMgrImpl) onDeletedTLSRoute(routeOrigin maps.ResourceOrigin, route *tree.TLSRoute,
	mapSNI *maps.MapFileState,
) error {
	return b.onInvalidTLSRouteUpserted(routeOrigin, route, mapSNI)
}

func (b *RouteMgrImpl) onDeletedHTTPRoute(origin maps.ResourceOrigin, route *tree.HTTPRoute,
	mapExact, mapPrefix, mapRegex *maps.MapFileState,
) error {
	return b.onInvalidHTTPRouteUpserted(origin, route, mapExact, mapPrefix, mapRegex)
}

func (b *RouteMgrImpl) onValidHTTPRouteUpserted(origin maps.ResourceOrigin, routeValueName string, route *tree.HTTPRoute,
	mapExact, mapPrefix, mapRegex *maps.MapFileState,
) error {
	desired := newDesiredBackendsMaps()

	for _, rule := range route.Rules {
		// Rules with a RequestRedirect filter map directly to a redirect pseudo-backend.
		// Handle this before the rule.Valid check since redirect rules may have no
		// backendRefs (which causes rule.Valid to be false).
		if hasRedirectFilter(rule.K8sResource.Filters) {
			// Skip rules that have incompatible filter combinations (e.g. URLRewrite + RequestRedirect).
			// checkFilters() will have set Valid=false and generated an IncompatibleFilters condition.
			if !rule.CheckFilters.Valid {
				continue
			}
			redirectBeName := b.topManager.getRedirectBackendName(rule.K8sResource.Filters)
			for _, match := range rule.K8sResource.Matches {
				bucket, _ := desired.resolveEntry(routeValueName, match)
				bucket[redirectBeName] = &maps.WeightedValue{ValueName: redirectBeName}
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
			for _, match := range rule.K8sResource.Matches {
				bucket, _ := desired.resolveEntry(routeValueName, match)
				existing := bucket[backendName]
				if existing == nil {
					bucket[backendName] = &maps.WeightedValue{
						ValueName: backendName,
						Weight:    backend.Weight,
					}
					continue
				}
				newWeight := utils.PointerDefaultValueIfNil(existing.Weight) +
					utils.PointerDefaultValueIfNil(backend.Weight)
				existing.Weight = &newWeight
			}
		}
	}

	mapExact.ApplyRoute(origin, desired.exact)
	mapPrefix.ApplyRoute(origin, desired.prefix)
	mapRegex.ApplyRoute(origin, desired.regex)
	return nil
}

// desiredBackendsMaps groups the three (hostname, path) → backends maps that
// correspond to the HAProxy path-match map files (exact, prefix, regex).
// Wildcard-host matching is handled upstream by the listener/listener-route
// map pipeline, not by a dedicated bucket here.
type desiredBackendsMaps struct {
	exact  map[maps.EntryKey]map[string]*maps.WeightedValue
	prefix map[maps.EntryKey]map[string]*maps.WeightedValue
	regex  map[maps.EntryKey]map[string]*maps.WeightedValue
}

func newDesiredBackendsMaps() desiredBackendsMaps {
	return desiredBackendsMaps{
		exact:  map[maps.EntryKey]map[string]*maps.WeightedValue{},
		prefix: map[maps.EntryKey]map[string]*maps.WeightedValue{},
		regex:  map[maps.EntryKey]map[string]*maps.WeightedValue{},
	}
}

// resolveEntry selects the correct bucket map and entry key for the given
// hostname and HTTPRouteMatch, normalising hostname and path according to
// the path-type routing rules.  The inner map is lazily initialised so the
// caller can write to the returned map directly.
func (d *desiredBackendsMaps) resolveEntry(hostname string, match gatewayv1.HTTPRouteMatch) (map[string]*maps.WeightedValue, maps.EntryKey) {
	path := "/"
	if match.Path != nil && match.Path.Value != nil {
		path = *match.Path.Value
	}
	pathType := gatewayv1.PathMatchPathPrefix
	if match.Path != nil && match.Path.Type != nil {
		pathType = *match.Path.Type
	}
	originalHostname := hostname
	hostname = sanitizeHostname(hostname, pathType)
	selected := d.exact
	switch pathType {
	case gatewayv1.PathMatchExact:
		// Wildcard-host + exact-path is routed via the listener/listener-route
		// map pipeline upstream; the path bucket remains d.exact here.
	case gatewayv1.PathMatchPathPrefix:
		if isDomainWildcard(originalHostname) {
			selected = d.regex
			path += ".*"
			path = sanitizeRegexp(path)
			hostname = sanitizeRegexp(hostname)
		} else {
			selected = d.prefix
		}
	case gatewayv1.PathMatchRegularExpression:
		selected = d.regex
		path = strings.TrimPrefix(path, "^")
		path = sanitizeRegexp(path)
		hostname = sanitizeRegexp(hostname)
	}
	key := maps.EntryKey{Hostname: hostname, Path: path}
	if selected[key] == nil {
		selected[key] = map[string]*maps.WeightedValue{}
	}
	return selected[key], key
}

func (RouteMgrImpl) onInvalidHTTPRouteUpserted(origin maps.ResourceOrigin, _ *tree.HTTPRoute,
	mapExact, mapPrefix, mapRegex *maps.MapFileState,
) error {
	empty := map[maps.EntryKey]map[string]*maps.WeightedValue{}
	mapExact.ApplyRoute(origin, empty)
	mapPrefix.ApplyRoute(origin, empty)
	mapRegex.ApplyRoute(origin, empty)
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
