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
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// The ListenerSet resource has its own condition vocabulary -- a third one, next
// to the Gateway's own listeners and the entries a ListenerSet contributes -- and
// it is not reached by the verdict tables, which spell *listener* situations. The
// constructors below are where gatewayv1.ListenerSetReason* constants are named,
// so that no call site has to know them.
//
// listenerSetReasonParentNotProgrammed is the reason the API documents for a
// ListenerSet's Programmed condition when its parent Gateway is not programmed.
// v1.5.0 names it in the field documentation but declares no constant for it, so
// it is spelled here; the alternative would be to fold that case into Invalid,
// which would blame the ListenerSet for its parent's state.
const listenerSetReasonParentNotProgrammed gatewayv1.ListenerSetConditionReason = "ParentNotProgrammed"

// listenerSetConditions builds a single-condition set. The two types are spelled
// as gatewayv1 constants at each call site below rather than passed in by callers,
// which is what keeps the vocabulary in this file.
func listenerSetConditions(
	conditionType gatewayv1.ListenerSetConditionType,
	status metav1.ConditionStatus,
	reason gatewayv1.ListenerSetConditionReason,
	msg string,
) generic.Conditions {
	genericType := generic.ConditionType(conditionType)
	return generic.Conditions{
		genericType: {
			Type:    genericType,
			Status:  status,
			Reason:  string(reason),
			Message: msg,
		},
	}
}

// ---------------------------------------------------------
// ListenerSetConditionAccepted

// NewListenerSetAcceptedOK is the ListenerSet attaching and every entry it declares
// being valid enough to serve.
func NewListenerSetAcceptedOK() generic.Conditions {
	return listenerSetConditions(
		gatewayv1.ListenerSetConditionAccepted,
		metav1.ConditionTrue,
		gatewayv1.ListenerSetReasonAccepted,
		"ListenerSet is accepted",
	)
}

// NewListenerSetAcceptedNotAllowed is the parent Gateway refusing this ListenerSet,
// through its allowedListeners policy. The entries contribute nothing, so the
// refusal is stated on the ListenerSet alone.
func NewListenerSetAcceptedNotAllowed() generic.Conditions {
	return listenerSetConditions(
		gatewayv1.ListenerSetConditionAccepted,
		metav1.ConditionFalse,
		gatewayv1.ListenerSetReasonNotAllowed,
		"ListenerSet is not allowed by the parent Gateway's allowedListeners policy",
	)
}

// NewListenerSetAcceptedParentNotAccepted is the parent Gateway itself not being
// accepted: whatever this ListenerSet declares, nothing can be served on it.
func NewListenerSetAcceptedParentNotAccepted() generic.Conditions {
	return listenerSetConditions(
		gatewayv1.ListenerSetConditionAccepted,
		metav1.ConditionFalse,
		gatewayv1.ListenerSetReasonParentNotAccepted,
		"Parent Gateway is not accepted",
	)
}

// NewListenerSetAcceptedSomeListenersInvalid is at least one entry being invalid
// while at least one remains valid. Accepted stays true: the API allows
// ListenersNotValid on either polarity, depending on whether the invalid entries
// prevent the ListenerSet from being served at all.
func NewListenerSetAcceptedSomeListenersInvalid() generic.Conditions {
	return listenerSetConditions(
		gatewayv1.ListenerSetConditionAccepted,
		metav1.ConditionTrue,
		gatewayv1.ListenerSetReasonListenersNotValid,
		"Some listeners of this ListenerSet are not valid",
	)
}

// NewListenerSetAcceptedAllListenersInvalid is no entry being valid: there is
// nothing left for the ListenerSet to serve.
func NewListenerSetAcceptedAllListenersInvalid() generic.Conditions {
	return listenerSetConditions(
		gatewayv1.ListenerSetConditionAccepted,
		metav1.ConditionFalse,
		gatewayv1.ListenerSetReasonListenersNotValid,
		"ListenerSet is not accepted - all its listeners are not valid",
	)
}

// ---------------------------------------------------------
// ListenerSetConditionProgrammed

// NewListenerSetProgrammedOK is every entry of the ListenerSet being reflected in
// HAProxy.
func NewListenerSetProgrammedOK() generic.Conditions {
	return listenerSetConditions(
		gatewayv1.ListenerSetConditionProgrammed,
		metav1.ConditionTrue,
		gatewayv1.ListenerSetReasonProgrammed,
		"ListenerSet is programmed",
	)
}

// NewListenerSetProgrammedPending is the ListenerSet being accepted with HAProxy not
// reflecting it yet.
func NewListenerSetProgrammedPending() generic.Conditions {
	return listenerSetConditions(
		gatewayv1.ListenerSetConditionProgrammed,
		metav1.ConditionUnknown,
		gatewayv1.ListenerSetReasonPending,
		"ListenerSet is pending HAProxy programmation",
	)
}

// NewListenerSetProgrammedParentNotProgrammed is the parent Gateway not being
// programmed, so this ListenerSet's entries cannot be either.
func NewListenerSetProgrammedParentNotProgrammed() generic.Conditions {
	return listenerSetConditions(
		gatewayv1.ListenerSetConditionProgrammed,
		metav1.ConditionFalse,
		listenerSetReasonParentNotProgrammed,
		"Parent Gateway is not programmed",
	)
}

// NewListenerSetProgrammedListenersNotValid is at least one entry not being
// programmable, which prevents the ListenerSet from being programmed as a whole.
func NewListenerSetProgrammedListenersNotValid() generic.Conditions {
	return listenerSetConditions(
		gatewayv1.ListenerSetConditionProgrammed,
		metav1.ConditionFalse,
		gatewayv1.ListenerSetReasonListenersNotValid,
		"One or more listeners of this ListenerSet are not programmed",
	)
}

// NewListenerSetProgrammedNotAllowed is the parent Gateway refusing this ListenerSet
// through its allowedListeners policy, so there is nothing to program.
//
// The API's list of Programmed reasons does not enumerate NotAllowed -- it names
// Invalid, ParentNotProgrammed, ListenersNotValid and PortUnavailable -- but it
// explicitly lets a controller raise the condition with other reasons, and the
// conformance suite requires NotAllowed here, on both conditions. Invalid would also
// read as a malformed ListenerSet, which a policy refusal is not.
func NewListenerSetProgrammedNotAllowed() generic.Conditions {
	return listenerSetConditions(
		gatewayv1.ListenerSetConditionProgrammed,
		metav1.ConditionFalse,
		gatewayv1.ListenerSetReasonNotAllowed,
		"ListenerSet is not allowed by the parent Gateway's allowedListeners policy",
	)
}
