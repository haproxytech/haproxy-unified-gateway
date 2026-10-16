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
	"slices"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

var _ Builder = &ListenerSetContributionBuilder{}

// ListenerSetContributionBuilder is the half of ListenerSet support that feeds the
// rest of the tree: it mirrors the ListenerSets of the cluster store, decides which
// of them may attach, and hands the listeners of those that may to the Gateway
// builder, which appends them after each Gateway's own.
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
// No condition is computed here: an entry is judged by the same checks and programmed
// by the same feedback as a Gateway's own listener, and what this pass decides about a
// ListenerSet is recorded on its Attachment for the reporting pass to turn into
// conditions. The two halves stay apart because one decides and the other reports,
// which is what keeps a status from disagreeing with the data plane.
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
	b.mirrorListenerSets()
	b.reprocessGatewaysOfChangedListenerSets()
	b.contributeListeners()
}

// mirrorListenerSets brings this cycle's ListenerSet events into the tree.
func (b *ListenerSetContributionBuilder) mirrorListenerSets() {
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

// reprocessGatewaysOfChangedListenerSets puts the parent of every ListenerSet that
// changed this cycle back into the Gateway updates, so the Gateway builder rebuilds
// its merged list and re-judges its listeners.
//
// A ListenerSet alone produces no configuration: its listeners live in its parent's
// merged list, and everything downstream -- conflicts, frontends, map entries -- is
// keyed on the Gateway. So a ListenerSet event that did not reach its parent would
// change nothing at all.
//
// Only the parents of *changed* ListenerSets, never every parent. Re-upserting a
// Gateway replays all five listener checks including checkCertificateRefs, which
// re-reads the Secrets and PEM-decodes them; doing that on every cycle for as long
// as a ListenerSet exists is the cost the conflict-verdict diff was written to
// avoid.
//
// A deleted ListenerSet is included, and its parent is taken from the update's old
// object: the tree keeps the ListenerSet marked deleted with a nil resource, so the
// parentRef is only still readable there. Without it the entries would stay in the
// merged list until something else happened to touch the Gateway.
//
// This is a builder-level stand-in for an enqueue edge, and it does not replace one.
// The two edges a ListenerSet owes towards the objects its certificateRefs depend on
// -- their Secrets and the ReferenceGrants permitting them -- are watch edges on the
// ListenerSet controller. The edge towards its parent Gateway cannot be one: the
// parent is served by its own controller, which has no reason to watch every
// ListenerSet that might name it. So a changed ListenerSet relays itself to its
// parent here, and reprocessGatewaysWhoseContributorsChanged covers the changes that
// reach no ListenerSet at all.
func (b *ListenerSetContributionBuilder) reprocessGatewaysOfChangedListenerSets() {
	for _, lsUpdate := range b.ClusterStore.Updates.ListenerSets {
		listenerSet := lsUpdate.NewObject
		if listenerSet == nil {
			listenerSet = lsUpdate.OldObject
		}
		if listenerSet == nil {
			continue
		}

		gwKey, namesGateway := parentGatewayKey(listenerSet)
		if !namesGateway {
			continue
		}
		b.injectGatewayUpdate(gwKey)
	}
}

// injectGatewayUpdate puts a Gateway back in this cycle's updates, so the Gateway
// builder rebuilds its merged list. An update already queued for it -- a real change,
// a deletion, or an injection from another pass -- is left alone.
func (b *ListenerSetContributionBuilder) injectGatewayUpdate(gwKey client.ObjectKey) {
	parentGw, inStore := b.ClusterStore.Gateways[gwKey]
	if !inStore || parentGw == nil {
		return
	}
	injectIndirectUpdate(b.ClusterStore.Updates.Gateways, gwKey, parentGw)
}

// reprocessGatewaysWhoseContributorsChanged puts back in the Gateway updates every
// Gateway whose set of contributing ListenerSets differs from the previous cycle.
//
// The events that change which ListenerSets contribute to a Gateway are not all
// ListenerSet events, and that is the point of the diff. A namespace whose labels
// change, under allowedListeners.from: Selector, or the Gateway's own
// allowedListeners being edited, changes the answer for ListenerSets that did not
// change at all: no ListenerSet event is raised, so no parent is enqueued, and the
// merged list would keep serving listeners the Gateway no longer allows -- or keep
// omitting ones it now does. Comparing the sets makes the merged list follow the
// decision, whatever changed it.
//
// The same shape as the conflict-verdict diff: rebuild the whole table every cycle,
// keep the previous one, and reprocess exactly what differs. The set is compared in
// order because that order is the API's precedence order, and a reordering is a
// change the merged list has to follow.
//
// On the first cycle the previous set is empty, so every Gateway that has a
// contributor is reprocessed. That cycle is the initial sync, where those Gateways
// are upserted anyway, and the set is then remembered -- so this happens once per
// process, not once per cycle.
func (b *ListenerSetContributionBuilder) reprocessGatewaysWhoseContributorsChanged(current map[client.ObjectKey][]client.ObjectKey) {
	previous := b.ControllerStore.previousContributorsByGatewayKey

	for gwKey, contributors := range current {
		if !slices.Equal(previous[gwKey], contributors) {
			b.injectGatewayUpdate(gwKey)
		}
	}

	// A Gateway that had contributors and has none left is absent from `current`
	// instead of present with an empty slice, so the loop above cannot see it.
	for gwKey, hadContributors := range previous {
		if len(hadContributors) == 0 {
			continue
		}
		if _, stillContributing := current[gwKey]; !stillContributing {
			b.injectGatewayUpdate(gwKey)
		}
	}
}

// contributeListeners rebuilds, from scratch, the listeners each parent Gateway
// receives from the ListenerSets allowed to attach to it -- and records on every
// ListenerSet what was decided about it.
//
// Rebuilt rather than updated, like the conflict table and for the same reason: the
// set of contributors to a Gateway depends on the whole tree (a ListenerSet's
// namespace, the Gateway's allowedListeners, the creation order of its siblings), so
// there is no local edit that keeps it correct. The cost is in memory only.
//
// The allowance check belongs here and not further down. Anything in the merged list
// is compared for conflicts and can therefore *win* a port: an entry from a
// ListenerSet the Gateway does not allow would arbitrate against a legitimate
// listener before being filtered, and filtering after the comparison is letting an
// intruder decide. A ListenerSet that may not attach contributes nothing, and its
// refusal is reported on its own status -- its entries never becoming listeners.
func (b *ListenerSetContributionBuilder) contributeListeners() {
	b.ControllerStore.contributedListenersByGatewayKey = make(map[client.ObjectKey][]*Listener)
	contributorsByGatewayKey := make(map[client.ObjectKey][]client.ObjectKey)

	for _, treeLs := range utils.MapToSortedListByCreationTimestamp(b.GateTree.ListenerSets) {
		if treeLs == nil {
			continue
		}
		if treeLs.TreeStatus.Status == store.StatusDeleted || treeLs.K8sResource == nil {
			treeLs.Attachment = attachmentUnreported
			continue
		}

		treeLs.Attachment = b.attachmentOfListenerSet(treeLs.K8sResource)
		if treeLs.Attachment != attachmentAllowed {
			continue
		}

		gwKey, _ := parentGatewayKey(treeLs.K8sResource)
		// Recorded even when the ListenerSet declares no entry at all: what is
		// compared is which ListenerSets contribute, not how many listeners they
		// brought, so a spec edit that removes every entry is still a change.
		contributorsByGatewayKey[gwKey] = append(contributorsByGatewayKey[gwKey], client.ObjectKeyFromObject(treeLs.K8sResource))
		b.ControllerStore.contributedListenersByGatewayKey[gwKey] = append(
			b.ControllerStore.contributedListenersByGatewayKey[gwKey],
			listenersFromEntries(gwKey, treeLs.K8sResource)...,
		)
	}

	b.reprocessGatewaysWhoseContributorsChanged(contributorsByGatewayKey)
	b.ControllerStore.previousContributorsByGatewayKey = contributorsByGatewayKey
}

// listenersFromEntries turns a ListenerSet's entries into listeners of the Gateway
// they attach to, in spec order.
//
// The key carries both resources: the Gateway the listener is served on, and the
// ListenerSet that declares it. They are different objects and each is load-bearing
// -- the Gateway decides which frontend the listener lands in, the ListenerSet
// decides whose status reports it and which ReferenceGrants cover its
// certificateRefs. A listener name is unique only within the resource declaring it,
// so the ListenerSet half is also what keeps two homonymous entries apart.
//
// Never exempt from ReferenceGrant: the exemption exists for the controller-owned
// synthetic Ingress gateway, and a ListenerSet is a tenant resource. GEP-1713 is
// explicit in the other direction too -- a grant naming a Gateway does not cover its
// ListenerSets, nor the converse -- which the origin stamped here is what enforces.
func listenersFromEntries(gwKey client.ObjectKey, listenerSet *gatewayv1.ListenerSet) []*Listener {
	lsKey := client.ObjectKeyFromObject(listenerSet)
	listeners := make([]*Listener, 0, len(listenerSet.Spec.Listeners))

	for _, entry := range listenerSet.Spec.Listeners {
		listeners = append(listeners, &Listener{
			ListenerKey:                 listenerSetListenerKey(gwKey, lsKey, entry.Name),
			K8sListenerSetListenerEntry: &entry,
			OwnerKind:                   kindListenerSet,
			AttachedRoutes:              make(map[client.ObjectKey]struct{}),
			Conditions:                  make(generic.Conditions),
		})
	}
	return listeners
}

func (b *ListenerSetContributionBuilder) CleanTreeUpdates() {
	cleanTreeUpdates(b.GateTree.ListenerSets)
}
