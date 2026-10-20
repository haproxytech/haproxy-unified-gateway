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
package tree

import (
	"testing"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

// rebuiltGatewayFixture is an upserted Gateway, one ListenerSet attached to it, and three
// HTTPRoutes: one naming the Gateway, one naming the ListenerSet, one naming another
// Gateway.
type rebuiltGatewayFixture struct {
	builder              *GatewayBuilderImpl
	controllerStore      *ControllerStore
	viaGatewayKey        client.ObjectKey
	viaListenerSetKey    client.ObjectKey
	elsewhereKey         client.ObjectKey
	tlsViaListenerSetKey client.ObjectKey
}

func newRebuiltGatewayFixture(t *testing.T) *rebuiltGatewayFixture {
	t.Helper()

	gwKey := client.ObjectKey{Namespace: "infra", Name: "gw"}
	lsKey := client.ObjectKey{Namespace: "tenant", Name: "ls"}

	gwNs := gatewayv1.Namespace(gwKey.Namespace)
	lsNs := gatewayv1.Namespace(lsKey.Namespace)
	lsGroup := gatewayv1.Group(gatewayv1.GroupName)
	lsKind := gatewayv1.Kind("ListenerSet")

	route := func(name string, parentRefs ...gatewayv1.ParentReference) (*HTTPRoute, client.ObjectKey) {
		key := client.ObjectKey{Namespace: "tenant", Name: name}
		return NewRoute(&gatewayv1.HTTPRoute{
			Namespace: "tenant", Name: name,
			Spec: gatewayv1.HTTPRouteSpec{
				CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: parentRefs},
			},
		}, "controller"), key
	}

	viaGateway, viaGatewayKey := route("route-via-gateway",
		gatewayv1.ParentReference{Namespace: &gwNs, Name: gatewayv1.ObjectName(gwKey.Name)})
	viaListenerSet, viaListenerSetKey := route("route-via-listenerset",
		gatewayv1.ParentReference{Group: &lsGroup, Kind: &lsKind, Namespace: &lsNs, Name: gatewayv1.ObjectName(lsKey.Name)})
	elsewhere, elsewhereKey := route("route-elsewhere",
		gatewayv1.ParentReference{Namespace: &gwNs, Name: "other"})

	tlsViaListenerSetKey := client.ObjectKey{Namespace: "tenant", Name: "tls-route-via-listenerset"}
	tlsViaListenerSet := NewTLSRoute(&gatewayv1alpha2.TLSRoute{
		Namespace: "tenant", Name: "tls-route-via-listenerset",
		Spec: gatewayv1alpha2.TLSRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: []gatewayv1.ParentReference{{
				Group: &lsGroup, Kind: &lsKind, Namespace: &lsNs, Name: gatewayv1.ObjectName(lsKey.Name),
			}}},
		},
	}, "controller")

	listenerSet := &gatewayv1.ListenerSet{
		Namespace: lsKey.Namespace, Name: lsKey.Name,
		Spec: gatewayv1.ListenerSetSpec{
			ParentRef: gatewayv1.ParentGatewayReference{
				Name:      gatewayv1.ObjectName(gwKey.Name),
				Namespace: &gwNs,
			},
		},
	}

	controllerStore := &ControllerStore{
		ClusterStore: &store.ClusterStore{Updates: store.NewClusterUpdates()},
		GateTree: &GateTree{
			Gateways: map[types.NamespacedName]*Gateway{
				gwKey: {
					K8sResource: &gatewayv1.Gateway{
						Namespace: gwKey.Namespace, Name: gwKey.Name,
					},
					TreeStatus: TreeUpdate[Gateway]{Status: store.StatusUpserted},
				},
			},
			ListenerSets: map[types.NamespacedName]*ListenerSet{lsKey: NewListenerSet(listenerSet)},
			HTTPRoutes: map[types.NamespacedName]*HTTPRoute{
				viaGatewayKey:     viaGateway,
				viaListenerSetKey: viaListenerSet,
				elsewhereKey:      elsewhere,
			},
			TLSRoutes: map[types.NamespacedName]*TLSRoute{
				tlsViaListenerSetKey: tlsViaListenerSet,
			},
		},
		UnmanagedGateTree: &GateTree{},
		ExtractGVK: func(obj client.Object) schema.GroupVersionKind {
			switch obj.(type) {
			case *gatewayv1.ListenerSet:
				return schema.GroupVersionKind{Group: gatewayv1.GroupName, Version: "v1", Kind: "ListenerSet"}
			default:
				return schema.GroupVersionKind{Group: gatewayv1.GroupName, Version: "v1", Kind: "Gateway"}
			}
		},
	}

	return &rebuiltGatewayFixture{
		builder:              &GatewayBuilderImpl{ControllerStore: controllerStore},
		controllerStore:      controllerStore,
		viaGatewayKey:        viaGatewayKey,
		viaListenerSetKey:    viaListenerSetKey,
		elsewhereKey:         elsewhereKey,
		tlsViaListenerSetKey: tlsViaListenerSetKey,
	}
}

// A rebuilt Gateway recreates its own listeners and the entries its ListenerSets
// contribute, so every route that serves it -- by either parent kind -- has to be
// re-marked, while a route serving another Gateway must not be.
func TestRebuiltGatewayRemarksEveryRouteServingIt(t *testing.T) {
	f := newRebuiltGatewayFixture(t)

	f.builder.markRoutesOfRebuiltGateways()

	updates := f.controllerStore.ClusterStore.Updates.HTTPRoutes
	require.Contains(t, updates, f.viaGatewayKey, "a route naming the rebuilt Gateway serves it")
	require.Contains(t, updates, f.viaListenerSetKey, "a route naming one of its ListenerSets serves it too")
	assert.NotContains(t, updates, f.elsewhereKey, "a route serving another Gateway is left alone")

	assert.True(t, updates[f.viaGatewayKey].Indirect)
	assert.Equal(t, store.StatusUpserted, updates[f.viaGatewayKey].Status)

	tlsUpdates := f.controllerStore.ClusterStore.Updates.TLSRoutes
	require.Contains(t, tlsUpdates, f.tlsViaListenerSetKey,
		"the ListenerSet branch is the same rule for a TLSRoute")
	assert.True(t, tlsUpdates[f.tlsViaListenerSetKey].Indirect)
}

// An update already queued for a route -- a real change, or a deletion -- is not
// overwritten by the indirect marking.
func TestRebuiltGatewayLeavesAPendingRouteUpdateAlone(t *testing.T) {
	f := newRebuiltGatewayFixture(t)

	f.controllerStore.ClusterStore.Updates.HTTPRoutes[f.viaGatewayKey] = store.Update[*gatewayv1.HTTPRoute]{
		Status: store.StatusDeleted,
	}

	f.builder.markRoutesOfRebuiltGateways()

	assert.Equal(t, store.StatusDeleted,
		f.controllerStore.ClusterStore.Updates.HTTPRoutes[f.viaGatewayKey].Status,
		"a pending deletion must survive the marking")
}
