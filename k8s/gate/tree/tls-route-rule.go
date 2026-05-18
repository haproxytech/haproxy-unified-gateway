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
	rc "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/routes"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"k8s.io/apimachinery/pkg/types"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/gateway-api/apis/v1alpha2"
)

type TLSRouteRule struct {
	// CheckBackendRef contains the result of the BackendRef checks for each BackendRef
	CheckBackendRef utils.KeyMap[gatewayv1.BackendObjectReference, CheckResult]
	K8sResource     v1alpha2.TLSRouteRule
	Valid           bool
}

func (r *TLSRouteRule) checkBackendRef(tlsRoute *TLSRoute, controllerStore ControllerStore,
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
		serviceKey := ServiceNsNameKeyTlSRoute(tlsRoute.K8sResource, backendRef.BackendObjectReference)
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
		backendNs := getNamespace(backendRef.BackendObjectReference.Namespace, tlsRoute.K8sResource.Namespace)
		accessGranted := backendNs == tlsRoute.K8sResource.Namespace ||
			referenceGrantManager.IsAccessGranted(gatewayv1.GroupName, "TLSRoute", tlsRoute.K8sResource.Namespace,
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
	}
	r.Valid = routeValid
}

// ServiceNsNameKey returns the service Ns/Name
// If the backendRef namespace is empty or nil, fills with the Route Namesapce
func ServiceNsNameKeyTlSRoute(tlsRoute *v1alpha2.TLSRoute, backendRef gatewayv1.BackendObjectReference) client.ObjectKey {
	if backendRef.Namespace == nil || *backendRef.Namespace == "" {
		return types.NamespacedName{
			Namespace: tlsRoute.Namespace,
			Name:      string(backendRef.Name),
		}
	}
	return types.NamespacedName{
		Namespace: string(*backendRef.Namespace),
		Name:      string(backendRef.Name),
	}
}
