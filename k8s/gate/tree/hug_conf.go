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
	v3 "github.com/haproxytech/haproxy-unified-gateway/api/gate/v3"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"k8s.io/apimachinery/pkg/types"
)

type HugConfBuilderImpl struct {
	*ControllerStore
	logCategoryFilterHandler *logging.CategoryFilterHandler
	hugConfNsName            types.NamespacedName
}

type HugConfBuilderParams struct {
	*ControllerStore
	LogCategoryFilterHandler *logging.CategoryFilterHandler
	HugConfNsName            types.NamespacedName
}

func NewHugConfBuilder(params HugConfBuilderParams) *HugConfBuilderImpl {
	return &HugConfBuilderImpl{
		ControllerStore:          params.ControllerStore,
		logCategoryFilterHandler: params.LogCategoryFilterHandler,
		hugConfNsName:            params.HugConfNsName,
	}
}

func (b *HugConfBuilderImpl) Build() {
	hugConfUpdates := b.ClusterStore.Updates.HugConfs
	if len(hugConfUpdates) == 0 {
		// no controller confs, nothing to do
		return
	}

	confUpdate, ok := hugConfUpdates[b.hugConfNsName]
	if !ok {
		// Update for controller conf not found, nothing to do
		return
	}

	// Reconcile the controller configuration
	switch confUpdate.Status {
	case store.StatusDeleted:
		// Case DELETED
		if confUpdate.Status == store.StatusDeleted {
			b.onDeleted(confUpdate)
			return
		}
	case store.StatusUpserted:
		// Case UPSERTED
		b.onUpserted(confUpdate)
	}
}

// -------------------------------
// HugConf deleted

// onDeleted handles the deletion of a HugConf resource by resetting all
// subsystems that were configured by it to their default state.
func (b *HugConfBuilderImpl) onDeleted(hugConfUpdate store.Update[*v3.HugConf]) {
	b.onDeletedSubSystemLogConf()
	b.onDeletedSubsystemGlobal(hugConfUpdate)
	b.onDeletedSubsystemDefaults(hugConfUpdate)
	b.onDeletedSubsystemBackendCR()
}

// -------------------------------
// HugConf upserted

// onUpserted handles the creation or update of a HugConf resource by
// triggering upsert logic for all sub-resources (log configuration, global, defaults).
func (b *HugConfBuilderImpl) onUpserted(hugConfUpdate store.Update[*v3.HugConf]) {
	b.onUpsertedSubsystemLogConf()
	b.onUpsertedSubsystemGlobal(hugConfUpdate)
	b.onUpsertedSubsystemDefaults(hugConfUpdate)
	b.onUpsertedSubsystemBackendCR(hugConfUpdate)
}

func (*HugConfBuilderImpl) BuildStatus() {
}
