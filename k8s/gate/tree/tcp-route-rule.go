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
	v1 "k8s.io/api/core/v1"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type TCPRouteRule struct {
	// CheckBackendRef contains the result of the BackendRef checks for each BackendRef
	CheckBackendRef utils.KeyMap[gatewayv1.BackendObjectReference, CheckResult]
	K8sResource     gatewayv1.TCPRouteRule
	Valid           bool
}

func (r *TCPRouteRule) checkBackendRef(tcpRoute *TCPRoute, controllerStore ControllerStore,
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
		serviceKey := ServiceNsNameKey(tcpRoute.K8sResource.Namespace, backendRef.BackendObjectReference)
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
		// 3- The port must exist on the Service: a missing port is an
		// unresolvable target, not a healthy one.
		if backendRef.Port == nil || !serviceExposesPort(service.K8sResource, int32(*backendRef.Port)) {
			cond := rc.ConditionKOResolvedRefNotFound(utils.BackendObjectReferenceToKey(backendRef.BackendObjectReference))
			r.CheckBackendRef.Set(backendRef.BackendObjectReference, CheckResult{
				Valid:      false,
				Conditions: cond,
			})
			routeValid = false
			continue
		}
		// 4- Check if a ReferenceGrant is needed and if so is it valid
		backendNs := getNamespace(backendRef.BackendObjectReference.Namespace, tcpRoute.K8sResource.Namespace)
		accessGranted := backendNs == tcpRoute.K8sResource.Namespace ||
			referenceGrantManager.IsAccessGranted(
				GrantFrom{Group: gatewayv1.GroupName, Kind: "TCPRoute", Namespace: tcpRoute.K8sResource.Namespace},
				GrantTo{Group: "", Kind: "Service", Namespace: backendNs, Name: string(backendRef.BackendObjectReference.Name)},
			)
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

// serviceExposesPort reports whether the Service publishes the port.
func serviceExposesPort(service *v1.Service, port int32) bool {
	if service == nil {
		return false
	}
	for _, svcPort := range service.Spec.Ports {
		if svcPort.Port == port {
			return true
		}
	}
	return false
}
