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
package routeconditions

import (
	"fmt"

	genericconditions "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

var _ RouteConditionAccessor[*gatewayv1.HTTPRoute] = &HTTPRouteConditionImpl{}

type HTTPRouteConditionImpl struct {
	ControllerName string
}

func (r *HTTPRouteConditionImpl) GetConditions(obj *gatewayv1.HTTPRoute) RouteConditions {
	return NewRouteConditionsFromV1RouteConditions(obj.Status.Parents, r.ControllerName)
}

func (*HTTPRouteConditionImpl) SetConditions(obj *gatewayv1.HTTPRoute, conds RouteConditions) {
	// TODO preserve other conditions from other controllers
	obj.Status = conds.ToV1RouteConditions()
}

func ConditionAccepted() genericconditions.Conditions {
	return genericconditions.Conditions{
		genericconditions.ConditionType(gatewayv1.RouteConditionAccepted): {
			Type:    genericconditions.ConditionType(gatewayv1.RouteConditionAccepted),
			Status:  metav1.ConditionTrue,
			Reason:  string(gatewayv1.RouteReasonAccepted),
			Message: "Route Accepted",
		},
	}
}

// RouteConditionAccepted

func ConditionNotAcceptedNoMatchingParent() genericconditions.Conditions {
	return genericconditions.Conditions{
		genericconditions.ConditionType(gatewayv1.RouteConditionAccepted): {
			Type:    genericconditions.ConditionType(gatewayv1.RouteConditionAccepted),
			Status:  metav1.ConditionFalse,
			Reason:  string(gatewayv1.RouteReasonNoMatchingParent),
			Message: "No matching parent found",
		},
	}
}

func ConditionNotAcceptedNoMatchingHostname() genericconditions.Conditions {
	return genericconditions.Conditions{
		genericconditions.ConditionType(gatewayv1.RouteConditionAccepted): {
			Type:    genericconditions.ConditionType(gatewayv1.RouteConditionAccepted),
			Status:  metav1.ConditionFalse,
			Reason:  string(gatewayv1.RouteReasonNoMatchingListenerHostname),
			Message: "No matching hostname found",
		},
	}
}

func ConditionNotAcceptedRouteReasonNotAllowedByListeners() genericconditions.Conditions {
	return genericconditions.Conditions{
		genericconditions.ConditionType(gatewayv1.RouteConditionAccepted): {
			Type:    genericconditions.ConditionType(gatewayv1.RouteConditionAccepted),
			Status:  metav1.ConditionFalse,
			Reason:  string(gatewayv1.RouteReasonNotAllowedByListeners),
			Message: "Route kind not allowed by listeners",
		},
	}
}

//  RouteConditionResolvedRefs

func ConditionKOResolvedRefInvalidKind(backendRef string) genericconditions.Conditions {
	return genericconditions.Conditions{
		genericconditions.ConditionType(gatewayv1.RouteConditionResolvedRefs): {
			Type:    genericconditions.ConditionType(gatewayv1.RouteConditionResolvedRefs),
			Status:  metav1.ConditionFalse,
			Reason:  string(gatewayv1.RouteReasonInvalidKind),
			Message: fmt.Sprintf("Invalid Kind/Group for backendRef %s", backendRef),
		},
	}
}

func ConditionKOResolvedRefNotFound(backendRef string) genericconditions.Conditions {
	return genericconditions.Conditions{
		genericconditions.ConditionType(gatewayv1.RouteConditionResolvedRefs): {
			Type:    genericconditions.ConditionType(gatewayv1.RouteConditionResolvedRefs),
			Status:  metav1.ConditionFalse,
			Reason:  string(gatewayv1.RouteReasonBackendNotFound),
			Message: fmt.Sprintf("backendRef not found %s", backendRef),
		},
	}
}

// RouteConditionAccepted — filter-related failures

func ConditionKOAcceptedIncompatibleFilters(msg string) genericconditions.Conditions {
	return genericconditions.Conditions{
		genericconditions.ConditionType(gatewayv1.RouteConditionAccepted): {
			Type:    genericconditions.ConditionType(gatewayv1.RouteConditionAccepted),
			Status:  metav1.ConditionFalse,
			Reason:  string(gatewayv1.RouteReasonIncompatibleFilters),
			Message: msg,
		},
	}
}

// ConditionPartiallyInvalidIncompatibleFilters sets PartiallyInvalid: True with
// Accepted left untouched. The message must start with "Dropped Rule" per the spec.
func ConditionPartiallyInvalidIncompatibleFilters(msg string) genericconditions.Conditions {
	return genericconditions.Conditions{
		genericconditions.ConditionType(gatewayv1.RouteConditionPartiallyInvalid): {
			Type:    genericconditions.ConditionType(gatewayv1.RouteConditionPartiallyInvalid),
			Status:  metav1.ConditionTrue,
			Reason:  string(gatewayv1.RouteReasonIncompatibleFilters),
			Message: msg,
		},
	}
}

func ConditionOKResolvedRef() genericconditions.Conditions {
	return genericconditions.Conditions{
		genericconditions.ConditionType(gatewayv1.RouteConditionResolvedRefs): {
			Type:    genericconditions.ConditionType(gatewayv1.RouteConditionResolvedRefs),
			Status:  metav1.ConditionTrue,
			Reason:  string(gatewayv1.RouteReasonResolvedRefs),
			Message: "References resolved",
		},
	}
}
