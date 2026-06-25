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
	for _, gw := range rm.ClusterStore.Gateways {
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
