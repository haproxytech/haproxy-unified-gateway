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
	"sigs.k8s.io/gateway-api/apis/v1alpha2"
)

var _ Builder = &TLSRouteBuilderImpl{}

type TLSRouteBuilderImpl struct {
	mapsStorage storage.MapsStorage
	*ControllerStore
}

type TLSRouteBuilderParams struct {
	storage.MapsStorage
	*ControllerStore
}

func NewTLSRouteBuilder(params TLSRouteBuilderParams) Builder {
	return &TLSRouteBuilderImpl{
		ControllerStore: params.ControllerStore,
		mapsStorage:     params.MapsStorage,
	}
}

// --------------------
// GateTree Updates
// --------------------

func (b *TLSRouteBuilderImpl) ComputeTreeUpdates() {
	b.computeGateTreeUpdates()
}

func (b *TLSRouteBuilderImpl) computeGateTreeUpdates() {
	for routeKey, routeUpdate := range b.ClusterStore.Updates.TLSRoutes {
		b.computeTreeGatewayUpdate(routeKey, routeUpdate)
	}

	for _, tlsRoute := range b.ControllerStore.GateTree.TLSRoutes {
		if tlsRoute.TreeStatus.Status != store.StatusUpserted {
			continue
		}
		if tlsRoute.isManaged() {
			// Process Rules
			b.buildRules(tlsRoute)
		}

		// Merge the backendRef conditions
		tlsRoute.mergeBackendConditions()
	}
}

func (b *TLSRouteBuilderImpl) computeTreeGatewayUpdate(gwKey client.ObjectKey, routeUpdate store.Update[*v1alpha2.TLSRoute]) {
	// here we check if its valid, if ref object exists and all checks
	var treeTLSRoute *TLSRoute
	alreadyManagedTreeRoute, alreadyManagedTreeRouteOK := b.GateTree.TLSRoutes[gwKey]
	alreadyUnmanagedTreeRoute, alreadyUnmanagedTreeRouteOK := b.UnmanagedGateTree.TLSRoutes[gwKey]

	if alreadyManagedTreeRouteOK {
		treeTLSRoute = alreadyManagedTreeRoute
	} else if alreadyUnmanagedTreeRouteOK {
		treeTLSRoute = alreadyUnmanagedTreeRoute
	}

	switch routeUpdate.Status {
	case store.StatusUpserted:
		if treeTLSRoute != nil {
			treeTLSRoute.SetAsUpserted(b.Logger, routeUpdate.NewObject)
			treeTLSRoute.ResetChecks()
		} else {
			treeTLSRoute = NewTLSRoute(routeUpdate.NewObject, b.ControllerStore.ControllerName)
		}

		treeTLSRoute.processChecks(*b.ControllerStore)

		if treeTLSRoute.isManaged() {
			b.SetAsManaged(treeTLSRoute)
		} else {
			b.SetAsUnmanaged(treeTLSRoute)
		}

		// Compute status only if managed HTTRoute
		// If not managed, then we should not update the status
		treeTLSRoute.BuildConditions()

	case store.StatusDeleted:
		if treeTLSRoute != nil {
			// Iterate over the listeners of the deleted route and remove the route from each listener
			for _, listeners := range treeTLSRoute.Listeners.Iterate {
				for _, listener := range listeners {
					// This impact the Gateway object (listener status AttachedRoute), so it needs to be done, even so the HTTPRoute by itself is deleted
					listener.deleteAttachedRoute(client.ObjectKeyFromObject(treeTLSRoute.K8sResource), *b.ControllerStore)
				}
			}
			treeTLSRoute.SetAsDeleted(b.Logger)
			treeTLSRoute.ResetChecks()
		}
		// else nothing to do
		// It did not exists, it's deleted, noop
	}
}

func (r *TLSRoute) isManaged() bool {
	return r.CheckParentRefs.Valid
}

func (r *TLSRoute) ResetChecks() {
	r.CheckParentRefs = CheckResultRoute{}
	r.Listeners.Clear()
}

// -----------------------------------------------

func (b *TLSRouteBuilderImpl) CleanTreeUpdates() {
	cleanTreeUpdates(b.GateTree.TLSRoutes)
	cleanTreeUpdates(b.UnmanagedGateTree.TLSRoutes)
}

func (b *TLSRouteBuilderImpl) SetAsManaged(tlsRoute *TLSRoute) {
	b.Logger.LogAttrs(context.Background(), slog.LevelDebug, fmt.Sprintf("%T MANAGED", *tlsRoute),
		logging.LogAttrObjectKey(tlsRoute.K8sResource))
	// Is it already in Managed
	key := client.ObjectKeyFromObject(tlsRoute.K8sResource)
	b.ControllerStore.GateTree.TLSRoutes[key] = tlsRoute
	delete(b.ControllerStore.UnmanagedGateTree.Gateways, key)
}

func (b *TLSRouteBuilderImpl) SetAsUnmanaged(tlsRoute *TLSRoute) {
	b.Logger.LogAttrs(context.Background(), slog.LevelDebug, fmt.Sprintf("%T UNMANAGED", *tlsRoute),
		logging.LogAttrObjectKey(tlsRoute.K8sResource))
	// Is it already in Managed
	key := client.ObjectKeyFromObject(tlsRoute.K8sResource)
	b.ControllerStore.UnmanagedGateTree.TLSRoutes[key] = tlsRoute
	delete(b.ControllerStore.GateTree.Gateways, key)
}

func (b *TLSRouteBuilderImpl) buildRules(tlsRoute *TLSRoute) {
	tlsRoute.Rules = make([]*TLSRouteRule, 0)

	for _, rule := range tlsRoute.K8sResource.Spec.Rules {
		treeRouteRule := TLSRouteRule{
			K8sResource:     rule,
			CheckBackendRef: utils.NewKeyMap[gatewayv1.BackendObjectReference, CheckResult](utils.BackendObjectReferenceToKey),
		}
		tlsRoute.Rules = append(tlsRoute.Rules, &treeRouteRule)
	}

	// Performs all needed checks
	for _, rule := range tlsRoute.Rules {
		rule.checkBackendRef(tlsRoute, *b.ControllerStore)
	}
}
