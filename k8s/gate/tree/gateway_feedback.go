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

// UpdateListenerProgrammedCondition updates the Programmed condition for all
// listeners of the given gateway using the result of an HAProxy apply cycle.
// The update is skipped for listeners whose current Programmed condition already
// has a higher ObservedGeneration than observedGen (stale result guard).
// Thread-safe: acquires the GateTree write lock.
//
// It takes a logger because it is the one condition writer with nowhere to put a
// failure: it either writes the Programmed condition or leaves the previous one
// alone, so a problem building that condition would otherwise leave no trace at
// all.
func (t *GateTree) UpdateListenerProgrammedCondition(logger *slog.Logger, gwKey types.NamespacedName, observedGen int64, applyErr error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	gw, ok := t.Gateways[gwKey]
	if !ok {
		// Legitimately silent, unlike the error below: the feedback arrives after the
		// apply, and the Gateway may have been deleted in between. There is nothing
		// left to report a Programmed condition on.
		return
	}

	condType := generic.ConditionType(gatewayv1.ListenerConditionProgrammed)
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
			"Cannot report the HAProxy apply result on a Gateway's listeners",
			logging.LogAttrKey(gwKey),
			logging.LogAttrError(err),
		)
		return
	}

	for _, listener := range gw.Listeners {
		// If the Listener was not accepted, then it was not Programmed.
		if !listener.Valid {
			continue
		}

		if existing, exists := listener.Conditions.GetCondition(condType); exists {
			if observedGen < existing.ObservedGeneration {
				continue // stale: a newer result already set this condition
			}
		}
		listener.Conditions[condType] = newCond
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
