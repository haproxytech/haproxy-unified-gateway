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
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
)

var _ Builder = &ListenerSetContributionBuilder{}

// ListenerSetContributionBuilder is the half of ListenerSet support that feeds the
// rest of the tree: it mirrors the ListenerSets of the cluster store and, once it
// does more than that, contributes their listeners to the merged list of each
// parent Gateway.
//
// Named after what it does rather than where it sits, because "before" is ambiguous
// in this package: the Builder lists are split on whether a builder runs before or
// after the reference rebuild, while the split that matters for ListenerSets is
// before or after the Gateway builder. Only the latter constrains this one, and it
// is met by its position among the post-builders.
//
// Its counterpart runs after the Gateway builder and reports verdicts: conditions
// the ListenerSet can only learn once the merged list has been compared and each
// listener judged. The two are separate builders rather than one registered twice,
// which would run the tree update twice over the same cluster updates.
//
// For now this one only maintains the tree map: nothing is contributed and no
// condition is computed, so a ListenerSet in the cluster changes nothing a client
// can see. That is deliberate -- it makes this half verifiable on its own.
type ListenerSetContributionBuilder struct {
	*ControllerStore
}

func NewListenerSetContributionBuilder(controllerStore *ControllerStore) Builder {
	return &ListenerSetContributionBuilder{ControllerStore: controllerStore}
}

// --------------------
// GateTree Updates
// --------------------

func (b *ListenerSetContributionBuilder) ComputeTreeUpdates() {
	for lsKey, lsUpdate := range b.ClusterStore.Updates.ListenerSets {
		existing := b.GateTree.ListenerSets[lsKey]

		switch {
		case lsUpdate.Status == store.StatusUpserted:
			if existing != nil {
				existing.SetAsUpserted(b.Logger, lsUpdate.NewObject)
				continue
			}
			b.GateTree.ListenerSets[lsKey] = NewListenerSet(lsUpdate.NewObject)

		case lsUpdate.Status == store.StatusDeleted && existing != nil:
			// The object is kept, marked deleted: its listeners still have to be
			// removed from the frontend and its map entries purged, and the parent
			// Gateway survives, so nothing else would carry that work.
			existing.SetAsDeleted(b.Logger)
		}
	}
}

func (b *ListenerSetContributionBuilder) CleanTreeUpdates() {
	cleanTreeUpdates(b.GateTree.ListenerSets)
}
