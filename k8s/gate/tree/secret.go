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
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// A Secret represents a Kubernetes Secret
type Secret struct {
	// K8sResource is the source resource.
	K8sResource *v1.Secret
	// TreeStatus
	TreeStatus TreeUpdate[Secret]
}

// NewSecret creates a new Secret for the GateTree.
func NewSecret(k8sObject *v1.Secret) *Secret {
	return &Secret{
		K8sResource: k8sObject,
		TreeStatus: TreeUpdate[Secret]{
			Status:          store.StatusUpserted,
			OldTreeResource: nil,
		},
	}
}

// SetAsUpserted marks the Secret as upserted in the GateTree.
func (s *Secret) SetAsUpserted(logger *slog.Logger, newK8sResource *v1.Secret) {
	setResourceStatus(logger, s, newK8sResource, store.StatusUpserted)
	s.K8sResource = newK8sResource
}

// SetAsDeleted marks the Secret as deleted in the GateTree.
func (s *Secret) SetAsDeleted(logger *slog.Logger) {
	setResourceStatus(logger, s, nil, store.StatusDeleted)
	s.K8sResource = nil
}

// SetAsManaged moves the Secret to the managed GateTree.
func (s *Secret) SetAsManaged(logger *slog.Logger, cs ControllerStore) {
	logger.LogAttrs(context.Background(), slog.LevelDebug, fmt.Sprintf("%T MANAGED", *s),
		logging.LogAttrObjectKey(s.K8sResource))
	key := client.ObjectKeyFromObject(s.K8sResource)
	cs.GateTree.Secrets[key] = s
}

// DeepCopy creates a deep copy of the Secret.
func (s *Secret) DeepCopy() *Secret {
	if s == nil {
		return nil
	}
	// Save TreeStatus
	treeStatus := s.TreeStatus
	s.TreeStatus = TreeUpdate[Secret]{}

	var copied Secret
	data, err := json.Marshal(s) // Serialize to JSON
	if err != nil {
		return nil
	}
	_ = json.Unmarshal(data, &copied) // Deserialize to a new struct	return &copied
	// Restore TreeStatus
	s.TreeStatus = treeStatus
	return &copied
}

// GetTreeStatus returns the TreeStatus of the Secret.
func (s *Secret) GetTreeStatus() *TreeUpdate[Secret] {
	return &s.TreeStatus
}

// SetTreeStatus sets the TreeStatus of the Secret.
func (s *Secret) SetTreeStatus(treeStatus TreeUpdate[Secret]) {
	s.TreeStatus = treeStatus
}

// GetCertificateRefNamespacedName returns the namespaced name a certificate
// reference denotes, resolving it in ownerNamespace when the reference does not
// name one.
//
// ownerNamespace is the namespace of the resource that declares the listener
// holding the reference -- the Gateway for one of its own listeners, the
// ListenerSet for an entry it contributes. It is not the Gateway's namespace in
// general: a reference is relative to the document it appears in.
func GetCertificateRefNamespacedName(certRef gatewayv1.SecretObjectReference, ownerNamespace string) types.NamespacedName {
	namespace := ownerNamespace
	if certRef.Namespace != nil {
		namespace = string(*certRef.Namespace)
	}
	return types.NamespacedName{
		Namespace: namespace,
		Name:      string(certRef.Name),
	}
}
