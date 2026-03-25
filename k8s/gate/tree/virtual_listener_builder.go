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
	"cmp"
	"context"
	"log/slog"
	"slices"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/protocols"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
)

var _ Builder = &VirtualListenerBuilderImpl{}

type VirtualListenerBuilderImpl struct {
	*ControllerStore
}

func NewVirtualListenerBuilder(controllerStore *ControllerStore) Builder {
	return &VirtualListenerBuilderImpl{
		ControllerStore: controllerStore,
	}
}

func (b *VirtualListenerBuilderImpl) ComputeTreeUpdates() {
	// Store the previous VirtualListeners before computing the new ones,
	// to be able to detect which VirtualListeners are deletedand which are created or updated
	b.ControllerStore.GateTree.PreviousVirtualListeners = b.ControllerStore.GateTree.VirtualListeners
	// And re-initialize the current VirtualListeners map to compute the new state
	b.ControllerStore.GateTree.VirtualListeners = make(map[string]*VirtualListener)

	// Iterate over all ports in mapPort2Listeners
	// Idea is to keep on the same VirtualListener all the Listeners that are on the same port and have no conflict,
	// even if they belong to different Gateways
	for port, listenerConflicts := range b.ControllerStore.mapPort2Listeners {
		// Track protocol category for this port (should be consistent for non-conflicting listeners)
		var protocolCategory protocols.ProtocolCategory
		var virtualListener *VirtualListener

		// Collect all non-conflicting listeners for this port
		for listenerKey, conflictCondition := range listenerConflicts {
			// Skip listeners that have conflicts
			if conflictCondition.hasConflict {
				continue
			}

			// Get the protocol category (should be the same for all non-conflicting listeners on this port)
			if virtualListener == nil {
				protocolCategory = conflictCondition.protocol
				virtualListener = NewVirtualListener(protocolCategory, port)
			}

			// Get the listener from the gateway
			listener := b.GetListenerForKey(listenerKey)
			if listener == nil {
				continue
			}

			// Skip invalid listeners
			if !listener.Valid {
				b.Logger.LogAttrs(context.Background(), slog.LevelInfo, "Skipping invalid listener",
					logging.LogAttrKey(listener.Owner),
					slog.String("listener", string(listener.K8sResource.Name)),
				)
				continue
			}

			// Check the Gateway validity before adding the listener to the virtual listener
			// Only add a VirtualListener if the Gateway it belongs to is valid
			gateway := b.ControllerStore.GetGatewayForListener(listener)
			if gateway == nil || !gateway.Valid {
				b.Logger.LogAttrs(context.Background(), slog.LevelInfo, "Skipping invalid listener as invalid Gateway",
					logging.LogAttrKey(listener.Owner),
					slog.String("listener", string(listener.K8sResource.Name)),
				)
				continue
			}
			// Add the listener to the virtual listener
			listener.VirtualListenerName = virtualListener.Name
			virtualListener.Listeners = append(virtualListener.Listeners, listener)
		}

		// Only store the VirtualListener if it has at least one listener
		if virtualListener != nil && len(virtualListener.Listeners) > 0 {
			b.GateTree.VirtualListeners[virtualListener.Name] = virtualListener
		}
	}

	// Sort the listeners in each VirtualListener for consistent ordering
	for _, vl := range b.GateTree.VirtualListeners {
		slices.SortStableFunc(vl.Listeners, func(a, b *Listener) int {
			return cmp.Compare(a.K8sResource.Name, b.K8sResource.Name)
		})
	}

	// Update TreeStatus by comparing with PreviousVirtualListeners
	b.updateVirtualListenerTreeStatus()
}

// updateVirtualListenerTreeStatus compares current VirtualListeners with PreviousVirtualListeners
// and updates the TreeStatus accordingly.
func (b *VirtualListenerBuilderImpl) updateVirtualListenerTreeStatus() {
	// Mark new or updated VirtualListeners
	for name, currentVL := range b.GateTree.VirtualListeners {
		prevVL, existedBefore := b.GateTree.PreviousVirtualListeners[name]

		if !existedBefore {
			// New VirtualListener
			currentVL.SetAsCreated(b.Logger)
		} else if !currentVL.Equal(prevVL) {
			// VirtualListener was updated
			currentVL.SetAsUpserted(b.Logger)
		}
	}

	// Mark deleted VirtualListeners (in previous but not in current)
	for name, prevVL := range b.GateTree.PreviousVirtualListeners {
		if _, exists := b.GateTree.VirtualListeners[name]; !exists {
			// VirtualListener was deleted
			// copy previous VirtualListener
			prev := prevVL.DeepCopy()
			prev.SetAsDeleted(b.Logger)
			b.GateTree.VirtualListeners[name] = prev
		}
	}
}

func (b *VirtualListenerBuilderImpl) CleanTreeUpdates() {
	cleanVirtualListenerTreeUpdates(b.GateTree.VirtualListeners)
	cleanVirtualListenerTreeUpdates(b.UnmanagedGateTree.VirtualListeners)
}

func cleanVirtualListenerTreeUpdates(resourceMap map[string]*VirtualListener) {
	for key, resource := range resourceMap {
		status := resource.Status
		if status == store.StatusDeleted {
			delete(resourceMap, key)
			continue
		}
		resource.Status = store.StatusUnchanged
	}
}
