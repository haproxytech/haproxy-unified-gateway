// Copyright 2026 HAProxy Technologies LLC
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
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/storage"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/storage/maps"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/protocols"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const tcpRouteFrontendName = "hug_tcp_8080"

// tcpRouteMgrFixture builds a RouteMgr over one TCP frontend on port 8080 with
// the given routes attached to its listener.
func tcpRouteMgrFixture(t *testing.T, compat bool, routes ...*tree.TCPRoute) (*RouteMgrImpl, storage.MapsStorage) {
	t.Helper()

	gateTree := tree.NewGateTree()
	listener := &tree.Listener{AttachedRoutes: tree.AttachedRoutes{}}
	for _, route := range routes {
		routeKey := k8stypes.NamespacedName{Namespace: route.K8sResource.Namespace, Name: route.K8sResource.Name}
		gateTree.TCPRoutes[routeKey] = route
		listener.AttachedRoutes[routeKey] = struct{}{}
	}
	gateTree.VirtualListeners["tcp_8080"] = &tree.VirtualListener{
		ProtocolCategory: protocols.ProcotolCategoryTCP,
		Port:             8080,
		Listeners:        []*tree.Listener{listener},
	}

	mgr := newTestBackendMgr(nil, nil)
	mgr.controllerStore.GateTree = gateTree
	mgr.params.LinkID = "hug"
	mgr.params.BackendNameTemplate = "{{ .LINK_ID }}_{{ .SERVICE_NAMESPACE }}_{{ .SERVICE_NAME }}_{{ .SERVICE_PORT }}_{{ .FILTER_HASH }}"
	mgr.params.GWAPICompat = compat
	mapsStorage := storage.NewMapsStorage(slog.New(slog.NewTextHandler(io.Discard, nil)), t.TempDir())
	mgr.params.mapsStorage = mapsStorage

	routeMgr := &RouteMgrImpl{topManager: mgr}
	routeMgr.fillMaps()
	return routeMgr, mapsStorage
}

// tcpRouteRuleForTest builds a rule with one check result per backendRef,
// invalid indexes listing the refs that failed resolution. Rule validity
// follows the refs, as the real builder sets it.
func tcpRouteRuleForTest(refs []gatewayv1.BackendRef, invalid map[int]struct{}) *tree.TCPRouteRule {
	rule := &tree.TCPRouteRule{
		K8sResource: gatewayv1.TCPRouteRule{
			BackendRefs: refs,
		},
		CheckBackendRef: utils.NewKeyMap[gatewayv1.BackendObjectReference, tree.CheckResult](utils.BackendObjectReferenceToKey),
		Valid:           len(invalid) == 0,
	}
	for i, ref := range refs {
		if _, ko := invalid[i]; ko {
			rule.CheckBackendRef.Set(ref.BackendObjectReference, tree.CheckResult{Valid: false})
			continue
		}
		rule.CheckBackendRef.Set(ref.BackendObjectReference, tree.CheckResult{Valid: true})
	}
	return rule
}

func tcpRouteForTest(name string, created time.Time, rules ...*tree.TCPRouteRule) *tree.TCPRoute {
	return &tree.TCPRoute{
		K8sResource: &gatewayv1.TCPRoute{
			Namespace: "ns", Name: name, CreationTimestamp: metav1.NewTime(created),
		},
		Valid: true,
		Rules: rules,
	}
}

func tcpBackendRef(t *testing.T, svcName string, weight int32) gatewayv1.BackendRef {
	t.Helper()
	return gatewayv1.BackendRef{
		Name:   gatewayv1.ObjectName(svcName),
		Port:   new(gatewayv1.PortNumber(8080)),
		Weight: new(weight),
	}
}

func tcpSvcBackendName(t *testing.T, mgr *HaproxyConfMgrImpl, svcName string) string {
	t.Helper()
	name, err := mgr.getBackendName(k8stypes.NamespacedName{Namespace: "ns", Name: svcName}, 8080, "")
	require.NoError(t, err)
	return name
}

func tcpRouteMapEntry(t *testing.T, mapsStorage storage.MapsStorage) string {
	t.Helper()
	file := mapsStorage.GetTCPRouteMapFile(tcpRouteFrontendName)
	entry, ok := file.Entries[maps.EntryKey{Hostname: "tcp_8080"}]
	if !ok {
		return ""
	}
	return maps.BuildRouteValue(entry.DesiredValue)
}

// Only the oldest attached route serves a TCP port, whatever the younger ones
// reference; a single-backend winner is written as a bare backend name.
func TestTCPRouteMapOldestRouteWins(t *testing.T) {
	created1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	created2 := created1.Add(time.Hour)

	older := tcpRouteForTest("older", created1, tcpRouteRuleForTest([]gatewayv1.BackendRef{tcpBackendRef(t, "svc-a", 1)}, nil))
	newer := tcpRouteForTest("newer", created2, tcpRouteRuleForTest([]gatewayv1.BackendRef{tcpBackendRef(t, "svc-b", 1)}, nil))

	mgr, mapsStorage := tcpRouteMgrFixture(t, false, older, newer)
	require.Equal(t, tcpSvcBackendName(t, mgr.topManager, "svc-a"), tcpRouteMapEntry(t, mapsStorage))
}

// Several valid backends are the port's weighted-random share, in the same
// JSON format the HTTP and TLS frontends already consume.
func TestTCPRouteMapWeighted(t *testing.T) {
	created := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	route := tcpRouteForTest("route", created, tcpRouteRuleForTest([]gatewayv1.BackendRef{
		tcpBackendRef(t, "svc-a", 80),
		tcpBackendRef(t, "svc-b", 20),
	}, nil))

	mgr, mapsStorage := tcpRouteMgrFixture(t, false, route)
	want := `{"a":"wr","l":"` + tcpSvcBackendName(t, mgr.topManager, "svc-a") + `:80,` +
		tcpSvcBackendName(t, mgr.topManager, "svc-b") + `:20"}`
	require.Equal(t, want, tcpRouteMapEntry(t, mapsStorage))
}

// A zero-weight ref receives no traffic. An unspecified weight means 1, so it
// still shares the port with its siblings.
func TestTCPRouteMapWeights(t *testing.T) {
	created := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("zero weight alone serves nothing", func(t *testing.T) {
		route := tcpRouteForTest("route", created, tcpRouteRuleForTest([]gatewayv1.BackendRef{tcpBackendRef(t, "svc-a", 0)}, nil))
		_, mapsStorage := tcpRouteMgrFixture(t, false, route)
		require.Empty(t, tcpRouteMapEntry(t, mapsStorage))
	})

	t.Run("zero weight is dropped beside a weighted sibling", func(t *testing.T) {
		refs := []gatewayv1.BackendRef{tcpBackendRef(t, "svc-a", 0), tcpBackendRef(t, "svc-b", 50)}
		route := tcpRouteForTest("route", created, tcpRouteRuleForTest(refs, nil))
		mgr, mapsStorage := tcpRouteMgrFixture(t, false, route)
		require.Equal(t, tcpSvcBackendName(t, mgr.topManager, "svc-b"), tcpRouteMapEntry(t, mapsStorage))
	})

	t.Run("unspecified weight counts as one", func(t *testing.T) {
		refs := []gatewayv1.BackendRef{tcpBackendRef(t, "svc-a", 1), tcpBackendRef(t, "svc-b", 1)}
		refs[0].Weight = nil
		refs[1].Weight = nil
		route := tcpRouteForTest("route", created, tcpRouteRuleForTest(refs, nil))
		mgr, mapsStorage := tcpRouteMgrFixture(t, false, route)
		want := `{"a":"wr","l":"` + tcpSvcBackendName(t, mgr.topManager, "svc-a") + `:1,` +
			tcpSvcBackendName(t, mgr.topManager, "svc-b") + `:1"}`
		require.Equal(t, want, tcpRouteMapEntry(t, mapsStorage))
	})
}

// A valid ref beside an invalid one keeps serving its share: rejected under
// compatibility, redistributed to the rest without it.
func TestTCPRouteMapMixedRefs(t *testing.T) {
	created := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	refs := []gatewayv1.BackendRef{tcpBackendRef(t, "svc-a", 20), tcpBackendRef(t, "missing", 80)}
	invalid := map[int]struct{}{1: {}}

	t.Run("compat keeps the valid share and rejects the rest", func(t *testing.T) {
		route := tcpRouteForTest("route", created, tcpRouteRuleForTest(refs, invalid))
		mgr, mapsStorage := tcpRouteMgrFixture(t, true, route)
		want := `{"a":"wr","l":"` + TCPBackendNotFound + `:80,` + tcpSvcBackendName(t, mgr.topManager, "svc-a") + `:20"}`
		require.Equal(t, want, tcpRouteMapEntry(t, mapsStorage))
	})

	t.Run("without compat the valid ref serves all", func(t *testing.T) {
		route := tcpRouteForTest("route", created, tcpRouteRuleForTest(refs, invalid))
		mgr, mapsStorage := tcpRouteMgrFixture(t, false, route)
		require.Equal(t, tcpSvcBackendName(t, mgr.topManager, "svc-a"), tcpRouteMapEntry(t, mapsStorage))
	})
}

// Every ref invalid rejects all connections in both modes: with compat the
// shares name the rejection backend, without it no entry is written.
func TestTCPRouteMapAllRefsInvalid(t *testing.T) {
	created := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	refs := []gatewayv1.BackendRef{tcpBackendRef(t, "missing", 80)}
	invalid := map[int]struct{}{0: {}}

	for _, compat := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[compat], func(t *testing.T) {
			route := tcpRouteForTest("route", created, tcpRouteRuleForTest(refs, invalid))
			_, mapsStorage := tcpRouteMgrFixture(t, compat, route)
			if compat {
				require.Equal(t, TCPBackendNotFound, tcpRouteMapEntry(t, mapsStorage))
				return
			}
			require.Empty(t, tcpRouteMapEntry(t, mapsStorage))
		})
	}
}

// A deleted winner hands the port to the next candidate in the same cycle.
func TestTCPRouteMapWinnerDeleted(t *testing.T) {
	created1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	created2 := created1.Add(time.Hour)

	older := tcpRouteForTest("older", created1, tcpRouteRuleForTest([]gatewayv1.BackendRef{tcpBackendRef(t, "svc-a", 1)}, nil))
	newer := tcpRouteForTest("newer", created2, tcpRouteRuleForTest([]gatewayv1.BackendRef{tcpBackendRef(t, "svc-b", 1)}, nil))

	mgr, mapsStorage := tcpRouteMgrFixture(t, false, older, newer)
	require.Equal(t, tcpSvcBackendName(t, mgr.topManager, "svc-a"), tcpRouteMapEntry(t, mapsStorage))

	older.SetAsDeleted(mgr.topManager.logger)
	older.K8sResource = nil
	older.TreeStatus.OldTreeResource = older.DeepCopy()
	older.TreeStatus.OldTreeResource.K8sResource = &gatewayv1.TCPRoute{
		Namespace: "ns", Name: "older",
	}

	mgr.fillMaps()
	require.Equal(t, tcpSvcBackendName(t, mgr.topManager, "svc-b"), tcpRouteMapEntry(t, mapsStorage))
}

// An attached route that is not valid serves nothing and writes no entry.
func TestTCPRouteMapInvalidRoute(t *testing.T) {
	created := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	route := tcpRouteForTest("route", created, tcpRouteRuleForTest([]gatewayv1.BackendRef{tcpBackendRef(t, "svc-a", 1)}, nil))
	route.Valid = false

	_, mapsStorage := tcpRouteMgrFixture(t, true, route)
	require.Empty(t, tcpRouteMapEntry(t, mapsStorage))
}

// A route that leaves our parents must not keep serving from the previous
// cycle's map.
func TestTCPRouteMapUnmanagedRouteClearsEntry(t *testing.T) {
	created := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	route := tcpRouteForTest("route", created, tcpRouteRuleForTest([]gatewayv1.BackendRef{tcpBackendRef(t, "svc-a", 1)}, nil))
	mgr, mapsStorage := tcpRouteMgrFixture(t, false, route)
	require.NotEmpty(t, tcpRouteMapEntry(t, mapsStorage))

	gateTree := mgr.topManager.controllerStore.GateTree
	routeKey := k8stypes.NamespacedName{Namespace: "ns", Name: "route"}
	// The builder detaches before moving the route to the unmanaged tree.
	for _, listener := range gateTree.VirtualListeners["tcp_8080"].Listeners {
		delete(listener.AttachedRoutes, routeKey)
	}
	delete(gateTree.TCPRoutes, routeKey)
	route.TreeStatus.OldTreeResource = route.DeepCopy()
	unmanaged := tree.NewGateTree()
	unmanaged.TCPRoutes[routeKey] = route
	mgr.topManager.controllerStore.UnmanagedGateTree = unmanaged

	mgr.fillMaps()
	require.Empty(t, tcpRouteMapEntry(t, mapsStorage))
}

// A backend shared with another route survives one owner's deletion and is
// removed with the last one.
func TestTCPRouteSharedBackendDeletion(t *testing.T) {
	mgr := newTestBackendMgr(nil, nil)
	mgr.backendOwners = NewBackendOwners()
	mgr.backendsImpactedInCycle = BackendsImpactedInCycle{
		Upserted:     map[string]map[client.ObjectKey]BackendImpactedInCycle{},
		Deleted:      map[string]struct{}{},
		Unreferenced: map[string]struct{}{},
	}
	keyA := k8stypes.NamespacedName{Namespace: "ns", Name: "a"}
	keyB := k8stypes.NamespacedName{Namespace: "ns", Name: "b"}
	require.NoError(t, mgr.backendOwners.addTCPRoute("be", keyA, &gatewayv1.TCPRoute{}))
	require.NoError(t, mgr.backendOwners.addTCPRoute("be", keyB, &gatewayv1.TCPRoute{}))

	require.NoError(t, mgr.onDeletedTCPRoute(keyA, nil))
	require.NotContains(t, mgr.backendsImpactedInCycle.Deleted, "be")
	require.Contains(t, mgr.backendsImpactedInCycle.Unreferenced, "be")

	require.NoError(t, mgr.onDeletedTCPRoute(keyB, nil))
	require.Contains(t, mgr.backendsImpactedInCycle.Deleted, "be")
}
