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
package predicate

import (
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// GatewayPredicate implements a predicate function based on specific Gateway to watch.
// If the GatewayNsName field is empty, all events will be allowed. Otherwise, only events for the specified Gateway will be allowed.
// This predicate will skip events for Gateways that don't reference this Gateway.
type GatewayPredicate struct {
	predicate.Funcs
	GatewayNsName types.NamespacedName
}

func NewGatewayPredicate(nsName types.NamespacedName) GatewayPredicate {
	return GatewayPredicate{
		GatewayNsName: nsName,
	}
}

// Create implements default CreateEvent filter for validating a Gateway gatewayClassName.
func (gp GatewayPredicate) Create(e event.CreateEvent) bool {
	if e.Object == nil {
		return false
	}
	if gp.GatewayNsName.Name == "" && gp.GatewayNsName.Namespace == "" {
		return true
	}

	g, ok := e.Object.(*gatewayv1.Gateway)
	if !ok {
		return false
	}
	allowed := gp.GatewayNsName.Name == g.Name && gp.GatewayNsName.Namespace == g.Namespace
	return allowed
}

// Update implements default UpdateEvent filter for validating a Gateway.
func (gp GatewayPredicate) Update(e event.UpdateEvent) bool {
	if gp.GatewayNsName.Name == "" && gp.GatewayNsName.Namespace == "" {
		return true
	}

	if e.ObjectOld != nil {
		if gOld, ok := e.ObjectOld.(*gatewayv1.Gateway); ok &&
			gp.GatewayNsName.Name == gOld.Name && gp.GatewayNsName.Namespace == gOld.Namespace {
			return true
		}
	}

	if e.ObjectNew != nil {
		if gNew, ok := e.ObjectNew.(*gatewayv1.Gateway); ok &&
			gp.GatewayNsName.Name == gNew.Name && gp.GatewayNsName.Namespace == gNew.Namespace {
			return true
		}
	}

	return false
}

// Delete implements default DeleteEvent filter for validating a Gateway gatewayClassName.
func (gp GatewayPredicate) Delete(e event.DeleteEvent) bool {
	if e.Object == nil {
		return false
	}
	if gp.GatewayNsName.Name == "" && gp.GatewayNsName.Namespace == "" {
		return true
	}

	g, ok := e.Object.(*gatewayv1.Gateway)
	if !ok {
		return false
	}
	allowed := gp.GatewayNsName.Name == g.Name && gp.GatewayNsName.Namespace == g.Namespace

	return allowed
}
