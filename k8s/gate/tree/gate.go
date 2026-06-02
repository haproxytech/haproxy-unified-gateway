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
	v3 "github.com/haproxytech/haproxy-unified-gateway/api/gate/v3"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type GateBuilderImpl struct {
	*ControllerStore
}

func NewGateBuilder(params *ControllerStore) *GateBuilderImpl {
	builder := &GateBuilderImpl{
		ControllerStore: params,
	}
	return builder
}

type HugGateParamsRefChecker struct {
	ParamRef      *gatewayv1.ParametersReference
	StoreHugGates map[types.NamespacedName]*v3.HugGate
}

func (c *HugGateParamsRefChecker) CheckGatewayClass() (CheckResult, *v3.HugGate) {
	conds := generic.Conditions{}
	var gateFound bool
	var huggate *v3.HugGate

	if c.ParamRef != nil {
		// Checks that Kind and Group are as expected
		paramPath := field.NewPath("spec").Child("parametersRef")
		if c.ParamRef.Kind != SupportedParametersRefKind {
			kindPath := paramPath.Child("kind")
			unsupportedKind := field.NotSupported(
				kindPath,
				c.ParamRef.Kind, []string{string(SupportedParametersRefKind)},
			)
			conds.MergeOverrideConditions(
				conditions.NewGatewayClassAcceptedInvalidParameters(unsupportedKind),
			)
			return CheckResult{
				Conditions: conds,
				Valid:      false,
			}, nil
		}
		if c.ParamRef.Group != SupportedParametersRefGroup {
			groupPath := paramPath.Child("group")
			unsupportedGroup := field.NotSupported(
				groupPath,
				c.ParamRef.Group, []string{string(SupportedParametersRefGroup)},
			)
			conds.MergeOverrideConditions(
				conditions.NewGatewayClassAcceptedInvalidParameters(unsupportedGroup),
			)
			return CheckResult{
				Conditions: conds,
				Valid:      false,
			}, nil
		}
		// Checks that the CR does exist
		if c.ParamRef.Namespace == nil {
			nsPath := paramPath.Child("namespace")
			nsrequired := field.Required(nsPath, "namespace is required")
			conds.MergeOverrideConditions(
				conditions.NewGatewayClassAcceptedInvalidParameters(nsrequired),
			)
			return CheckResult{
				Conditions: conds,
				Valid:      false,
			}, nil
		}
		huggate, gateFound = c.StoreHugGates[types.NamespacedName{
			Name:      c.ParamRef.Name,
			Namespace: string(*c.ParamRef.Namespace),
		}]
		if !gateFound {
			notFound := field.NotFound(paramPath, c.ParamRef.Name)
			conds.MergeOverrideConditions(
				conditions.NewGatewayClassAcceptedInvalidParameters(notFound),
			)
			return CheckResult{
				Conditions: conds,
				Valid:      false,
			}, nil
		}
	}
	return CheckResult{
		Conditions: conditions.NewGatewayClassAcceptedOK(),
		Valid:      true,
	}, huggate
}

func (c *HugGateParamsRefChecker) CheckGateway() (CheckResult, *v3.HugGate) {
	conds := generic.Conditions{}
	var gateFound bool
	var huggate *v3.HugGate

	if c.ParamRef != nil {
		// Checks that Kind and Group are as expected
		paramPath := field.NewPath("spec").Child("parametersRef")
		if c.ParamRef.Kind != SupportedParametersRefKind {
			kindPath := paramPath.Child("kind")
			unsupportedKind := field.NotSupported(
				kindPath,
				c.ParamRef.Kind, []string{string(SupportedParametersRefKind)},
			)
			conds.MergeOverrideConditions(
				conditions.NewGatewayAcceptedInvalidParameters(unsupportedKind),
			)
			return CheckResult{
				Conditions: conds,
				Valid:      false,
			}, nil
		}
		if c.ParamRef.Group != SupportedParametersRefGroup {
			groupPath := paramPath.Child("group")
			unsupportedGroup := field.NotSupported(
				groupPath,
				c.ParamRef.Group, []string{string(SupportedParametersRefGroup)},
			)
			conds.MergeOverrideConditions(
				conditions.NewGatewayAcceptedInvalidParameters(unsupportedGroup),
			)
			return CheckResult{
				Conditions: conds,
				Valid:      false,
			}, nil
		}
		// Checks that the CR does exist
		if c.ParamRef.Namespace == nil {
			nsPath := paramPath.Child("namespace")
			nsrequired := field.Required(nsPath, "namespace is required")
			conds.MergeOverrideConditions(
				conditions.NewGatewayAcceptedInvalidParameters(nsrequired),
			)
			return CheckResult{
				Conditions: conds,
				Valid:      false,
			}, nil
		}
		huggate, gateFound = c.StoreHugGates[types.NamespacedName{
			Name:      c.ParamRef.Name,
			Namespace: string(*c.ParamRef.Namespace),
		}]
		if !gateFound {
			notFound := field.NotFound(paramPath, c.ParamRef.Name)
			conds.MergeOverrideConditions(
				conditions.NewGatewayAcceptedInvalidParameters(notFound),
			)
			return CheckResult{
				Conditions: conds,
				Valid:      false,
			}, nil
		}
	}
	return CheckResult{
		Conditions: conditions.NewGatewayAcceptedOK(),
		Valid:      true,
	}, huggate
}

func (*GateBuilderImpl) BuildStatus() {
}
