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
	// The count is a roll-up over every ListenerSet of the tree, so it is cleared
	// before the roll-up rather than patched up: a ListenerSet deleted this cycle
	// contributes nothing and must not leave its contribution behind in the count.
	b.resetAttachedListenerSetCounts()

	for _, treeLs := range b.GateTree.ListenerSets {
		b.computeListenerSetVerdict(treeLs)
	}
}

// CleanTreeUpdates owns nothing: ListenerSets are mirrored, and reaped when
// deleted, by the contribution pass, which is the one that puts them in the tree.
func (*ListenerSetVerdictBuilder) CleanTreeUpdates() {}

func (b *ListenerSetVerdictBuilder) resetAttachedListenerSetCounts() {
	for _, treeGw := range b.GateTree.Gateways {
		if treeGw != nil {
			treeGw.AttachedListenerSets = 0
		}
	}
}

// computeListenerSetVerdict settles a ListenerSet's own conditions, its per-entry
// status records, and its parent Gateway's attachedListenerSets.
func (b *ListenerSetVerdictBuilder) computeListenerSetVerdict(treeLs *ListenerSet) {
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
		treeLs.Conditions.MergeOverrideConditions(conditions.NewListenerSetProgrammedInvalid())
		return
	}
	if treeLs.Attachment != attachmentAllowed {
		// attachmentUnreported: the parentRef names a resource of another controller,
		// or no Gateway of this one. Writing a status here would be commenting on
		// someone else's resource, so nothing is written -- the refusal those cases
		// owe is to stay quiet, not to report.
		return
	}

	parentGw := b.parentGatewayOf(treeLs)
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
func (b *ListenerSetVerdictBuilder) parentGatewayOf(treeLs *ListenerSet) *Gateway {
	gwKey, namesGateway := parentGatewayKey(treeLs.K8sResource)
	if !namesGateway {
		return nil
	}
	return b.GateTree.Gateways[gwKey]
}

// listenerSetEntryRollUp is what a ListenerSet's own conditions are derived from:
// the per-entry records to publish, and the counts the resource-level conditions
// are a function of.
type listenerSetEntryRollUp struct {
	statuses   []ListenerEntryStatus
	total      int
	accepted   int
	programmed int
	pending    int
}

// entryRollUp walks the entries this ListenerSet contributed to its parent's
// merged list, in the merged list's order -- the Gateway's own listeners first,
// then ListenerSets oldest first -- and so in spec order within this one.
//
// Acceptance and programmation are read from two different places on purpose. An
// entry whose certificateRef is not permitted is Accepted but not Valid: the API
// keeps route attachment independent of ResolvedRefs, so the ListenerSet stays
// Accepted while its Programmed condition is false. Reading `Valid` for both would
// make a ReferenceGrant mistake refuse the whole ListenerSet.
func rollUpListenerSetEntries(parentGw *Gateway, lsKey client.ObjectKey, ownerGeneration int64) listenerSetEntryRollUp {
	var rollUp listenerSetEntryRollUp

	for _, listener := range parentGw.Listeners {
		if listener.Key().ListenerSetKey != lsKey {
			continue
		}

		rollUp.total++
		if listener.Accepted {
			rollUp.accepted++
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
	}

	return rollUp
}

// rollUpListenerSetVerdict derives the ListenerSet's own Accepted and Programmed
// conditions from its entries.
//
// Accepted keeps the API's allowance that ListenersNotValid may sit on either
// polarity: it is false only when no entry is left to serve, and true when some
// entries still are. That is the same reading the Gateway uses for its own
// listeners, where a Gateway with one valid listener stays accepted.
func rollUpListenerSetVerdict(treeLs *ListenerSet, rollUp listenerSetEntryRollUp) {
	switch {
	case rollUp.total == 0:
		// Allowed and parent accepted, yet no entry in the merged list. The spec
		// declares at least one entry (MinItems=1), so this is an inconsistency
		// rather than a state: Accepted is vacuously true, and Programmed stays
		// Unknown because nothing was in fact programmed.
		treeLs.Conditions.MergeOverrideConditions(conditions.NewListenerSetAcceptedOK())
		treeLs.Conditions.MergeOverrideConditions(conditions.NewListenerSetProgrammedPending())
	case rollUp.accepted == 0:
		treeLs.Conditions.MergeOverrideConditions(conditions.NewListenerSetAcceptedAllListenersInvalid())
		treeLs.Conditions.MergeOverrideConditions(conditions.NewListenerSetProgrammedListenersNotValid())
	case rollUp.accepted < rollUp.total:
		treeLs.Conditions.MergeOverrideConditions(conditions.NewListenerSetAcceptedSomeListenersInvalid())
		treeLs.Conditions.MergeOverrideConditions(conditions.NewListenerSetProgrammedListenersNotValid())
	case rollUp.programmed == rollUp.total:
		treeLs.Conditions.MergeOverrideConditions(conditions.NewListenerSetAcceptedOK())
		treeLs.Conditions.MergeOverrideConditions(conditions.NewListenerSetProgrammedOK())
	case rollUp.programmed+rollUp.pending == rollUp.total:
		// Every entry accepted, none refused by a check, and at least one is still
		// waiting for HAProxy.
		treeLs.Conditions.MergeOverrideConditions(conditions.NewListenerSetAcceptedOK())
		treeLs.Conditions.MergeOverrideConditions(conditions.NewListenerSetProgrammedPending())
	default:
		// Every entry accepted, and at least one is not programmable -- an
		// unresolved reference, for instance, which leaves Accepted true.
		treeLs.Conditions.MergeOverrideConditions(conditions.NewListenerSetAcceptedOK())
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
