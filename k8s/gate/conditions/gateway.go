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

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

var _ generic.ConditionAccessor[*gatewayv1.Gateway] = &GatewayConditionImpl{}

type GatewayConditionImpl struct{}

func (*GatewayConditionImpl) GetConditions(obj *gatewayv1.Gateway) generic.Conditions {
	return generic.NewConditionsFromMetav1Conditions(obj.Status.Conditions)
}

func (*GatewayConditionImpl) SetConditions(obj *gatewayv1.Gateway, conds generic.Conditions) {
	obj.Status.Conditions = conds.ToMetav1Conditions()
}

// ---------------------------------------------------------
// GatewayConditionAccepted

func NewGatewayAcceptedOK() generic.Conditions {
	return generic.Conditions{
		generic.ConditionType(gatewayv1.GatewayConditionAccepted): {
			Type:    generic.ConditionType(gatewayv1.GatewayConditionAccepted),
			Status:  metav1.ConditionTrue,
			Reason:  string(gatewayv1.GatewayReasonAccepted),
			Message: "Gateway is accepted",
		},
	}
}

func NewGatewayAcceptedInvalidConditions(msg string) generic.Conditions {
	return generic.Conditions{
		generic.ConditionType(gatewayv1.GatewayConditionAccepted): {
			Type:    generic.ConditionType(gatewayv1.GatewayConditionAccepted),
			Status:  metav1.ConditionFalse,
			Reason:  string(gatewayv1.GatewayReasonInvalid),
			Message: fmt.Sprintf("GatewayClass '%s' is not accepted", msg),
		},
	}
}

func NewGatewayAcceptedInvalidParameters(err *field.Error) generic.Conditions {
	return generic.Conditions{
		generic.ConditionType(gatewayv1.GatewayConditionAccepted): {
			Type:    generic.ConditionType(gatewayv1.GatewayConditionAccepted),
			Status:  metav1.ConditionFalse,
			Reason:  string(gatewayv1.GatewayReasonInvalidParameters),
			Message: fmt.Sprintf("invalid parametersRef: %s", err),
		},
	}
}

func NewGatewayAcceptedListenersNotValid(msg string) generic.Conditions {
	return generic.Conditions{
		generic.ConditionType(gatewayv1.GatewayConditionAccepted): {
			Type:    generic.ConditionType(gatewayv1.GatewayConditionAccepted),
			Status:  metav1.ConditionFalse,
			Reason:  string(gatewayv1.GatewayReasonListenersNotValid),
			Message: msg,
		},
	}
}

func NewGatewayAcceptedListenerNotValidAtLeast1Valid() generic.Conditions {
	return generic.Conditions{
		generic.ConditionType(gatewayv1.GatewayConditionAccepted): {
			Type:    generic.ConditionType(gatewayv1.GatewayConditionAccepted),
			Status:  metav1.ConditionTrue,
			Reason:  string(gatewayv1.GatewayReasonListenersNotValid),
			Message: "Some listeners are Conflicting",
		},
	}
}

func NewGatewayAcceptedListenerNotValidAllInvalid() generic.Conditions {
	return generic.Conditions{
		generic.ConditionType(gatewayv1.GatewayConditionAccepted): {
			Type:    generic.ConditionType(gatewayv1.GatewayConditionAccepted),
			Status:  metav1.ConditionFalse,
			Reason:  string(gatewayv1.GatewayReasonListenersNotValid),
			Message: "Gateway is not accepted - All listeners are conflicting",
		},
	}
}

// ---------------------------------------------------------
// GatewayConditionProgrammed

func NewGatewayProgrammedOK() generic.Conditions {
	return generic.Conditions{
		generic.ConditionType(gatewayv1.GatewayConditionProgrammed): {
			Type:    generic.ConditionType(gatewayv1.GatewayConditionProgrammed),
			Status:  metav1.ConditionTrue,
			Reason:  string(gatewayv1.GatewayConditionProgrammed),
			Message: "Gateway is programmed",
		},
	}
}

func NewGatewayProgrammedInvalidParameters(msg string) generic.Conditions {
	return generic.Conditions{
		generic.ConditionType(gatewayv1.GatewayConditionProgrammed): {
			Type:    generic.ConditionType(gatewayv1.GatewayConditionProgrammed),
			Status:  metav1.ConditionFalse,
			Reason:  string(gatewayv1.GatewayReasonInvalidParameters),
			Message: msg,
		},
	}
}

func NewGatewayProgrammedListenersNotValid(msg string) generic.Conditions {
	return generic.Conditions{
		generic.ConditionType(gatewayv1.GatewayConditionProgrammed): {
			Type:    generic.ConditionType(gatewayv1.GatewayConditionProgrammed),
			Status:  metav1.ConditionFalse,
			Reason:  string(gatewayv1.GatewayReasonListenersNotValid),
			Message: msg,
		},
	}
}
