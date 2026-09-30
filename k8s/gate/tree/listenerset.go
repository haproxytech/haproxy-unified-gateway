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

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	// Conditions are this ListenerSet's own Accepted and Programmed conditions.
	// Rebuilt from scratch every cycle by the verdict builder: a status is a
	// conclusion about the whole tree, and the previous one is not an input to it.
	Conditions generic.Conditions
	// TreeStatus
	TreeStatus TreeUpdate[ListenerSet]
	// Entries is status.listeners: one record per entry this ListenerSet declares,
	// in spec order, with the conditions its contributed listener was judged on.
	Entries []ListenerEntryStatus
	// Attachment is what the contribution pass decided: whether this ListenerSet's
	// listeners entered the parent Gateway's merged list, and if not, whether the
	// refusal is this controller's to report. Written once per cycle by the
	// contribution pass, which is the only place the decision is taken -- the pass
	// that reports it must not re-derive it, or the status and the data plane could
	// disagree.
	Attachment listenerSetAttachment
}

// ListenerEntryStatus is a ListenerSet's status.listeners record for one of its
// entries.
//
// A tree type rather than gatewayv1.ListenerEntryStatus so that conditions stay
// generic.Conditions inside the tree and the conversion to the API type happens
// once, where the status is written.
//
// Name keys a listType=map within this ListenerSet's own status, which is safe:
// names are unique within the resource declaring them. It is deliberately not the
// merged list that the parent Gateway publishes, where the same bare name can
// legally appear twice -- that is why the Gateway publishes only its own listeners.
type ListenerEntryStatus struct {
	Name           gatewayv1.SectionName
	Conditions     generic.Conditions
	SupportedKinds []gatewayv1.RouteGroupKind
	AttachedRoutes int32
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

// GetCreationTimestamp, GetNamespace and GetName make a tree ListenerSet sortable
// by SortByCreationTimestamp, which is exactly the precedence the API prescribes for
// the ListenerSets attached to one Gateway: oldest first, ties broken on
// "{namespace}/{name}". They return zero values once the resource is gone, which is
// harmless -- a deleted ListenerSet contributes nothing, so where it sorts does not
// matter.
func (ls *ListenerSet) GetCreationTimestamp() metav1.Time {
	if ls != nil && ls.K8sResource != nil {
		return ls.K8sResource.GetCreationTimestamp()
	}
	return metav1.Time{}
}

func (ls *ListenerSet) GetNamespace() string {
	if ls != nil && ls.K8sResource != nil {
		return ls.K8sResource.GetNamespace()
	}
	return ""
}

func (ls *ListenerSet) GetName() string {
	if ls != nil && ls.K8sResource != nil {
		return ls.K8sResource.GetName()
	}
	return ""
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
