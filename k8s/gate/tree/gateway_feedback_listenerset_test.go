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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// The apply feedback stamps Programmed on the listeners, and a ListenerSet's
// status.listeners rolls those conditions up. Three things follow, and each has been wrong
// in a different way: the roll-up must be taken again after the apply, in the entry's own
// vocabulary, and the count must not be taken twice for the same ListenerSet.
func TestProgrammedFeedbackRefreshesTheListenerSetRollUp(t *testing.T) {
	f := newVerdictFixture(t, true)
	f.addListenerSet("tenant", "ls", attachmentAllowed, 11, "http")
	entry := f.addContributedEntry("tenant", "ls", "http", true, metav1.ConditionUnknown)
	entry.Valid = true

	// The verdict pass runs before the apply: the entry is Pending, and the ListenerSet is
	// counted once.
	f.builder.ComputeTreeUpdates()
	lsKey := client.ObjectKey{Namespace: "tenant", Name: "ls"}
	treeLs := f.store.GateTree.ListenerSets[lsKey]
	require.Len(t, treeLs.Entries, 1)
	require.Equal(t, metav1.ConditionUnknown,
		conditionOf(t, treeLs.Conditions, string(gatewayv1.ListenerSetConditionProgrammed)).Status)
	require.Equal(t, int32(1), f.gw.AttachedListenerSets)

	// The apply succeeded, against ListenerSet generation 11.
	f.store.GateTree.UpdateListenerProgrammedConditions(
		f.store.Logger,
		ObservedGenerations{{Kind: OwnerKindListenerSet, Key: lsKey}: 11},
		nil,
	)

	programmed := conditionOf(t, entry.Conditions, string(gatewayv1.ListenerEntryConditionProgrammed))
	assert.Equal(t, metav1.ConditionTrue, programmed.Status)
	assert.Equal(t, int64(11), programmed.ObservedGeneration,
		"the entry answers to the ListenerSet, not to the Gateway serving it")

	assert.Equal(t, metav1.ConditionTrue,
		conditionOf(t, treeLs.Conditions, string(gatewayv1.ListenerSetConditionProgrammed)).Status,
		"the roll-up was taken before the apply and has to be taken again after it")
	assert.Equal(t, int32(1), f.gw.AttachedListenerSets,
		"re-taking the roll-up must not count the same ListenerSet twice")
}
