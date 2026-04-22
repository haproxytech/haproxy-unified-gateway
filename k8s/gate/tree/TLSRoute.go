package tree

import (
	"encoding/json"
	"log/slog"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	rc "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/routes"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	utilsk8s "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils-k8s"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/gateway-api/apis/v1alpha2"
)

type TLSRoute struct {
	// K8sResource is the source resource.
	K8sResource *v1alpha2.TLSRoute
	// selected listener
	Listeners utils.KeyMap[gatewayv1.ParentReference, []*Listener] // map[parentRef]
	// Rules
	Rules []*TLSRouteRule
	// Final Conditions
	Conditions rc.RouteConditions
	// TreeStatus
	TreeStatus     TreeUpdate[TLSRoute]
	ControllerName string
	// Management Checks
	// CheckParentRefs will only contains conditions for parents (Gateways) managed by us
	CheckParentRefs CheckResultRoute
	// Valid
	Valid bool
}

func NewTLSRoute(k8sObject *v1alpha2.TLSRoute, controllerName string) *TLSRoute {
	return &TLSRoute{
		K8sResource: k8sObject,
		TreeStatus: TreeUpdate[TLSRoute]{
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
		Rules:          make([]*TLSRouteRule, 0),
	}
}

// SetAsUpserted marks the HTTPRoute as upserted in the GateTree.
func (r *TLSRoute) SetAsUpserted(logger *slog.Logger, newK8sResource *v1alpha2.TLSRoute) {
	setResourceStatus(logger, r, newK8sResource, store.StatusUpserted)
	r.K8sResource = newK8sResource
}

func (r *TLSRoute) SetAsDeleted(logger *slog.Logger) {
	setResourceStatus(logger, r, nil, store.StatusDeleted)
	r.K8sResource = nil
}

func (r *TLSRoute) GetTreeStatus() *TreeUpdate[TLSRoute] {
	return &r.TreeStatus
}

// SetTreeStatus sets the TreeStatus of the HTTPRoute.
func (r *TLSRoute) SetTreeStatus(treeStatus TreeUpdate[TLSRoute]) {
	r.TreeStatus = treeStatus
}

func (r *TLSRoute) DeepCopy() *TLSRoute {
	if r == nil {
		return nil
	}

	var copied TLSRoute

	data, _ := json.Marshal(r)
	_ = json.Unmarshal(data, &copied) // Deserialize to a new struct

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

func (r *TLSRoute) processChecks(controllerStore ControllerStore) {
	r.checkParentRefs(controllerStore)
	r.Valid = r.CheckParentRefs.Valid
}

func (r *TLSRoute) checkParentRefs(controllerStore ControllerStore) {
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
		result := r.checkParentRef(parentRef, controllerStore)

		if !result.Managed {
			// do not add the parentRef in Listener not check result
			continue
		}

		// From now on, parentRef is a managed Gateway

		// 1- The parentRef is not Valid
		if !result.Valid {
			// do not add the parentREf in Listener or check result
			routeConditions.MergeOverrideConditionsForParentRef(parentRef, result.Conditions)
			continue
		}

		// 2- The parentRef is Valid
		atLeastOneValidParentRef = true
		routeConditions.MergeOverrideConditionsForParentRef(parentRef, result.Conditions)
	}

	// Gather all Conditions for all parentRefs in the result
	checkParentsRefs.Conditions = routeConditions

	// If at least one parentRef is valid, the whole check is Valid
	if atLeastOneValidParentRef {
		checkParentsRefs.Valid = true
	}

	r.CheckParentRefs = checkParentsRefs
}

//revive:disable:function-length
func (r *TLSRoute) checkParentRef(parentRef gatewayv1.ParentReference, controllerStore ControllerStore) checkParentRefResult {
	if !utilsk8s.IsParentRefGroupKindSupported(parentRef, controllerStore.ExtractGVK) {
		return checkParentRefResult{
			Managed:    false,
			Valid:      false,
			Conditions: generic.Conditions{},
		}
	}

	gwKey := GetParentRefNamespacedNameFromResource(parentRef, getNamespaceOrDefault(parentRef.Namespace, r.K8sResource.Namespace))

	treeGw, ok := controllerStore.GateTree.Gateways[gwKey]
	if ok && treeGw.TreeStatus.Status == store.StatusDeleted {
		// gateway exists, but it was deleted
		return checkParentRefResult{
			Managed:    true,
			Valid:      false,
			Conditions: rc.ConditionNotAcceptedNoMatchingParent(),
		}
	}
	if !ok {
		// It's a whole different story, it means the Gateway does not exists,
		// we can not know if it's managed by our controller or not
		return checkParentRefResult{
			Managed:    false,
			Valid:      false,
			Conditions: generic.Conditions{},
		}
	}

	// From now on, the parent references an exising Gateway managed by our controller
	// SectionName is optional
	// 1- if set, the only attachable listener is the one references by the sectionName
	// 2- if not set, all listeners from the Gateway are attachable
	attachableListeners := make([]*Listener, 0)
	if parentRef.SectionName != nil {
		// Check if the Gateway has this listener
		listener, ok := treeGw.Listeners[string(*parentRef.SectionName)]
		if !ok {
			return checkParentRefResult{
				Managed:    true,
				Valid:      false,
				Conditions: rc.ConditionNotAcceptedNoMatchingParent(),
			}
		}
		tlsMode := utils.PointerDefaultValueIfNil(listener.K8sResource.TLS).Mode
		if tlsMode == nil || *tlsMode != gatewayv1.TLSModePassthrough {
			return checkParentRefResult{
				Managed:    true,
				Valid:      false,
				Conditions: rc.ConditionNotAcceptedRouteReasonNotAllowedByListeners(),
			}
		}
		// We found the listener, it does exists
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

		// Vérifie le hostname
		if !matchHostname(r.K8sResource.Spec.Hostnames, listener.K8sResource.Hostname) {
			conds.MergeOverrideConditions(rc.ConditionNotAcceptedNoMatchingHostname())
			continue
		}

		// Vérifie le type de route autorisé
		if !r.isAllowedRouteKind(listener, controllerStore.ExtractGVK) {
			conds.MergeOverrideConditions(rc.ConditionNotAcceptedRouteReasonNotAllowedByListeners())
			continue
		}

		// Check if the route's namespace is permitted by allowedRoutes.namespaces
		if !isRouteNamespaceAllowed(r.K8sResource.Namespace, listener, treeGw.K8sResource.Namespace, controllerStore.ClusterStore.Namespaces) {
			conds.MergeOverrideConditions(rc.ConditionNotAcceptedRouteReasonNotAllowedByListeners())
			continue
		}

		// Listener valide, attache la route
		listener.addAttachedRoute(client.ObjectKeyFromObject(r.K8sResource), controllerStore)
		validListeners = append(validListeners, listener)

		// Met à jour la KeyMap r.Listeners
		existing, _ := r.Listeners.Get(parentRef)
		existing = append(existing, listener)
		r.Listeners.Set(parentRef, existing)
	}

	// Si aucun listener valide, retourne les conditions cumulées
	if len(validListeners) == 0 {
		return checkParentRefResult{
			Managed:    true,
			Valid:      false,
			Conditions: conds,
		}
	}

	// Au moins un listener valide
	return checkParentRefResult{
		Managed:    true,
		Valid:      true,
		Conditions: rc.ConditionAccepted(),
	}
}

func GetParentRefNamespacedNameFromResource(parentRef gatewayv1.ParentReference, resourceNamespace string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: resourceNamespace,
		Name:      string(parentRef.Name),
	}
}

func getNamespaceOrDefault(namespace *gatewayv1.Namespace, defaultNamespace string) string {
	if namespace != nil {
		return string(*namespace)
	}
	return defaultNamespace
}

func (r *TLSRoute) isAllowedRouteKind(listener *Listener, extractGVK utilsk8s.ExtractGVK) bool {
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

func (r *TLSRoute) BuildConditions() {
	if !r.isManaged() {
		return
	}
	r.Conditions = rc.RouteConditions{
		ControllerName: r.ControllerName,
		Conditions:     utils.NewKeyMap[gatewayv1.ParentReference, generic.Conditions](utils.ParentRefToKey),
	}
	r.Conditions.SetGeneration(r.K8sResource.Generation)

	r.CheckParentRefs.Conditions.Conditions.Iterate(func(key string, parentRefConds generic.Conditions) bool {
		parentRef, err := utils.KeyToParentRef(key)
		if err != nil {
			return true // continue iteration
		}
		r.Conditions.MergeOverrideConditionsForParentRef(parentRef, parentRefConds)
		return true
	})
	// This needs to change when we implement more checks
	r.Valid = r.CheckParentRefs.Valid
}

func (r *TLSRoute) mergeBackendConditions() {
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
