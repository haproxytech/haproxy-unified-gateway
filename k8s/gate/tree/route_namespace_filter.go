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
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// isRouteNamespaceAllowed reports whether a route in routeNamespace is permitted to
// attach to listener, which belongs to a gateway in gatewayNamespace.
//
// The decision follows spec.listeners[*].allowedRoutes.namespaces:
//   - nil / From=Same  → only routes in the same namespace as the gateway
//   - From=All         → routes from any namespace
//   - From=Selector    → routes from namespaces whose labels match the selector
//
// The default when From is absent is Same (per the Gateway API spec).
func isRouteNamespaceAllowed(
	routeNamespace string,
	listener *Listener,
	gatewayNamespace string,
	namespaces map[types.NamespacedName]*v1.Namespace,
) bool {
	allowedRoutes := listener.K8sResource.AllowedRoutes
	if allowedRoutes == nil || allowedRoutes.Namespaces == nil || allowedRoutes.Namespaces.From == nil {
		return routeNamespace == gatewayNamespace
	}

	switch *allowedRoutes.Namespaces.From {
	case gatewayv1.NamespacesFromAll:
		return true
	case gatewayv1.NamespacesFromSame:
		return routeNamespace == gatewayNamespace
	case gatewayv1.NamespacesFromSelector:
		sel := allowedRoutes.Namespaces.Selector
		if sel == nil {
			return false
		}
		labelSel, err := metav1.LabelSelectorAsSelector(sel)
		if err != nil {
			return false
		}
		ns, ok := namespaces[types.NamespacedName{Name: routeNamespace}]
		if !ok || ns == nil {
			return false
		}
		return labelSel.Matches(labels.Set(ns.Labels))
	default:
		return false
	}
}
