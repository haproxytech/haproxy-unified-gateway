package tree

import (
	"encoding/json"
	"log/slog"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

type ReferenceGrant struct {
	// K8sResource is the source resource.
	K8sResource *gatewayv1beta1.ReferenceGrant
	// TreeStatus
	TreeStatus TreeUpdate[ReferenceGrant]
}

func NewReferenceGrant(k8sObject *gatewayv1beta1.ReferenceGrant) *ReferenceGrant {
	return &ReferenceGrant{
		K8sResource: k8sObject,
		TreeStatus: TreeUpdate[ReferenceGrant]{
			Status:          store.StatusUpserted,
			OldTreeResource: nil,
		},
	}
}

func (rg *ReferenceGrant) SetAsDeleted(logger *slog.Logger) {
	setResourceStatus(logger, rg, nil, store.StatusDeleted)
	rg.K8sResource = nil
}

func (rg *ReferenceGrant) SetAsUpserted(logger *slog.Logger, newK8sResource *gatewayv1beta1.ReferenceGrant) {
	setResourceStatus(logger, rg, newK8sResource, store.StatusUpserted)
	rg.K8sResource = newK8sResource
}

// GetTreeStatus returns the TreeStatus of the ReferenceGrant.
func (rg *ReferenceGrant) GetTreeStatus() *TreeUpdate[ReferenceGrant] {
	return &rg.TreeStatus
}

// SetTreeStatus sets the TreeStatus of the ReferenceGrant.
func (rg *ReferenceGrant) SetTreeStatus(treeStatus TreeUpdate[ReferenceGrant]) {
	rg.TreeStatus = treeStatus
}

// DeepCopy creates a deep copy of the ReferenceGrant.
func (rg *ReferenceGrant) DeepCopy() *ReferenceGrant {
	if rg == nil {
		return nil
	}
	// Save TreeStatus
	treeStatus := rg.TreeStatus
	rg.TreeStatus = TreeUpdate[ReferenceGrant]{}

	var copied ReferenceGrant
	data, err := json.Marshal(rg) // Serialize to JSON
	if err != nil {
		return nil
	}
	err = json.Unmarshal(data, &copied) // Deserialize to a new struct
	if err != nil {
		return nil
	}
	// Restore TreeStatus
	rg.TreeStatus = treeStatus
	return &copied
}
