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
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/protocols"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type listenerRefs struct {
	gatewayRef  *gatewayv1.Gateway
	listenerRef gatewayv1.Listener
}

// computeListenerConflicts detects conflicts between listeners on the same port.
// It populates b.mapPort2ListenerConflict with the results of the conflict detection.
// Conflicts can arise from:
// - Different protocol categories (e.g., HTTP vs HTTPS) on the same port.
// - Overlapping hostnames for listeners with compatible protocols on the same Gateway.
//
// Listeners with overlapping hostnames on different Gateways are NOT considered
// conflicts: they are merged into the same VirtualListener, following the Gateway
// API conformance requirement.
func (b *GatewayBuilderImpl) computeListenerConflicts() {
	// 0- Compute conflicts only between non-deleted Gateways and their listeners. Deleted Gateways and their listeners are ignored in the conflict detection.
	// 1- Sort all non-deleted Gateways by creation timestamp
	sortedGws := utils.MapToSortedListByCreationTimestamp(b.nonDeletedGateways())

	portListeners := b.listenersPerPort(sortedGws)

	// In portListeners we have now all listeners grouped by port.
	// Some of them are already marked as conflicting because of protocol category difference.
	// Now we need to check for hostname conflicts, but only within the same Gateway.
	for port, listeners := range portListeners {
		if _, ok := b.ControllerStore.mapPort2Listeners[port]; !ok {
			b.ControllerStore.mapPort2Listeners[port] = make(map[client.ObjectKey]listenerConflictCondition)
		}

		// Mark all listeners as non-conflicting initially.
		for _, lr := range listeners {
			lk := NewListenerKey(lr.gatewayRef, lr.listenerRef)
			b.ControllerStore.mapPort2Listeners[port][lk] = listenerConflictCondition{
				hasConflict: false,
				reason:      "",
				protocol:    protocols.ProtocolCategories[lr.listenerRef.Protocol],
			}
		}

		// Detect hostname conflicts only within the same Gateway.
		// The first listener in spec order is the winner; later overlapping listeners in the
		// same Gateway are marked as conflicting.
		// Listeners from different Gateways with overlapping hostnames are allowed: they will
		// be merged into the same VirtualListener.
		for i := range listeners {
			iKey := NewListenerKey(listeners[i].gatewayRef, listeners[i].listenerRef)
			// A listener already marked as conflicting cannot be a winner.
			if b.ControllerStore.mapPort2Listeners[port][iKey].hasConflict {
				continue
			}
			iHostname := utils.PointerDefaultValueIfNil(listeners[i].listenerRef.Hostname)

			for j := i + 1; j < len(listeners); j++ {
				// Only detect conflicts within the same Gateway.
				if listeners[i].gatewayRef.Name != listeners[j].gatewayRef.Name ||
					listeners[i].gatewayRef.Namespace != listeners[j].gatewayRef.Namespace {
					continue
				}

				jKey := NewListenerKey(listeners[j].gatewayRef, listeners[j].listenerRef)
				jHostname := utils.PointerDefaultValueIfNil(listeners[j].listenerRef.Hostname)

				if hostnameConflicts(string(iHostname), string(jHostname)) {
					b.ControllerStore.mapPort2Listeners[port][jKey] = listenerConflictCondition{
						hasConflict: true,
						reason:      string(gatewayv1.ListenerReasonHostnameConflict),
						protocol:    protocols.ProtocolCategories[listeners[j].listenerRef.Protocol],
					}
				}
			}
		}
	}
}

// nonDeletedGateways returns a filtered copy of b.GateTree.Gateways containing
// only Gateways that have not been marked for deletion.
func (b *GatewayBuilderImpl) nonDeletedGateways() map[types.NamespacedName]*Gateway {
	result := make(map[types.NamespacedName]*Gateway, len(b.GateTree.Gateways))
	for k, gw := range b.GateTree.Gateways {
		if gw == nil || gw.K8sResource == nil {
			continue
		}
		if gw.TreeStatus.Status == store.StatusDeleted {
			continue
		}
		result[k] = gw
	}
	return result
}

// listenersPerPort groups listeners by port.
// It ensures that all listeners on the same port share a compatible protocol category (e.g., all HTTP or all HTTPS).
// Listeners with incompatible protocols are marked as conflicting in mapPort2ListenerConflict
// and are not included in the returned map.
func (b *GatewayBuilderImpl) listenersPerPort(sortedGws []*Gateway) map[gatewayv1.PortNumber][]listenerRefs {
	portListeners := make(map[gatewayv1.PortNumber][]listenerRefs)
	mapPort2ProtocolCategory := make(map[gatewayv1.PortNumber]protocols.ProtocolCategory)

	// 2- For each Gateway, for each listener, check if the port is conflicting with some other port
	for _, treeGw := range sortedGws {
		if treeGw == nil || treeGw.K8sResource == nil {
			// ... deleted
			continue
		}
		if treeGw.TreeStatus.Status == store.StatusDeleted {
			continue
		}

		for _, listener := range treeGw.K8sResource.Spec.Listeners {
			// Compute the gatewaylistener key (reference to both Gateway and Listener)
			lk := NewListenerKey(treeGw.K8sResource, listener)

			// Check if the protocol category (secure/insecure/...) for the port is different from the current listener's protocol.
			_, ok := mapPort2ProtocolCategory[listener.Port]
			if !ok {
				// First listener on this port, the port protocol category is set for this port (secure/insecure/...)
				mapPort2ProtocolCategory[listener.Port] = protocols.ProtocolCategories[listener.Protocol]
				portListeners[listener.Port] = append(portListeners[listener.Port], listenerRefs{
					gatewayRef:  treeGw.K8sResource,
					listenerRef: listener,
				})
				continue
			}
			// Second (or more) listener on this port
			if mapPort2ProtocolCategory[listener.Port] != protocols.ProtocolCategories[listener.Protocol] {
				// If the protocol category is different, it's a conflict
				// This listener is conflicting
				if _, ok := b.ControllerStore.mapPort2Listeners[listener.Port]; !ok {
					b.ControllerStore.mapPort2Listeners[listener.Port] = make(map[client.ObjectKey]listenerConflictCondition)
				}
				lcc := listenerConflictCondition{
					hasConflict: true,
					reason:      string(gatewayv1.ListenerReasonProtocolConflict),
					protocol:    protocols.ProtocolCategories[listener.Protocol],
				}
				b.ControllerStore.mapPort2Listeners[listener.Port][lk] = lcc
				continue
			}
			// Same protocol category, check for overlap
			portListeners[listener.Port] = append(portListeners[listener.Port], listenerRefs{
				gatewayRef:  treeGw.K8sResource,
				listenerRef: listener,
			})
		}
	}
	return portListeners
}
