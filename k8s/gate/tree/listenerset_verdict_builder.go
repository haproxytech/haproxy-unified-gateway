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
	"maps"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

var _ Builder = &ListenerSetVerdictBuilder{}

// ListenerSetVerdictBuilder is the counterpart of ListenerSetContributionBuilder:
// it runs after the Gateway builder and reports on each ListenerSet what could only
// be known once the merged list existed, was compared, and each listener judged.
//
// The two passes are separate builders rather than one registered twice because
// their positions are on either side of the Gateway builder: the contribution must
// run before it, the reporting after, and the build loop runs each builder once.
//
// It is also the only place a ListenerSet's status is decided. The contribution
// pass records a decision (attachment) and computes nothing; this pass derives the
// conditions from the tree and never re-reads `allowedListeners`, so the status and
// the data plane cannot disagree about who contributed.
//
// Nothing here reaches Kubernetes: the conditions and per-entry records are stored
// on the tree, and a dedicated patcher publishes them.
type ListenerSetVerdictBuilder struct {
	*ControllerStore
}

func NewListenerSetVerdictBuilder(controllerStore *ControllerStore) Builder {
	return &ListenerSetVerdictBuilder{ControllerStore: controllerStore}
}

// --------------------
// GateTree Updates
// --------------------

func (b *ListenerSetVerdictBuilder) ComputeTreeUpdates() {
	b.GateTree.refreshListenerSetVerdicts()
}

// refreshListenerSetVerdicts re-derives every ListenerSet's entries, its own conditions, and
// its parent Gateway's attachedListenerSets count, from the listeners it contributed.
//
// It is what the verdict pass runs once per cycle, and what the apply feedback runs again
// after stamping the Programmed conditions the entries roll up: the entries are built from
// the listeners' conditions, so a roll-up taken before the apply would publish a Pending
// that the apply has already answered.
//
// The count is a roll-up over every ListenerSet of the tree, so it is cleared before the
// roll-up rather than patched up: a ListenerSet deleted this cycle contributes nothing and
// must not leave its contribution behind in the count.
func (g *GateTree) refreshListenerSetVerdicts() {
	for _, treeGw := range g.Gateways {
		if treeGw != nil {
			treeGw.AttachedListenerSets = 0
		}
	}

	for _, treeLs := range g.ListenerSets {
		computeListenerSetVerdict(g, treeLs)
	}
}

// CleanTreeUpdates owns nothing: ListenerSets are mirrored, and reaped when
// deleted, by the contribution pass, which is the one that puts them in the tree.
func (*ListenerSetVerdictBuilder) CleanTreeUpdates() {}

// computeListenerSetVerdict settles a ListenerSet's own conditions, its per-entry
// status records, and its parent Gateway's attachedListenerSets.
func computeListenerSetVerdict(gates *GateTree, treeLs *ListenerSet) {
	if treeLs == nil {
		return
	}

	// Rebuilt from scratch every cycle: a status is a conclusion about the whole
	// tree, and the previous one is not an input to it. Clearing first is also what
	// makes the silent cases silent -- a ListenerSet that stops being reported must
	// not keep the status it was given while it still was.
	treeLs.Conditions = make(generic.Conditions)
	treeLs.Entries = nil

	// A deleted ListenerSet has no resource left to be informed about, and its
	// listeners are already leaving the merged list.
	if treeLs.K8sResource == nil || treeLs.TreeStatus.Status == store.StatusDeleted {
		return
	}

	// The conditions are published in the ListenerSet's own status and compared
	// against THAT object's metadata.generation, not the parent Gateway's.
	// BuildConditions stamps every listener with the generation of the Gateway it is
	// served on -- right for a listener the Gateway declares, wrong here -- and
	// listenerConditionsForStatus applies the same correction to the per-entry
	// conditions. Stamping on the way out keeps it to one place that no branch can
	// skip; SetGeneration is a no-op on the empty conditions the silent cases leave.
	defer func() { treeLs.Conditions.SetGeneration(treeLs.K8sResource.GetGeneration()) }()

	if treeLs.Attachment == attachmentNotAllowed {
		treeLs.Conditions.MergeOverrideConditions(conditions.NewListenerSetAcceptedNotAllowed())
		treeLs.Conditions.MergeOverrideConditions(conditions.NewListenerSetProgrammedNotAllowed())
		return
	}
	if treeLs.Attachment != attachmentAllowed {
		// attachmentUnreported: the parentRef names a resource of another controller,
		// or no Gateway of this one. Writing a status here would be commenting on
		// someone else's resource, so nothing is written -- the refusal those cases
		// owe is to stay quiet, not to report.
		return
	}

	parentGw := parentGatewayOf(gates, treeLs)
	if parentGw == nil || !conditionStatusIs(
		parentGw.Conditions, string(gatewayv1.GatewayConditionAccepted), metav1.ConditionTrue,
	) {
		// The parent is ours but not accepted, so whatever this ListenerSet declares
		// will not be served. The cause is stated on the Gateway; this is the echo
		// the ListenerSet owes its own reader.
		treeLs.Conditions.MergeOverrideConditions(conditions.NewListenerSetAcceptedParentNotAccepted())
		treeLs.Conditions.MergeOverrideConditions(conditions.NewListenerSetProgrammedParentNotProgrammed())
		return
	}

	rollUp := rollUpListenerSetEntries(parentGw, client.ObjectKeyFromObject(treeLs.K8sResource), treeLs.K8sResource.GetGeneration())
	treeLs.Entries = rollUp.statuses
	rollUpListenerSetVerdict(treeLs, rollUp)

	// Counted only when accepted, which is what the API's attachedListenerSets
	// means: the three criteria are the parent's allowance (already settled, since
	// the attachment is `allowed`), a valid parentRef, and the ListenerSet's own
	// Accepted condition being true. A ListenerSet refused by the Gateway is not
	// counted, and neither is one whose parent is not accepted.
	if conditionStatusIs(treeLs.Conditions, string(gatewayv1.ListenerSetConditionAccepted), metav1.ConditionTrue) {
		parentGw.AttachedListenerSets++
	}
}

// parentGatewayOf resolves the Gateway a ListenerSet attaches to, in the tree.
//
// A read, not a decision: the attachment was settled by the contribution pass, and
// the key it used was recorded on the listener keys themselves. Re-deriving it here
// would be a second place to keep in agreement with the first.
func parentGatewayOf(gates *GateTree, treeLs *ListenerSet) *Gateway {
	gwKey, namesGateway := parentGatewayKey(treeLs.K8sResource)
	if !namesGateway {
		return nil
	}
	return gates.Gateways[gwKey]
}

// listenerSetEntryRollUp is what a ListenerSet's own conditions are derived from:
// the per-entry records to publish, and the counts the resource-level conditions
// are a function of.
type listenerSetEntryRollUp struct {
	statuses   []ListenerEntryStatus
	total      int
	valid      int
	programmed int
	pending    int
}

// entryRollUp walks the entries this ListenerSet contributed to its parent's
// merged list, in the merged list's order -- the Gateway's own listeners first,
// then ListenerSets oldest first -- and so in spec order within this one.
//
// Validity, not acceptance, is what the ListenerSet's own acceptance is counted
// from. An entry whose certificateRef is not permitted stays accepted -- the API
// keeps route attachment independent of ResolvedRefs -- but it cannot be
// configured, and a ListenerSet whose only entry is in that state is not accepted
// either. Programmation is read from the entry's own Programmed condition: pending
// until the apply answers, true once it does, and false on an entry the checks
// refused.
func rollUpListenerSetEntries(parentGw *Gateway, lsKey client.ObjectKey, ownerGeneration int64) listenerSetEntryRollUp {
	var rollUp listenerSetEntryRollUp

	forEachListenerDeclaredByListenerSet(parentGw, lsKey, func(listener *Listener) {
		rollUp.total++
		if listener.Valid {
			rollUp.valid++
		}
		if programmed, ok := listener.programmedCondition(); ok {
			switch programmed.Status {
			case metav1.ConditionTrue:
				rollUp.programmed++
			case metav1.ConditionUnknown:
				rollUp.pending++
			}
		}

		rollUp.statuses = append(rollUp.statuses, ListenerEntryStatus{
			Name:           listener.Name(),
			Conditions:     listenerConditionsForStatus(listener.Conditions, ownerGeneration),
			SupportedKinds: listener.AllowedRouteKinds,
			AttachedRoutes: int32(len(listener.AttachedRoutes)),
		})
	})

	return rollUp
}

// forEachListenerDeclaredByListenerSet walks the listeners a Gateway's merged list holds for
// one ListenerSet, in that list's order: the Gateway's own listeners first, then ListenerSets
// oldest first, and so spec order within this one.
//
// It selects on the declaring resource, not on a purpose: what a caller does with the walk --
// roll the entries up, stamp a condition, or anything else -- is the caller's business.
func forEachListenerDeclaredByListenerSet(parentGw *Gateway, lsKey client.ObjectKey, fn func(*Listener)) {
	if parentGw == nil {
		return
	}
	for _, listener := range parentGw.Listeners {
		if listener.Key().ListenerSetKey != lsKey {
			continue
		}
		fn(listener)
	}
}

// rollUpListenerSetVerdict settles a ListenerSet's two conditions from its entries.
//
// Both are "some" summaries, which is what the API asks them to be and what the
// conformance suite checks:
//
//   - Accepted is true as soon as one entry can be served, and carries
//     ListenersNotValid when some entries cannot -- the API states that reason may
//     sit on either polarity, "depending on whether the listener being invalid
//     causes the entire ListenerSet to not be accepted". It is false only when no
//     entry is left to serve.
//   - Programmed is true as soon as one entry is programmed: the API's wording is
//     "has generated some configuration", so an entry a conflict refuses does not
//     deny the configuration the others produced. It stays Unknown while entries
//     are still waiting for HAProxy, and is false with ListenersNotValid only when
//     nothing could be programmed at all.
//
// What Accepted counts is validity, not attachment: see entryRollUp.
func rollUpListenerSetVerdict(treeLs *ListenerSet, rollUp listenerSetEntryRollUp) {
	switch {
	case rollUp.total == 0:
		// Allowed and parent accepted, yet no entry in the merged list. The spec
		// declares at least one entry (MinItems=1), so this is an inconsistency
		// rather than a state: Accepted is vacuously true, and Programmed stays
		// Unknown because nothing was in fact programmed.
		treeLs.Conditions.MergeOverrideConditions(conditions.NewListenerSetAcceptedOK())
		treeLs.Conditions.MergeOverrideConditions(conditions.NewListenerSetProgrammedPending())
		return
	case rollUp.valid == 0:
		treeLs.Conditions.MergeOverrideConditions(conditions.NewListenerSetAcceptedAllListenersInvalid())
	case rollUp.valid < rollUp.total:
		treeLs.Conditions.MergeOverrideConditions(conditions.NewListenerSetAcceptedSomeListenersInvalid())
	default:
		treeLs.Conditions.MergeOverrideConditions(conditions.NewListenerSetAcceptedOK())
	}

	switch {
	case rollUp.programmed > 0:
		treeLs.Conditions.MergeOverrideConditions(conditions.NewListenerSetProgrammedOK())
	case rollUp.pending > 0:
		treeLs.Conditions.MergeOverrideConditions(conditions.NewListenerSetProgrammedPending())
	default:
		treeLs.Conditions.MergeOverrideConditions(conditions.NewListenerSetProgrammedListenersNotValid())
	}
}

// listenerConditionsForStatus copies a listener's conditions and stamps them with
// the generation of the resource that owns them.
//
// BuildConditions stamps every listener with the generation of the Gateway it is
// served on, which is right for a listener the Gateway declares and wrong for an
// entry: an entry's conditions are published in its ListenerSet's status and
// compared against that object's metadata.generation. Re-stamping here, where the
// owning resource is known, keeps the correction in the pass that writes the status
// rather than spreading it into the checks.
func listenerConditionsForStatus(listenerConditions generic.Conditions, ownerGeneration int64) generic.Conditions {
	copied := make(generic.Conditions, len(listenerConditions))
	maps.Copy(copied, listenerConditions)
	copied.SetGeneration(ownerGeneration)
	return copied
}

// conditionStatusIs reports whether a condition of the given type is present with
// the given status. The type is passed as a string because the two resources
// involved here -- the parent Gateway and the ListenerSet -- spell their
// conditions with distinct Go types that happen to share the values.
func conditionStatusIs(conds generic.Conditions, conditionType string, status metav1.ConditionStatus) bool {
	condition, ok := conds.GetCondition(generic.ConditionType(conditionType))
	return ok && condition.Status == status
}
