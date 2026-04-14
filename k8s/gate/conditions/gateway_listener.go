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
	generic "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// ---------------------------------------------------------
// ListenerConditionResolvedRefs

func NewListenerResolvedRefInvalidRouteKinds(msg string) generic.Conditions {
	return generic.Conditions{
		generic.ConditionType(gatewayv1.ListenerConditionResolvedRefs): {
			Type:    generic.ConditionType(gatewayv1.ListenerConditionResolvedRefs),
			Status:  metav1.ConditionFalse,
			Reason:  string(gatewayv1.ListenerReasonInvalidRouteKinds),
			Message: msg,
		},
	}
}

func NewListenerResolvedRefInvalidCertificateRefs(msg string) generic.Conditions {
	return generic.Conditions{
		generic.ConditionType(gatewayv1.ListenerConditionResolvedRefs): {
			Type:    generic.ConditionType(gatewayv1.ListenerConditionResolvedRefs),
			Status:  metav1.ConditionFalse,
			Reason:  string(gatewayv1.ListenerReasonInvalidCertificateRef),
			Message: msg,
		},
	}
}

func NewListenerResolvedRefOK() generic.Conditions {
	return generic.Conditions{
		generic.ConditionType(gatewayv1.ListenerConditionResolvedRefs): {
			Type:    generic.ConditionType(gatewayv1.ListenerConditionResolvedRefs),
			Status:  metav1.ConditionTrue,
			Reason:  string(gatewayv1.ListenerReasonResolvedRefs),
			Message: "Listener references have been resolved",
		},
	}
}

func NewListenerResolvedRefListenerRefNotPermitted(msg string) generic.Conditions {
	return generic.Conditions{
		generic.ConditionType(gatewayv1.ListenerConditionResolvedRefs): {
			Type:    generic.ConditionType(gatewayv1.ListenerConditionResolvedRefs),
			Status:  metav1.ConditionFalse,
			Reason:  string(gatewayv1.ListenerReasonRefNotPermitted),
			Message: msg,
		},
	}
}

// ---------------------------------------------------------
// ListenerConditionProgrammed

func NewListenerProgrammedPending() generic.Conditions {
	return generic.Conditions{
		generic.ConditionType(gatewayv1.ListenerConditionProgrammed): {
			Type:    generic.ConditionType(gatewayv1.ListenerConditionProgrammed),
			Status:  metav1.ConditionUnknown,
			Reason:  string(gatewayv1.ListenerReasonPending),
			Message: "Listener is pending Haproxy programmation",
		},
	}
}

func NewListenerProgrammedInvalid() generic.Conditions {
	return generic.Conditions{
		generic.ConditionType(gatewayv1.ListenerConditionProgrammed): {
			Type:    generic.ConditionType(gatewayv1.ListenerConditionProgrammed),
			Status:  metav1.ConditionFalse,
			Reason:  string(gatewayv1.ListenerReasonInvalid),
			Message: "Listener is invalid",
		},
	}
}

func NewListenerProgrammedOK() generic.Conditions {
	return generic.Conditions{
		generic.ConditionType(gatewayv1.ListenerConditionProgrammed): {
			Type:    generic.ConditionType(gatewayv1.ListenerConditionProgrammed),
			Status:  metav1.ConditionTrue,
			Reason:  string(gatewayv1.ListenerReasonProgrammed),
			Message: "Listener is programmed in Haproxy",
		},
	}
}

// ---------------------------------------------------------
// ListenerConditionAccepted

func NewListenerAcceptedUnsupportedProtocol(msg string) generic.Conditions {
	return generic.Conditions{
		generic.ConditionType(gatewayv1.ListenerConditionAccepted): {
			Type:    generic.ConditionType(gatewayv1.ListenerConditionAccepted),
			Status:  metav1.ConditionFalse,
			Reason:  string(gatewayv1.ListenerReasonUnsupportedProtocol),
			Message: msg,
		},
	}
}

func NewListenerAcceptedOK() generic.Conditions {
	return generic.Conditions{
		generic.ConditionType(gatewayv1.ListenerConditionAccepted): {
			Type:    generic.ConditionType(gatewayv1.ListenerConditionAccepted),
			Status:  metav1.ConditionTrue,
			Reason:  string(gatewayv1.ListenerReasonAccepted),
			Message: "Listener is accepted",
		},
	}
}

// ---------------------------------------------------------
// ListenerConditionConflicted

func NewListenerConflicted(msg, reason string) generic.Conditions {
	return generic.Conditions{
		generic.ConditionType(gatewayv1.ListenerConditionConflicted): {
			Type:    generic.ConditionType(gatewayv1.ListenerConditionConflicted),
			Status:  metav1.ConditionTrue,
			Reason:  reason,
			Message: msg,
		},
		generic.ConditionType(gatewayv1.ListenerConditionAccepted): {
			Type:    generic.ConditionType(gatewayv1.ListenerConditionAccepted),
			Status:  metav1.ConditionFalse,
			Reason:  string(gatewayv1.ListenerReasonInvalid),
			Message: "Listener is invalid (see ConditionType Conflicted)",
		},
	}
}
