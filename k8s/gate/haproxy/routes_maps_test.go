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
	"testing"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/storage"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/storage/maps"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

// Two homonymous routes served on distinct frontends. Their map values deliberately
// use the same listener key: cleanup must be scoped by kind, not inferred from a port.
type routeMapsFixture struct {
	manager     *RouteMgrImpl
	httpRoute   *tree.HTTPRoute
	tlsRoute    *tree.TLSRoute
	key         client.ObjectKey
	httpPaths   *maps.MapFileState
	httpHosts   *maps.MapFileState
	tlsBackends *maps.MapFileState
	tlsHosts    *maps.MapFileState
}

func newRouteMapsFixture(t *testing.T) *routeMapsFixture {
	t.Helper()
	mgr := newNoBackendRefsTestMgr()
	mgr.controllerStore.GateTree = tree.NewGateTree()
	mgr.params.mapsStorage = storage.NewMapsStorage(mgr.logger, t.TempDir())
	key := client.ObjectKey{Namespace: "tenant", Name: "route"}
	parentRef := gatewayv1.ParentReference{Name: "gw"}
	listener := func(name string, protocol gatewayv1.ProtocolType) *tree.Listener {
		return &tree.Listener{
			K8sGatewayListener:  &gatewayv1.Listener{Name: gatewayv1.SectionName(name), Protocol: protocol},
			VirtualListenerName: name,
			AttachedRoutes:      map[client.ObjectKey]struct{}{key: {}},
		}
	}
	ref := gatewayv1.BackendObjectReference{Name: "echo", Port: new(gatewayv1.PortNumber(8080))}
	httpRoute := tree.NewRoute(&gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name},
		Spec:       gatewayv1.HTTPRouteSpec{Hostnames: []gatewayv1.Hostname{"example.com"}},
	}, "controller")
	httpRoute.Valid = true
	httpRoute.Listeners.Set(parentRef, []*tree.Listener{listener("http_8080", gatewayv1.HTTPProtocolType)})
	httpRoute.Rules = []*tree.HTTPRouteRule{backendRefsRule(nil, blockedPrefixMatches(),
		[]gatewayv1.HTTPBackendRef{{BackendRef: gatewayv1.BackendRef{BackendObjectReference: ref}}}, true)}
	tlsRoute := tree.NewTLSRoute(&gatewayv1alpha2.TLSRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name},
		Spec:       gatewayv1alpha2.TLSRouteSpec{Hostnames: []gatewayv1.Hostname{"example.com"}},
	}, "controller")
	tlsRoute.Valid = true
	tlsRoute.Listeners.Set(parentRef, []*tree.Listener{listener("tls_8443", gatewayv1.TLSProtocolType)})
	checks := utils.NewKeyMap[gatewayv1.BackendObjectReference, tree.CheckResult](utils.BackendObjectReferenceToKey)
	checks.Set(ref, tree.CheckResult{Valid: true})
	tlsRoute.Rules = []*tree.TLSRouteRule{{
		K8sResource:     gatewayv1alpha2.TLSRouteRule{BackendRefs: []gatewayv1.BackendRef{{BackendObjectReference: ref}}},
		CheckBackendRef: checks,
		Valid:           true,
	}}
	mgr.controllerStore.GateTree.HTTPRoutes[key] = httpRoute
	mgr.controllerStore.GateTree.TLSRoutes[key] = tlsRoute
	return &routeMapsFixture{
		manager: &RouteMgrImpl{topManager: mgr}, httpRoute: httpRoute, tlsRoute: tlsRoute, key: key,
		httpPaths:   mgr.params.mapsStorage.GetPathPrefixMapFile("hug_http_8080"),
		httpHosts:   mgr.params.mapsStorage.GetListenerRouteExactMatchMapFile("hug_http_8080"),
		tlsBackends: mgr.params.mapsStorage.GetSniMapFile("hug_tls_8443"),
		tlsHosts:    mgr.params.mapsStorage.GetListenerRouteExactMatchMapFile("hug_tls_8443"),
	}
}

func (f *routeMapsFixture) reset() {
	f.manager.ResetMapFiles()
	f.httpRoute.TreeStatus = tree.TreeUpdate[tree.HTTPRoute]{Status: store.StatusUnchanged}
	f.tlsRoute.TreeStatus = tree.TreeUpdate[tree.TLSRoute]{Status: store.StatusUnchanged}
}

func (f *routeMapsFixture) change(kind string, deleted bool) {
	status := store.StatusUpserted
	if deleted {
		status = store.StatusDeleted
	}
	if kind == "HTTPRoute" {
		f.httpRoute.TreeStatus = tree.TreeUpdate[tree.HTTPRoute]{Status: status, OldTreeResource: f.httpRoute.DeepCopy()}
		if deleted {
			f.httpRoute.K8sResource = nil
			f.httpRoute.Listeners.Clear()
		}
	} else {
		f.tlsRoute.TreeStatus = tree.TreeUpdate[tree.TLSRoute]{Status: status, OldTreeResource: f.tlsRoute.DeepCopy()}
		if deleted {
			f.tlsRoute.K8sResource = nil
			f.tlsRoute.Listeners.Clear()
		}
	}
}

func desiredMapValues(m *maps.MapFileState) int {
	count := 0
	for _, entry := range m.Entries {
		count += len(entry.DesiredValue)
	}
	return count
}

func TestRouteMapsSurviveUpdates(t *testing.T) {
	for _, kind := range []string{"HTTPRoute", "TLSRoute"} {
		t.Run(kind, func(t *testing.T) {
			f := newRouteMapsFixture(t)
			paths, hosts := f.httpPaths, f.httpHosts
			if kind == "HTTPRoute" {
				delete(f.manager.topManager.controllerStore.GateTree.TLSRoutes, f.key)
			} else {
				delete(f.manager.topManager.controllerStore.GateTree.HTTPRoutes, f.key)
				paths, hosts = f.tlsBackends, f.tlsHosts
			}
			f.manager.fillMaps()
			require.Equal(t, 1, desiredMapValues(paths), "initial backend map")
			require.Equal(t, 1, desiredMapValues(hosts), "initial listener-route map")
			f.reset()
			f.change(kind, false)
			if kind == "HTTPRoute" {
				f.httpRoute.Rules[0].K8sResource.Matches[0].Path.Value = new("/updated")
			} else {
				rule := f.tlsRoute.Rules[0]
				rule.K8sResource.BackendRefs[0].Name = "echo-updated"
				rule.CheckBackendRef.Set(rule.K8sResource.BackendRefs[0].BackendObjectReference, tree.CheckResult{Valid: true})
			}
			f.manager.fillMaps()
			assert.Equal(t, 1, desiredMapValues(paths), "a later cleanup must not erase freshly refilled backends")
			for entryKey, entry := range paths.Entries {
				if len(entry.DesiredValue) == 0 {
					continue
				}
				if kind == "HTTPRoute" {
					assert.Equal(t, "/updated", entryKey.Path)
				} else {
					for _, value := range entry.DesiredValue {
						assert.Contains(t, value.ValueName, "echo-updated")
					}
				}
			}
			assert.Equal(t, 1, desiredMapValues(hosts))
			f.reset()
			f.change(kind, true)
			f.manager.fillMaps()
			assert.Zero(t, desiredMapValues(paths), "deletion must not need the old listeners")
			assert.Zero(t, desiredMapValues(hosts))
		})
	}
}

func TestGoneListenerClearsKindScopedRouteMaps(t *testing.T) {
	f := newRouteMapsFixture(t)
	f.manager.fillMaps()
	f.reset()
	listeners, found := f.httpRoute.Listeners.Get(gatewayv1.ParentReference{Name: "gw"})
	require.True(t, found)
	require.Len(t, listeners, 1)

	f.manager.topManager.clearListenerMapOrigins("http_8080", listeners[0])
	f.manager.topManager.params.mapsStorage.ProcessMapFiles()

	assert.Zero(t, desiredMapValues(f.httpHosts))
	assert.Equal(t, 1, desiredMapValues(f.tlsHosts), "a surviving frontend must be untouched")
}

func TestRouteMapCleanupPreservesAnotherKind(t *testing.T) {
	for _, kind := range []string{"HTTPRoute", "TLSRoute"} {
		for _, deleted := range []bool{false, true} {
			name := kind + "/update"
			if deleted {
				name = kind + "/delete"
			}
			t.Run(name, func(t *testing.T) {
				f := newRouteMapsFixture(t)
				f.manager.fillMaps()
				for _, m := range []*maps.MapFileState{f.httpPaths, f.httpHosts, f.tlsBackends, f.tlsHosts} {
					require.Equal(t, 1, desiredMapValues(m))
				}
				f.reset()
				f.change(kind, deleted)
				f.manager.fillMaps()
				paths, hosts := f.tlsBackends, f.tlsHosts
				if kind == "TLSRoute" {
					paths, hosts = f.httpPaths, f.httpHosts
				}
				assert.Equal(t, 1, desiredMapValues(paths), "the homonym's backend map must survive")
				assert.Equal(t, 1, desiredMapValues(hosts), "the homonym's listener-route map must survive")
			})
		}
	}
}
