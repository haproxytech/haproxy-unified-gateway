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

var _ Builder = &HTTPRouteBuilderImpl{}

type HTTPRouteBuilderImpl struct {
	mapsStorage storage.MapsStorageEx
	*ControllerStore
	// runtimeUpdate indicates if the builder should trigger a runtime update of haproxy
	// when a map is changed
	runtimeUpdate bool
}

type HTTPRouteBuilderParams struct {
	storage.MapsStorageEx
	*ControllerStore
	RuntimeUpdate bool
}

func NewHTTPRouteBuilder(params HTTPRouteBuilderParams) Builder {
	return &HTTPRouteBuilderImpl{
		ControllerStore: params.ControllerStore,
		mapsStorage:     params.MapsStorageEx,
		runtimeUpdate:   params.RuntimeUpdate,
	}
}

// --------------------
// GateTree Updates
// --------------------

func (b *HTTPRouteBuilderImpl) ComputeTreeUpdates() {
	b.computeGateTreeUpdates()
}

func (b *HTTPRouteBuilderImpl) computeGateTreeUpdates() {
	for routeKey, routeUpdate := range b.ClusterStore.Updates.HTTPRoutes {
		b.computeTreeGatewayUpdate(routeKey, routeUpdate)
	}

	for _, httpRoute := range b.ControllerStore.GateTree.HTTPRoutes {
		if httpRoute == nil || httpRoute.TreeStatus.Status != store.StatusUpserted {
			continue
		}
		if httpRoute.hasValidParentRef() {
			// Process Rules
			b.buildRules(httpRoute)
		}

		// Merge the backendRef conditions, then filter conditions
		httpRoute.mergeBackendConditions()
		httpRoute.mergeFilterConditions()
		httpRoute.Conditions.SetGeneration(httpRoute.K8sResource.Generation)
	}
}

func (b *HTTPRouteBuilderImpl) computeTreeGatewayUpdate(gwKey client.ObjectKey, routeUpdate store.Update[*gatewayv1.HTTPRoute]) {
	// here we check if its valid, if ref object exists and all checks
	var treeHTTPRoute *HTTPRoute
	alreadyManagedTreeRoute, alreadyManagedTreeRouteOK := b.GateTree.HTTPRoutes[gwKey]
	alreadyUnmanagedTreeRoute, alreadyUnmanagedTreeRouteOK := b.UnmanagedGateTree.HTTPRoutes[gwKey]

	if alreadyManagedTreeRouteOK {
		treeHTTPRoute = alreadyManagedTreeRoute
	} else if alreadyUnmanagedTreeRouteOK {
		treeHTTPRoute = alreadyUnmanagedTreeRoute
	}

	switch routeUpdate.Status {
	case store.StatusUpserted:
		if treeHTTPRoute != nil {
			treeHTTPRoute.SetAsUpserted(b.Logger, routeUpdate.NewObject)
			treeHTTPRoute.ResetChecks()
		} else {
			treeHTTPRoute = NewRoute(routeUpdate.NewObject, b.ControllerStore.ControllerName)
		}

		treeHTTPRoute.processChecks(*b.ControllerStore)

		if treeHTTPRoute.hasManagedParentRef() {
			b.SetAsManaged(treeHTTPRoute)
		} else {
			b.SetAsUnmanaged(treeHTTPRoute)
		}

		// Compute status only if managed HTTRoute
		// If not managed, then we should not update the status
		treeHTTPRoute.BuildConditions()

	case store.StatusDeleted:
		if treeHTTPRoute != nil {
			// Iterate over the listeners of the deleted route and remove the route from each listener
			for _, listeners := range treeHTTPRoute.Listeners.Iterate {
				for _, listener := range listeners {
					// This impact the Gateway object (listener status AttachedRoute), so it needs to be done, even so the HTTPRoute by itself is deleted
					listener.deleteAttachedRoute(client.ObjectKeyFromObject(treeHTTPRoute.K8sResource), *b.ControllerStore)
				}
			}
			treeHTTPRoute.SetAsDeleted(b.Logger)
			treeHTTPRoute.ResetChecks()
		}
		// else nothing to do
		// It did not exists, it's deleted, noop
	}
}

func (r *HTTPRoute) hasValidParentRef() bool {
	return r.CheckParentRefs.Valid
}

func (r *HTTPRoute) hasManagedParentRef() bool {
	return r.CheckParentRefs.Managed
}

func (r *HTTPRoute) ResetChecks() {
	r.CheckParentRefs = CheckResultRoute{}
	r.Listeners.Clear()
}

// -----------------------------------------------

func (b *HTTPRouteBuilderImpl) CleanTreeUpdates() {
	cleanTreeUpdates(b.GateTree.HTTPRoutes)
	cleanTreeUpdates(b.UnmanagedGateTree.HTTPRoutes)
}

func (b *HTTPRouteBuilderImpl) SetAsManaged(route *HTTPRoute) {
	b.Logger.LogAttrs(context.Background(), slog.LevelDebug, fmt.Sprintf("%T MANAGED", *route),
		logging.LogAttrObjectKey(route.K8sResource))
	// Is it already in Managed
	key := client.ObjectKeyFromObject(route.K8sResource)
	b.ControllerStore.GateTree.HTTPRoutes[key] = route
	delete(b.ControllerStore.UnmanagedGateTree.Gateways, key)
}

func (b *HTTPRouteBuilderImpl) SetAsUnmanaged(route *HTTPRoute) {
	b.Logger.LogAttrs(context.Background(), slog.LevelDebug, fmt.Sprintf("%T UNMANAGED", *route),
		logging.LogAttrObjectKey(route.K8sResource))
	// Is it already in Managed
	key := client.ObjectKeyFromObject(route.K8sResource)
	b.ControllerStore.UnmanagedGateTree.HTTPRoutes[key] = route
	delete(b.ControllerStore.GateTree.Gateways, key)
}

func (b *HTTPRouteBuilderImpl) buildRules(httpRoute *HTTPRoute) {
	httpRoute.Rules = make([]*HTTPRouteRule, 0)

	for _, rule := range httpRoute.K8sResource.Spec.Rules {
		treeRouteRule := HTTPRouteRule{
			K8sResource:     rule,
			CheckBackendRef: utils.NewKeyMap[gatewayv1.BackendObjectReference, CheckResult](utils.BackendObjectReferenceToKey),
		}
		httpRoute.Rules = append(httpRoute.Rules, &treeRouteRule)
	}

	// Performs all needed checks
	for _, rule := range httpRoute.Rules {
		rule.checkBackendRef(httpRoute, *b.ControllerStore)
		rule.checkFilters()
	}
}
