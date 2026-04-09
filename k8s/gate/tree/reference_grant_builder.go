package tree

import "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"

var _ Builder = &ReferenceGrantBuilderImpl{}

type ReferenceGrantBuilderImpl struct {
	controllerStore       *ControllerStore
	referenceGrantManager *ReferenceGrantManager
}

func NewReferenceGrantBuilder(controllerStore *ControllerStore, referenceGrantManager *ReferenceGrantManager) Builder {
	return &ReferenceGrantBuilderImpl{
		controllerStore:       controllerStore,
		referenceGrantManager: referenceGrantManager,
	}
}

// --------------------
// GateTree Updates
// --------------------

func (b *ReferenceGrantBuilderImpl) ComputeTreeUpdates() {
	for rgKey, rgUpdate := range b.controllerStore.ClusterStore.Updates.ReferenceGrants {
		existingReferenceGrant := b.controllerStore.GateTree.ReferenceGrants[rgKey]
		switch {
		case rgUpdate.Status == store.StatusUpserted:
			if existingReferenceGrant != nil {
				existingReferenceGrant.SetAsUpserted(b.controllerStore.Logger, rgUpdate.NewObject)
				b.referenceGrantManager.UpsertReferenceGrant(*existingReferenceGrant)
			} else {
				newReferenceGrant := NewReferenceGrant(rgUpdate.NewObject)
				b.controllerStore.GateTree.ReferenceGrants[rgKey] = newReferenceGrant
				b.referenceGrantManager.UpsertReferenceGrant(*newReferenceGrant)
			}
		case rgUpdate.Status == store.StatusDeleted && existingReferenceGrant != nil:
			existingReferenceGrant.SetAsDeleted(b.controllerStore.Logger)
			b.referenceGrantManager.RemoveReferenceGrant(*existingReferenceGrant)
		}
	}
	if len(b.controllerStore.ClusterStore.Updates.ReferenceGrants) > 0 {
		b.referenceGrantManager.ComputeToFrom()
	}
}

func (b *ReferenceGrantBuilderImpl) CleanTreeUpdates() {
	cleanTreeUpdates(b.controllerStore.GateTree.ReferenceGrants)
}
