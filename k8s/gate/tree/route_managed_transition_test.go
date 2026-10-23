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
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	v1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
)

// A route that stops being managed leaves the managed tree. That tree is what the data
// plane and the status are built from -- the backends, the route maps, the servers, and
// the status updater all range over it -- so a route left behind is still programmed and
// still reported for parents this controller no longer serves.
//
// The eviction used to be written against the Gateways map with the route's key, which
// did two things: it left the route in the managed tree, and it removed any Gateway
// sharing the route's namespace and name. Both directions are asserted here, with that
// homonym in place, because it is the object the mistake reached.
func TestRouteManagedTransitionMovesItBetweenTrees(t *testing.T) {
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	routeKey := client.ObjectKey{Namespace: "tenant", Name: "route"}
	otherGwKey := client.ObjectKey{Namespace: "tenant", Name: "gw"}

	// A Gateway whose key is the route's own key: allowed, names are unique per kind, and
	// it is the object an eviction addressed at the Gateways map with a route key hits.
	newHomonymGateway := func() *Gateway {
		return &Gateway{K8sResource: &gatewayv1.Gateway{
			Namespace: routeKey.Namespace, Name: routeKey.Name,
		}}
	}
	newOtherGateway := func() *Gateway {
		return &Gateway{K8sResource: &gatewayv1.Gateway{
			Namespace: otherGwKey.Namespace, Name: otherGwKey.Name,
		}}
	}
	newStore := func() *ControllerStore {
		return &ControllerStore{
			Logger: discard,
			GateTree: &GateTree{
				Gateways:   map[client.ObjectKey]*Gateway{},
				HTTPRoutes: map[client.ObjectKey]*HTTPRoute{},
				TLSRoutes:  map[client.ObjectKey]*TLSRoute{},
			},
			UnmanagedGateTree: &GateTree{
				Gateways:   map[client.ObjectKey]*Gateway{},
				HTTPRoutes: map[client.ObjectKey]*HTTPRoute{},
				TLSRoutes:  map[client.ObjectKey]*TLSRoute{},
			},
		}
	}

	t.Run("HTTPRoute", func(t *testing.T) {
		route := NewRoute(&gatewayv1.HTTPRoute{
			Namespace: routeKey.Namespace, Name: routeKey.Name,
		}, "gate.haproxy.org/hug")
		builder := &HTTPRouteBuilderImpl{ControllerStore: newStore()}
		builder.GateTree.Gateways[routeKey] = newHomonymGateway()
		builder.GateTree.Gateways[otherGwKey] = newOtherGateway()
		builder.GateTree.HTTPRoutes[routeKey] = route

		builder.SetAsUnmanaged(route)

		assert.NotContains(t, builder.GateTree.HTTPRoutes, routeKey,
			"the route must leave the tree the data plane and the status are built from")
		require.Contains(t, builder.UnmanagedGateTree.HTTPRoutes, routeKey,
			"and it is the unmanaged tree that holds it from now on")
		assert.Contains(t, builder.GateTree.Gateways, routeKey,
			"a Gateway sharing the route's key is not the route")
		assert.Contains(t, builder.GateTree.Gateways, otherGwKey)

		builder.SetAsManaged(route)

		assert.Contains(t, builder.GateTree.HTTPRoutes, routeKey)
		assert.NotContains(t, builder.UnmanagedGateTree.HTTPRoutes, routeKey,
			"or the next cycle finds the stale copy first and processes that one")
	})

	t.Run("TLSRoute", func(t *testing.T) {
		route := NewTLSRoute(&v1alpha2.TLSRoute{
			Namespace: routeKey.Namespace, Name: routeKey.Name,
		}, "gate.haproxy.org/hug")
		builder := &TLSRouteBuilderImpl{ControllerStore: newStore()}
		builder.GateTree.Gateways[routeKey] = newHomonymGateway()
		builder.GateTree.Gateways[otherGwKey] = newOtherGateway()
		builder.GateTree.TLSRoutes[routeKey] = route

		builder.SetAsUnmanaged(route)

		assert.NotContains(t, builder.GateTree.TLSRoutes, routeKey,
			"the route must leave the tree the data plane and the status are built from")
		require.Contains(t, builder.UnmanagedGateTree.TLSRoutes, routeKey,
			"and it is the unmanaged tree that holds it from now on")
		assert.Contains(t, builder.GateTree.Gateways, routeKey,
			"a Gateway sharing the route's key is not the route")
		assert.Contains(t, builder.GateTree.Gateways, otherGwKey)

		builder.SetAsManaged(route)

		assert.Contains(t, builder.GateTree.TLSRoutes, routeKey)
		assert.NotContains(t, builder.UnmanagedGateTree.TLSRoutes, routeKey,
			"or the next cycle finds the stale copy first and processes that one")
	})
}

// The decision itself, driven through the builder: a route whose parentRefs resolve to no
// Gateway this controller manages is moved to the unmanaged tree, and out of the managed one
// -- even when a Gateway shares its namespace and name, which is the object the old eviction
// reached instead of the route.
//
// Nothing else covers this path: the integration suites name the Gateway their routes attach
// to in every fixture, so no case makes a route unmanaged, and the two functions above are
// asserted without the checks that decide between them.
func TestBuilderMovesARouteWithNoManagedParentToTheUnmanagedTree(t *testing.T) {
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	key := client.ObjectKey{Namespace: "tenant", Name: "route"}

	// A Gateway sharing the route's namespace and name: allowed, names being unique per kind,
	// and the trap of an eviction addressed at the Gateways map with a route key.
	gw := &Gateway{K8sResource: &gatewayv1.Gateway{
		Namespace: key.Namespace, Name: key.Name,
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "haproxy",
			Listeners: []gatewayv1.Listener{
				{Name: "http", Port: 8080, Protocol: gatewayv1.HTTPProtocolType},
			},
		},
	}}
	updates := store.NewClusterUpdates()
	updates.HTTPRoutes[key] = store.Update[*gatewayv1.HTTPRoute]{
		NewObject: &gatewayv1.HTTPRoute{
			Namespace: key.Namespace, Name: key.Name,
			Spec: gatewayv1.HTTPRouteSpec{
				CommonRouteSpec: gatewayv1.CommonRouteSpec{
					// No Gateway of that name exists here, so the parentRef resolves to nothing
					// we manage.
					ParentRefs: []gatewayv1.ParentReference{{Name: "absent-gateway"}},
				},
			},
		},
		Status: store.StatusUpserted,
	}
	cs := &ControllerStore{
		Logger:       discard,
		ClusterStore: &store.ClusterStore{Updates: updates},
		GateTree: &GateTree{
			Gateways:   map[client.ObjectKey]*Gateway{key: gw},
			HTTPRoutes: map[client.ObjectKey]*HTTPRoute{},
		},
		UnmanagedGateTree: &GateTree{
			Gateways:   map[client.ObjectKey]*Gateway{},
			HTTPRoutes: map[client.ObjectKey]*HTTPRoute{},
		},
		ControllerName: "gate.haproxy.org/hug",
		// The check that resolves a parentRef asks the scheme what a Gateway is; one kind is
		// all this case has to answer for.
		ExtractGVK: func(client.Object) schema.GroupVersionKind {
			return schema.GroupVersionKind{Group: gatewayv1.GroupName, Version: "v1", Kind: "Gateway"}
		},
	}

	(&HTTPRouteBuilderImpl{ControllerStore: cs}).ComputeTreeUpdates()

	unmanaged, moved := cs.UnmanagedGateTree.HTTPRoutes[key]
	require.True(t, moved, "a route with no managed parent leaves the managed tree")
	assert.NotContains(t, cs.GateTree.HTTPRoutes, key,
		"the managed tree is what the data plane and the status are built from")
	assert.False(t, unmanaged.hasManagedParentRef(),
		"the move came from the checks the builder ran, not from the caller")
	assert.Contains(t, cs.GateTree.Gateways, key,
		"a Gateway sharing the route's key is not the route")
}
