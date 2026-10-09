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

	condType := generic.ConditionType(gatewayv1.ListenerConditionProgrammed)

	conditions := make(map[ObservedOwner]generic.Condition, len(observed))
	for owner, observedGen := range observed {
		newCond, err := buildListenerProgrammedCondition(observedGen, applyErr)
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
			continue
		}
		conditions[owner] = newCond
	}

	for _, gw := range t.Gateways {
		if gw == nil {
			continue
		}
		for _, listener := range gw.Listeners {
			// If the Listener was not accepted, then it was not Programmed.
			if !listener.Valid {
				continue
			}

			owner := ObservedOwner{Kind: listener.OwnerKind, Key: listener.Key().OwnerKey()}
			newCond, programmed := conditions[owner]
			if !programmed {
				// Not part of this apply: never programmed from this result.
				continue
			}

			if existing, exists := listener.Conditions.GetCondition(condType); exists {
				if observed[owner] < existing.ObservedGeneration {
					continue // stale: a newer result already set this condition
				}
			}
			listener.Conditions[condType] = newCond
		}
	}
}

// RLockGateways acquires a read lock on the GateTree and returns the Gateways
// map. The caller must call RUnlockGateways when done reading.
func (t *GateTree) RLockGateways() map[types.NamespacedName]*Gateway {
	t.mu.RLock()
	return t.Gateways
}

// RUnlockGateways releases the read lock acquired by RLockGateways.
func (t *GateTree) RUnlockGateways() {
	t.mu.RUnlock()
}

func buildListenerProgrammedCondition(observedGen int64, applyErr error) (generic.Condition, error) {
	if applyErr == nil {
		return conditions.NewGatewayListenerProgrammedCondition("", observedGen, true)
	}
	msg := applyErr.Error()
	// Current MaxLength of the Message field in a Condition is 32768 characters.
	// Truncate if necessary to avoid issues with Kubernetes API rejections.
	const maxLen = 32768
	if len(msg) > maxLen {
		msg = msg[:maxLen]
	}
	return conditions.NewGatewayListenerProgrammedCondition(msg, observedGen, false)
}
