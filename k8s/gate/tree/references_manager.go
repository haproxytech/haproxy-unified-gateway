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
	"maps"
	"slices"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	objtypes "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/object-types"
	utilsk8s "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils-k8s"
)

type ReferenceManager struct {
	*ControllerStore
}

func NewReferenceManager(controllerStore *ControllerStore) *ReferenceManager {
	return &ReferenceManager{
		ControllerStore: controllerStore,
	}
}

// UpdateRefences refreshes the Secret references derived from Gateway listener
// TLS certificateRefs (consumed by the CertificateBuilder via ReferencedSecrets).
//
// The previous snapshot is taken on every call so the CertificateBuilder's
// current-vs-previous diff stays correct even on cycles without a rebuild. The
// reference set itself is fully rebuilt from the current Gateways only when a
// Gateway changed this cycle: a stateless rebuild avoids maintaining the reverse
// index incrementally, and the cost stays in-memory (the cert I/O is made
// incremental by the diff downstream).
func (rm *ReferenceManager) UpdateRefences() {
	rm.ReferencedObjects.PreviousReferencedSecrets = rm.ReferencedObjects.ReferencedSecrets.DeepCopy()

	if len(rm.ClusterStore.Updates.Gateways) == 0 {
		return
	}

	rm.ReferencedObjects.ReferencedSecrets.CleanOwners()
	for _, gw := range rm.gatewaysForSecretRefs() {
		for _, listener := range gw.Spec.Listeners {
			if listener.TLS == nil {
				continue
			}
			for _, certRef := range listener.TLS.CertificateRefs {
				// We only accept v1.Secret
				if !utilsk8s.IsSecretGroupKindSupported(certRef) {
					continue
				}
				nsName := GetCertificateRefNamespacedName(certRef, gw)
				ownerGVK := rm.ControllerStore.ExtractGVK(objtypes.ObjectTypeGateway)
				rm.ReferencedObjects.ReferencedSecrets.AddReferencedByUsingKeys(rm.Logger, nsName, NewListenerKey(gw, listener), ownerGVK)
			}
		}
	}
}

// gatewaysForSecretRefs returns the Gateways whose listener certificateRefs feed
// ReferencedSecrets: the real Gateways from the store, plus the synthetic ingress
// gateway. The synthetic gateway is deliberately kept out of ClusterStore.Gateways
// (which must stay a faithful mirror of real K8s objects); it is merged into a
// local copy here rather than polluting the store.
func (rm *ReferenceManager) gatewaysForSecretRefs() []*gatewayv1.Gateway {
	gateways := slices.Collect(maps.Values(rm.ClusterStore.Gateways))
	if synthetic := rm.syntheticGateway(); synthetic != nil {
		gateways = append(gateways, synthetic)
	}
	return gateways
}

// syntheticGateway returns the synthetic ingress gateway for this cycle, or nil if
// there is none. It is taken from Updates.Gateways when (re)injected this cycle,
// otherwise from the persisted GateTree: a full ReferencedSecrets rebuild can be
// triggered by an unrelated real Gateway change on a cycle where the synthetic
// gateway was not re-injected, and its certificateRefs must survive that rebuild.
func (rm *ReferenceManager) syntheticGateway() *gatewayv1.Gateway {
	if upd, ok := rm.ClusterStore.Updates.Gateways[syntheticGatewayNamespacedName]; ok && upd.NewObject != nil {
		return upd.NewObject
	}
	if treeGw, ok := rm.GateTree.Gateways[syntheticGatewayNamespacedName]; ok && treeGw != nil {
		return treeGw.K8sResource
	}
	return nil
}
