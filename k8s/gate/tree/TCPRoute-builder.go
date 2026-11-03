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
	"context"
	"fmt"
	"log/slog"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/storage"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

var _ Builder = &TCPRouteBuilderImpl{}

type TCPRouteBuilderImpl struct {
	mapsStorage storage.MapsStorage
	*ControllerStore
	referenceGrantManager *ReferenceGrantManager
}

type TCPRouteBuilderParams struct {
	storage.MapsStorage
	*ControllerStore
	*ReferenceGrantManager
}

func NewTCPRouteBuilder(params TCPRouteBuilderParams) Builder {
	return &TCPRouteBuilderImpl{
		ControllerStore:       params.ControllerStore,
		mapsStorage:           params.MapsStorage,
		referenceGrantManager: params.ReferenceGrantManager,
	}
}

func (b *TCPRouteBuilderImpl) ComputeTreeUpdates() {
	for routeKey, routeUpdate := range b.ClusterStore.Updates.TCPRoutes {
		b.computeTreeGatewayUpdate(routeKey, routeUpdate)
	}

	for _, tcpRoute := range b.ControllerStore.GateTree.TCPRoutes {
		if tcpRoute.TreeStatus.Status != store.StatusUpserted {
			continue
		}
		if tcpRoute.hasValidParentRef() {
			// Process Rules
			b.buildRules(tcpRoute)
		}

		// Merge the backendRef conditions
		tcpRoute.mergeBackendConditions()
		// Stamp the generation once every condition has been merged, so the
		// backendRef conditions carry it too.
		tcpRoute.Conditions.SetGeneration(tcpRoute.K8sResource.Generation)
	}
}

func (b *TCPRouteBuilderImpl) computeTreeGatewayUpdate(gwKey client.ObjectKey, routeUpdate store.Update[*gatewayv1.TCPRoute]) {
	var treeTCPRoute *TCPRoute
	alreadyManagedTreeRoute, alreadyManagedTreeRouteOK := b.GateTree.TCPRoutes[gwKey]
	alreadyUnmanagedTreeRoute, alreadyUnmanagedTreeRouteOK := b.UnmanagedGateTree.TCPRoutes[gwKey]

	if alreadyManagedTreeRouteOK {
		treeTCPRoute = alreadyManagedTreeRoute
	} else if alreadyUnmanagedTreeRouteOK {
		treeTCPRoute = alreadyUnmanagedTreeRoute
	}

	switch routeUpdate.Status {
	case store.StatusUpserted:
		if treeTCPRoute != nil {
			// The route may have moved between listeners: its previous
			// attachments owe a detach before the new checks attach it again.
			treeTCPRoute.detachFromListeners(*b.ControllerStore)
			treeTCPRoute.SetAsUpserted(b.Logger, routeUpdate.NewObject)
			treeTCPRoute.ResetChecks()
		} else {
			treeTCPRoute = NewTCPRoute(routeUpdate.NewObject, b.ControllerStore.ControllerName)
		}

		treeTCPRoute.processChecks(*b.ControllerStore)

		if treeTCPRoute.hasManagedParentRef() {
			b.SetAsManaged(treeTCPRoute)
		} else {
			b.SetAsUnmanaged(treeTCPRoute)
		}

		treeTCPRoute.BuildConditions()

	case store.StatusDeleted:
		if treeTCPRoute != nil {
			// Remove the route from each listener it was attached to, so the
			// listener's attachedRoutes count drops with the deletion.
			treeTCPRoute.detachFromListeners(*b.ControllerStore)
			treeTCPRoute.SetAsDeleted(b.Logger)
			treeTCPRoute.ResetChecks()
		}
		// It did not exist: nothing to do.
	}
}

// detachFromListeners removes the route from every listener it was attached to
// in the previous cycle, keeping the Gateway-side counts in step.
func (r *TCPRoute) detachFromListeners(controllerStore ControllerStore) {
	if r.K8sResource == nil {
		return
	}
	routeKey := client.ObjectKeyFromObject(r.K8sResource)
	for _, listeners := range r.Listeners.Iterate {
		for _, listener := range listeners {
			listener.deleteAttachedRoute(routeKey, controllerStore)
		}
	}
}

func (r *TCPRoute) hasValidParentRef() bool {
	return r.CheckParentRefs.Valid
}

func (r *TCPRoute) hasManagedParentRef() bool {
	return r.CheckParentRefs.Managed
}

func (r *TCPRoute) ResetChecks() {
	r.CheckParentRefs = CheckResultRoute{}
	r.Listeners.Clear()
}

func (b *TCPRouteBuilderImpl) CleanTreeUpdates() {
	cleanTreeUpdates(b.GateTree.TCPRoutes)
	cleanTreeUpdates(b.UnmanagedGateTree.TCPRoutes)
}

func (b *TCPRouteBuilderImpl) SetAsManaged(tcpRoute *TCPRoute) {
	b.Logger.LogAttrs(context.Background(), slog.LevelDebug, fmt.Sprintf("%T MANAGED", *tcpRoute),
		logging.LogAttrObjectKey(tcpRoute.K8sResource))
	key := client.ObjectKeyFromObject(tcpRoute.K8sResource)
	b.ControllerStore.GateTree.TCPRoutes[key] = tcpRoute
	delete(b.ControllerStore.UnmanagedGateTree.TCPRoutes, key)
}

func (b *TCPRouteBuilderImpl) SetAsUnmanaged(tcpRoute *TCPRoute) {
	b.Logger.LogAttrs(context.Background(), slog.LevelDebug, fmt.Sprintf("%T UNMANAGED", *tcpRoute),
		logging.LogAttrObjectKey(tcpRoute.K8sResource))
	key := client.ObjectKeyFromObject(tcpRoute.K8sResource)
	b.ControllerStore.UnmanagedGateTree.TCPRoutes[key] = tcpRoute
	delete(b.ControllerStore.GateTree.TCPRoutes, key)
}

func (b *TCPRouteBuilderImpl) buildRules(tcpRoute *TCPRoute) {
	tcpRoute.Rules = make([]*TCPRouteRule, 0)

	for _, rule := range tcpRoute.K8sResource.Spec.Rules {
		treeRouteRule := TCPRouteRule{
			K8sResource:     rule,
			CheckBackendRef: utils.NewKeyMap[gatewayv1.BackendObjectReference, CheckResult](utils.BackendObjectReferenceToKey),
		}
		tcpRoute.Rules = append(tcpRoute.Rules, &treeRouteRule)
	}

	for _, rule := range tcpRoute.Rules {
		rule.checkBackendRef(tcpRoute, *b.ControllerStore, b.referenceGrantManager)
	}
}
