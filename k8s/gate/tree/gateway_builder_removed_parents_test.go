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
	"log/slog"
	"testing"

	rc "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/routes"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

func TestRemovedParentsRemarkDependentRoutes(t *testing.T) {
	for _, change := range []string{"deleted ListenerSet", "missing new parent", "wrong new kind", "deleted Gateway"} {
		t.Run(change, func(t *testing.T) {
			f := newRebuiltGatewayFixture(t)
			logger := slog.New(slog.DiscardHandler)
			ls := f.controllerStore.GateTree.ListenerSets[client.ObjectKey{Namespace: "tenant", Name: "ls"}]
			switch change {
			case "deleted ListenerSet":
				ls.SetAsDeleted(logger)
			case "missing new parent", "wrong new kind":
				next := ls.K8sResource.DeepCopy()
				if change == "missing new parent" {
					next.Spec.ParentRef.Name = "absent"
				} else {
					next.Spec.ParentRef.Kind = new(gatewayv1.Kind("Service"))
				}
				ls.SetAsUpserted(logger, next)
			case "deleted Gateway":
				for _, gw := range f.controllerStore.GateTree.Gateways {
					gw.TreeStatus.Status = store.StatusDeleted
				}
			}

			f.builder.markRoutesOfRebuiltGateways()

			assert.Contains(t, f.controllerStore.ClusterStore.Updates.HTTPRoutes, f.viaListenerSetKey)
			assert.Contains(t, f.controllerStore.ClusterStore.Updates.TLSRoutes, f.tlsViaListenerSetKey)
			assert.NotContains(t, f.controllerStore.ClusterStore.Updates.HTTPRoutes, f.elsewhereKey)
		})
	}
}

func TestDeletedListenerSetRemarksUnmanagedRoutesWithoutOverwritingDeletion(t *testing.T) {
	f := newRebuiltGatewayFixture(t)
	ls := f.controllerStore.GateTree.ListenerSets[client.ObjectKey{Namespace: "tenant", Name: "ls"}]
	ls.SetAsDeleted(slog.New(slog.DiscardHandler))
	f.controllerStore.UnmanagedGateTree.HTTPRoutes = f.controllerStore.GateTree.HTTPRoutes
	f.controllerStore.GateTree.HTTPRoutes = nil
	f.controllerStore.ClusterStore.Updates.TLSRoutes[f.tlsViaListenerSetKey] = store.Update[*gatewayv1alpha2.TLSRoute]{Status: store.StatusDeleted}

	f.builder.markRoutesOfRebuiltGateways()

	assert.Contains(t, f.controllerStore.ClusterStore.Updates.HTTPRoutes, f.viaListenerSetKey)
	assert.Equal(t, store.StatusDeleted, f.controllerStore.ClusterStore.Updates.TLSRoutes[f.tlsViaListenerSetKey].Status)
}

func TestDeletedListenerSetRouteIsRecheckedInTheSameCycle(t *testing.T) {
	f := newRouteAttachFixture(t)
	f.store.UnmanagedGateTree = NewGateTree()
	route := f.route(f.listenerSetParentRef(nil))
	route.processChecks(*f.store)
	require.True(t, route.Valid)
	routeKey := client.ObjectKeyFromObject(route.K8sResource)
	f.store.GateTree.HTTPRoutes = map[client.ObjectKey]*HTTPRoute{routeKey: route}
	f.treeLs.SetAsDeleted(f.store.Logger)
	f.treeGw.TreeStatus.Status = store.StatusUpserted

	gatewayBuilder := &GatewayBuilderImpl{ControllerStore: f.store}
	gatewayBuilder.markRoutesOfRebuiltGateways()
	update, found := f.store.ClusterStore.Updates.HTTPRoutes[routeKey]
	require.True(t, found, "the route must be checked before the deleted ListenerSet is reaped")
	routeBuilder := &HTTPRouteBuilderImpl{ControllerStore: f.store}
	routeBuilder.computeTreeGatewayUpdate(routeKey, update)

	assert.False(t, route.Valid)
	assert.True(t, route.CheckParentRefs.Managed, "a deleted parent still owes a refusal in this cycle")
	conds, found := route.CheckParentRefs.Conditions.Conditions.Get(f.listenerSetParentRef(nil))
	require.True(t, found)
	assert.Equal(t, rc.ConditionNotAcceptedNoMatchingParent(), conds)
}
