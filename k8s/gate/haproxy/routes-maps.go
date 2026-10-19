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
	b.clearChangedRouteMaps()
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

	// Fill for the current state of each route.
	for routeKey, route := range controllerStore.GateTree.HTTPRoutes {
		for _, listener := range b.listenersForRoute(route) {
			b.applyHTTPRouteListenerMaps(routeKey, route, listener)
		}
	}
}

// clearChangedRouteMaps runs once before all refill passes: a purge between the path and
// listener-route passes would erase the backend entries the path pass just wrote.
func (b *RouteMgrImpl) clearChangedRouteMaps() {
	controllerStore := b.topManager.controllerStore
	for routeKey, route := range controllerStore.GateTree.HTTPRoutes {
		if route.TreeStatus.OldTreeResource != nil {
			b.clearRouteMapEntries("HTTPRoute", routeKey)
		}
	}
	for routeKey, route := range controllerStore.GateTree.TLSRoutes {
		if route.TreeStatus.OldTreeResource != nil {
			b.clearRouteMapEntries("TLSRoute", routeKey)
		}
	}
}

// clearRouteMapEntries drops every map entry a route wrote, in every frontend and in
// every map, without asking which listeners it was attached to.
//
// Asking that question is what this replaced, and it cannot be answered reliably at the
// moment the entries have to go. Deriving the listeners from the route's spec misses the
// entries a ListenerSet contributed, and reading the attachments its checks recorded
// misses everything for a route that was created and deleted without an intervening
// update: those attachments are still the empty map it was built with, so such a route
// left its entries behind.
//
// The storage already knows which entries each origin wrote, and an origin names the
// route it belongs to, so this asks the files instead of a reconstruction. It serves the
// listener-route maps and the path maps alike: both are written from an origin that names
// the route, and both used to reconstruct the route's listeners to find them.
func (b *RouteMgrImpl) clearRouteMapEntries(kind string, routeKey k8stypes.NamespacedName) {
	for _, mapDir := range b.topManager.params.mapsStorage.GetMaps() {
		for _, mapFile := range mapDir {
			if mapFile == nil {
				continue
			}
			mapFile.RemoveEntriesFromRoute(kind, routeKey)
		}
	}
}

func (b *RouteMgrImpl) applyHTTPRouteListenerMaps(
	routeKey k8stypes.NamespacedName,
	route *tree.HTTPRoute,
	listener *tree.Listener,
) {
	if listener.VirtualListenerName == "" {
		return
	}
	mapsStorage := b.topManager.params.mapsStorage
	frontendName := b.topManager.getFrontendName(listener.VirtualListenerName)
	listenerRouteExactMap := mapsStorage.GetListenerRouteExactMatchMapFile(frontendName)
	listenerRouteWildcardMap := mapsStorage.GetListenerRouteWildcardMatchMapFile(frontendName)
	listenerKeyName := listener.Key().String()
	routeOrigin := maps.NewRouteOrigin("HTTPRoute", listenerKeyName, routeKey)

	switch route.TreeStatus.Status {
	case store.StatusUnchanged:
		return
	case store.StatusDeleted:
		listenerRouteExactMap.RemoveEntriesFrom(routeOrigin)
		listenerRouteWildcardMap.RemoveEntriesFrom(routeOrigin)
		return
	}

	// Only fill if the route is actually accepted by this listener.
	// A listener that rejects the route kind (e.g. TLS listener receiving an
	// HTTPRoute) will not have the route in its AttachedRoutes.
	if _, accepted := listener.AttachedRoutes[routeKey]; !accepted {
		listenerRouteExactMap.RemoveEntriesFrom(routeOrigin)
		listenerRouteWildcardMap.RemoveEntriesFrom(routeOrigin)
		return
	}

	// StatusUpserted — only fill for valid routes.
	if !route.Valid {
		listenerRouteExactMap.RemoveEntriesFrom(routeOrigin)
		listenerRouteWildcardMap.RemoveEntriesFrom(routeOrigin)
		return
	}

	routeValueName := listenerKeyName + "/" + routeKey.String()
	// The creation timestamp orders the comma-joined listener-route value:
	// spec tie-break is oldest route first, then ns/name.
	routeCreatedAt := route.K8sResource.CreationTimestamp.Time
	exactEntries := map[maps.EntryKey]map[string]*maps.WeightedValue{}
	wildcardEntries := map[maps.EntryKey]map[string]*maps.WeightedValue{}

	if len(route.K8sResource.Spec.Hostnames) == 0 {
		// No hostnames = catch-all: add a wildcard entry that prefixes any reversed host.
		// The key "<listener>/." is a prefix of every "<listener>/.<reversed-host>" produced
		// by lua.reverse_host, so map_beg() will match any incoming host.
		entryKey := maps.EntryKey{Hostname: listenerKeyName + "/."}
		wildcardEntries[entryKey] = map[string]*maps.WeightedValue{
			routeValueName: {ValueName: routeValueName, CreatedAt: routeCreatedAt},
		}
	}

	for _, h := range route.K8sResource.Spec.Hostnames {
		hostname := string(h)
		if isDomainWildcard(hostname) {
			wildcardSuffix := removeDomainWildcard(hostname)
			reversedKey := reverseDomain(wildcardSuffix)
			entryKey := maps.EntryKey{Hostname: listenerKeyName + "/" + reversedKey}
			wildcardEntries[entryKey] = map[string]*maps.WeightedValue{
				routeValueName: {ValueName: routeValueName, CreatedAt: routeCreatedAt},
			}
		} else {
			entryKey := maps.EntryKey{Hostname: listenerKeyName + "/" + hostname}
			exactEntries[entryKey] = map[string]*maps.WeightedValue{
				routeValueName: {ValueName: routeValueName, CreatedAt: routeCreatedAt},
			}
		}
	}

	listenerRouteExactMap.ApplyRoute(routeOrigin, exactEntries)
	listenerRouteWildcardMap.ApplyRoute(routeOrigin, wildcardEntries)
}

// listenersFromAttachments returns the listeners a route is attached to, from the
// parentRef-keyed map its checks filled, deduplicated.
//
// Read rather than re-derived: resolving spec.parentRefs against Gateways cannot reach
// an entry a ListenerSet contributes, because the resource naming that entry is not a
// Gateway. The path maps are already written from this same map, so the listener-route
// maps follow it here instead of keeping a second definition of the relationship.
//
// Generic over the route type, because the HTTP and TLS checks fill the same shape.
func listenersFromAttachments[K any](attached utils.KeyMap[K, []*tree.Listener]) []*tree.Listener {
	var result []*tree.Listener
	seen := make(map[string]struct{})
	attached.Iterate(func(_ string, listeners []*tree.Listener) bool {
		for _, listener := range listeners {
			key := listener.Key().String()
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, listener)
		}
		return true
	})
	return result
}

func (*RouteMgrImpl) listenersForRoute(route *tree.HTTPRoute) []*tree.Listener {
	if route.K8sResource == nil {
		return nil
	}
	return listenersFromAttachments(route.Listeners)
}

func (b *RouteMgrImpl) fillMapsForTLSRoutes() {
	var errs utils.Errors
	controllerStore := b.topManager.controllerStore
	mapsStorage := b.topManager.params.mapsStorage
	// Managed TLSRoutes => Create / update/ delete backends
	for routeKey, route := range controllerStore.GateTree.TLSRoutes {
		for _, listener := range route.Listeners.Iterate {
			for _, listener := range listener {
				if listener.VirtualListenerName == "" {
					continue
				}
				frontendName := b.topManager.getFrontendName(listener.VirtualListenerName)
				routeValueName := listener.Key().String() + "/" + routeKey.String()
				routeOrigin := maps.NewRouteOrigin("TLSRoute", listener.Key().String(), routeKey)
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
		b.topManager.logger.LogAttrs(
			context.Background(), slog.LevelError, "Failed to fill maps for TLS routes",
			logging.LogAttrError(errs.Result()),
		)
	}
}

// fillListenerRouteMapsForTLSRoutes fills MAP_LISTENER_ROUTE_EXACT_MATCH and MAP_LISTENER_ROUTE_WILDCARD_MATCH
// for TLSRoutes, mirroring the same logic as fillListenerRouteMapsForHTTPRoutes.
func (b *RouteMgrImpl) fillListenerRouteMapsForTLSRoutes() {
	controllerStore := b.topManager.controllerStore

	// Fill for the current state of each route.
	for routeKey, route := range controllerStore.GateTree.TLSRoutes {
		for _, listener := range b.listenersForTLSRoute(route) {
			b.applyTLSRouteListenerMaps(routeKey, route, listener)
		}
	}
}

func (b *RouteMgrImpl) applyTLSRouteListenerMaps(
	routeKey k8stypes.NamespacedName,
	route *tree.TLSRoute,
	listener *tree.Listener,
) {
	if listener.VirtualListenerName == "" {
		return
	}
	mapsStorage := b.topManager.params.mapsStorage
	frontendName := b.topManager.getFrontendName(listener.VirtualListenerName)
	listenerRouteExactMap := mapsStorage.GetListenerRouteExactMatchMapFile(frontendName)
	listenerRouteWildcardMap := mapsStorage.GetListenerRouteWildcardMatchMapFile(frontendName)
	listenerKeyName := listener.Key().String()
	routeOrigin := maps.NewRouteOrigin("TLSRoute", listenerKeyName, routeKey)

	switch route.TreeStatus.Status {
	case store.StatusUnchanged:
		return
	case store.StatusDeleted:
		listenerRouteExactMap.RemoveEntriesFrom(routeOrigin)
		listenerRouteWildcardMap.RemoveEntriesFrom(routeOrigin)
		return
	}

	if _, accepted := listener.AttachedRoutes[routeKey]; !accepted {
		listenerRouteExactMap.RemoveEntriesFrom(routeOrigin)
		listenerRouteWildcardMap.RemoveEntriesFrom(routeOrigin)
		return
	}

	if !route.Valid {
		listenerRouteExactMap.RemoveEntriesFrom(routeOrigin)
		listenerRouteWildcardMap.RemoveEntriesFrom(routeOrigin)
		return
	}

	routeValueName := listenerKeyName + "/" + routeKey.String()
	routeCreatedAt := route.K8sResource.CreationTimestamp.Time
	exactEntries := map[maps.EntryKey]map[string]*maps.WeightedValue{}
	wildcardEntries := map[maps.EntryKey]map[string]*maps.WeightedValue{}

	if len(route.K8sResource.Spec.Hostnames) == 0 {
		entryKey := maps.EntryKey{Hostname: listenerKeyName + "/."}
		wildcardEntries[entryKey] = map[string]*maps.WeightedValue{
			routeValueName: {ValueName: routeValueName, CreatedAt: routeCreatedAt},
		}
	}

	for _, h := range route.K8sResource.Spec.Hostnames {
		hostname := string(h)
		if isDomainWildcard(hostname) {
			wildcardSuffix := removeDomainWildcard(hostname)
			reversedKey := reverseDomain(wildcardSuffix)
			entryKey := maps.EntryKey{Hostname: listenerKeyName + "/" + reversedKey}
			wildcardEntries[entryKey] = map[string]*maps.WeightedValue{
				routeValueName: {ValueName: routeValueName, CreatedAt: routeCreatedAt},
			}
		} else {
			entryKey := maps.EntryKey{Hostname: listenerKeyName + "/" + hostname}
			exactEntries[entryKey] = map[string]*maps.WeightedValue{
				routeValueName: {ValueName: routeValueName, CreatedAt: routeCreatedAt},
			}
		}
	}

	listenerRouteExactMap.ApplyRoute(routeOrigin, exactEntries)
	listenerRouteWildcardMap.ApplyRoute(routeOrigin, wildcardEntries)
}

func (*RouteMgrImpl) listenersForTLSRoute(route *tree.TLSRoute) []*tree.Listener {
	if route.K8sResource == nil {
		return nil
	}
	return listenersFromAttachments(route.Listeners)
}

func (b *RouteMgrImpl) fillMapsForHTTPRoutes() {
	var errs utils.Errors
	controllerStore := b.topManager.controllerStore
	mapsStorage := b.topManager.params.mapsStorage

	// Managed HTTPRoutes => Create / update/ delete backends
	for routeKey, route := range controllerStore.GateTree.HTTPRoutes {
		for _, listener := range route.Listeners.Iterate {
			for _, listener := range listener {
				// A listener with no virtual listener name is not programmed
				// (e.g. an invalid listener the route still attaches to); skip it
				// so its maps are not written under an empty-named frontend ("hug_").
				if listener.VirtualListenerName == "" {
					continue
				}
				frontendName := b.topManager.getFrontendName(listener.VirtualListenerName)
				routeValueName := listener.Key().String() + "/" + routeKey.String()
				origin := maps.NewRouteOrigin("HTTPRoute", listener.Key().String(), routeKey)
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
		b.topManager.logger.LogAttrs(
			context.Background(), slog.LevelError, "Failed to fill maps for HTTP routes",
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
		b.topManager.logger.LogAttrs(
			context.Background(), slog.LevelError, "Failed to fill maps for HTTP routes",
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
				b.topManager.logger.LogAttrs(
					context.Background(), slog.LevelError,
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
					key += escapeSlashForRuntime(entryKey.Path)
				}
				routeValue := mapData.BuildValue(entryValue.DesiredValue)
				// if routeValue is empty, delete the entry
				if routeValue == "" {
					b.topManager.logger.LogAttrs(context.Background(), slog.LevelDebug, "Deleting map [runtime] entry", slog.String("map", mapData.Path.FileName), slog.String("key", key))
					err := runtimeClient.DeleteMapEntry(mapID, key)
					if err != nil {
						metrics.MapStorageOperations.WithLabelValues("runtime_delete", "error").Inc()
						b.topManager.logger.LogAttrs(
							context.Background(), slog.LevelError,
							"[failure] Deleting map [runtime] entry",
							logging.LogAttrMapFilePath(mapData.Path.FileName),
							slog.String("key", key),
							logging.LogAttrError(err),
						)
						return err
					}
					metrics.MapStorageOperations.WithLabelValues("runtime_delete", "ok").Inc()
					b.topManager.logger.LogAttrs(
						context.Background(), slog.LevelInfo,
						"[success] Deleting map [runtime] entry",
						logging.LogAttrMapFilePath(mapData.Path.FileName),
						slog.String("key", key),
					)
					continue
				}
				// else update the entry
				b.topManager.logger.LogAttrs(context.Background(), slog.LevelDebug, "Set map [runtime] entry", slog.String("map", mapData.Path.FileName), slog.String("key", key))

				err := runtimeClient.SetMapEntry(mapID, key, escapeValueForRuntime(routeValue))
				if err != nil {
					if strings.Contains(err.Error(), "entry not found") {
						err = runtimeClient.AddMapEntry(mapID, key, escapeValueForRuntime(routeValue))
					}
					if err != nil {
						metrics.MapStorageOperations.WithLabelValues("runtime_set", "error").Inc()
						b.topManager.logger.LogAttrs(
							context.Background(), slog.LevelError,
							"[failure] Set map [runtime] entry",
							slog.String("map", mapData.Path.FileName),
							slog.String("key", key),
							logging.LogAttrError(err),
						)
						return err
					}
				}
				metrics.MapStorageOperations.WithLabelValues("runtime_set", "ok").Inc()
				b.topManager.logger.LogAttrs(
					context.Background(), slog.LevelDebug,
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

// escapeSlashForRuntime escapes CLI metacharacters in a map key: the runtime
// API splits commands on ';' and consumes a lone '\' before other characters,
// so a key containing either would be truncated or altered on the wire.
func escapeSlashForRuntime(key string) string {
	escaped := re.ReplaceAllStringFunc(key, func(s string) string {
		return `\` + s
	})
	return strings.ReplaceAll(escaped, `;`, `\;`)
}

// escapeValueForRuntime escapes a map value for the runtime CLI. On-disk map
// files take raw values; only the CLI protocol needs escaping. Conditional
// route values are ';'-delimited candidate lists, so without this the stored
// value would be cut at the first delimiter.
func escapeValueForRuntime(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(escaped, `;`, `\;`)
}
