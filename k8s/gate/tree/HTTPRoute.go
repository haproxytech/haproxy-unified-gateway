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
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	rc "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/routes"
	objtypes "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/object-types"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	utilsk8s "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils-k8s"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// A HTTPRoute represents a Kubernetes HTTPRoute
type HTTPRoute struct {
	// K8sResource is the source resource.
	K8sResource *gatewayv1.HTTPRoute
	// selected listener
	Listeners utils.KeyMap[gatewayv1.ParentReference, []*Listener] // map[parentRef]
	// Rules
	Rules []*HTTPRouteRule
	// Final Conditions
	Conditions rc.RouteConditions
	// TreeStatus
	TreeStatus     TreeUpdate[HTTPRoute]
	ControllerName string
	// Management Checks
	// CheckParentRefs will only contains conditions for parents (Gateways) managed by us
	CheckParentRefs CheckResultRoute
	// Valid
	Valid bool
}

// NewRoute creates a new Route for the GateTree.
func NewRoute(k8sObject *gatewayv1.HTTPRoute, controllerName string) *HTTPRoute {
	listeners := utils.NewKeyMap[gatewayv1.ParentReference, *Listener](func(pr gatewayv1.ParentReference) string {
		var namespace string
		if pr.Namespace != nil {
			namespace = string(*pr.Namespace)
		}
		var sectionName string
		if pr.SectionName != nil {
			sectionName = string(*pr.SectionName)
		}
		return strings.Join([]string{namespace, string(pr.Name), sectionName}, "/")
	})
	_ = listeners // TODO

	return &HTTPRoute{
		K8sResource: k8sObject,
		TreeStatus: TreeUpdate[HTTPRoute]{
			Status:          store.StatusUpserted,
			OldTreeResource: nil,
		},
		Listeners: utils.NewKeyMap[gatewayv1.ParentReference, []*Listener](utils.ParentRefToKey),
		CheckParentRefs: CheckResultRoute{
			Valid: false,
			Conditions: rc.RouteConditions{
				Conditions:     utils.NewKeyMap[gatewayv1.ParentReference, generic.Conditions](utils.ParentRefToKey),
				ControllerName: controllerName,
			},
		},
		ControllerName: controllerName,
		Rules:          make([]*HTTPRouteRule, 0),
	}
}

// SetAsUpserted marks the HTTPRoute as upserted in the GateTree.
func (r *HTTPRoute) SetAsUpserted(logger *slog.Logger, newK8sResource *gatewayv1.HTTPRoute) {
	setResourceStatus(logger, r, newK8sResource, store.StatusUpserted)
	r.K8sResource = newK8sResource
}

// SetAsDeleted marks the Secret as deleted in the GateTree.
func (r *HTTPRoute) SetAsDeleted(logger *slog.Logger) {
	setResourceStatus(logger, r, nil, store.StatusDeleted)
	r.K8sResource = nil
}

// DeepCopy creates a deep copy of the Secret.
func (r *HTTPRoute) DeepCopy() *HTTPRoute {
	if r == nil {
		return nil
	}
	var copied HTTPRoute
	// We can ignore the error here, as we are controlling the input
	data, _ := json.Marshal(r)
	_ = json.Unmarshal(data, &copied)

	// Manually deep copy the Listeners KeyMap.
	newListeners := utils.NewKeyMap[gatewayv1.ParentReference, []*Listener](utils.ParentRefToKey)
	for key, value := range r.Listeners.Iterate {
		parentRef, err := utils.KeyToParentRef(key)
		if err != nil {
			continue // continue iteration
		}

		copiedListeners := make([]*Listener, len(value))
		for i, l := range value {
			copiedListeners[i] = l.DeepCopy()
		}
		newListeners.Set(parentRef, copiedListeners)
	}
	copied.Listeners = newListeners

	return &copied
}

// GetTreeStatus returns the TreeStatus of the HTTPRoute.
func (r *HTTPRoute) GetTreeStatus() *TreeUpdate[HTTPRoute] {
	return &r.TreeStatus
}

// SetTreeStatus sets the TreeStatus of the HTTPRoute.
func (r *HTTPRoute) SetTreeStatus(treeStatus TreeUpdate[HTTPRoute]) {
	r.TreeStatus = treeStatus
}

// processChecks processes the all checks for a HTTPRoute
func (r *HTTPRoute) processChecks(controllerStore ControllerStore) {
	r.checkParentRefs(controllerStore)
	// g.checkGatewayClassIsValid(controllerStore)

	r.Valid = r.CheckParentRefs.Valid
}

//revive:disable:function-length,cognitive-complexity
func (r *HTTPRoute) checkParentRefs(controllerStore ControllerStore) {
	routeConditions := rc.RouteConditions{
		ControllerName: r.ControllerName,
		Conditions:     utils.NewKeyMap[gatewayv1.ParentReference, generic.Conditions](utils.ParentRefToKey),
	}
	checkParentsRefs := CheckResultRoute{
		Valid:      false,
		Conditions: routeConditions,
	}

	if r.K8sResource == nil {
		r.CheckParentRefs = checkParentsRefs
		return
	}
	parentrefs := r.K8sResource.Spec.ParentRefs
	atLeastOneValidParentRef := false
	for _, parentRef := range parentrefs {
		// 1. if we do not have parent ref find the gateway with the hostname
		// --- This loop will do nothing and we will have no parentRef condition, the route is not attached
		// 2. if we have parent ref find the gateway listener with the parent ref
		//    Note that the parentRef.Name (the Gateway name) is mandatory, only sectionName is optional
		// I might have multiple listeners, so maybe I needs to find all of them
		// Listener has also allowedRoutes CHECK
		parentRefIs := r.checkParentRef(parentRef, controllerStore)

		if !parentRefIs.Managed {
			// do not add the parentRef in Listener not check result
			continue
		}
		checkParentsRefs.Managed = true
		routeConditions.MergeOverrideConditionsForParentRef(parentRef, parentRefIs.Conditions)
		atLeastOneValidParentRef = parentRefIs.Valid || atLeastOneValidParentRef
	}

	// Gather all Conditions for all parentRefs in the result
	checkParentsRefs.Conditions = routeConditions

	// If at least one parentRef is valid, the whole check is Valid
	if atLeastOneValidParentRef {
		checkParentsRefs.Valid = true
	}

	r.CheckParentRefs = checkParentsRefs
}

type checkParentRefResult struct {
	// Conditions has a set of failing conditions
	// It is set only if Managed is true
	Conditions generic.Conditions
	// Managed is true if the Gateway is managed by our controller
	Managed bool
	// Valid is set only if Managed is true
	Valid bool
}

// checkParentRef checks 1 parentRef and returns:
// - a bool indicating if the parentRef is valid, and if it's not, a set of conditions detailing why
func (r *HTTPRoute) checkParentRef(parentRef gatewayv1.ParentReference, controllerStore ControllerStore) checkParentRefResult {
	// Vérifie si le type de parentRef est supporté
	if !utilsk8s.IsParentRefGroupKindSupported(parentRef, controllerStore.ExtractGVK) {
		return checkParentRefResult{
			Managed:    false,
			Valid:      false,
			Conditions: generic.Conditions{},
		}
	}

	// Get the Gateway
	gwKey := GetParentRefNamespacedName(parentRef, r.K8sResource.Namespace)
	treeGw, ok := controllerStore.GateTree.Gateways[gwKey]
	if ok && treeGw.TreeStatus.Status == store.StatusDeleted {
		return checkParentRefResult{
			Managed:    true,
			Valid:      false,
			Conditions: rc.ConditionNotAcceptedNoMatchingParent(),
		}
	}
	if !ok {
		return checkParentRefResult{
			Managed:    false,
			Valid:      false,
			Conditions: generic.Conditions{},
		}
	}

	// Compute the list of attachable Listeners
	attachableListeners := make([]*Listener, 0)
	if parentRef.SectionName != nil {
		listener, ok := treeGw.Listeners[string(*parentRef.SectionName)]
		if !ok {
			return checkParentRefResult{
				Managed:    true,
				Valid:      false,
				Conditions: rc.ConditionNotAcceptedNoMatchingParent(),
			}
		}
		attachableListeners = append(attachableListeners, listener)
	} else {
		for _, listener := range treeGw.Listeners {
			attachableListeners = append(attachableListeners, listener)
		}
	}

	validListeners := make([]*Listener, 0)
	conds := generic.Conditions{}

	for _, listener := range attachableListeners {
		// Check if the listener is accepted (routes can attach to accepted listeners even with unresolved refs)
		if !listener.Accepted {
			conds.MergeOverrideConditions(rc.ConditionNotAcceptedNoMatchingParent())
			continue
		}

		// Check hostname
		if !matchHostname(r.K8sResource.Spec.Hostnames, listener.K8sResource.Hostname) {
			conds.MergeOverrideConditions(rc.ConditionNotAcceptedNoMatchingHostname())
			continue
		}

		// Check if the route kind is allowed
		if !r.isAllowedRouteKind(listener, controllerStore.ExtractGVK) {
			conds.MergeOverrideConditions(rc.ConditionNotAcceptedRouteReasonNotAllowedByListeners())
			continue
		}

		// Add the route to the listener
		listener.addAttachedRoute(client.ObjectKeyFromObject(r.K8sResource), controllerStore)
		validListeners = append(validListeners, listener)

		// Add the listener to the parentRef
		existing, _ := r.Listeners.Get(parentRef)
		existing = append(existing, listener)
		r.Listeners.Set(parentRef, existing)
	}

	// If no listener is valid
	if len(validListeners) == 0 {
		return checkParentRefResult{
			Managed:    true,
			Valid:      false,
			Conditions: conds,
		}
	}

	// At least one listener is valid
	return checkParentRefResult{
		Managed:    true,
		Valid:      true,
		Conditions: rc.ConditionAccepted(),
	}
}

func (r *HTTPRoute) isAllowedRouteKind(listener *Listener, extractGVK utilsk8s.ExtractGVK) bool {
	gvk := extractGVK(r.K8sResource)
	for _, allowed := range listener.AllowedRouteKinds {
		if allowed.Group != nil && *allowed.Group == gatewayv1.Group(gvk.Group) {
			if allowed.Kind == gatewayv1.Kind(gvk.Kind) {
				return true
			}
		}
		if allowed.Group == nil && allowed.Kind == gatewayv1.Kind(gvk.Kind) {
			return true
		}
	}
	return false
}

// matchHostname checks if a route's hostnames match a listener's hostname.
// The rules are based on the Gateway API specification.
func matchHostname(routeHostnames []gatewayv1.Hostname, listenerHostname *gatewayv1.Hostname) bool {
	// If the listener hostname is not set, it matches any route hostname.
	if listenerHostname == nil || *listenerHostname == "" {
		return true
	}

	for _, routeHostname := range routeHostnames {
		if routeHostname == "" {
			return true
		}
		if match(string(routeHostname), string(*listenerHostname)) {
			return true
		}
	}
	return len(routeHostnames) == 0
}

// match performs the actual hostname matching between a route and a listener hostname.
func match(routeHostname, listenerHostname string) bool {
	// Exact match
	if routeHostname == listenerHostname {
		return true
	}

	// Wildcard match for listener
	if after, ok := strings.CutPrefix(listenerHostname, "*."); ok {
		domain := after
		if routeHostname != domain && strings.HasSuffix(routeHostname, "."+domain) {
			return true
		}
	}

	// Wildcard match for route
	if after, ok := strings.CutPrefix(routeHostname, "*."); ok {
		domain := after
		if listenerHostname != domain && strings.HasSuffix(listenerHostname, "."+domain) {
			return true
		}
	}

	return false
}

func (r *HTTPRoute) BuildConditions() {
	// We build conditions if parent ref is managed whatever the validity of the parent ref
	if !r.hasManagedParentRef() {
		return
	}
	r.Conditions = rc.RouteConditions{
		ControllerName: r.ControllerName,
		Conditions:     utils.NewKeyMap[gatewayv1.ParentReference, generic.Conditions](utils.ParentRefToKey),
	}

	r.CheckParentRefs.Conditions.Conditions.Iterate(func(key string, parentRefConds generic.Conditions) bool {
		parentRef, err := utils.KeyToParentRef(key)
		if err != nil {
			return true // continue iteration
		}
		r.Conditions.MergeOverrideConditionsForParentRef(parentRef, parentRefConds)
		return true
	})
	r.Conditions.SetGeneration(r.K8sResource.Generation)

	// This needs to change when we implement more checks
	r.Valid = r.hasValidParentRef()
}

// GetParentRefNamespacedName returns the namespaced name for a parentRef reference,
// using the Route's namespace as a default if the reference does not specify one.
func GetParentRefNamespacedName(parentRef gatewayv1.ParentReference, defaultNamespace string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: getNamespace(parentRef.Namespace, defaultNamespace),
		Name:      string(parentRef.Name),
	}
}

// GetBackendRefNamespacedName returns the namespaced name for a backendref reference,
// using the Route's namespace as a default if the reference does not specify one.
func GetBackendRefNamespacedName(backendRef gatewayv1.BackendObjectReference, defaultNamespace string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: getNamespace(backendRef.Namespace, defaultNamespace),
		Name:      string(backendRef.Name),
	}
}

func getNamespace(ns *gatewayv1.Namespace, defaultNamespace string) string {
	if ns != nil {
		return string(*ns)
	}
	return defaultNamespace
}

// isBackendRefGroupKindSupported checks if the provided HTTPRoute parent reference has a supported Group and Kind.
// It only supports `corev1.Service` resources.
func isBackendRefGroupKindSupported(backendRef gatewayv1.BackendObjectReference, extractGVK utilsk8s.ExtractGVK) bool {
	servicetype := objtypes.ObjectTypeService
	serviceGVK := extractGVK(servicetype)
	if backendRef.Kind != nil && *backendRef.Kind != gatewayv1.Kind(serviceGVK.Kind) {
		return false
	}
	if backendRef.Group != nil && *backendRef.Group != gatewayv1.Group(serviceGVK.Group) {
		return false
	}
	return true
}

// mergeFilterConditions applies any filter validation failures collected by
// checkFilters() to the route conditions for every parent ref.
//
// If all rules have invalid filters the route is fully invalid: Accepted is set
// to False. If only a portion of rules are invalid, the spec (lines 224-227 of
// httproute_types.go) requires Accepted: True and PartiallyInvalid: True, with
// the invalid rules dropped.
func (r *HTTPRoute) mergeFilterConditions() {
	type invalidRule struct {
		conds generic.Conditions
		index int
	}
	var invalidRules []invalidRule
	for i, rule := range r.Rules {
		if !rule.CheckFilters.Valid && len(rule.CheckFilters.Conditions) > 0 {
			invalidRules = append(invalidRules, invalidRule{index: i, conds: rule.CheckFilters.Conditions})
		}
	}
	if len(invalidRules) == 0 {
		return
	}

	var conds generic.Conditions
	if len(invalidRules) == len(r.Rules) {
		// All rules invalid — route is fully invalid.
		// Build a single message covering all invalid rules so no context is lost.
		var msg strings.Builder
		for i, ir := range invalidRules {
			if i > 0 {
				msg.WriteString("; ")
			}
			reason := ir.conds.GetMessage(generic.ConditionType(gatewayv1.RouteConditionAccepted))
			msg.WriteString(fmt.Sprintf("rule %d: %s", ir.index, reason))
		}
		conds = rc.ConditionKOAcceptedIncompatibleFilters(msg.String())
	} else {
		// Only a portion of rules are invalid — drop them and signal PartiallyInvalid.
		var msg strings.Builder
		msg.WriteString("Dropped Rule")
		for _, ir := range invalidRules {
			reason := ir.conds.GetMessage(generic.ConditionType(gatewayv1.RouteConditionAccepted))
			msg.WriteString(fmt.Sprintf(" (rule %d: %s)", ir.index, reason))
		}
		conds = rc.ConditionPartiallyInvalidIncompatibleFilters(msg.String())
	}

	r.Conditions.Conditions.Iterate(func(key string, _ generic.Conditions) bool {
		parentRefKey, err := utils.KeyToParentRef(key)
		if err != nil {
			return true
		}
		r.Conditions.MergeOverrideConditionsForParentRef(parentRefKey, conds)
		return true
	})
}

func (r *HTTPRoute) mergeBackendConditions() {
	// Complete the RouteConditions with the BackendRef check results
	// The rules checks result will apply to each parent
	koBackendRefConds := generic.Conditions{}

	for _, rule := range r.Rules {
		// iterate over rules,
		// 1- overall rule is OK,
		// 2- overall rule is KO, set a failing condition ResolvedRef (take any on the failing condition, let say the latest one
		// as there could be several reason for failure

		// First merge the 'failing; BackendRef Conditions over all rules and backendRefs
		rule.CheckBackendRef.Iterate(
			func(_ string, checkResult CheckResult) bool {
				if !checkResult.Valid {
					koBackendRefConds.MergeOverrideConditions(checkResult.Conditions)
				}
				return true
			})
	}

	// There is 1 failing condition ResolvedRefs
	resolvedRefConds := generic.Conditions{}
	if len(koBackendRefConds) > 0 {
		resolvedRefConds = koBackendRefConds
	} else {
		// All are OK
		resolvedRefConds = rc.ConditionOKResolvedRef()
	}

	r.Conditions.Conditions.Iterate(
		func(key string, _ generic.Conditions) bool {
			parentRefKey, err := utils.KeyToParentRef(key)
			if err != nil {
				return true // continue iteration
			}
			r.Conditions.MergeOverrideConditionsForParentRef(parentRefKey, resolvedRefConds)
			return true
		})
}
