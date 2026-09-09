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
package conditions

import (
	"testing"

	generic "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// The typed tables make a vocabulary mix-up a compile error; this is the other
// half of the dispositive, and it catches the omission. A verdict missing from a
// table renders nothing at all, which would publish no condition where the API
// expects one -- a silent gap, visible only in a resource's status.
//
// An absence has to be *declared*, which is the whole point of the two
// "…WithoutSpelling" maps: a forgotten entry and a deliberate gap look identical to
// a map lookup, and only one of them is acceptable.
func TestEveryVerdictHasBothSpellings(t *testing.T) {
	for _, verdict := range listenerConditionVerdicts {
		t.Run(string(verdict), func(t *testing.T) {
			// The condition being asserted or denied is a property of the situation,
			// so a verdict must not be reported as True on one resource and False on
			// the other. An absent status would also read as an empty string, which
			// the API server rejects.
			status, ok := listenerConditionVerdictStatuses[verdict]
			assert.True(t, ok, "no condition status")
			assert.NotEmpty(t, status)

			gatewaySpelling, spelledForGateway := gatewayListenerConditionSpellings[verdict]
			_, absentForGateway := gatewayListenerVerdictsWithoutSpelling[verdict]
			assert.NotEqual(t, spelledForGateway, absentForGateway,
				"a verdict is either spelled for a Gateway's own listener or declared absent, never both or neither")

			entrySpelling, spelledForEntry := listenerEntryConditionSpellings[verdict]
			_, absentForEntry := listenerEntryVerdictsWithoutSpelling[verdict]
			assert.NotEqual(t, spelledForEntry, absentForEntry,
				"a verdict is either spelled for a ListenerSet entry or declared absent, never both or neither")

			if spelledForGateway {
				assert.NotEmpty(t, gatewaySpelling.ConditionType)
				assert.NotEmpty(t, gatewaySpelling.ConditionReason)
			}
			if spelledForEntry {
				assert.NotEmpty(t, entrySpelling.ConditionType)
				assert.NotEmpty(t, entrySpelling.ConditionReason)
			}

			// The two vocabularies must at least agree on which condition they hang
			// the verdict from: a situation reported under Accepted for a Gateway
			// listener and under ResolvedRefs for an entry would make the same
			// misconfiguration unrecognisable across two listeners of one frontend.
			if spelledForGateway && spelledForEntry {
				assert.Equal(t, string(gatewaySpelling.ConditionType), string(entrySpelling.ConditionType),
					"same verdict under two different condition types")
			}
		})
	}
}

// The one asymmetry so far, and the reason the declaration exists. Asserted on the
// tables rather than on prose so that a spelling appearing upstream, or being
// removed here, fails instead of quietly changing what a listener publishes.
func TestSeveralConflictsIsAnEntryOnlyVerdict(t *testing.T) {
	_, spelledForGateway := gatewayListenerConditionSpellings[VerdictSeveralConflicts]
	assert.False(t, spelledForGateway,
		"the API declares only HostnameConflict and ProtocolConflict for a Gateway listener")

	rendered, err := NewListenerEntryConditions(VerdictSeveralConflicts, "conflicts on protocol and hostname")
	assert.NoError(t, err)

	conflicted := generic.ConditionType(gatewayv1.ListenerEntryConditionConflicted)
	assert.Equal(t, string(gatewayv1.ListenerEntryReasonListenerConflict), rendered[conflicted].Reason)

	// And it carries the companion refusal like the single-kind conflicts do: without
	// it the entry would read as accepted while its port is configured nowhere.
	accepted := generic.ConditionType(gatewayv1.ListenerEntryConditionAccepted)
	assert.Equal(t, metav1.ConditionFalse, rendered[accepted].Status)
}

// Every verdict must be on exactly one side of the message split: either it names
// a culprit and its check owes a message, or it describes a state and carries its
// own wording. A verdict on neither side would publish an empty message; one on
// both would let a call site forget its message without anyone noticing.
func TestEveryVerdictSettlesItsMessage(t *testing.T) {
	for _, verdict := range listenerConditionVerdicts {
		t.Run(string(verdict), func(t *testing.T) {
			_, requiresMessage := listenerConditionVerdictsRequiringMessage[verdict]
			defaultMessage, hasDefault := listenerConditionVerdictMessages[verdict]

			assert.NotEqual(t, requiresMessage, hasDefault,
				"a verdict either owes its message to the check or carries a default, never both or neither")

			// A message from the check always wins, including over a default.
			assert.Equal(t, "a message from the check", verdict.message("a message from the check"))

			if hasDefault {
				assert.NotEmpty(t, defaultMessage)
				assert.Equal(t, defaultMessage, verdict.message(""))
			}
		})
	}
}

// A conflict is the one verdict that produces two conditions, because the Gateway
// API asks for both: Conflicted asserting the conflict, and Accepted refusing the
// listener because of it. Rendering only the first would leave a listener reported
// as accepted while its port is not configured anywhere.
func TestConflictAlsoRefusesAcceptance(t *testing.T) {
	conflicted := generic.ConditionType(gatewayv1.ListenerConditionConflicted)
	accepted := generic.ConditionType(gatewayv1.ListenerConditionAccepted)

	for _, verdict := range []ListenerConditionVerdict{VerdictHostnameConflict, VerdictProtocolConflict} {
		t.Run(string(verdict), func(t *testing.T) {
			rendered, err := NewGatewayListenerConditions(verdict, "Conflicting listeners: other")
			assert.NoError(t, err)

			assert.Len(t, rendered, 2)

			assert.Equal(t, metav1.ConditionTrue, rendered[conflicted].Status)
			assert.Equal(t, string(verdict), rendered[conflicted].Reason,
				"the reason names the kind of conflict")
			assert.Equal(t, "Conflicting listeners: other", rendered[conflicted].Message,
				"the check's message, which names the other listeners, is the one worth keeping")

			assert.Equal(t, metav1.ConditionFalse, rendered[accepted].Status)
			assert.Equal(t, string(gatewayv1.ListenerReasonInvalid), rendered[accepted].Reason)
			assert.Equal(t, "Listener is invalid (see ConditionType Conflicted)", rendered[accepted].Message,
				"the companion condition keeps its own wording, pointing at the Conflicted one")
		})
	}
}

// The entry vocabulary is not exercised by any production path yet, so these are
// the assertions that would fail if a rename upstream silently dropped one of its
// constants -- and what keeps the table from being deleted as unused.
func TestListenerEntryVocabularyIsUsed(t *testing.T) {
	rendered, err := NewListenerEntryConditions(VerdictUnsupportedProtocol, "TCP is not served here")
	assert.NoError(t, err)

	entryAccepted := generic.ConditionType(gatewayv1.ListenerEntryConditionAccepted)
	assert.Equal(t, metav1.ConditionFalse, rendered[entryAccepted].Status)
	assert.Equal(t, string(gatewayv1.ListenerEntryReasonUnsupportedProtocol), rendered[entryAccepted].Reason)
	assert.Equal(t, "TCP is not served here", rendered[entryAccepted].Message)
}

// A verdict no vocabulary can spell is an error, not an empty result. Returning an
// empty condition set would be indistinguishable from a check with nothing to
// report -- and a caller reading it that way publishes the listener as accepted.
func TestUnknownVerdictIsAnError(t *testing.T) {
	rendered, err := NewGatewayListenerConditions(ListenerConditionVerdict("NotAVerdict"), "ignored")
	assert.Error(t, err)
	assert.ErrorContains(t, err, "NotAVerdict", "the error has to name the verdict it could not spell")
	assert.Nil(t, rendered)

	rendered, err = NewListenerEntryConditions(ListenerConditionVerdict("NotAVerdict"), "ignored")
	assert.Error(t, err)
	assert.Nil(t, rendered)

	_, err = NewGatewayListenerProgrammedCondition("", 1, true)
	assert.NoError(t, err, "a constant verdict of this package always has a spelling")
}

// The programming feedback goes through the same table as every other publisher of
// the Programmed condition, and adds the generation the apply was made against --
// which is what lets its caller drop a result that a newer apply has superseded.
func TestProgrammedConditionCarriesTheObservedGeneration(t *testing.T) {
	programmed, err := NewGatewayListenerProgrammedCondition("", 7, true)
	assert.NoError(t, err)
	assert.Equal(t, generic.ConditionType(gatewayv1.ListenerConditionProgrammed), programmed.Type)
	assert.Equal(t, metav1.ConditionTrue, programmed.Status)
	assert.Equal(t, string(gatewayv1.ListenerReasonProgrammed), programmed.Reason)
	assert.Equal(t, "Listener is programmed in Haproxy", programmed.Message)
	assert.Equal(t, int64(7), programmed.ObservedGeneration)

	failed, err := NewGatewayListenerProgrammedCondition("socket refused the transaction", 8, false)
	assert.NoError(t, err)
	assert.Equal(t, generic.ConditionType(gatewayv1.ListenerConditionProgrammed), failed.Type)
	assert.Equal(t, metav1.ConditionFalse, failed.Status)
	assert.Equal(t, string(gatewayv1.ListenerReasonInvalid), failed.Reason)
	assert.Equal(t, "socket refused the transaction", failed.Message)
	assert.Equal(t, int64(8), failed.ObservedGeneration)
}
