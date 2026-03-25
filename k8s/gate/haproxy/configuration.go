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
package haproxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/constants"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/diffs"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/metadata"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/structured"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
)

type Configuration struct {
	// structured contains the complete Structured configuration
	structured structured.Structured
	// mergeStrategies holds the merge strategies currently applied for each config type.
	// It is used to detect changes in merge strategy even when the config content is unchanged.
	mergeStrategies diffs.MergeStrategies
	diffs           diffs.HaproxyConfDiffs
}

func (c *Configuration) resetDiffs() {
	c.diffs = diffs.HaproxyConfDiffs{
		Created: structured.NewStructuredConf(),
		Updated: structured.NewStructuredConf(),
		Deleted: structured.NewStructuredConf(),
	}
}

func (c *Configuration) upsertFrontend(logger *slog.Logger, fe *models.Frontend, reprogramm bool) error {
	if fe == nil {
		logger.LogAttrs(context.Background(), slog.LevelError, "nil frontend")
		return errors.New("nil frontend")
	}

	if previousFe, ok := c.structured.Frontends[fe.Name]; ok {
		// Check if they are the same
		if previousFe.Equal(*fe) && !reprogramm {
			logger.LogAttrs(context.Background(), slog.LevelDebug, "Frontend [same]",
				logging.LogAttrFrontendName(fe.Name),
			)
			return nil
		}

		// Update existing frontend
		msg := fmt.Sprintf("Frontend [UPDATE] (%t)", reprogramm)
		logger.LogAttrs(context.Background(), slog.LevelInfo, msg,
			logging.LogAttrFrontendName(fe.Name),
		)

		// We need to deep copy the frontend to avoid modifying the original
		// as the diffs will be sent on a channel and used at the same time we continue to update the haproxy cfg store.
		deepCopied, err := DeepCopyFrontend(fe)
		if err != nil {
			return err
		}

		c.diffs.Updated.Frontends[fe.Name] = deepCopied
		c.structured.Frontends[fe.Name] = fe
	} else {
		// Create new frontend
		logger.LogAttrs(context.Background(), slog.LevelInfo, "Frontend [CREATE]",
			logging.LogAttrFrontendName(fe.Name),
		)

		// We need to deep copy the frontend to avoid modifying the original
		// as the diffs will be sent on a channel and used at the same time we continue to update the haproxy cfg store.
		deepCopied, err := DeepCopyFrontend(fe)
		if err != nil {
			return err
		}

		c.structured.Frontends[fe.Name] = deepCopied
		c.diffs.Created.Frontends[fe.Name] = deepCopied
	}
	return nil
}

func (c *Configuration) deleteFrontend(logger *slog.Logger, feName string) error {
	// Retrieve the frontend from the store
	fe, ok := c.structured.Frontends[feName]
	if !ok {
		// It could happen that the frontend was already deleted
		// like gateway is:
		// - first unmanaged: Frontend is not created, not in store
		// - then deleted: Frontend is deleted, not in store
		return nil
	}
	// delete the frontend from the store
	logger.LogAttrs(context.Background(), slog.LevelInfo, "Frontend [DELETE]",
		logging.LogAttrFrontendName(feName),
	)
	// We need to deep copy the frontend to avoid modifying the original
	// as the diffs will be sent on a channel and used at the same time we continue to update the haproxy cfg store.

	c.diffs.Deleted.Frontends[fe.Name] = nil
	delete(c.structured.Frontends, feName)
	return nil
}

func (c *Configuration) upsertBackend(logger *slog.Logger, be *models.Backend) error {
	if be == nil {
		logger.LogAttrs(context.Background(), slog.LevelError, "nil backend")
		return errors.New("nil backend")
	}

	if previousBe, ok := c.structured.Backends[be.Name]; ok {
		// Check if they are the same
		// Here, the input parameter be has no Servers, it's only the Backend without Servers that we want to compare
		// Even though Servers are part of the structured Backend, they are managed differently from Backend fields.
		if cmp.Equal(*previousBe, *be, cmpopts.IgnoreFields(models.Backend{}, "Servers")) {
			logger.LogAttrs(context.Background(), slog.LevelDebug, "Backend [same]",
				logging.LogAttrBackendName(be.Name),
			)
			return nil
		}
		be.Servers = previousBe.Servers

		// Update existing backend
		logger.LogAttrs(context.Background(), slog.LevelInfo, "Backend [UPDATE]",
			logging.LogAttrBackendName(be.Name),
		)

		// We need to deep copy the backend to avoid modifying the original
		// as the diffs will be sent on a channel and used at the same time we continue to update the haproxy cfg store.
		deepCopied, err := DeepCopyBackend(be)
		if err != nil {
			return err
		}

		c.diffs.Updated.Backends[be.Name] = deepCopied
		c.structured.Backends[be.Name] = be
	} else {
		// Create new backend
		logger.LogAttrs(context.Background(), slog.LevelInfo, "Backend [CREATE]",
			logging.LogAttrBackendName(be.Name),
		)

		// We need to deep copy the backend to avoid modifying the original
		// as the diffs will be sent on a channel and used at the same time we continue to update the haproxy cfg store.
		deepCopied, err := DeepCopyBackend(be)
		if err != nil {
			return err
		}

		c.structured.Backends[be.Name] = deepCopied
		c.diffs.Created.Backends[be.Name] = deepCopied
	}
	return nil
}

type ServerDiff struct {
	deleted map[string]struct{}
	added   map[string]struct{}
}

func (c *Configuration) upsertBackendWithServers(logger *slog.Logger, beName string, servers map[string]models.Server) (ServerDiff, error) {
	be, ok := c.structured.Backends[beName]
	if !ok {
		return ServerDiff{}, fmt.Errorf("could not find backend %s", beName)
	}

	beBackup, err := DeepCopyBackend(be)
	if err != nil {
		return ServerDiff{}, err
	}
	be.Servers = servers
	// 1- Same servers
	// TODO : use models.EqualMapStringServer
	if cmp.Equal(beBackup.Servers, be.Servers) {
		// if beBackup.Equal(*be) {
		logger.LogAttrs(context.Background(), slog.LevelDebug, "Backend [servers][same]",
			logging.LogAttrBackendName(be.Name),
		)
		return ServerDiff{}, nil
	}

	// 2- Servers differ
	// Update existing backend
	logger.LogAttrs(context.Background(), slog.LevelInfo, "Backend [servers][UPDATE]",
		logging.LogAttrBackendName(be.Name),
	)

	// Compute list of delete servers to update them through runtime = to set to MAINT
	// Compute list of added servers to create them through runtime
	serverDiffs := ServerDiff{
		deleted: utils.SetDifference(beBackup.Servers, be.Servers),
		added:   utils.SetDifference(be.Servers, beBackup.Servers),
	}

	// 2.1- Update the new Servers in Backend, so it will be written in the configuration
	c.diffs.Updated.Backends[be.Name] = be
	c.structured.Backends[be.Name] = be

	return serverDiffs, nil
}

func (c *Configuration) upsertBackendMetadata(logger *slog.Logger, beName string, md metadata.MetaData) error {
	if previousBe, ok := c.structured.Backends[beName]; ok {
		// Update existing backend
		logger.LogAttrs(context.Background(), slog.LevelInfo, "Backend [UPDATE_METADATA]",
			logging.LogAttrBackendName(beName),
		)

		previousBe.Metadata = md

		// We need to deep copy the backend to avoid modifying the original
		// as the diffs will be sent on a channel and used at the same time we continue to update the haproxy cfg store.
		deepCopied, err := DeepCopyBackend(previousBe)
		if err != nil {
			return err
		}

		c.diffs.Updated.Backends[beName] = deepCopied
		c.structured.Backends[beName] = previousBe
	}
	return nil
}

// upsertGlobal stores the given Global in the structured configuration and
// records the appropriate diff. If a Global was already present and is
// unchanged, the call is a no-op. If it differs from the stored one it is
// recorded as Updated; if no Global was stored yet it is recorded as Created.
func (c *Configuration) upsertGlobal(logger *slog.Logger, global *models.Global, mergeStrategy string) error {
	if global == nil {
		logger.LogAttrs(context.Background(), slog.LevelError, "nil global")
		return errors.New("nil global")
	}

	if previous, ok := c.structured.Globals[structured.GlobalKey]; ok {
		if previous.Equal(*global) && c.mergeStrategies.Global == mergeStrategy {
			logger.LogAttrs(context.Background(), slog.LevelDebug, "Global [same]")
			return nil
		}
		logger.LogAttrs(context.Background(), slog.LevelInfo, "Global [UPDATE]")
		deepCopied, err := DeepCopyGlobal(global)
		if err != nil {
			return err
		}
		c.diffs.Updated.Globals[structured.GlobalKey] = deepCopied
		c.diffs.MergeStrategies.Global = mergeStrategy
		c.mergeStrategies.Global = mergeStrategy
		c.structured.Globals[structured.GlobalKey] = global
	} else {
		logger.LogAttrs(context.Background(), slog.LevelInfo, "Global [CREATE]")
		deepCopied, err := DeepCopyGlobal(global)
		if err != nil {
			return err
		}
		c.structured.Globals[structured.GlobalKey] = deepCopied
		c.diffs.Created.Globals[structured.GlobalKey] = deepCopied
		c.diffs.MergeStrategies.Global = mergeStrategy
		c.mergeStrategies.Global = mergeStrategy
	}
	return nil
}

// deleteGlobal removes the Global from the structured configuration and records
// it as Deleted in HaproxyConfDiffs. If no Global is currently stored the call
// is a no-op.
func (c *Configuration) deleteGlobal(logger *slog.Logger) error {
	if _, ok := c.structured.Globals[structured.GlobalKey]; !ok {
		return nil
	}
	logger.LogAttrs(context.Background(), slog.LevelInfo, "Global [DELETE]")
	c.diffs.Deleted.Globals[structured.GlobalKey] = nil
	c.diffs.MergeStrategies.Global = "" // useless but clear
	c.mergeStrategies.Global = ""
	delete(c.structured.Globals, structured.GlobalKey)
	return nil
}

// upsertDefaults stores the given Defaults in the structured configuration and
// records the appropriate diff (Created or Updated). A no-op if unchanged.
func (c *Configuration) upsertDefaults(logger *slog.Logger, defaults *models.Defaults, mergeStrategy string) error {
	if defaults == nil {
		logger.LogAttrs(context.Background(), slog.LevelError, "nil defaults")
		return errors.New("nil defaults")
	}

	if defaults.Name != constants.DefaultsSectionName {
		return c.deleteDefaults(logger)
	}

	if previous, ok := c.structured.Defaults[constants.DefaultsSectionName]; ok {
		if previous.Equal(*defaults) && c.mergeStrategies.Defaults == mergeStrategy {
			logger.LogAttrs(context.Background(), slog.LevelDebug, "Defaults [same]")
			return nil
		}
		logger.LogAttrs(context.Background(), slog.LevelInfo, "Defaults [UPDATE]")
		deepCopied, err := DeepCopyDefaults(defaults)
		if err != nil {
			return err
		}
		c.diffs.Updated.Defaults[constants.DefaultsSectionName] = deepCopied
		c.diffs.MergeStrategies.Defaults = mergeStrategy
		c.mergeStrategies.Defaults = mergeStrategy
		c.structured.Defaults[constants.DefaultsSectionName] = defaults
	} else {
		logger.LogAttrs(context.Background(), slog.LevelInfo, "Defaults [CREATE]")
		deepCopied, err := DeepCopyDefaults(defaults)
		if err != nil {
			return err
		}
		c.structured.Defaults[constants.DefaultsSectionName] = deepCopied
		c.diffs.Created.Defaults[constants.DefaultsSectionName] = deepCopied
		c.diffs.MergeStrategies.Defaults = mergeStrategy
		c.mergeStrategies.Defaults = mergeStrategy
	}
	return nil
}

// deleteDefaults removes the Defaults from the structured configuration and records
// it as Deleted in HaproxyConfDiffs. If no Defaults is currently stored the call
// is a no-op.
func (c *Configuration) deleteDefaults(logger *slog.Logger) error {
	if _, ok := c.structured.Defaults[constants.DefaultsSectionName]; !ok {
		return nil
	}
	logger.LogAttrs(context.Background(), slog.LevelInfo, "Defaults [DELETE]")
	c.diffs.Deleted.Defaults[constants.DefaultsSectionName] = nil
	c.diffs.MergeStrategies.Defaults = "" // useless but clear
	c.mergeStrategies.Defaults = ""
	delete(c.structured.Defaults, constants.DefaultsSectionName)
	return nil
}

func (c *Configuration) deleteBackend(logger *slog.Logger, beName string) error {
	// Retrieve the backend from the store
	be, ok := c.structured.Backends[beName]
	if !ok {
		// It could happen that the backend was already deleted
		return nil
	}
	// delete the backend from the store
	logger.LogAttrs(context.Background(), slog.LevelInfo, "Backend [DELETE]",
		logging.LogAttrBackendName(beName),
	)
	// We need to deep copy the backend to avoid modifying the original
	// as the diffs will be sent on a channel and used at the same time we continue to update the haproxy cfg store.

	c.diffs.Deleted.Backends[be.Name] = nil
	delete(c.structured.Backends, beName)
	return nil
}
