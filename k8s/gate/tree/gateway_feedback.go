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
	"context"
	"fmt"
	"log/slog"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// ObservedOwner names a resource a frontend was programmed against: the Gateway it serves,
// or the ListenerSet that declared one of the entries it serves.
type ObservedOwner struct {
	Kind gatewayv1.Kind
	Key  types.NamespacedName
}

// String names an owner in one token: its kind, then its namespaced name.
func (o ObservedOwner) String() string {
	return string(o.Kind) + "/" + o.Key.String()
}

// MarshalText is what lets a map of owners be marshalled at all.
//
// The apply result is logged as a whole, and it travels to the controller as this map, and
// encoding/json refuses a map whose member name is not a string: without this the log reports
// "object member name must be a string" instead of the generations it was handed. The old
// shape -- a map keyed by NamespacedName, which has no MarshalText either -- had the same
// failure, so this is not new to the per-owner routing.
func (o ObservedOwner) MarshalText() ([]byte, error) {
	return []byte(o.String()), nil
}

// ObservedGenerations maps each resource a frontend was programmed against to the
// generation that programming was made from.
type ObservedGenerations map[ObservedOwner]int64

// UpdateListenerProgrammedConditions stamps the Programmed condition of every listener whose
// declaring resource was programmed in this apply cycle, with the generation of that
// resource.
//
// Each listener answers to the resource that declares it -- the Gateway for its own
// listeners, the ListenerSet for the entries it contributed. The generation to report, and
// the one the staleness guard compares, is therefore the owner's: a ListenerSet and its
// parent Gateway have unrelated generation counters, and stamping an entry with the
// Gateway's would publish an observedGeneration belonging to a resource that does not carry
// the condition.
//
// Thread-safe: acquires the GateTree write lock.
//
// It takes a logger because it is the one condition writer with nowhere to put a
// failure: it either writes the Programmed condition or leaves the previous one
// alone, so a problem building that condition would otherwise leave no trace at
// all.
func (t *GateTree) UpdateListenerProgrammedConditions(logger *slog.Logger, observed ObservedGenerations, applyErr error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(observed) == 0 {
		// Nothing was programmed: no listener owes a condition, and the roll-ups the verdict
		// pass took are still true.
		return
	}

	for owner, observedGen := range observed {
		// Resolve what the owner declares rather than walking the tree: the observed owners
		// are the frontends this apply wrote, a handful, while the tree holds every Gateway
		// the controller serves.
		t.forEachListenerDeclaredBy(owner, func(listener *Listener) {
			// If the Listener was not accepted, then it was not Programmed.
			if !listener.Valid {
				return
			}

			newCond, err := listenerProgrammedCondition(listener, observedGen, applyErr)
			if err != nil {
				// Leaving the existing condition alone is still the safe answer -- it says
				// Pending, the truth of an unconfirmed apply, where a zero-valued condition
				// would publish the empty Reason the API server rejects. But this path
				// deliberately writes nothing, so unlike the checks it has no status to carry
				// the gap: a listener would sit on Pending for ever with no trace anywhere.
				// The log is the only channel left, and this is a programming error -- the
				// verdict reported here is a constant of the conditions package -- so it is
				// worth an error level.
				logger.LogAttrs(context.Background(), slog.LevelError,
					"Cannot report the HAProxy apply result on a listener",
					logging.LogAttrKey(owner.Key),
					logging.LogAttrError(err),
				)
				return
			}

			if existing, exists := listener.Conditions.GetCondition(newCond.Type); exists {
				if observedGen < existing.ObservedGeneration {
					return // stale: a newer result already set this condition
				}
			}
			listener.Conditions[newCond.Type] = newCond
		})
	}

	// The entries a ListenerSet publishes roll up the conditions just stamped, so the
	// roll-up has to be taken again: the one the verdict pass left was computed before the
	// apply, and publishing it now would report the Pending the apply has answered.
	t.refreshListenerSetVerdicts()
}

// forEachListenerDeclaredBy calls fn for every listener the owner declares: a Gateway's own
// listeners, or the entries a ListenerSet contributed to its parent's merged list.
//
// Reading the owner's listeners rather than scanning the tree is what keeps the feedback
// proportional to what was programmed -- the observed owners are the frontends the apply
// wrote, while the tree holds every Gateway the controller serves.
func (t *GateTree) forEachListenerDeclaredBy(owner ObservedOwner, fn func(*Listener)) {
	if owner.Kind == OwnerKindListenerSet {
		treeLs := t.ListenerSets[owner.Key]
		if treeLs == nil || treeLs.K8sResource == nil {
			return
		}
		gwKey, namesGateway := parentGatewayKey(treeLs.K8sResource)
		if !namesGateway {
			return
		}
		forEachListenerDeclaredByListenerSet(t.Gateways[gwKey], owner.Key, fn)
		return
	}

	treeGw := t.Gateways[owner.Key]
	if treeGw == nil {
		return
	}
	for _, listener := range treeGw.Listeners {
		if listener.FromListenerSet() {
			continue
		}
		fn(listener)
	}
}

// RLockFeedbackView acquires a read lock on the GateTree and returns the collections the
// apply feedback resolves its observed owners against. The caller must call
// RUnlockFeedbackView when done reading.
//
// Both maps under one lock: taking a second read lock while the first is held can deadlock
// against a waiting writer, so the pair comes back together.
func (t *GateTree) RLockFeedbackView() (map[types.NamespacedName]*Gateway, map[types.NamespacedName]*ListenerSet) {
	t.mu.RLock()
	return t.Gateways, t.ListenerSets
}

// RUnlockFeedbackView releases the read lock acquired by RLockFeedbackView.
func (t *GateTree) RUnlockFeedbackView() {
	t.mu.RUnlock()
}

// listenerProgrammedCondition builds the Programmed condition an apply cycle owes a
// listener, in that listener's own vocabulary.
//
// An entry's condition is published in its ListenerSet's status and spelled with the entry
// type and reason; a Gateway listener's with the Gateway's. The spelling is what the
// listener already knows about itself -- the same table the checks go through -- so the
// verdict is rendered through conditionsFor rather than through a constructor that would
// fix the Gateway vocabulary for every listener.
func listenerProgrammedCondition(listener *Listener, observedGen int64, applyErr error) (generic.Condition, error) {
	verdict := conditions.VerdictProgrammed
	msg := ""
	if applyErr != nil {
		verdict = conditions.VerdictNotProgrammable
		msg = applyErr.Error()
		// Current MaxLength of the Message field in a Condition is 32768 characters.
		// Truncate if necessary to avoid issues with Kubernetes API rejections.
		const maxLen = 32768
		if len(msg) > maxLen {
			msg = msg[:maxLen]
		}
	}

	rendered := listener.conditionsFor(verdict, msg)
	for _, condition := range rendered {
		condition.ObservedGeneration = observedGen
		return condition, nil
	}
	return generic.Condition{}, fmt.Errorf("no condition rendered for verdict %s", verdict)
}
