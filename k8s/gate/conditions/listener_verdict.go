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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ListenerConditionVerdict is what a listener check concludes: one value per
// situation this controller can detect on a listener, in the domain of the
// conditions a resource publishes about it.
//
// It is deliberately this project's own vocabulary and not the Gateway API's,
// because the API has two vocabularies for these situations --
// ListenerConditionReason for the listeners a Gateway declares,
// ListenerEntryConditionReason for the entries a ListenerSet contributes -- and a
// check has no way of knowing which one its listener will be reported in.
//
// The name sits in that family on purpose: same subject, same domain, and
// "verdict" for the role no API type has, which is the word that marks this one as
// ours. What the type is independent of is the vocabulary, not the domain -- every
// value here maps to exactly one condition type and reason, which is why it lives
// in this package.
//
// The two API vocabularies share most of their values today, which is the trap
// this type exists to close: metav1.Condition.Reason is a bare string, so once a
// constant of either family is converted nothing ties the value back to the family
// it came from, and an upstream divergence would break neither the build nor a
// test. The coincidence is already partial in v1.5.0 -- ListenerConflict and
// TooManyListeners exist only for entries, NoConflicts and NoValidCACertificate
// only for a Gateway's listeners.
type ListenerConditionVerdict string

const (
	// VerdictAccepted is the listener being usable as configured.
	VerdictAccepted ListenerConditionVerdict = "Accepted"
	// VerdictUnsupportedProtocol is a protocol this controller does not serve.
	VerdictUnsupportedProtocol ListenerConditionVerdict = "UnsupportedProtocol"
	// VerdictUnsupportedValue is a protocol this controller serves, but not in the
	// form asked for -- a TLS listener requesting a mode other than Passthrough.
	//
	// Only the Gateway vocabulary has a reason for it: the API gives a Gateway's
	// listeners UnsupportedValue and gives the entries a ListenerSet contributes only
	// UnsupportedProtocol, so an entry reports that instead.
	VerdictUnsupportedValue ListenerConditionVerdict = "UnsupportedValue"
	// VerdictPortNotBindable is the controller process having no permission to bind
	// the requested port.
	VerdictPortNotBindable ListenerConditionVerdict = "PortNotBindable"
	// VerdictRejectedByConflict is a listener refused acceptance because it
	// conflicts with another one; the conflict itself is reported separately, by
	// one of the conflict verdicts below.
	VerdictRejectedByConflict ListenerConditionVerdict = "RejectedByConflict"
	// VerdictUnreportable is this controller finding itself unable to describe a
	// listener: the verdict a check concluded has no spelling in the vocabulary the
	// listener is reported in. The listener is refused acceptance and the message
	// carries the gap, because reporting nothing would publish it as accepted, then
	// as programmed, while nothing serves it.
	//
	// It is named after that situation and not after the reason it renders as. Three
	// other verdicts render as Invalid too -- two under Accepted, one under
	// Programmed -- so a verdict called "Invalid" would say which reason it produces
	// while leaving unsaid which condition it hangs from, and would read as the
	// generic invalid that anything could reuse. That is the one mistake this table
	// cannot catch: the key would still be distinct, and the wrong condition would be
	// published silently.
	VerdictUnreportable ListenerConditionVerdict = "Unreportable"

	// VerdictRefsResolved is every object reference of the listener resolving to an
	// object it is allowed to reach.
	VerdictRefsResolved ListenerConditionVerdict = "RefsResolved"
	// VerdictInvalidRouteKinds is the listener allowing a Route kind that cannot be
	// served on its protocol.
	VerdictInvalidRouteKinds ListenerConditionVerdict = "InvalidRouteKinds"
	// VerdictInvalidCertificateRef covers a certificateRef this controller cannot
	// use: absent, of an unsupported kind, or holding an unusable certificate.
	VerdictInvalidCertificateRef ListenerConditionVerdict = "InvalidCertificateRef"
	// VerdictRefNotPermitted is a reference reaching into another namespace with no
	// ReferenceGrant permitting it.
	VerdictRefNotPermitted ListenerConditionVerdict = "RefNotPermitted"

	// VerdictHostnameConflict is two listeners on one port claiming the same
	// hostname.
	VerdictHostnameConflict ListenerConditionVerdict = "HostnameConflict"
	// VerdictProtocolConflict is two listeners on one port asking for protocols
	// that cannot share it.
	VerdictProtocolConflict ListenerConditionVerdict = "ProtocolConflict"
	// VerdictSeveralConflicts is a listener in more than one kind of conflict at
	// once -- on its protocol and on its hostname.
	//
	// Only a ListenerSet entry can report it: the API gives entries the reason
	// ListenerConflict for exactly this case and gives a Gateway's own listeners no
	// word for it, which is why it is the first verdict one vocabulary cannot spell.
	// Nothing may derive it for a Gateway listener -- see the note on the absent
	// spelling.
	VerdictSeveralConflicts ListenerConditionVerdict = "SeveralConflicts"

	// VerdictProgrammed is the listener being configured in HAProxy.
	VerdictProgrammed ListenerConditionVerdict = "Programmed"
	// VerdictProgrammingPending is the listener being accepted, with HAProxy not
	// reflecting it yet.
	VerdictProgrammingPending ListenerConditionVerdict = "ProgrammingPending"
	// VerdictNotProgrammable is the listener not being configured in HAProxy
	// because one of the checks above rejected it.
	VerdictNotProgrammable ListenerConditionVerdict = "NotProgrammable"
)

// listenerConditionVerdicts is every verdict, so that the rendering tables can be checked
// for completeness. A verdict absent from this list is invisible to that check, so
// it is the one place to update when adding one.
var listenerConditionVerdicts = []ListenerConditionVerdict{
	VerdictAccepted,
	VerdictUnsupportedProtocol,
	VerdictUnsupportedValue,
	VerdictPortNotBindable,
	VerdictRejectedByConflict,
	VerdictUnreportable,
	VerdictRefsResolved,
	VerdictInvalidRouteKinds,
	VerdictInvalidCertificateRef,
	VerdictRefNotPermitted,
	VerdictHostnameConflict,
	VerdictProtocolConflict,
	VerdictSeveralConflicts,
	VerdictProgrammed,
	VerdictProgrammingPending,
	VerdictNotProgrammable,
}

// listenerConditionVerdictStatuses is the condition status each verdict is reported with.
// It is shared by both vocabularies: whether a situation is a rejection is a
// property of the situation, not of the resource reporting it. Only the type and
// the reason are spelled differently.
var listenerConditionVerdictStatuses = map[ListenerConditionVerdict]metav1.ConditionStatus{
	VerdictAccepted:              metav1.ConditionTrue,
	VerdictUnsupportedProtocol:   metav1.ConditionFalse,
	VerdictUnsupportedValue:      metav1.ConditionFalse,
	VerdictPortNotBindable:       metav1.ConditionFalse,
	VerdictRejectedByConflict:    metav1.ConditionFalse,
	VerdictUnreportable:          metav1.ConditionFalse,
	VerdictRefsResolved:          metav1.ConditionTrue,
	VerdictInvalidRouteKinds:     metav1.ConditionFalse,
	VerdictInvalidCertificateRef: metav1.ConditionFalse,
	VerdictRefNotPermitted:       metav1.ConditionFalse,
	// A Conflicted condition is asserted, not denied: True means there is a
	// conflict.
	VerdictHostnameConflict:   metav1.ConditionTrue,
	VerdictProtocolConflict:   metav1.ConditionTrue,
	VerdictSeveralConflicts:   metav1.ConditionTrue,
	VerdictProgrammed:         metav1.ConditionTrue,
	VerdictProgrammingPending: metav1.ConditionUnknown,
	VerdictNotProgrammable:    metav1.ConditionFalse,
}

// listenerConditionVerdictsRequiringMessage are the verdicts whose whole content is the
// message: they say that one named object, field or peer listener is at fault, and
// a generic wording would drop the only part a user can act on. A check reporting
// one of them must supply its own message, which is why they have no default -- and
// the split between these and the table below is total, so a verdict can neither
// silently lose its wording nor be given a default that hides a call site that
// forgot to write one.
var listenerConditionVerdictsRequiringMessage = map[ListenerConditionVerdict]struct{}{
	VerdictUnreportable:          {},
	VerdictUnsupportedProtocol:   {},
	VerdictUnsupportedValue:      {},
	VerdictPortNotBindable:       {},
	VerdictInvalidRouteKinds:     {},
	VerdictInvalidCertificateRef: {},
	VerdictRefNotPermitted:       {},
	VerdictHostnameConflict:      {},
	VerdictProtocolConflict:      {},
	VerdictSeveralConflicts:      {},
}

// listenerConditionVerdictMessages is the wording of the verdicts that describe a state
// rather than a culprit, and so read the same whatever produced them.
var listenerConditionVerdictMessages = map[ListenerConditionVerdict]string{
	VerdictAccepted:           "Listener is accepted",
	VerdictRejectedByConflict: "Listener is invalid (see ConditionType Conflicted)",
	VerdictRefsResolved:       "Listener references have been resolved",
	VerdictProgrammed:         "Listener is programmed in Haproxy",
	VerdictProgrammingPending: "Listener is pending Haproxy programmation",
	VerdictNotProgrammable:    "Listener is invalid",
}

func (v ListenerConditionVerdict) message(msg string) string {
	if msg != "" {
		return msg
	}
	return listenerConditionVerdictMessages[v]
}
