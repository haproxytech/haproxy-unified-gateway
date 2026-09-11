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
	"encoding/json"
	"log/slog"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// ListenerSet is a set of listeners contributed to an existing Gateway.
//
// It holds no listeners of its own in the tree: the listeners it declares are
// contributed to the merged list of its parent Gateway, which is where they are
// compared against the others for conflicts and where their conditions are
// computed. What stays here is the ListenerSet's own status -- whether it is
// allowed to attach at all, and the roll-up of its entries.
//
// Every field is exported, which DeepCopy requires: it round-trips through JSON
// and silently drops unexported state.
type ListenerSet struct {
	K8sResource *gatewayv1.ListenerSet
	// TreeStatus
	TreeStatus TreeUpdate[ListenerSet]
}

func NewListenerSet(k8sObject *gatewayv1.ListenerSet) *ListenerSet {
	return &ListenerSet{
		K8sResource: k8sObject,
		TreeStatus: TreeUpdate[ListenerSet]{
			Status:          store.StatusUpserted,
			OldTreeResource: nil,
		},
	}
}

func (ls *ListenerSet) SetAsDeleted(logger *slog.Logger) {
	setResourceStatus(logger, ls, nil, store.StatusDeleted)
	ls.K8sResource = nil
}

func (ls *ListenerSet) SetAsUpserted(logger *slog.Logger, newK8sResource *gatewayv1.ListenerSet) {
	setResourceStatus(logger, ls, newK8sResource, store.StatusUpserted)
	ls.K8sResource = newK8sResource
}

// GetTreeStatus returns the TreeStatus of the ListenerSet.
func (ls *ListenerSet) GetTreeStatus() *TreeUpdate[ListenerSet] {
	return &ls.TreeStatus
}

// SetTreeStatus sets the TreeStatus of the ListenerSet.
func (ls *ListenerSet) SetTreeStatus(treeStatus TreeUpdate[ListenerSet]) {
	ls.TreeStatus = treeStatus
}

// DeepCopy creates a deep copy of the ListenerSet.
func (ls *ListenerSet) DeepCopy() *ListenerSet {
	if ls == nil {
		return nil
	}
	// Save TreeStatus
	treeStatus := ls.TreeStatus
	ls.TreeStatus = TreeUpdate[ListenerSet]{}

	var copied ListenerSet
	data, err := json.Marshal(ls) // Serialize to JSON
	if err != nil {
		return nil
	}
	err = json.Unmarshal(data, &copied) // Deserialize to a new struct
	if err != nil {
		return nil
	}
	// Restore TreeStatus
	ls.TreeStatus = treeStatus
	return &copied
}
