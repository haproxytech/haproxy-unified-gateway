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

	v3 "github.com/haproxytech/haproxy-unified-gateway/api/gate/v3"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
)

// onDeletedSubSystemLogConf resets the controller's log configuration to its default
// settings when the HugConf resource is deleted. It resets the category filter
// handler and logs the resulting log settings.
func (b *HugConfBuilderImpl) onDeletedSubSystemLogConf() {
	b.Logger.LogAttrs(
		context.Background(), slog.LevelInfo,
		"Resetting controller log configuration to defaults",
	)
	// Reset the log category filter handler to defaults
	b.logCategoryFilterHandler.ResetToDefaults()
	l, m := logging.GetLogSettings()
	b.Logger.LogAttrs(
		context.Background(), slog.LevelInfo,
		"Reconciled controller log configuration",
		logging.LogAttrLogSettings(l, m),
	)
}

// onUpsertedSubsystemLogConf reconciles the log configuration when a HugConf resource
// is created or updated. It reads the desired log levels from the HugConf spec
// and applies them via the log category filter handler, logging the result if
// settings changed.
func (b *HugConfBuilderImpl) onUpsertedSubsystemLogConf() {
	newConf := b.ClusterStore.HugConfs[b.hugConfNsName]
	if newConf == nil {
		b.Logger.LogAttrs(
			context.Background(), slog.LevelError,
			"Controller configuration not found",
			logging.LogAttrNsName(b.hugConfNsName),
		)
		return
	}

	expectedLogCategoryPerLevel := make(map[v3.Category]slog.Level)
	for _, catLevel := range newConf.Spec.Logging.CategoryLevelList {
		expectedLogCategoryPerLevel[catLevel.Category] = logging.LogLevelString2SlogLevel(string(catLevel.Level))
	}
	expectedLevel := logging.LogLevelString2SlogLevel(string(newConf.Spec.Logging.DefaultLevel))

	changed := b.logCategoryFilterHandler.ReconcileLogSettings(expectedLevel, expectedLogCategoryPerLevel)
	if changed {
		l, m := logging.GetLogSettings()
		b.Logger.LogAttrs(
			context.Background(), slog.LevelInfo,
			"Reconciled controller log configuration",
			logging.LogAttrLogSettings(l, m),
		)
	}
}
