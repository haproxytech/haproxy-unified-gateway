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
	"log/slog"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/constants"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
)

// DefaultsCRBuilder processes Defaults CR updates and maintains the Defaults node
// in the GateTree. Only the Defaults CR whose spec.name equals "haproxytech" is
// managed; CRs with any other spec.name are discarded with a warning log.
type DefaultsCRBuilder struct {
	*ControllerStore
}

func NewDefaultsCRBuilder(controllerStore *ControllerStore) *DefaultsCRBuilder {
	return &DefaultsCRBuilder{ControllerStore: controllerStore}
}

func (b *DefaultsCRBuilder) ComputeTreeUpdates() {
	for nsName, update := range b.ClusterStore.Updates.DefaultsCRs {
		// Identify the CR by spec.name (the HAProxy defaults section name), not by K8s metadata name.
		var specName string
		if update.NewObject != nil {
			specName = update.NewObject.Spec.Name
		} else if update.OldObject != nil {
			specName = update.OldObject.Spec.Name
		}
		if specName != constants.DefaultsSectionName {
			b.Logger.LogAttrs(context.Background(), slog.LevelWarn,
				"Defaults CR ignored: only the Defaults CR with spec.name 'haproxytech' is managed",
				slog.String("name", nsName.Name),
				slog.String("namespace", nsName.Namespace),
				slog.String("spec_name", specName),
			)
		}

		switch update.Status {
		case store.StatusUpserted:
			if b.GateTree.Defaults != nil {
				b.GateTree.Defaults.SetAsUpserted(b.Logger, update.NewObject)
			} else {
				b.GateTree.Defaults = NewDefaultsCR(update.NewObject)
			}
		case store.StatusDeleted:
			if b.GateTree.Defaults != nil {
				b.GateTree.Defaults.SetAsDeleted(b.Logger)
			}
		}
	}
}

func (b *DefaultsCRBuilder) CleanTreeUpdates() {
	if b.GateTree.Defaults == nil {
		return
	}
	status := b.GateTree.Defaults.GetTreeStatus()
	if status.Status == store.StatusDeleted {
		b.GateTree.Defaults = nil
		return
	}
	status.Status = ""
	status.OldTreeResource = nil
}
