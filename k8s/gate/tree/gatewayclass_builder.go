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
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type GatewayClassBuilderImpl struct {
	*ControllerStore
}

var _ Builder = &GatewayClassBuilderImpl{}

type GatewayClassBuilderParams struct {
	*ControllerStore
}

func NewGatewayClassBuilder(params GatewayClassBuilderParams) Builder {
	builder := &GatewayClassBuilderImpl{
		ControllerStore: params.ControllerStore,
	}

	return builder
}

// --------------------
// GateTree Updates
// --------------------

func (b *GatewayClassBuilderImpl) ComputeTreeUpdates() {
	b.addIndirectClusterStoreUpdates()
	// After this step, the clusterStore.Updates contains all impacted GatewayClass
	// Including the one impacted by:
	// - installedVersions updates
	b.computeGateTreeUpdates()
}

func (b *GatewayClassBuilderImpl) addIndirectClusterStoreUpdates() {
	b.addIndirectGatewayClassesFromInstalledVersions()
}

func (b *GatewayClassBuilderImpl) addIndirectGatewayClassesFromInstalledVersions() {
	// If installedVersions has changed all GatewayClasses are impacted
	if b.InstalledGwAPIVersions.Updated != nil && *b.InstalledGwAPIVersions.Updated {
		for gwcKey := range b.ControllerStore.ClusterStore.GatewayClasses {
			gwc := b.ClusterStore.GatewayClasses[gwcKey]
			_, alreadyPresent := b.ClusterStore.Updates.GatewayClasses[gwcKey]
			if alreadyPresent {
				continue
			}
			b.ClusterStore.Updates.GatewayClasses[gwcKey] = store.Update[*gatewayv1.GatewayClass]{
				NewObject: gwc,
				OldObject: gwc, // old = new when indirect update
				Status:    store.StatusUpserted,
				Indirect:  true,
			}
		}
	}
}

func (b *GatewayClassBuilderImpl) computeGateTreeUpdates() {
	for gwcKey, gwcUpdate := range b.ClusterStore.Updates.GatewayClasses {
		b.computeTreeGatewayClassUpdate(gwcKey, gwcUpdate)
	}
}

func (b *GatewayClassBuilderImpl) computeTreeGatewayClassUpdate(gwcKey client.ObjectKey, gwcUpdate store.Update[*gatewayv1.GatewayClass]) {
	// Is the GatewayClass already in Managed or Unmanaged tree ?
	// If not, add a new one
	var treeGwc *GatewayClass
	alreadyManagedTreeGwc, alreadyManagedTreeGwcOK := b.GateTree.GatewayClasses[gwcKey]
	alreadyUnmanagedTreeGwc, alreadyUnmanagedTreeGwcOK := b.UnmanagedGateTree.GatewayClasses[gwcKey]

	if alreadyManagedTreeGwcOK {
		treeGwc = alreadyManagedTreeGwc
	} else if alreadyUnmanagedTreeGwcOK {
		treeGwc = alreadyUnmanagedTreeGwc
	}

	switch gwcUpdate.Status {
	case store.StatusUpserted:
		if treeGwc != nil {
			treeGwc.SetAsUpserted(b.Logger, gwcUpdate.NewObject)
			treeGwc.ResetChecks()
		} else {
			treeGwc = NewGatewayClass(gwcUpdate.NewObject)
		}
		// Managed is a conclusion about the resource, so it is re-derived here rather than
		// carried over from the previous cycle: a class handed to another controller must
		// leave the managed tree, and with it its Gateways.
		treeGwc.UpdateManaged(b.ControllerName)
		// Perform Validity Check on all updated GatewayClasses
		// Do we keep it in Managed or Unmanaged???
		treeGwc.checkParametersRef(*b.ControllerStore)
		// Validity is based only on treeGwc.CheckParamsRef.Valid
		// We are in best effort mode for Version and accept GatewayClass with invalid version
		// treeGwc.Valid = b.GateTree.IsGwAPIVersionValid && treeGwc.CheckParamsRef.Valid
		treeGwc.Valid = treeGwc.CheckParamsRef.Valid
		treeGwc.BuildConditions(*b.ControllerStore)
		if treeGwc.Managed {
			treeGwc.SetAsManaged(b.Logger, *b.ControllerStore)
		} else {
			treeGwc.SetAsUnmanaged(b.Logger, *b.ControllerStore)
		}
	case store.StatusDeleted:
		if treeGwc != nil {
			treeGwc.SetAsDeleted(b.Logger)
			treeGwc.ResetChecks()
		}
		// else nothing to do
		// It did not exists, it's deleted, noop
	}
}

// -----------
// Cleanup
// -----------

func (b *GatewayClassBuilderImpl) CleanTreeUpdates() {
	cleanTreeUpdates(b.GateTree.GatewayClasses)
	cleanTreeUpdates(b.UnmanagedGateTree.GatewayClasses)
}
