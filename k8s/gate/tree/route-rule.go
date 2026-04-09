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
	genericconditions "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	rc "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/routes"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"k8s.io/apimachinery/pkg/types"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type HTTPRouteRule struct {
	K8sResource gatewayv1.HTTPRouteRule
	// CheckBackendRef contains the result of the BackendRef checks for each BackendRef
	CheckBackendRef utils.KeyMap[gatewayv1.BackendObjectReference, CheckResult]
	// CheckFilters holds the result of filter validation for this rule.
	// An invalid result prevents backend creation and surfaces a status condition.
	CheckFilters CheckResult
	Valid        bool
}

func (r *HTTPRouteRule) checkBackendRef(httpRoute *HTTPRoute, controllerStore ControllerStore,
	referenceGrantManager *ReferenceGrantManager,
) {
	routeValid := true
	for _, backendRef := range r.K8sResource.BackendRefs {
		// 1- Check is the Kind/Group is supported
		if !isBackendRefGroupKindSupported(backendRef.BackendObjectReference, controllerStore.ExtractGVK) {
			cond := rc.ConditionKOResolvedRefInvalidKind(utils.BackendObjectReferenceToKey(backendRef.BackendObjectReference))
			r.CheckBackendRef.Set(backendRef.BackendObjectReference, CheckResult{
				Valid:      false,
				Conditions: cond,
			})
			routeValid = false
			continue
		}

		// 2- Check if the Service does exists
		serviceKey := ServiceNsNameKey(httpRoute.K8sResource.Namespace, backendRef.BackendObjectReference)
		service, ok := controllerStore.GateTree.Services[serviceKey]
		if !ok || service.TreeStatus.Status == store.StatusDeleted {
			cond := rc.ConditionKOResolvedRefNotFound(utils.BackendObjectReferenceToKey(backendRef.BackendObjectReference))
			r.CheckBackendRef.Set(backendRef.BackendObjectReference, CheckResult{
				Valid:      false,
				Conditions: cond,
			})
			routeValid = false
			continue
		}

		// 3- Check if a ReferenceGrant is needed and if so is it valid
		backendNs := getNamespace(backendRef.BackendObjectReference.Namespace, httpRoute.K8sResource.Namespace)
		accessGranted := backendNs == httpRoute.K8sResource.Namespace ||
			referenceGrantManager.IsAccessGranted(gatewayv1.GroupName, "HTTPRoute", httpRoute.K8sResource.Namespace,
				"", "Service", backendNs, string(backendRef.BackendObjectReference.Name))
		if !accessGranted {
			cond := rc.ConditionKORefNotPermitted(utils.BackendObjectReferenceToKey(backendRef.BackendObjectReference))
			r.CheckBackendRef.Set(backendRef.BackendObjectReference, CheckResult{
				Valid:      false,
				Conditions: cond,
			})
			routeValid = false
			continue
		}
		// All checks done
		r.CheckBackendRef.Set(backendRef.BackendObjectReference, CheckResult{
			Valid:      true,
			Conditions: rc.ConditionOKResolvedRef(),
		})
		r.Valid = routeValid
	}
}

// checkFilters validates the HTTPRoute filters at both rule and backendRef level.
// Incompatible filter combinations are recorded in CheckFilters and cause Valid
// to be set to false so that no HAProxy backend is created for the rule.
// Unsupported Extended filters (e.g. RequestMirror) are silently ignored: the
// route rule remains valid and all supported filters still apply.
func (r *HTTPRouteRule) checkFilters() {
	// Rule-level filter checks.
	if cond, ok := validateFilterList(r.K8sResource.Filters, "rule"); !ok {
		r.CheckFilters = CheckResult{Valid: false, Conditions: cond}
		r.Valid = false
		return
	}

	// BackendRef-level filter checks, including cross-level incompatibility.
	ruleHasURLRewrite := filterListContains(r.K8sResource.Filters, gatewayv1.HTTPRouteFilterURLRewrite)
	for _, backendRef := range r.K8sResource.BackendRefs {
		if cond, ok := validateFilterList(backendRef.Filters, "backendRef"); !ok {
			r.CheckFilters = CheckResult{Valid: false, Conditions: cond}
			r.Valid = false
			return
		}
		// Cross-level: rule URLRewrite + backendRef RequestRedirect is contradictory.
		// The reverse (rule RequestRedirect + backendRef URLRewrite) cannot occur because
		// the Gateway API CEL validation prevents backendRefs when RequestRedirect is present at rule level.
		if ruleHasURLRewrite && filterListContains(backendRef.Filters, gatewayv1.HTTPRouteFilterRequestRedirect) {
			r.CheckFilters = CheckResult{
				Valid: false,
				Conditions: rc.ConditionKOAcceptedIncompatibleFilters(
					"rule URLRewrite and backendRef RequestRedirect cannot be used together"),
			}
			r.Valid = false
			return
		}
	}

	r.CheckFilters = CheckResult{Valid: true}
}

// validateFilterList checks a single filter slice for incompatible combinations.
// Returns the failing Conditions and false on error.
// Unsupported Extended filters (e.g. RequestMirror) are silently skipped.
func validateFilterList(filters []gatewayv1.HTTPRouteFilter, scope string) (genericconditions.Conditions, bool) {
	hasURLRewrite := false
	hasRedirect := false
	for _, f := range filters {
		switch f.Type {
		case gatewayv1.HTTPRouteFilterURLRewrite:
			hasURLRewrite = true
		case gatewayv1.HTTPRouteFilterRequestRedirect:
			hasRedirect = true
		}
	}
	if hasURLRewrite && hasRedirect {
		return rc.ConditionKOAcceptedIncompatibleFilters(
			scope + " URLRewrite and RequestRedirect filters cannot be used together"), false
	}
	return nil, true
}

// filterListContains reports whether a filter slice contains a filter of the given type.
func filterListContains(filters []gatewayv1.HTTPRouteFilter, t gatewayv1.HTTPRouteFilterType) bool {
	for _, f := range filters {
		if f.Type == t {
			return true
		}
	}
	return false
}

// ServiceNsNameKey returns the service Ns/Name
// If the backendRef namespace is empty or nil, fills with the Route Namesapce
func ServiceNsNameKey(routeNs string, backendRef gatewayv1.BackendObjectReference) client.ObjectKey {
	if backendRef.Namespace == nil || *backendRef.Namespace == "" {
		return types.NamespacedName{
			Namespace: routeNs,
			Name:      string(backendRef.Name),
		}
	}
	return types.NamespacedName{
		Namespace: string(*backendRef.Namespace),
		Name:      string(backendRef.Name),
	}
}
