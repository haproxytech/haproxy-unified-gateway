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
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/storage"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/protocols"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

var _ Builder = &GatewayBuilderImpl{}

type GatewayBuilderImpl struct {
	certStorage storage.CertificateStorage
	*ControllerStore
}

type listenerConflictCondition struct {
	// reason might be:
	// - gatewayv1.ListenerReasonProtocolConflict
	// - gatewayv1.ListenerReasonHostnameConflict
	reason   string
	protocol protocols.ProtocolCategory
	// hasConflict is true if the listener has a conflict
	// false if the listener has no conflict
	hasConflict bool
}

type listenerConflict map[client.ObjectKey]listenerConflictCondition // map[listenerKey] for example "default/gw1_l1"

type GatewayBuilderParams struct {
	storage.CertificateStorage
	*ControllerStore
}

func NewGatewayBuilder(params GatewayBuilderParams) Builder {
	return &GatewayBuilderImpl{
		ControllerStore: params.ControllerStore,
		certStorage:     params.CertificateStorage,
	}
}

// --------------------
// GateTree Updates
// --------------------

func (b *GatewayBuilderImpl) ComputeTreeUpdates() {
	b.resetListenerConflicts()

	// After this step, the clusterStore.Updates contains all impacted Gateways
	// Including the one impacted by:
	// - GatewayClass updates
	b.computeGateTreeUpdates()
}

func (b *GatewayBuilderImpl) computeGateTreeUpdates() {
	// Compute the upserted/delete Tree Gateways
	for gwKey, gwUpdate := range b.ClusterStore.Updates.Gateways {
		b.computeTreeGatewayUpdate(gwKey, gwUpdate)
	}

	// Check for conflicts
	// Add all Gateways that have a conflict in the list of updated Gateways
	// in order to recompute the checks and status (both Gateway and listeners)
	b.checkListenerConflicts()

	for _, treeGw := range b.ControllerStore.GateTree.Gateways {
		if treeGw == nil || treeGw.TreeStatus.Status != store.StatusUpserted {
			continue
		}

		if treeGw.isManaged() {
			// Process Listeners
			b.buildListeners(treeGw)

			// Compute status only if managed Gateway
			// If not managed, then we should not update the status
			// Build Listener conditions
			for _, listener := range treeGw.Listeners {
				listener.BuildConditions(treeGw)
			}
			treeGw.checkListenerConflicts(b.ControllerStore.mapPort2Listeners)
			treeGw.BuildConditions()
		}
	}
}

func (b *GatewayBuilderImpl) computeTreeGatewayUpdate(gwKey client.ObjectKey, gwUpdate store.Update[*gatewayv1.Gateway]) {
	var treeGw *Gateway
	alreadyManagedTreeGw, alreadyManagedTreeGwOK := b.GateTree.Gateways[gwKey]
	alreadyUnmanagedTreeGw, alreadyUnmanagedTreeGwOK := b.UnmanagedGateTree.Gateways[gwKey]

	if alreadyManagedTreeGwOK {
		treeGw = alreadyManagedTreeGw
	} else if alreadyUnmanagedTreeGwOK {
		treeGw = alreadyUnmanagedTreeGw
	}

	switch gwUpdate.Status {
	case store.StatusUpserted:
		if treeGw != nil {
			treeGw.SetAsUpserted(b.Logger, gwUpdate.NewObject)
		} else {
			treeGw = NewGateway(gwUpdate.NewObject)
		}

		// Do we keep it in Managed or Unmanaged???
		b.processManagementChecks(treeGw)

	case store.StatusDeleted:
		if treeGw != nil {
			treeGw.SetAsDeleted(b.Logger, gwUpdate.OldObject)
		}
		// else nothing to do
		// It did not exists, it's deleted, noop
	}
}

func (b *GatewayBuilderImpl) processManagementChecks(treeGw *Gateway) {
	// If the GatewayClass is not in the store, it means that the GatewayClass is not managed by our controller
	if ok := b.ControllerStore.CheckGatewayClassExists(string(treeGw.K8sResource.Spec.GatewayClassName)); !ok {
		return
	}

	treeGw.checkParametersRef(*b.ControllerStore)
	treeGw.checkGatewayClassIsValid(*b.ControllerStore)
	treeGw.Valid = treeGw.CheckParamsRef.Valid && treeGw.CheckValidGatewayClass.Valid

	if treeGw.isManaged() {
		treeGw.SetAsManaged(b.Logger, *b.ControllerStore)
	} else {
		treeGw.SetAsUnmanaged(b.Logger, *b.ControllerStore)
	}
}

// -----------------------------------------------

func (b *GatewayBuilderImpl) CleanTreeUpdates() {
	cleanTreeUpdates(b.GateTree.Gateways)
	cleanTreeUpdates(b.UnmanagedGateTree.Gateways)
}

func (b *GatewayBuilderImpl) buildListeners(treeGw *Gateway) {
	processedListeners := make(map[string]*Listener)

	for _, listener := range treeGw.K8sResource.Spec.Listeners {
		kinds := supportedKinds(listener, gateSupportedRouteKindsByProtocol)
		processedListener := Listener{
			Owner:             client.ObjectKeyFromObject(treeGw.K8sResource),
			K8sResource:       listener,
			AllowedRouteKinds: kinds,
			AttachedRoutes:    make(map[client.ObjectKey]struct{}),
			Conditions:        make(generic.Conditions),
		}
		processedListeners[string(listener.Name)] = &processedListener
	}
	treeGw.Listeners = processedListeners

	// Performs all needed checks
	for _, listener := range treeGw.Listeners {
		listener.resetChecks()
		switch listener.K8sResource.Protocol {
		// This switch will be completed with all needed checks per protocol
		case gatewayv1.HTTPProtocolType, gatewayv1.TLSProtocolType:
			listener.checkRouteGroupKind(treeGw, gateSupportedRouteKindsByProtocol)
			listener.checkProtocol(gateSupportedRouteKindsByProtocol)
			listener.checkConflict(treeGw, b.ControllerStore.mapPort2Listeners)
		case gatewayv1.HTTPSProtocolType:
			listener.checkRouteGroupKind(treeGw, gateSupportedRouteKindsByProtocol)
			listener.checkCertificateRefs(treeGw, b.GateTree.Secrets)
			listener.checkProtocol(gateSupportedRouteKindsByProtocol)
			listener.checkConflict(treeGw, b.ControllerStore.mapPort2Listeners)
		default:
			listener.checkProtocol(gateSupportedRouteKindsByProtocol)
			listener.checkConflict(treeGw, b.ControllerStore.mapPort2Listeners)
		}
	}
}

func (b *GatewayBuilderImpl) resetListenerConflicts() {
	b.ControllerStore.mapPort2Listeners = make(map[gatewayv1.PortNumber]listenerConflict)
	b.ControllerStore.previousMapPort2Listeners = make(map[gatewayv1.PortNumber]listenerConflict)
}

// checkListenerConflicts checks the conflicts between all Gateway listeners
// See computeListenerConflicts to see how the conflicts are detected
// For now, as there are only a few number of Gateways, we do this check on all Gateway/ all listeners
func (b *GatewayBuilderImpl) checkListenerConflicts() {
	oldGwWithPortConflicts := b.previousGatewaysWithPortConflicts()

	// Detect new conflicts
	b.computeListenerConflicts()

	newGwWithPortConflict := b.gatewaysWithPortConflicts()
	oldAndNewGwWithPortConflicts := map[client.ObjectKey]struct{}{}
	for gwKey := range newGwWithPortConflict {
		oldAndNewGwWithPortConflicts[gwKey] = struct{}{}
	}
	for gwKey := range oldGwWithPortConflicts {
		oldAndNewGwWithPortConflicts[gwKey] = struct{}{}
	}
	for gwKey := range oldAndNewGwWithPortConflicts {
		treeGw, ok := b.ControllerStore.GateTree.Gateways[gwKey]
		if !ok {
			continue
		}
		// Set the treeGw as UPSERTED
		if treeGw.TreeStatus.Status != store.StatusUpserted && treeGw.TreeStatus.Status != store.StatusDeleted {
			treeGw.SetAsUpserted(b.Logger, treeGw.K8sResource)
			b.processManagementChecks(treeGw)
		}
	}
}

// gatewaysWithPortConflicts returns a map of Gateway keys which have a conflict
func (b *GatewayBuilderImpl) gatewaysWithPortConflicts() map[client.ObjectKey]struct{} {
	return gatewaysWithPortConflicts(b.ControllerStore.mapPort2Listeners)
}

// previousGatewaysWithPortConflicts returns a map of Gateway keys which have a conflict
func (b *GatewayBuilderImpl) previousGatewaysWithPortConflicts() map[client.ObjectKey]struct{} {
	return gatewaysWithPortConflicts(b.ControllerStore.previousMapPort2Listeners)
}

// gatewaysWithPortConflicts returns a set of Gateway keys that have at least one listener with a conflict.
func gatewaysWithPortConflicts(mapPort2ListenerConflict map[gatewayv1.PortNumber]listenerConflict) map[client.ObjectKey]struct{} {
	gwKeys := map[client.ObjectKey]struct{}{}
	for _, conflictMap := range mapPort2ListenerConflict {
		for glk, v := range conflictMap {
			// If there is a conflict for this listener
			if v.hasConflict {
				// Compute the Gw key from the listener key
				gwKey := ConvertListenerKeyToGatewayKey(glk)
				gwKeys[gwKey] = struct{}{}
			}
		}
	}
	return gwKeys
}

type nbListeners struct {
	withConflict    int32
	withoutConflict int32
}

// nbListenersWithAndWithoutConflict returns the number of listeners that have a conflict
// and the number of listeners that do not have a conflict for a given Gateway.
func nbListenersWithAndWithoutConflict(mapPort2ListenerConflict map[gatewayv1.PortNumber]listenerConflict, treeGw *Gateway) nbListeners {
	nbListeners := nbListeners{}
	for _, conflictMap := range mapPort2ListenerConflict {
		for glk, v := range conflictMap {
			// First rebuild the Gateway key from the listenerKey
			mapgwk := ConvertListenerKeyToGatewayKey(glk)
			gwk := client.ObjectKeyFromObject(treeGw.K8sResource)
			// Not our Gateway, continue
			if mapgwk != gwk {
				continue
			}

			// If there is a conflict for this listener
			if v.hasConflict {
				nbListeners.withConflict++
			} else {
				nbListeners.withoutConflict++
			}
		}
	}
	return nbListeners
}
