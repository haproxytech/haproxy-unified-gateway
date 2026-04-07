package tree

import "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"

var _ Builder = &ReferenceGrantBuilderImpl{}

type ReferenceGrantBuilderImpl struct {
	controllerStore *ControllerStore
}

func NewReferenceGrantBuilder(controllerStore *ControllerStore) Builder {
	return &ReferenceGrantBuilderImpl{
		controllerStore: controllerStore,
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
			} else {
				b.controllerStore.GateTree.ReferenceGrants[rgKey] = NewReferenceGrant(rgUpdate.NewObject)
			}
		case rgUpdate.Status == store.StatusDeleted && existingReferenceGrant != nil:
			existingReferenceGrant.SetAsDeleted(b.controllerStore.Logger)
		}
	}
}

func (b *ReferenceGrantBuilderImpl) CleanTreeUpdates() {
	cleanTreeUpdates(b.controllerStore.GateTree.ReferenceGrants)
}
