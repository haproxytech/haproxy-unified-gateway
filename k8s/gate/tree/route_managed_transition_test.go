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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	v1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
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
			ObjectMeta: metav1.ObjectMeta{Namespace: routeKey.Namespace, Name: routeKey.Name},
		}}
	}
	newOtherGateway := func() *Gateway {
		return &Gateway{K8sResource: &gatewayv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Namespace: otherGwKey.Namespace, Name: otherGwKey.Name},
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
			ObjectMeta: metav1.ObjectMeta{Namespace: routeKey.Namespace, Name: routeKey.Name},
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
			ObjectMeta: metav1.ObjectMeta{Namespace: routeKey.Namespace, Name: routeKey.Name},
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
