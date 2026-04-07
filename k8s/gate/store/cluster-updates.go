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
package store

import (
	v3 "github.com/haproxytech/haproxy-unified-gateway/api/gate/v3"

	v1 "k8s.io/api/core/v1"
	discoveryV1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

type Status string

const (
	StatusUpserted  Status = "UPSERTED"
	StatusDeleted   Status = "DELETED"
	StatusUnchanged Status = ""
)

type Update[T client.Object] struct {
	// OldObject is the previous version of the object before any update
	// The first time we receive an update, we keep the object as it is
	// This is useful to compare the previous version with the current version
	OldObject T
	// NewObject is the new version of the object after all updated, the latest value
	NewObject T
	Status    Status
	// Indirect is set to true when the update is not direct from a K8s object but
	// from a linked K8s object update
	// For example a GatewayClass referencing a HugGate and the HugGate is updated
	Indirect bool
}

// ClusterUpdated contains the updates that happened to cluster objects during a sync cycle
type ClusterUpdates struct {
	GatewayClasses  map[types.NamespacedName]Update[*gatewayv1.GatewayClass]
	Gateways        map[types.NamespacedName]Update[*gatewayv1.Gateway]
	HTTPRoutes      map[types.NamespacedName]Update[*gatewayv1.HTTPRoute]
	TLSRoutes       map[types.NamespacedName]Update[*gatewayv1alpha2.TLSRoute]
	Services        map[types.NamespacedName]Update[*v1.Service]
	Namespaces      map[types.NamespacedName]Update[*v1.Namespace]
	Secrets         map[types.NamespacedName]Update[*v1.Secret]
	ConfigMaps      map[types.NamespacedName]Update[*v1.ConfigMap]
	GatewayAPICRDs  map[types.NamespacedName]Update[*metav1.PartialObjectMetadata]
	HugGates        map[types.NamespacedName]Update[*v3.HugGate]
	BackendCRs      map[types.NamespacedName]Update[*v3.Backend]
	GlobalCRs       map[types.NamespacedName]Update[*v3.Global]
	DefaultsCRs     map[types.NamespacedName]Update[*v3.Defaults]
	HugConfs        map[types.NamespacedName]Update[*v3.HugConf]
	EndpointSlices  map[types.NamespacedName]Update[*discoveryV1.EndpointSlice]
	ReferenceGrants map[types.NamespacedName]Update[*gatewayv1.ReferenceGrant]
}

func NewClusterUpdates() ClusterUpdates {
	return ClusterUpdates{
		GatewayClasses:  make(map[types.NamespacedName]Update[*gatewayv1.GatewayClass]),
		Gateways:        make(map[types.NamespacedName]Update[*gatewayv1.Gateway]),
		HTTPRoutes:      make(map[types.NamespacedName]Update[*gatewayv1.HTTPRoute]),
		TLSRoutes:       make(map[types.NamespacedName]Update[*gatewayv1alpha2.TLSRoute]),
		Services:        make(map[types.NamespacedName]Update[*v1.Service]),
		Namespaces:      make(map[types.NamespacedName]Update[*v1.Namespace]),
		Secrets:         make(map[types.NamespacedName]Update[*v1.Secret]),
		ConfigMaps:      make(map[types.NamespacedName]Update[*v1.ConfigMap]),
		GatewayAPICRDs:  make(map[types.NamespacedName]Update[*metav1.PartialObjectMetadata]),
		HugGates:        make(map[types.NamespacedName]Update[*v3.HugGate]),
		BackendCRs:      make(map[types.NamespacedName]Update[*v3.Backend]),
		GlobalCRs:       make(map[types.NamespacedName]Update[*v3.Global]),
		DefaultsCRs:     make(map[types.NamespacedName]Update[*v3.Defaults]),
		HugConfs:        make(map[types.NamespacedName]Update[*v3.HugConf]),
		EndpointSlices:  make(map[types.NamespacedName]Update[*discoveryV1.EndpointSlice]),
		ReferenceGrants: make(map[types.NamespacedName]Update[*gatewayv1.ReferenceGrant]),
	}
}
