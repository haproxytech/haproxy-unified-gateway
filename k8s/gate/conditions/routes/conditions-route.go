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
	generic "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type RouteConditions struct {
	Conditions     utils.KeyMap[gatewayv1.ParentReference, generic.Conditions]
	ControllerName string
}

func (c RouteConditions) MergeOverrideConditionsForParentRef(parentRef gatewayv1.ParentReference, conds generic.Conditions) {
	parentConds, ok := c.Conditions.Get(parentRef)
	if !ok {
		parentConds = make(map[generic.ConditionType]generic.Condition)
	}
	parentConds.MergeOverrideConditions(conds)
	c.Conditions.Set(parentRef, parentConds)
}

func (c RouteConditions) Equal(b RouteConditions) bool {
	if c.ControllerName != b.ControllerName {
		return false
	}
	if c.Conditions.Len() != b.Conditions.Len() {
		return false
	}

	equal := true
	c.Conditions.Iterate(func(key string, valA generic.Conditions) bool {
		parentRef, err := utils.KeyToParentRef(key)
		if err != nil {
			equal = false
			return false // Stop iteration
		}
		valB, ok := b.Conditions.Get(parentRef)
		if !ok || !valA.Equal(valB) {
			equal = false
			return false // Stop iteration
		}
		return true // Continue iteration
	})
	return equal
}

func (c RouteConditions) SetGeneration(generation int64) {
	c.Conditions.Iterate(func(_ string, conditionsForParentRef generic.Conditions) bool {
		conditionsForParentRef.SetGeneration(generation)
		return true // Continue iteration
	})
}

func NewRouteConditionsFromV1RouteConditions(parents []gatewayv1.RouteParentStatus, controllerName string) RouteConditions {
	conditionsMap := utils.NewKeyMap[gatewayv1.ParentReference, generic.Conditions](utils.ParentRefToKey)
	for _, parentConditions := range parents {
		parentRef := parentConditions.ParentRef
		if parentConditions.ControllerName != gatewayv1.GatewayController(controllerName) {
			continue
		}

		condsForParent, ok := conditionsMap.Get(parentRef)
		if !ok {
			condsForParent = make(generic.Conditions)
		}

		for _, condition := range parentConditions.Conditions {
			condsForParent[generic.ConditionType(condition.Type)] = generic.NewConditionFromMetav1Condition(condition)
		}
		conditionsMap.Set(parentRef, condsForParent)
	}
	return RouteConditions{
		Conditions:     conditionsMap,
		ControllerName: controllerName,
	}
}

func (c RouteConditions) ToV1RouteConditions() gatewayv1.HTTPRouteStatus {
	parents := make([]gatewayv1.RouteParentStatus, 0, c.Conditions.Len())

	c.Conditions.Iterate(func(key string, conditions generic.Conditions) bool {
		parentRef, err := utils.KeyToParentRef(key)
		if err != nil {
			return true // Continue iteration
		}
		parents = append(parents, gatewayv1.RouteParentStatus{
			ParentRef:      parentRef,
			ControllerName: gatewayv1.GatewayController(c.ControllerName),
			Conditions:     conditions.ToMetav1Conditions(),
		})
		return true // Continue iteration
	})

	return gatewayv1.HTTPRouteStatus{
		Parents: parents,
	}
}

// GetCondition of certain type
func (c RouteConditions) GetCondition(conditionType generic.ConditionType, parentRef gatewayv1.ParentReference) (generic.Condition, bool) {
	parent, exists := c.Conditions.Get(parentRef)
	if !exists {
		return generic.Condition{}, false
	}

	condition, exists := parent[conditionType]
	return condition, exists
}
