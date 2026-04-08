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

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/storage/maps"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/metrics"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
)

var re = regexp.MustCompile(`\\+`)

// ErrMapRuntimeUpdate is an error type for runtime map update failures
type ErrMapRuntimeUpdate struct {
	Err     error
	Type    string
	MapName string
	Key     string
	Value   string
}

func (e *ErrMapRuntimeUpdate) Error() string {
	return "runtime " + e.Type + " map error: map '" + e.MapName + "', key '" + e.Key + "', value '" + e.Value + "': " + e.Err.Error()
}

func (b *RouteMgrImpl) fillMaps() {
	b.fillMapsForHTTPRoutes()
	b.fillMapsForTLSRoutes()
	mapsStorage := b.topManager.params.mapsStorageEx
	mapsStorage.ProcessMapFiles()
}

func (b *RouteMgrImpl) fillMapsForTLSRoutes() {
	var errs utils.Errors
	controllerStore := b.topManager.controllerStore
	mapsStorage := b.topManager.params.mapsStorageEx
	// Managed TLSRoutes => if there is a old resource, clean the state before update
	for routeKey, route := range controllerStore.GateTree.TLSRoutes {
		if route.TreeStatus.OldTreeResource == nil {
			continue
		}
		route = route.TreeStatus.OldTreeResource
		for _, listeners := range route.Listeners.Iterate {
			for _, listener := range listeners {
				if listener.VirtualListenerName == "" {
					continue
				}
				frontendName := b.topManager.getFrontendName(listener.VirtualListenerName)

				mapSNIMap := mapsStorage.GetSniMapFile(frontendName)
				mapSNIDomainWildcardMap := mapsStorage.GetSniDomainWildcardMapFile(frontendName)
				err := b.onDeletedTLSRoute(routeKey, route, mapSNIMap, mapSNIDomainWildcardMap)
				// errs.Add(err)
				_ = err // TODO ignore error for now
			}
		}
	}
	// Managed TLSRoutes => Create / update/ delete backends
	for routeKey, route := range controllerStore.GateTree.TLSRoutes {
		for _, listener := range route.Listeners.Iterate {
			for _, listener := range listener {
				frontendName := b.topManager.getFrontendName(listener.VirtualListenerName)

				routesHosnames := utils.ConvertSliceWithFunc(route.K8sResource.Spec.Hostnames, utils.ConvertV1Alpha2HostnameToString)
				listenerHostname := (*string)(listener.K8sResource.Hostname)
				acceptedHostnamesForRoute := utils.GetHostnamesForRouteWithListener(listenerHostname, routesHosnames)
				mapSNIMap := mapsStorage.GetSniMapFile(frontendName)
				mapSNIDomainWildcardMap := mapsStorage.GetSniDomainWildcardMapFile(frontendName)
				switch route.TreeStatus.Status {
				case store.StatusUnchanged:
					continue
				case store.StatusUpserted:
					err := b.onUpsertedTLSRoute(routeKey, route, mapSNIMap, mapSNIDomainWildcardMap, acceptedHostnamesForRoute)
					errs.Add(err)
				case store.StatusDeleted:
					err := b.onDeletedTLSRoute(routeKey, route, mapSNIMap, mapSNIDomainWildcardMap)
					errs.Add(err)
				}
			}
		}
	}

	if len(errs) > 0 {
		b.topManager.logger.LogAttrs(context.Background(), slog.LevelError, "Failed to fill maps for TLS routes",
			logging.LogAttrError(errs.Result()),
		)
	}
}

func (b *RouteMgrImpl) fillMapsForHTTPRoutes() {
	var errs utils.Errors
	controllerStore := b.topManager.controllerStore
	mapsStorage := b.topManager.params.mapsStorageEx
	// Managed HTTPRoutes => if there is a old resource, clean the state before update
	for routeKey, route := range controllerStore.GateTree.HTTPRoutes {
		if route.TreeStatus.OldTreeResource == nil {
			continue
		}
		route = route.TreeStatus.OldTreeResource
		for _, listeners := range route.Listeners.Iterate {
			for _, listener := range listeners {
				vListenerName := listener.VirtualListenerName
				if vListenerName == "" {
					continue
				}
				frontendName := b.topManager.getFrontendName(vListenerName)
				mapExact := mapsStorage.GetPathExactMapFile(frontendName)
				mapPrefix := mapsStorage.GetPathPrefixMapFile(frontendName)
				mapRegex := mapsStorage.GetPathRegexMapFile(frontendName)
				mapDomainWPathExact := mapsStorage.GetPathExactDomainWildcardMapFile(frontendName)
				err := b.onDeletedHTTPRoute(routeKey, route, mapExact, mapPrefix, mapRegex, mapDomainWPathExact)
				// errs.Add(err)
				_ = err // TODO ignore error for now
			}
		}
	}

	// Managed HTTPRoutes => Create / update/ delete backends
	for routeKey, route := range controllerStore.GateTree.HTTPRoutes {
		for _, listener := range route.Listeners.Iterate {
			for _, listener := range listener {
				frontendName := b.topManager.getFrontendName(listener.VirtualListenerName)

				routesHosnames := utils.ConvertSliceWithFunc(route.K8sResource.Spec.Hostnames, utils.ConvertV1Alpha2HostnameToString)
				listenerHostname := (*string)(listener.K8sResource.Hostname)
				acceptedHostnamesForRoute := utils.GetHostnamesForRouteWithListener(listenerHostname, routesHosnames)
				mapExact := mapsStorage.GetPathExactMapFile(frontendName)
				mapPrefix := mapsStorage.GetPathPrefixMapFile(frontendName)
				mapRegex := mapsStorage.GetPathRegexMapFile(frontendName)
				mapDomainWPathExact := mapsStorage.GetPathExactDomainWildcardMapFile(frontendName)

				switch route.TreeStatus.Status {
				case store.StatusUnchanged:
					continue
				case store.StatusUpserted:
					err := b.onUpsertedHTTPRoute(routeKey, route, mapExact, mapPrefix, mapRegex, mapDomainWPathExact, acceptedHostnamesForRoute)
					errs.Add(err)
				case store.StatusDeleted:
					err := b.onDeletedHTTPRoute(routeKey, route, mapExact, mapPrefix, mapRegex, mapDomainWPathExact)
					errs.Add(err)
				}
			}
		}
	}
	if len(errs) > 0 {
		b.topManager.logger.LogAttrs(context.Background(), slog.LevelError, "Failed to fill maps for HTTP routes",
			logging.LogAttrError(errs.Result()),
		)
	}
}

func (b *RouteMgrImpl) writeMaps() error {
	if !b.topManager.params.StoreMapsOnDisk {
		return nil
	}

	var errs utils.Errors
	mapsStorage := b.topManager.params.mapsStorageEx
	for _, mapDir := range mapsStorage.GetMaps() {
		if mapDir == nil {
			continue
		}
		for _, mapFile := range mapDir {
			if err := mapFile.WriteOnDiskIfChanged(); err != nil {
				metrics.MapStorageOperations.WithLabelValues("write", "error").Inc()
				errs.Add(err)
			} else {
				metrics.MapStorageOperations.WithLabelValues("write", "ok").Inc()
			}
		}
	}
	if len(errs) > 0 {
		b.topManager.logger.LogAttrs(context.Background(), slog.LevelError, "Failed to fill maps for HTTP routes",
			logging.LogAttrError(errs.Result()),
		)
	}
	return errs.Result()
}

// runtimeMapSync updates the runtime maps through runtime API
func (b *RouteMgrImpl) runtimeMapSync() error {
	mapsStorage := b.topManager.params.mapsStorageEx
	runtimeClient := b.topManager.haproxyClient.RuntimeClient()

	for _, mapFiles := range mapsStorage.GetMaps() {
		for _, mapData := range mapFiles {
			if len(mapData.Entries) == 0 {
				continue
			}
			mapID, err := b.getMapID(mapData.Path.FullPath())
			if err != nil {
				b.topManager.logger.LogAttrs(context.Background(), slog.LevelError,
					"map [runtime] show maps error",
					logging.LogAttrMapFilePath(mapData.Path.FileName),
					logging.LogAttrError(err),
				)
				return err
			}
			if mapID == "" {
				b.topManager.logger.LogAttrs(context.Background(), slog.LevelDebug, "map [runtime] skipping as mapID is empty", slog.String("map", mapData.Path.FileName))
				continue
			}
			for entryKey, entryValue := range mapData.Entries {
				key := escapeSlashForRuntime(entryKey.Hostname)
				if entryKey.Path != "" {
					key += entryKey.Path
				}
				routeValue := maps.BuildRouteValue(entryValue.DesiredValue)
				// if routeValue is empty, delete the entry
				if routeValue == "" {
					b.topManager.logger.LogAttrs(context.Background(), slog.LevelInfo, "Deleting map [runtime] entry", slog.String("map", mapData.Path.FileName), slog.String("key", key))
					err := runtimeClient.DeleteMapEntry(mapID, key)
					if err != nil {
						metrics.MapStorageOperations.WithLabelValues("runtime_delete", "error").Inc()
						b.topManager.logger.LogAttrs(context.Background(), slog.LevelError,
							"[failure] Deleting map [runtime] entry",
							logging.LogAttrMapFilePath(mapData.Path.FileName),
							slog.String("key", key),
							logging.LogAttrError(err),
						)
						return err
					}
					metrics.MapStorageOperations.WithLabelValues("runtime_delete", "ok").Inc()
					b.topManager.logger.LogAttrs(context.Background(), slog.LevelInfo,
						"[success] Deleting map [runtime] entry",
						logging.LogAttrMapFilePath(mapData.Path.FileName),
						slog.String("key", key),
					)
					continue
				}
				// else update the entry
				b.topManager.logger.LogAttrs(context.Background(), slog.LevelDebug, "Set map [runtime] entry", slog.String("map", mapData.Path.FileName), slog.String("key", key))

				err := runtimeClient.SetMapEntry(mapID, key, routeValue)
				if err != nil {
					if strings.Contains(err.Error(), "entry not found") {
						err = runtimeClient.AddMapEntry(mapID, key, routeValue)
					}
					if err != nil {
						metrics.MapStorageOperations.WithLabelValues("runtime_set", "error").Inc()
						b.topManager.logger.LogAttrs(context.Background(), slog.LevelError,
							"[failure] Set map [runtime] entry",
							slog.String("map", mapData.Path.FileName),
							slog.String("key", key),
							logging.LogAttrError(err),
						)
						return err
					}
				}
				metrics.MapStorageOperations.WithLabelValues("runtime_set", "ok").Inc()
				b.topManager.logger.LogAttrs(context.Background(), slog.LevelDebug,
					"[success] Set map [runtime] entry",
					slog.String("map", mapData.Path.FileName),
					slog.String("key", key),
				)
			}
		}
	}
	return nil
}

func (b *RouteMgrImpl) getMapID(fullPath string) (string, error) {
	runtimeClient := b.topManager.haproxyClient.RuntimeClient()
	// find the id of a map entry
	shownMaps, err := runtimeClient.ShowMaps()
	if err != nil {
		return "", err
	}
	for _, m := range shownMaps {
		if m.File == fullPath {
			return "#" + m.ID, nil
		}
	}
	return "", nil
}

func escapeSlashForRuntime(key string) string {
	return re.ReplaceAllStringFunc(key, func(s string) string {
		return `\` + s
	})
}
