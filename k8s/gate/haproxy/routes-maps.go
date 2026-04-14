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

	k8stypes "k8s.io/apimachinery/pkg/types"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/storage/maps"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/metrics"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"
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
	b.fillListenerRouteMapsForHTTPRoutes()
	b.fillMapsForTLSRoutes()
	b.fillListenerRouteMapsForTLSRoutes()
	mapsStorage := b.topManager.params.mapsStorage
	mapsStorage.ProcessMapFiles()
}

// fillListenerRouteMapsForHTTPRoutes fills MAP_LISTENER_ROUTE_EXACT_MATCH and MAP_LISTENER_ROUTE_WILDCARD_MATCH.
// For each HTTPRoute that references a Listener via parentRefs, and for each hostname declared on
// the route, one entry is added — regardless of whether the route's hostname matches the listener's hostname:
//
//	key:   listener-name/host     (for exact) or listener-name/reversed-host-suffix (for wildcard)
//	value: listener-name/ns/route
//
// Where listener-name is "<ns>/<gateway>_<listener>" and route-name is "<ns>/<name>".
func (b *RouteMgrImpl) fillListenerRouteMapsForHTTPRoutes() {
	controllerStore := b.topManager.controllerStore
	empty := map[maps.EntryKey]map[string]*maps.WeightedValue{}

	// Pre-loop: clear old state for routes that have been updated.
	for routeKey, route := range controllerStore.GateTree.HTTPRoutes {
		if route.TreeStatus.OldTreeResource == nil {
			continue
		}
		for _, listener := range b.listenersForRoute(route.TreeStatus.OldTreeResource) {
			b.clearHTTPRouteListenerMaps(routeKey, listener, empty)
		}
	}

	// Main loop: fill for the current state of each route.
	for routeKey, route := range controllerStore.GateTree.HTTPRoutes {
		for _, listener := range b.listenersForRoute(route) {
			b.applyHTTPRouteListenerMaps(routeKey, route, listener, empty)
		}
	}
}

func (b *RouteMgrImpl) clearHTTPRouteListenerMaps(
	routeKey k8stypes.NamespacedName,
	listener *tree.Listener,
	empty map[maps.EntryKey]map[string]*maps.WeightedValue,
) {
	if listener.VirtualListenerName == "" {
		return
	}
	mapsStorage := b.topManager.params.mapsStorage
	frontendName := b.topManager.getFrontendName(listener.VirtualListenerName)
	listenerRouteExactMap := mapsStorage.GetListenerRouteExactMatchMapFile(frontendName)
	listenerRouteWildcardMap := mapsStorage.GetListenerRouteWildcardMatchMapFile(frontendName)
	listenerKeyName := listener.Key().String()
	routeOrigin := maps.ResourceOrigin{
		Namespace: routeKey.Namespace,
		Name:      listenerKeyName + "/" + routeKey.Name,
	}
	listenerRouteExactMap.ApplyRoute(routeOrigin, empty)
	listenerRouteWildcardMap.ApplyRoute(routeOrigin, empty)
}

func (b *RouteMgrImpl) applyHTTPRouteListenerMaps(
	routeKey k8stypes.NamespacedName,
	route *tree.HTTPRoute,
	listener *tree.Listener,
	empty map[maps.EntryKey]map[string]*maps.WeightedValue,
) {
	if listener.VirtualListenerName == "" {
		return
	}
	mapsStorage := b.topManager.params.mapsStorage
	frontendName := b.topManager.getFrontendName(listener.VirtualListenerName)
	listenerRouteExactMap := mapsStorage.GetListenerRouteExactMatchMapFile(frontendName)
	listenerRouteWildcardMap := mapsStorage.GetListenerRouteWildcardMatchMapFile(frontendName)
	listenerKeyName := listener.Key().String()
	routeOrigin := maps.ResourceOrigin{
		Namespace: routeKey.Namespace,
		Name:      listenerKeyName + "/" + routeKey.Name,
	}

	switch route.TreeStatus.Status {
	case store.StatusUnchanged:
		return
	case store.StatusDeleted:
		listenerRouteExactMap.ApplyRoute(routeOrigin, empty)
		listenerRouteWildcardMap.ApplyRoute(routeOrigin, empty)
		return
	}

	// Only fill if the route is actually accepted by this listener.
	// A listener that rejects the route kind (e.g. TLS listener receiving an
	// HTTPRoute) will not have the route in its AttachedRoutes.
	if _, accepted := listener.AttachedRoutes[routeKey]; !accepted {
		listenerRouteExactMap.ApplyRoute(routeOrigin, empty)
		listenerRouteWildcardMap.ApplyRoute(routeOrigin, empty)
		return
	}

	// StatusUpserted — only fill for valid routes.
	if !route.Valid {
		listenerRouteExactMap.ApplyRoute(routeOrigin, empty)
		listenerRouteWildcardMap.ApplyRoute(routeOrigin, empty)
		return
	}

	routeValueName := listenerKeyName + "/" + routeKey.String()
	exactEntries := map[maps.EntryKey]map[string]*maps.WeightedValue{}
	wildcardEntries := map[maps.EntryKey]map[string]*maps.WeightedValue{}

	if len(route.K8sResource.Spec.Hostnames) == 0 {
		// No hostnames = catch-all: add a wildcard entry that prefixes any reversed host.
		// The key "<listener>/." is a prefix of every "<listener>/.<reversed-host>" produced
		// by lua.reverse_host, so map_beg() will match any incoming host.
		entryKey := maps.EntryKey{Hostname: listenerKeyName + "/."}
		wildcardEntries[entryKey] = map[string]*maps.WeightedValue{
			routeValueName: {ValueName: routeValueName},
		}
	}

	for _, h := range route.K8sResource.Spec.Hostnames {
		hostname := string(h)
		if isDomainWildcard(hostname) {
			wildcardSuffix := removeDomainWildcard(hostname)
			reversedKey := reverseDomain(wildcardSuffix)
			entryKey := maps.EntryKey{Hostname: listenerKeyName + "/" + reversedKey}
			wildcardEntries[entryKey] = map[string]*maps.WeightedValue{
				routeValueName: {ValueName: routeValueName},
			}
		} else {
			entryKey := maps.EntryKey{Hostname: listenerKeyName + "/" + hostname}
			exactEntries[entryKey] = map[string]*maps.WeightedValue{
				routeValueName: {ValueName: routeValueName},
			}
		}
	}

	listenerRouteExactMap.ApplyRoute(routeOrigin, exactEntries)
	listenerRouteWildcardMap.ApplyRoute(routeOrigin, wildcardEntries)
}

// listenersForRoute returns all gateway Listener objects referenced by the route's parentRefs,
// regardless of hostname matching. Deleted gateways and missing section names are skipped.
func (b *RouteMgrImpl) listenersForRoute(route *tree.HTTPRoute) []*tree.Listener {
	if route.K8sResource == nil {
		return nil
	}
	controllerStore := b.topManager.controllerStore
	var result []*tree.Listener
	for _, parentRef := range route.K8sResource.Spec.ParentRefs {
		gwKey := tree.GetParentRefNamespacedName(parentRef, route.K8sResource.Namespace)
		treeGw, ok := controllerStore.GateTree.Gateways[gwKey]
		if !ok || treeGw.TreeStatus.Status == store.StatusDeleted {
			continue
		}
		if parentRef.SectionName != nil {
			l, ok := treeGw.Listeners[string(*parentRef.SectionName)]
			if ok {
				result = append(result, l)
			}
		} else {
			for _, l := range treeGw.Listeners {
				result = append(result, l)
			}
		}
	}
	return result
}

func (b *RouteMgrImpl) fillMapsForTLSRoutes() {
	var errs utils.Errors
	controllerStore := b.topManager.controllerStore
	mapsStorage := b.topManager.params.mapsStorage
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
				routeValueName := listener.Key().String() + "/" + routeKey.String()
				routeOrigin := maps.ResourceOrigin{Namespace: routeKey.Namespace, Name: routeValueName}
				mapSNIMap := mapsStorage.GetSniMapFile(frontendName)
				err := b.onDeletedTLSRoute(routeOrigin, route, mapSNIMap)
				// errs.Add(err)
				_ = err // TODO ignore error for now
			}
		}
	}
	// Managed TLSRoutes => Create / update/ delete backends
	for routeKey, route := range controllerStore.GateTree.TLSRoutes {
		for _, listener := range route.Listeners.Iterate {
			for _, listener := range listener {
				if listener.VirtualListenerName == "" {
					continue
				}
				frontendName := b.topManager.getFrontendName(listener.VirtualListenerName)
				routeValueName := listener.Key().String() + "/" + routeKey.String()
				routeOrigin := maps.ResourceOrigin{Namespace: routeKey.Namespace, Name: routeValueName}
				mapSNIMap := mapsStorage.GetSniMapFile(frontendName)
				switch route.TreeStatus.Status {
				case store.StatusUnchanged:
					continue
				case store.StatusUpserted:
					err := b.onUpsertedTLSRoute(routeOrigin, routeValueName, route, mapSNIMap)
					errs.Add(err)
				case store.StatusDeleted:
					err := b.onDeletedTLSRoute(routeOrigin, route, mapSNIMap)
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

// fillListenerRouteMapsForTLSRoutes fills MAP_LISTENER_ROUTE_EXACT_MATCH and MAP_LISTENER_ROUTE_WILDCARD_MATCH
// for TLSRoutes, mirroring the same logic as fillListenerRouteMapsForHTTPRoutes.
func (b *RouteMgrImpl) fillListenerRouteMapsForTLSRoutes() {
	controllerStore := b.topManager.controllerStore
	empty := map[maps.EntryKey]map[string]*maps.WeightedValue{}

	// Pre-loop: clear old state for routes that have been updated.
	for routeKey, route := range controllerStore.GateTree.TLSRoutes {
		if route.TreeStatus.OldTreeResource == nil {
			continue
		}
		for _, listener := range b.listenersForTLSRoute(route.TreeStatus.OldTreeResource) {
			b.clearTLSRouteListenerMaps(routeKey, listener, empty)
		}
	}

	// Main loop: fill for the current state of each route.
	for routeKey, route := range controllerStore.GateTree.TLSRoutes {
		for _, listener := range b.listenersForTLSRoute(route) {
			b.applyTLSRouteListenerMaps(routeKey, route, listener, empty)
		}
	}
}

func (b *RouteMgrImpl) clearTLSRouteListenerMaps(
	routeKey k8stypes.NamespacedName,
	listener *tree.Listener,
	empty map[maps.EntryKey]map[string]*maps.WeightedValue,
) {
	if listener.VirtualListenerName == "" {
		return
	}
	mapsStorage := b.topManager.params.mapsStorage
	frontendName := b.topManager.getFrontendName(listener.VirtualListenerName)
	listenerRouteExactMap := mapsStorage.GetListenerRouteExactMatchMapFile(frontendName)
	listenerRouteWildcardMap := mapsStorage.GetListenerRouteWildcardMatchMapFile(frontendName)
	listenerKeyName := listener.Key().String()
	routeOrigin := maps.ResourceOrigin{
		Namespace: routeKey.Namespace,
		Name:      listenerKeyName + "/" + routeKey.Name,
	}
	listenerRouteExactMap.ApplyRoute(routeOrigin, empty)
	listenerRouteWildcardMap.ApplyRoute(routeOrigin, empty)
}

func (b *RouteMgrImpl) applyTLSRouteListenerMaps(
	routeKey k8stypes.NamespacedName,
	route *tree.TLSRoute,
	listener *tree.Listener,
	empty map[maps.EntryKey]map[string]*maps.WeightedValue,
) {
	if listener.VirtualListenerName == "" {
		return
	}
	mapsStorage := b.topManager.params.mapsStorage
	frontendName := b.topManager.getFrontendName(listener.VirtualListenerName)
	listenerRouteExactMap := mapsStorage.GetListenerRouteExactMatchMapFile(frontendName)
	listenerRouteWildcardMap := mapsStorage.GetListenerRouteWildcardMatchMapFile(frontendName)
	listenerKeyName := listener.Key().String()
	routeOrigin := maps.ResourceOrigin{
		Namespace: routeKey.Namespace,
		Name:      listenerKeyName + "/" + routeKey.Name,
	}

	switch route.TreeStatus.Status {
	case store.StatusUnchanged:
		return
	case store.StatusDeleted:
		listenerRouteExactMap.ApplyRoute(routeOrigin, empty)
		listenerRouteWildcardMap.ApplyRoute(routeOrigin, empty)
		return
	}

	if _, accepted := listener.AttachedRoutes[routeKey]; !accepted {
		listenerRouteExactMap.ApplyRoute(routeOrigin, empty)
		listenerRouteWildcardMap.ApplyRoute(routeOrigin, empty)
		return
	}

	if !route.Valid {
		listenerRouteExactMap.ApplyRoute(routeOrigin, empty)
		listenerRouteWildcardMap.ApplyRoute(routeOrigin, empty)
		return
	}

	routeValueName := listenerKeyName + "/" + routeKey.String()
	exactEntries := map[maps.EntryKey]map[string]*maps.WeightedValue{}
	wildcardEntries := map[maps.EntryKey]map[string]*maps.WeightedValue{}

	if len(route.K8sResource.Spec.Hostnames) == 0 {
		entryKey := maps.EntryKey{Hostname: listenerKeyName + "/."}
		wildcardEntries[entryKey] = map[string]*maps.WeightedValue{
			routeValueName: {ValueName: routeValueName},
		}
	}

	for _, h := range route.K8sResource.Spec.Hostnames {
		hostname := string(h)
		if isDomainWildcard(hostname) {
			wildcardSuffix := removeDomainWildcard(hostname)
			reversedKey := reverseDomain(wildcardSuffix)
			entryKey := maps.EntryKey{Hostname: listenerKeyName + "/" + reversedKey}
			wildcardEntries[entryKey] = map[string]*maps.WeightedValue{
				routeValueName: {ValueName: routeValueName},
			}
		} else {
			entryKey := maps.EntryKey{Hostname: listenerKeyName + "/" + hostname}
			exactEntries[entryKey] = map[string]*maps.WeightedValue{
				routeValueName: {ValueName: routeValueName},
			}
		}
	}

	listenerRouteExactMap.ApplyRoute(routeOrigin, exactEntries)
	listenerRouteWildcardMap.ApplyRoute(routeOrigin, wildcardEntries)
}

// listenersForTLSRoute returns all gateway Listener objects referenced by the TLSRoute's parentRefs,
// regardless of hostname matching. Deleted gateways and missing section names are skipped.
func (b *RouteMgrImpl) listenersForTLSRoute(route *tree.TLSRoute) []*tree.Listener {
	if route.K8sResource == nil {
		return nil
	}
	controllerStore := b.topManager.controllerStore
	var result []*tree.Listener
	for _, parentRef := range route.K8sResource.Spec.ParentRefs {
		gwKey := tree.GetParentRefNamespacedName(parentRef, route.K8sResource.Namespace)
		treeGw, ok := controllerStore.GateTree.Gateways[gwKey]
		if !ok || treeGw.TreeStatus.Status == store.StatusDeleted {
			continue
		}
		if parentRef.SectionName != nil {
			l, ok := treeGw.Listeners[string(*parentRef.SectionName)]
			if ok {
				result = append(result, l)
			}
		} else {
			for _, l := range treeGw.Listeners {
				result = append(result, l)
			}
		}
	}
	return result
}

func (b *RouteMgrImpl) fillMapsForHTTPRoutes() {
	var errs utils.Errors
	controllerStore := b.topManager.controllerStore
	mapsStorage := b.topManager.params.mapsStorage
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
				routeValueName := listener.Key().String() + "/" + routeKey.String()
				origin := maps.ResourceOrigin{Namespace: routeKey.Namespace, Name: routeValueName}
				mapExact := mapsStorage.GetPathExactMapFile(frontendName)
				mapPrefix := mapsStorage.GetPathPrefixMapFile(frontendName)
				mapRegex := mapsStorage.GetPathRegexMapFile(frontendName)
				err := b.onDeletedHTTPRoute(origin, route, mapExact, mapPrefix, mapRegex)
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
				routeValueName := listener.Key().String() + "/" + routeKey.String()
				origin := maps.ResourceOrigin{Namespace: routeKey.Namespace, Name: routeValueName}
				mapExact := mapsStorage.GetPathExactMapFile(frontendName)
				mapPrefix := mapsStorage.GetPathPrefixMapFile(frontendName)
				mapRegex := mapsStorage.GetPathRegexMapFile(frontendName)

				switch route.TreeStatus.Status {
				case store.StatusUnchanged:
					continue
				case store.StatusUpserted:
					err := b.onUpsertedHTTPRoute(origin, routeValueName, route, mapExact, mapPrefix, mapRegex)
					errs.Add(err)
				case store.StatusDeleted:
					err := b.onDeletedHTTPRoute(origin, route, mapExact, mapPrefix, mapRegex)
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
	mapsStorage := b.topManager.params.mapsStorage
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
	mapsStorage := b.topManager.params.mapsStorage
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
				routeValue := mapData.BuildValue(entryValue.DesiredValue)
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
