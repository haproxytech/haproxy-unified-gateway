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
	"log/slog"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	rc "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/routes"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	utilsk8s "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils-k8s"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// TCPRoute mirrors TLSRoute's lifecycle minus the hostname machinery: a TCP
// listener carries no hostname, so listener acceptance is the whole matching.
type TCPRoute struct {
	// K8sResource is the source resource.
	K8sResource *gatewayv1.TCPRoute
	// selected listener
	Listeners utils.KeyMap[gatewayv1.ParentReference, []*Listener] // map[parentRef]
	// Rules
	Rules []*TCPRouteRule
	// Final Conditions
	Conditions rc.RouteConditions
	// TreeStatus
	TreeStatus     TreeUpdate[TCPRoute]
	ControllerName string
	// CheckParentRefs will only contains conditions for parents (Gateways) managed by us
	CheckParentRefs CheckResultRoute
	// Valid
	Valid bool
}

func NewTCPRoute(k8sObject *gatewayv1.TCPRoute, controllerName string) *TCPRoute {
	return &TCPRoute{
		K8sResource: k8sObject,
		TreeStatus: TreeUpdate[TCPRoute]{
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
		Rules:          make([]*TCPRouteRule, 0),
	}
}

// SetAsUpserted marks the TCPRoute as upserted in the GateTree.
func (r *TCPRoute) SetAsUpserted(logger *slog.Logger, newK8sResource *gatewayv1.TCPRoute) {
	setResourceStatus(logger, r, newK8sResource, store.StatusUpserted)
	r.K8sResource = newK8sResource
}

func (r *TCPRoute) SetAsDeleted(logger *slog.Logger) {
	setResourceStatus(logger, r, nil, store.StatusDeleted)
	r.K8sResource = nil
}

func (r *TCPRoute) GetTreeStatus() *TreeUpdate[TCPRoute] {
	return &r.TreeStatus
}

// SetTreeStatus sets the TreeStatus of the TCPRoute.
func (r *TCPRoute) SetTreeStatus(treeStatus TreeUpdate[TCPRoute]) {
	r.TreeStatus = treeStatus
}

func (r *TCPRoute) DeepCopy() *TCPRoute {
	if r == nil {
		return nil
	}

	var copied TCPRoute

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

func (r *TCPRoute) processChecks(controllerStore ControllerStore) {
	r.checkParentRefs(controllerStore)
	r.Valid = r.CheckParentRefs.Valid
}

func (r *TCPRoute) checkParentRefs(controllerStore ControllerStore) {
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
		result := r.checkParentRef(parentRef, controllerStore)

		if !result.Managed {
			continue
		}

		checkParentsRefs.Managed = true

		if !result.Valid {
			routeConditions.MergeOverrideConditionsForParentRef(parentRef, result.Conditions)
			continue
		}

		atLeastOneValidParentRef = true
		routeConditions.MergeOverrideConditionsForParentRef(parentRef, result.Conditions)
	}

	checkParentsRefs.Conditions = routeConditions

	if atLeastOneValidParentRef {
		checkParentsRefs.Valid = true
	}

	r.CheckParentRefs = checkParentsRefs
}

//revive:disable:function-length,cognitive-complexity
func (r *TCPRoute) checkParentRef(parentRef gatewayv1.ParentReference, controllerStore ControllerStore) checkParentRefResult {
	parentRefKind := utilsk8s.ResolveParentRefKind(parentRef, controllerStore.ExtractGVK)
	if parentRefKind.Unsupported() {
		return checkParentRefResult{
			Managed:    false,
			Valid:      false,
			Conditions: generic.Conditions{},
		}
	}

	parentKey := GetParentRefNamespacedNameFromResource(parentRef, getNamespaceOrDefault(parentRef.Namespace, r.K8sResource.Namespace))

	var (
		status *store.Status
		ok     bool
		treeGw *Gateway
		treeLs *ListenerSet
	)

	switch parentRefKind {
	case utilsk8s.ParentRefKindGateway:
		treeGw, ok = controllerStore.GateTree.Gateways[parentKey]
		if ok {
			status = &treeGw.TreeStatus.Status
		}
	case utilsk8s.ParentRefKindListenerSet:
		treeLs, ok = controllerStore.GateTree.ListenerSets[parentKey]
		if ok {
			status = &treeLs.TreeStatus.Status
		}
	}

	if status != nil && *status == store.StatusDeleted {
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

	// Same silence rule as a TLSRoute: a parent of another controller, or an
	// unreported ListenerSet, is not ours to comment on.
	if treeLs != nil && !treeLs.IsAttachmentReported() {
		return checkParentRefResult{
			Managed:    false,
			Valid:      false,
			Conditions: generic.Conditions{},
		}
	}

	if treeLs != nil && treeLs.Attachment == attachmentNotAllowed {
		return checkParentRefResult{
			Managed:    true,
			Valid:      false,
			Conditions: rc.ConditionNotAcceptedNoMatchingParent(),
		}
	}

	// SectionName is optional
	// 1- if set, the only attachable listener is the one referenced by the sectionName
	// 2- if not set, all listeners from the Gateway are attachable
	attachableListeners := make([]*Listener, 0)
	if parentRef.SectionName != nil {
		var listener *Listener
		if treeGw != nil {
			listener = treeGw.OwnListenerByName(*parentRef.SectionName)
		} else if treeLs != nil && treeLs.K8sResource != nil {
			if gwKey, namesGateway := parentGatewayKey(treeLs.K8sResource); namesGateway {
				if gw := controllerStore.GateTree.Gateways[gwKey]; gw != nil {
					listener = gw.ContributedListenerByName(parentKey, *parentRef.SectionName)
				}
			}
		}
		if listener == nil || !listener.MatchesParentRefPort(parentRef.Port) {
			return checkParentRefResult{
				Managed:    true,
				Valid:      false,
				Conditions: rc.ConditionNotAcceptedNoMatchingParent(),
			}
		}
		attachableListeners = append(attachableListeners, listener)
	} else {
		// No sectionName: every listener the Gateway declares itself is a candidate.
		var listeners []*Listener
		if treeGw != nil {
			listeners = treeGw.OwnListeners()
		} else if treeLs != nil && treeLs.K8sResource != nil {
			if gwKey, namesGateway := parentGatewayKey(treeLs.K8sResource); namesGateway {
				if gw := controllerStore.GateTree.Gateways[gwKey]; gw != nil {
					listeners = gw.ContributedListeners(parentKey)
				}
			}
		}
		for _, listener := range listeners {
			if !listener.MatchesParentRefPort(parentRef.Port) {
				continue
			}
			attachableListeners = append(attachableListeners, listener)
		}
		if parentRef.Port != nil && len(attachableListeners) == 0 {
			return checkParentRefResult{
				Managed:    true,
				Valid:      false,
				Conditions: rc.ConditionNotAcceptedNoMatchingParent(),
			}
		}
	}

	validListeners := make([]*Listener, 0)
	conds := generic.Conditions{}

	for _, listener := range attachableListeners {
		if !listener.Accepted {
			conds.MergeOverrideConditions(rc.ConditionNotAcceptedNoMatchingParent())
			continue
		}

		if !r.isAllowedRouteKind(listener, controllerStore.ExtractGVK) {
			conds.MergeOverrideConditions(rc.ConditionNotAcceptedRouteReasonNotAllowedByListeners())
			continue
		}

		var nsParentRef string
		if treeGw != nil {
			nsParentRef = treeGw.K8sResource.Namespace
		} else if treeLs != nil && treeLs.K8sResource != nil {
			nsParentRef = treeLs.K8sResource.Namespace
		}
		if !isRouteNamespaceAllowed(r.K8sResource.Namespace, listener, nsParentRef, controllerStore.ClusterStore.Namespaces) {
			conds.MergeOverrideConditions(rc.ConditionNotAcceptedRouteReasonNotAllowedByListeners())
			continue
		}

		listener.addAttachedRoute(client.ObjectKeyFromObject(r.K8sResource), controllerStore)
		validListeners = append(validListeners, listener)

		existing, _ := r.Listeners.Get(parentRef)
		existing = append(existing, listener)
		r.Listeners.Set(parentRef, existing)
	}

	if len(validListeners) == 0 {
		return checkParentRefResult{
			Managed:    true,
			Valid:      false,
			Conditions: conds,
		}
	}

	return checkParentRefResult{
		Managed:    true,
		Valid:      true,
		Conditions: rc.ConditionAccepted(),
	}
}

func (r *TCPRoute) isAllowedRouteKind(listener *Listener, extractGVK utilsk8s.ExtractGVK) bool {
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

func (r *TCPRoute) BuildConditions() {
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
	// SetGeneration only stamps the conditions already present, so it must run
	// after the merge, otherwise observedGeneration stays 0.
	r.Conditions.SetGeneration(r.K8sResource.Generation)

	r.Valid = r.CheckParentRefs.Valid
}

func (r *TCPRoute) mergeBackendConditions() {
	koBackendRefConds := generic.Conditions{}

	for _, rule := range r.Rules {
		rule.CheckBackendRef.Iterate(
			func(_ string, checkResult CheckResult) bool {
				if !checkResult.Valid {
					koBackendRefConds.MergeOverrideConditions(checkResult.Conditions)
				}
				return true
			},
		)
	}

	resolvedRefConds := generic.Conditions{}
	if len(koBackendRefConds) > 0 {
		resolvedRefConds = koBackendRefConds
	} else {
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
		},
	)
}
