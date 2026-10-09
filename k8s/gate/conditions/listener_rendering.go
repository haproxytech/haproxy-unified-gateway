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
	"fmt"
	"maps"

	generic "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// listenerConditionSpelling is how one vocabulary spells one verdict. The two type
// parameters are the point of the whole construct: a table instantiated on the
// Gateway's condition types cannot be filled with a ListenerEntry reason, or the
// other way round, because that does not compile. Only the status and the default
// message are shared, being properties of the situation rather than of the
// vocabulary.
type listenerConditionSpelling[ConditionType ~string, ConditionReason ~string] struct {
	ConditionType   ConditionType
	ConditionReason ConditionReason
}

// isConflict reports whether a verdict states a conflict, which is the one case
// where a verdict produces two conditions: the Conflicted condition asserting the
// conflict, and the Accepted condition refusing the listener because of it. The
// Gateway API requires both, so the rule lives here once instead of in each
// vocabulary.
func (v ListenerConditionVerdict) isConflict() bool {
	return v == VerdictHostnameConflict ||
		v == VerdictProtocolConflict ||
		v == VerdictSeveralConflicts
}

// conditionsForVerdict is how one vocabulary reports one verdict: the conditions a
// resource publishes for it.
//
// A verdict the vocabulary cannot spell is an error and not an empty result. The
// difference matters because an empty condition set is a legitimate value
// elsewhere -- it is what a check that has nothing to report produces -- so
// returning one here would make a missing table entry indistinguishable from
// "this listener is fine", and the caller would go on to publish it as accepted.
func conditionsForVerdict[ConditionType ~string, ConditionReason ~string](
	conditionSpellingsByVerdict map[ListenerConditionVerdict]listenerConditionSpelling[ConditionType, ConditionReason],
	verdict ListenerConditionVerdict,
	msg string,
) (generic.Conditions, error) {
	spelling, ok := conditionSpellingsByVerdict[verdict]
	if !ok {
		return nil, fmt.Errorf("no condition spelling for listener verdict %q", verdict)
	}

	conditionType := generic.ConditionType(spelling.ConditionType)
	conditions := generic.Conditions{
		conditionType: {
			Type:    conditionType,
			Status:  listenerConditionVerdictStatuses[verdict],
			Reason:  string(spelling.ConditionReason),
			Message: verdict.message(msg),
		},
	}

	if verdict.isConflict() {
		// The companion is a constant of this package, so a table missing it is the
		// same programming error as above and is reported the same way.
		companion, err := conditionsForVerdict(conditionSpellingsByVerdict, VerdictRejectedByConflict, "")
		if err != nil {
			return nil, err
		}
		maps.Copy(conditions, companion)
	}

	return conditions, nil
}

// gatewayListenerConditionSpellings spells the verdicts in the vocabulary of the listeners
// a Gateway declares itself, for status.listeners[].conditions.
var gatewayListenerConditionSpellings = map[ListenerConditionVerdict]listenerConditionSpelling[gatewayv1.ListenerConditionType, gatewayv1.ListenerConditionReason]{
	VerdictAccepted: {
		ConditionType:   gatewayv1.ListenerConditionAccepted,
		ConditionReason: gatewayv1.ListenerReasonAccepted,
	},
	VerdictUnsupportedProtocol: {
		ConditionType:   gatewayv1.ListenerConditionAccepted,
		ConditionReason: gatewayv1.ListenerReasonUnsupportedProtocol,
	},
	VerdictUnsupportedValue: {
		ConditionType:   gatewayv1.ListenerConditionAccepted,
		ConditionReason: gatewayv1.ListenerReasonUnsupportedValue,
	},
	// Invalid is what this controller has always published here, and it is only
	// tolerated: for Accepted=False the API declares PortUnavailable,
	// UnsupportedProtocol, NoValidCACertificate and UnsupportedValue, and asks
	// controllers to prefer those "to improve interoperability". PortUnavailable is
	// documented as covering a port that "cannot be used on the Gateway", with an
	// explicitly open list of instances -- which is this case, the process lacking
	// CAP_NET_BIND_SERVICE. Switching is a change to a published status, so it is
	// held with the other Accepted-semantics decisions rather than slipped in here;
	// no test asserts this reason, so the change costs nothing but the decision.
	VerdictPortNotBindable: {
		ConditionType:   gatewayv1.ListenerConditionAccepted,
		ConditionReason: gatewayv1.ListenerReasonInvalid,
	},
	VerdictRejectedByConflict: {
		ConditionType:   gatewayv1.ListenerConditionAccepted,
		ConditionReason: gatewayv1.ListenerReasonInvalid,
	},
	VerdictUnreportable: {
		ConditionType:   gatewayv1.ListenerConditionAccepted,
		ConditionReason: gatewayv1.ListenerReasonInvalid,
	},
	VerdictRefsResolved: {
		ConditionType:   gatewayv1.ListenerConditionResolvedRefs,
		ConditionReason: gatewayv1.ListenerReasonResolvedRefs,
	},
	VerdictInvalidRouteKinds: {
		ConditionType:   gatewayv1.ListenerConditionResolvedRefs,
		ConditionReason: gatewayv1.ListenerReasonInvalidRouteKinds,
	},
	VerdictInvalidCertificateRef: {
		ConditionType:   gatewayv1.ListenerConditionResolvedRefs,
		ConditionReason: gatewayv1.ListenerReasonInvalidCertificateRef,
	},
	VerdictRefNotPermitted: {
		ConditionType:   gatewayv1.ListenerConditionResolvedRefs,
		ConditionReason: gatewayv1.ListenerReasonRefNotPermitted,
	},
	VerdictHostnameConflict: {
		ConditionType:   gatewayv1.ListenerConditionConflicted,
		ConditionReason: gatewayv1.ListenerReasonHostnameConflict,
	},
	VerdictProtocolConflict: {
		ConditionType:   gatewayv1.ListenerConditionConflicted,
		ConditionReason: gatewayv1.ListenerReasonProtocolConflict,
	},
	VerdictProgrammed: {
		ConditionType:   gatewayv1.ListenerConditionProgrammed,
		ConditionReason: gatewayv1.ListenerReasonProgrammed,
	},
	VerdictProgrammingPending: {
		ConditionType:   gatewayv1.ListenerConditionProgrammed,
		ConditionReason: gatewayv1.ListenerReasonPending,
	},
	VerdictNotProgrammable: {
		ConditionType:   gatewayv1.ListenerConditionProgrammed,
		ConditionReason: gatewayv1.ListenerReasonInvalid,
	},
}

// gatewayListenerVerdictsWithoutSpelling are the verdicts the vocabulary of a
// Gateway's own listeners has no reason for. Declaring the gap is what lets the
// exhaustiveness test tell a deliberate absence from a forgotten entry, which it
// otherwise could not: both look like a missing key.
//
// Nothing may derive one of these for a Gateway listener. Asking the table for it
// is an error, so the fallback would reject the listener under VerdictUnreportable
// -- a listener conflicting on two counts reported as unreportable. The derivation
// is therefore where the vocabulary is known, on the Listener itself, and it never
// produces a verdict its own vocabulary cannot spell.
var gatewayListenerVerdictsWithoutSpelling = map[ListenerConditionVerdict]struct{}{
	// The API gives entries ListenerConflict for a listener in several conflicts and
	// gives a Gateway's listeners no equivalent: for Conflicted it declares only
	// HostnameConflict and ProtocolConflict. A Gateway listener in both therefore
	// reports the coarser of the two, and its message names the rest.
	VerdictSeveralConflicts: {},
}

// listenerEntryVerdictsWithoutSpelling is the same declaration for the entry
// vocabulary: a verdict this vocabulary has no reason for.
//
// TooManyListeners is the mirror image -- a verdict only an entry can carry -- and is
// not listed here: it is not this controller's to conclude yet.
var listenerEntryVerdictsWithoutSpelling = map[ListenerConditionVerdict]struct{}{
	// The API gives a Gateway's listeners UnsupportedValue for a protocol it serves
	// in another form only (a TLS listener asking for a mode other than Passthrough)
	// and gives entries no such reason: an entry has UnsupportedProtocol alone.
	VerdictUnsupportedValue: {},
}

// listenerEntryConditionSpellings spells the verdicts in the vocabulary of the entries a
// ListenerSet contributes, for its status.listeners[].conditions. Same values as
// the Gateway table for every verdict so far, and separate types so that a future
// divergence has to be spelled out here rather than inherited by accident.
var listenerEntryConditionSpellings = map[ListenerConditionVerdict]listenerConditionSpelling[gatewayv1.ListenerEntryConditionType, gatewayv1.ListenerEntryConditionReason]{
	VerdictAccepted: {
		ConditionType:   gatewayv1.ListenerEntryConditionAccepted,
		ConditionReason: gatewayv1.ListenerEntryReasonAccepted,
	},
	VerdictUnsupportedProtocol: {
		ConditionType:   gatewayv1.ListenerEntryConditionAccepted,
		ConditionReason: gatewayv1.ListenerEntryReasonUnsupportedProtocol,
	},
	VerdictPortNotBindable: {
		ConditionType:   gatewayv1.ListenerEntryConditionAccepted,
		ConditionReason: gatewayv1.ListenerEntryReasonInvalid,
	},
	VerdictRejectedByConflict: {
		ConditionType:   gatewayv1.ListenerEntryConditionAccepted,
		ConditionReason: gatewayv1.ListenerEntryReasonInvalid,
	},
	VerdictUnreportable: {
		ConditionType:   gatewayv1.ListenerEntryConditionAccepted,
		ConditionReason: gatewayv1.ListenerEntryReasonInvalid,
	},
	VerdictRefsResolved: {
		ConditionType:   gatewayv1.ListenerEntryConditionResolvedRefs,
		ConditionReason: gatewayv1.ListenerEntryReasonResolvedRefs,
	},
	VerdictInvalidRouteKinds: {
		ConditionType:   gatewayv1.ListenerEntryConditionResolvedRefs,
		ConditionReason: gatewayv1.ListenerEntryReasonInvalidRouteKinds,
	},
	VerdictInvalidCertificateRef: {
		ConditionType:   gatewayv1.ListenerEntryConditionResolvedRefs,
		ConditionReason: gatewayv1.ListenerEntryReasonInvalidCertificateRef,
	},
	VerdictRefNotPermitted: {
		ConditionType:   gatewayv1.ListenerEntryConditionResolvedRefs,
		ConditionReason: gatewayv1.ListenerEntryReasonRefNotPermitted,
	},
	VerdictHostnameConflict: {
		ConditionType:   gatewayv1.ListenerEntryConditionConflicted,
		ConditionReason: gatewayv1.ListenerEntryReasonHostnameConflict,
	},
	VerdictProtocolConflict: {
		ConditionType:   gatewayv1.ListenerEntryConditionConflicted,
		ConditionReason: gatewayv1.ListenerEntryReasonProtocolConflict,
	},
	VerdictSeveralConflicts: {
		ConditionType:   gatewayv1.ListenerEntryConditionConflicted,
		ConditionReason: gatewayv1.ListenerEntryReasonListenerConflict,
	},
	VerdictProgrammed: {
		ConditionType:   gatewayv1.ListenerEntryConditionProgrammed,
		ConditionReason: gatewayv1.ListenerEntryReasonProgrammed,
	},
	VerdictProgrammingPending: {
		ConditionType:   gatewayv1.ListenerEntryConditionProgrammed,
		ConditionReason: gatewayv1.ListenerEntryReasonPending,
	},
	VerdictNotProgrammable: {
		ConditionType:   gatewayv1.ListenerEntryConditionProgrammed,
		ConditionReason: gatewayv1.ListenerEntryReasonInvalid,
	},
}

// NewGatewayListenerConditions reports a verdict on a listener a Gateway declares
// itself. msg overrides the verdict's default wording; pass "" to keep it.
func NewGatewayListenerConditions(verdict ListenerConditionVerdict, msg string) (generic.Conditions, error) {
	return conditionsForVerdict(gatewayListenerConditionSpellings, verdict, msg)
}

// NewListenerEntryConditions reports a verdict on an entry a ListenerSet
// contributes. msg overrides the verdict's default wording; pass "" to keep it.
func NewListenerEntryConditions(verdict ListenerConditionVerdict, msg string) (generic.Conditions, error) {
	return conditionsForVerdict(listenerEntryConditionSpellings, verdict, msg)
}

// NewGatewayListenerProgrammedCondition reports the outcome of programming a
// listener of a Gateway in HAProxy, stamped with the generation the apply was made
// against.
//
// It returns a single condition rather than a set because its caller writes it
// into an existing condition map under a staleness guard, comparing observed
// generations. The type and reason still come from the rendering table, so this
// path cannot drift from the ones that publish the same condition earlier in the
// cycle.
func NewGatewayListenerProgrammedCondition(msg string, observedGeneration int64, programmed bool) (generic.Condition, error) {
	verdict := VerdictProgrammed
	if !programmed {
		verdict = VerdictNotProgrammable
	}
	rendered, err := NewGatewayListenerConditions(verdict, msg)
	if err != nil {
		return generic.Condition{}, err
	}
	condition := rendered[generic.ConditionType(gatewayListenerConditionSpellings[verdict].ConditionType)]
	condition.ObservedGeneration = observedGeneration
	return condition, nil
}
