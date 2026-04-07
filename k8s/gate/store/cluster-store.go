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
	"log/slog"

	v3 "github.com/haproxytech/haproxy-unified-gateway/api/gate/v3"
	utilsk8s "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils-k8s"

	v1 "k8s.io/api/core/v1"
	discoveryV1 "k8s.io/api/discovery/v1"
	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

// ClusterStore includes cluster resources necessary to build the Tree.
type ClusterStore struct {
	GatewayClasses  map[types.NamespacedName]*gatewayv1.GatewayClass
	Gateways        map[types.NamespacedName]*gatewayv1.Gateway
	HTTPRoutes      map[types.NamespacedName]*gatewayv1.HTTPRoute
	TLSRoutes       map[types.NamespacedName]*gatewayv1alpha2.TLSRoute
	Services        map[types.NamespacedName]*v1.Service
	Namespaces      map[types.NamespacedName]*v1.Namespace
	Secrets         map[types.NamespacedName]*v1.Secret
	ConfigMaps      map[types.NamespacedName]*v1.ConfigMap
	GatewayAPICRDs  map[types.NamespacedName]*metav1.PartialObjectMetadata
	HugGates        map[types.NamespacedName]*v3.HugGate
	BackendCRs      map[types.NamespacedName]*v3.Backend
	GlobalCRs       map[types.NamespacedName]*v3.Global
	DefaultsCRs     map[types.NamespacedName]*v3.Defaults
	HugConfs        map[types.NamespacedName]*v3.HugConf
	EndpointSlices  map[types.NamespacedName]*discoveryV1.EndpointSlice
	ReferenceGrants map[types.NamespacedName]*gatewayv1.ReferenceGrant
	Updates         ClusterUpdates
}

// ClusterStoreUpdater updates the cluster store.
type ClusterStoreUpdater interface {
	Upsert(obj client.Object)
	Delete(obj client.Object, nsname types.NamespacedName)
	ResetUpdates()
}

type ClusterStoreUpdaterImpl struct {
	clusterStore *ClusterStore
	storeAdapter *storeAdapter
	extractGVK   utilsk8s.ExtractGVK
	logger       *slog.Logger
}

// to ensure that objectStoreImpl implements ObjectSore interface
var _ ClusterStoreUpdater = &ClusterStoreUpdaterImpl{}

func NewClusterStoreUpdaterImpl(
	clusterStore *ClusterStore,
	extractGVK utilsk8s.ExtractGVK,
	logger *slog.Logger,
) ClusterStoreUpdater {
	return &ClusterStoreUpdaterImpl{
		clusterStore: clusterStore,
		storeAdapter: &storeAdapter{
			stores: map[schema.GroupVersionKind]ObjectStoreUpdater{
				extractGVK(&gatewayv1.GatewayClass{}):          newObjectStoreImpl(clusterStore.GatewayClasses, clusterStore.Updates.GatewayClasses, logger),
				extractGVK(&gatewayv1.Gateway{}):               newObjectStoreImpl(clusterStore.Gateways, clusterStore.Updates.Gateways, logger),
				extractGVK(&gatewayv1.HTTPRoute{}):             newObjectStoreImpl(clusterStore.HTTPRoutes, clusterStore.Updates.HTTPRoutes, logger),
				extractGVK(&gatewayv1alpha2.TLSRoute{}):        newObjectStoreImpl(clusterStore.TLSRoutes, clusterStore.Updates.TLSRoutes, logger),
				extractGVK(&v1.Service{}):                      newObjectStoreImpl(clusterStore.Services, clusterStore.Updates.Services, logger),
				extractGVK(&v1.Namespace{}):                    newObjectStoreImpl(clusterStore.Namespaces, clusterStore.Updates.Namespaces, logger),
				extractGVK(&v1.Secret{}):                       newObjectStoreImpl(clusterStore.Secrets, clusterStore.Updates.Secrets, logger),
				extractGVK(&v1.ConfigMap{}):                    newObjectStoreImpl(clusterStore.ConfigMaps, clusterStore.Updates.ConfigMaps, logger),
				extractGVK(&apiext.CustomResourceDefinition{}): newObjectStoreImpl(clusterStore.GatewayAPICRDs, clusterStore.Updates.GatewayAPICRDs, logger),
				extractGVK(&v3.HugGate{}):                      newObjectStoreImpl(clusterStore.HugGates, clusterStore.Updates.HugGates, logger),
				extractGVK(&v3.Backend{}):                      newObjectStoreImpl(clusterStore.BackendCRs, clusterStore.Updates.BackendCRs, logger),
				extractGVK(&v3.Global{}):                       newObjectStoreImpl(clusterStore.GlobalCRs, clusterStore.Updates.GlobalCRs, logger),
				extractGVK(&v3.Defaults{}):                     newObjectStoreImpl(clusterStore.DefaultsCRs, clusterStore.Updates.DefaultsCRs, logger),
				extractGVK(&v3.HugConf{}):                      newObjectStoreImpl(clusterStore.HugConfs, clusterStore.Updates.HugConfs, logger),
				extractGVK(&discoveryV1.EndpointSlice{}):       newObjectStoreImpl(clusterStore.EndpointSlices, clusterStore.Updates.EndpointSlices, logger),
				extractGVK(&gatewayv1.ReferenceGrant{}):        newObjectStoreImpl(clusterStore.ReferenceGrants, clusterStore.Updates.ReferenceGrants, logger),
			},
		},
		extractGVK: extractGVK,
		logger:     logger,
	}
}

func (cs *ClusterStoreUpdaterImpl) Upsert(obj client.Object) {
	gvk := cs.extractGVK(obj)
	objectStore, ok := cs.storeAdapter.stores[gvk]
	if !ok {
		return
	}
	objectStore.upsert(obj)
}

func (cs *ClusterStoreUpdaterImpl) Delete(obj client.Object, nsname types.NamespacedName) {
	gvk := cs.extractGVK(obj)
	objectStore, ok := cs.storeAdapter.stores[gvk]
	if !ok {
		return
	}
	objectStore.delete(obj, nsname)
}

func (cs *ClusterStoreUpdaterImpl) ResetUpdates() {
	for _, v := range cs.storeAdapter.stores {
		v.resetUpdates()
	}
}
