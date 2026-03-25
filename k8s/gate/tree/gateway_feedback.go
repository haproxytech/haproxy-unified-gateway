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
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// UpdateListenerProgrammedCondition updates the Programmed condition for all
// listeners of the given gateway using the result of an HAProxy apply cycle.
// The update is skipped for listeners whose current Programmed condition already
// has a higher ObservedGeneration than observedGen (stale result guard).
// Thread-safe: acquires the GateTree write lock.
func (t *GateTree) UpdateListenerProgrammedCondition(gwKey types.NamespacedName, observedGen int64, applyErr error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	gw, ok := t.Gateways[gwKey]
	if !ok {
		return
	}

	condType := generic.ConditionType(gatewayv1.ListenerConditionProgrammed)
	newCond := buildListenerProgrammedCondition(condType, observedGen, applyErr)

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

func buildListenerProgrammedCondition(condType generic.ConditionType, observedGen int64, applyErr error) generic.Condition {
	if applyErr == nil {
		return generic.Condition{
			Type:               condType,
			Status:             metav1.ConditionTrue,
			Reason:             string(gatewayv1.ListenerReasonProgrammed),
			Message:            "Listener is programmed in Haproxy",
			ObservedGeneration: observedGen,
		}
	}
	msg := applyErr.Error()
	// Current MaxLength of the Message field in a Condition is 32768 characters.
	// Truncate if necessary to avoid issues with Kubernetes API rejections.
	const maxLen = 32768
	if len(msg) > maxLen {
		msg = msg[:maxLen]
	}
	return generic.Condition{
		Type:               condType,
		Status:             metav1.ConditionFalse,
		Reason:             string(gatewayv1.ListenerReasonInvalid),
		Message:            msg,
		ObservedGeneration: observedGen,
	}
}
