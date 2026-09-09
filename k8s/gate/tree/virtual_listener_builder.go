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

	// One VirtualListener per port, gathering every listener on that port that is in
	// no conflict, whichever Gateway declares it.
	for port, conflictsOnThisPort := range b.ControllerStore.conflictsByPort {
		// The category comes from the port, which is where it is decided: the first
		// listener to claim a port fixes it, and every other listener on that port
		// either matches it or conflicts on protocol. It used to be read off the
		// first non-conflicting entry met while ranging over the map -- a "first"
		// that map iteration makes arbitrary, and which only ever gave the right
		// answer because the protocol pass guarantees a single category per port.
		virtualListener := NewVirtualListener(b.ControllerStore.protocolCategoryByPort[port], port)

		for key, conflicts := range conflictsOnThisPort {
			if conflicts.any() {
				continue
			}

			// Get the listener from the gateway
			listener := b.listenerFor(key)
			if listener == nil {
				continue
			}

			// Skip invalid listeners
			if !listener.Valid {
				b.Logger.LogAttrs(
					context.Background(), slog.LevelInfo, "Skipping invalid listener",
					logging.LogAttrKey(listener.Key().OwnerKey()),
					slog.String("listener", string(listener.Name())),
				)
				continue
			}

			// Check the Gateway validity before adding the listener to the virtual listener
			// Only add a VirtualListener if the Gateway it belongs to is valid
			gateway := b.ControllerStore.GetGatewayForListener(listener)
			if gateway == nil || !gateway.Valid {
				b.Logger.LogAttrs(
					context.Background(), slog.LevelInfo, "Skipping invalid listener as invalid Gateway",
					logging.LogAttrKey(listener.Key().GatewayKey),
					slog.String("listener", string(listener.Name())),
				)
				continue
			}
			// Add the listener to the virtual listener
			listener.VirtualListenerName = virtualListener.Name
			virtualListener.Listeners = append(virtualListener.Listeners, listener)
		}

		// A port whose every listener conflicts, or whose listeners all belong to an
		// invalid Gateway, gets no frontend.
		if len(virtualListener.Listeners) > 0 {
			b.GateTree.VirtualListeners[virtualListener.Name] = virtualListener
		}
	}

	// Sort the listeners in each VirtualListener for consistent ordering
	for _, vl := range b.GateTree.VirtualListeners {
		slices.SortStableFunc(vl.Listeners, func(a, b *Listener) int {
			return cmp.Compare(a.Name(), b.Name())
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
