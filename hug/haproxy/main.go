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
	"fmt"
	"log/slog"
	"maps"
	"sync"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/haproxytech/haproxy-unified-gateway/hug/haproxy/api"
	"github.com/haproxytech/haproxy-unified-gateway/hug/haproxy/params"
	"github.com/haproxytech/haproxy-unified-gateway/hug/haproxy/process"
	"github.com/haproxytech/haproxy-unified-gateway/hug/reload"
	gatehaproxy "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/diffs"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/storage"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/structured"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/metrics"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
)

type AppManager interface {
	Stop()
	Run()
	HaproxyClient() api.HAProxyClient
}

type AppManagerImpl struct {
	client       api.HAProxyClient
	process      process.Process
	ctx          context.Context
	mapsStorage  storage.MapsStorage
	wg           *sync.WaitGroup
	logger       *slog.Logger
	haproxyCfgCh chan diffs.HaproxyConfDiffs
	// frontendsPendingMapsRemoval holds the frontends whose maps directory is
	// waiting to be removed. A frontend is queued when a committed configuration no
	// longer declares it, and stays queued while the removal fails: the diffs that
	// carried the deletion are reset once applied, so nothing else would retry it.
	frontendsPendingMapsRemoval map[string]struct{}
	params                      params.Params
}

var _ AppManager = &AppManagerImpl{}

func NewAppManager(ctx context.Context, wg *sync.WaitGroup,
	cfgCh chan diffs.HaproxyConfDiffs,
	// runtimeClientCh chan runtime.Runtime,
	haproxyClient api.HAProxyClient,
	p process.Process,
	param params.Params,
	logger *slog.Logger,
	mapsStorage storage.MapsStorage,
) (AppManager, error) {
	mylogger := logger.With(logging.LogAttrCategory(logging.LogCategoryApp))

	reload.Instance().SetLogger(logger)

	return &AppManagerImpl{
		client:                      haproxyClient,
		process:                     p,
		ctx:                         ctx,
		wg:                          wg,
		logger:                      mylogger,
		params:                      param,
		haproxyCfgCh:                cfgCh,
		mapsStorage:                 mapsStorage,
		frontendsPendingMapsRemoval: map[string]struct{}{},
	}, nil
}

func (h *AppManagerImpl) HaproxyClient() api.HAProxyClient {
	return h.client
}

func (h *AppManagerImpl) Stop() {
	h.process.Service("stop")
}

func (h *AppManagerImpl) Run() {
	// Collect the maps directories the configuration loaded from disk does not
	// reference, before the controller can submit a single diff.
	h.removeOrphanMapsDirectories()

	// Goroutine to listen on haproxyCfgCh and perform the haproxy configuration update
	h.wg.Go(func() {
		for {
			select {
			case <-h.ctx.Done():
				h.logger.LogAttrs(
					context.Background(), slog.LevelInfo,
					"shutting down AppManager Run() goroutine",
				)
				return
			case haproxyCfg := <-h.haproxyCfgCh:
				err := h.applyCfgUpdates(haproxyCfg)
				if err != nil {
					h.logger.LogAttrs(
						context.Background(), slog.LevelError, "failed to update Haproxy config",
						logging.LogAttrError(err),
					)
				}
			}
		}
	})
}

func (h *AppManagerImpl) applyCfgUpdates(haproxyCfgDiffs diffs.HaproxyConfDiffs) error {
	var err error
	var deletedFrontends map[string]struct{}
	// Process the received HaproxyConfDiffs
	h.logger.LogAttrs(context.Background(), slog.LevelDebug,
		"Starting processing HaproxyConfDiffs",
		slog.String("HaproxyConfDiffs", fmt.Sprintf("%+v", haproxyCfgDiffs.Stats())))

	if haproxyCfgDiffs.IsEmpty() && !haproxyCfgDiffs.ReloadNeed {
		// Should not happen, already checked before
		return nil
	}

	// Log, send result to the controller to update status
	defer func() {
		h.confUpdateProcessed(haproxyCfgDiffs, err)
	}()

	// -----------------
	// Start transaction

	err = h.client.APIStartTransaction()
	if err != nil {
		h.logger.LogAttrs(
			context.Background(), slog.LevelError, "failed to start transaction",
			logging.LogAttrError(err),
		)
		return err
	}

	if err = h.processCreate(haproxyCfgDiffs.Created, haproxyCfgDiffs.MergeStrategies); err != nil {
		return err
	}
	if err = h.processUpdate(haproxyCfgDiffs.Updated, haproxyCfgDiffs.MergeStrategies); err != nil {
		return err
	}
	if deletedFrontends, err = h.processDelete(haproxyCfgDiffs.Deleted); err != nil {
		return err
	}

	// -----------------
	// Commit transaction

	err = h.client.APIFinalCommitTransaction()
	if err != nil {
		h.logger.LogAttrs(
			context.Background(), slog.LevelError, "failed to commit transaction",
			logging.LogAttrError(err),
		)
		return err
	}

	// The configuration is committed: the frontends it no longer declares can
	// lose their maps directory now, and only now. Doing it during the delete
	// stage would drop the maps of a frontend that is still in the applied
	// configuration when the commit is rejected.
	//
	// A frontend this same commit declares again -- a listener re-created under a
	// name a previous commit had deleted -- keeps its directory: the pending
	// removal belongs to the deletion it survived, and the files written for it in
	// this cycle are the ones HAProxy reads after the reload.
	h.cancelPendingMapsRemovalOfDeclaredFrontends(haproxyCfgDiffs)
	maps.Copy(h.frontendsPendingMapsRemoval, deletedFrontends)
	h.removePendingMapsDirectoriesFromFrontends()

	// -----------------
	// Reload ?
	if reload.Instance().NeedReload() {
		h.logger.LogAttrs(context.Background(), slog.LevelInfo,
			"Haproxy reload")
		msg := ""
		if msg, err = h.process.Service("reload"); err != nil {
			h.logger.LogAttrs(
				context.Background(), slog.LevelError, "failed to reload Haproxy",
				slog.String("reason", msg),
				logging.LogAttrError(err),
			)
			return err
		}
		metrics.HaproxyReloadTotal.Inc()
		h.logger.LogAttrs(context.Background(), slog.LevelInfo,
			"Haproxy reloaded")
	}

	return nil
}

func (h *AppManagerImpl) processCreate(created structured.Structured, mergeStategies diffs.MergeStrategies) error {
	var errors utils.Errors

	// Frontends
	for _, createdFE := range created.Frontends {
		if createdFE == nil {
			// Should not happend
			h.logger.LogAttrs(context.Background(), slog.LevelError, "nil frontend")
			continue
		}

		err := h.client.FrontendCreate(*createdFE)
		if err != nil {
			h.logger.LogAttrs(
				context.Background(), slog.LevelError, "failed to create frontend",
				logging.LogAttrError(err),
			)
			errors.Add(err)
			continue
		}
	}

	// Backends
	for _, createdBE := range created.Backends {
		if createdBE == nil {
			// Should not happend
			h.logger.LogAttrs(context.Background(), slog.LevelError, "nil backend")
			continue
		}

		err := h.client.BackendCreate(*createdBE)
		if err != nil {
			h.logger.LogAttrs(
				context.Background(), slog.LevelError, "failed to create backend",
				logging.LogAttrError(err),
			)
			errors.Add(err)
			continue
		}
	}

	// Global
	for _, global := range created.Globals {
		err := h.client.GlobalEdit(global, mergeStategies.Global)
		if err != nil {
			h.logger.LogAttrs(
				context.Background(), slog.LevelError, "failed to edit global",
				logging.LogAttrError(err),
			)
			errors.Add(err)
		}
	}

	// Defaults
	for _, defaults := range created.Defaults {
		err := h.client.DefaultsSectionEdit(defaults, mergeStategies.Defaults)
		if err != nil {
			h.logger.LogAttrs(
				context.Background(), slog.LevelError, "failed to edit defaults section",
				logging.LogAttrError(err),
			)
			errors.Add(err)
		}
	}

	return errors.Result()
}

// processDelete applies the deletions of the given structured configuration.
//
// It returns the frontends it deleted: their maps directory belongs to the
// caller, which removes it once the configuration is committed.
func (h *AppManagerImpl) processDelete(deletedSections structured.Structured) (map[string]struct{}, error) {
	var errors utils.Errors

	deletedFrontends := make(map[string]struct{}, len(deletedSections.Frontends))

	// Frontends
	for feName := range deletedSections.Frontends {
		err := h.client.FrontendDelete(feName)
		if err != nil {
			h.logger.LogAttrs(
				context.Background(), slog.LevelError, "failed to delete frontend",
				logging.LogAttrError(err),
			)
			errors.Add(err)
			continue
		}
		// The maps directory is removed by the caller, once the configuration is
		// committed: until then the frontend is still declared by the applied
		// configuration, which references its maps.
		deletedFrontends[feName] = struct{}{}
	}

	// Backends
	for beName := range deletedSections.Backends {
		err := h.client.BackendDelete(beName)
		if err != nil {
			h.logger.LogAttrs(
				context.Background(), slog.LevelError, "failed to delete backend",
				logging.LogAttrError(err),
			)
			errors.Add(err)
			continue
		}
	}

	// Global
	for range deletedSections.Globals {
		err := h.client.GlobalEdit(nil, "")
		if err != nil {
			h.logger.LogAttrs(
				context.Background(), slog.LevelError, "failed to reset global to default value",
				logging.LogAttrError(err),
			)
			errors.Add(err)
		}
	}

	// Defaults
	for range deletedSections.Defaults {
		err := h.client.DefaultsSectionEdit(nil, "")
		if err != nil {
			h.logger.LogAttrs(
				context.Background(), slog.LevelError, "failed to reset defaults to default value",
				logging.LogAttrError(err),
			)
			errors.Add(err)
		}
	}

	return deletedFrontends, errors.Result()
}

// cancelPendingMapsRemovalOfDeclaredFrontends drops from the pending removal the
// frontends the committed configuration declares. Their directory was queued by a
// deletion whose removal failed, and their name is all a frontend keeps across a
// deletion and a re-creation: without this, the removal would target the files the
// cycle just wrote for the frontend HAProxy is about to serve.
func (h *AppManagerImpl) cancelPendingMapsRemovalOfDeclaredFrontends(committedSections diffs.HaproxyConfDiffs) {
	for _, sectionsOfOneDirection := range []structured.Structured{
		committedSections.Created, committedSections.Updated,
	} {
		for frontendName := range sectionsOfOneDirection.Frontends {
			delete(h.frontendsPendingMapsRemoval, frontendName)
		}
	}
}

// removePendingMapsDirectoriesFromFrontends removes the maps directory of every
// frontend whose removal is still pending -- the ones a committed configuration no
// longer declares -- and keeps the ones that fail for the next successful commit to
// retry: once the diffs are applied they are reset, so they cannot carry the
// deletion again.
func (h *AppManagerImpl) removePendingMapsDirectoriesFromFrontends() {
	for frontendName := range h.frontendsPendingMapsRemoval {
		if err := h.mapsStorage.DeleteMapsDirectoryForFrontend(frontendName); err != nil {
			h.logger.LogAttrs(context.Background(), slog.LevelWarn,
				"failed to remove the maps directory of a deleted frontend, the removal is retried after the next successful configuration apply",
				slog.String("frontend", frontendName),
				logging.LogAttrError(err),
			)
			continue
		}

		delete(h.frontendsPendingMapsRemoval, frontendName)
	}
}

// removeOrphanMapsDirectories removes the maps directories of the frontends the
// configuration loaded from disk does not reference. It collects the orphans left
// by a frontend removal that did not complete before the previous stop: a crash
// between the commit and the removal, or a filesystem error that was never
// retried.
//
// It runs once, at startup, before any transaction can change the file: that
// configuration is the one HAProxy is started with, so a directory it does not
// reference is unreachable by the process serving traffic. A frontend the
// controller declares later is written its map files again during the cycle that
// introduces it, before the reload that makes HAProxy read them.
//
// Removing them after the first applied configuration instead, as a way to wait
// for a state the resources own, would miss the case this is meant for: a
// controller restarted on a cluster that did not change receives no diff at all,
// so no apply runs and the orphans stay for good.
func (h *AppManagerImpl) removeOrphanMapsDirectories() {
	frontends, err := h.client.FrontendsGet()
	if err != nil {
		h.logger.LogAttrs(context.Background(), slog.LevelWarn,
			"cannot list the frontends of the applied configuration, orphan maps directories removal skipped",
			logging.LogAttrError(err),
		)
		return
	}

	// The frontends the configuration on disk declares: what the removal has to
	// keep. A frontend that no cluster resource declares any more is part of them,
	// because HAProxy is started with that configuration and still reads its map
	// files; the deletion of the frontend removes them once it is committed.
	configuredFrontends := make([]string, 0, len(frontends))
	for _, frontend := range frontends {
		if frontend == nil {
			continue
		}
		configuredFrontends = append(configuredFrontends, frontend.Name)
	}

	if err = h.mapsStorage.RemoveOrphanMapsDirectories(configuredFrontends); err != nil {
		h.logger.LogAttrs(context.Background(), slog.LevelWarn,
			"orphan maps directories removal incomplete",
			logging.LogAttrError(err),
		)
	}
}

func (h *AppManagerImpl) processUpdate(updated structured.Structured, mergeStrategies diffs.MergeStrategies) error {
	var errors utils.Errors

	// Frontends
	for _, updatedFE := range updated.Frontends {
		if updatedFE == nil {
			// Should not happend
			h.logger.LogAttrs(context.Background(), slog.LevelError, "nil frontend")
			continue
		}
		// TODO: need to check if only the Metadata has changed
		// If so, no need to reload

		err := h.client.FrontendEdit(*updatedFE)
		if err != nil {
			h.logger.LogAttrs(
				context.Background(), slog.LevelError, "failed to edit frontend",
				logging.LogAttrError(err),
			)
			errors.Add(err)
			continue
		}
	}

	// Backends
	for _, updatedBE := range updated.Backends {
		if updatedBE == nil {
			// Should not happend
			h.logger.LogAttrs(context.Background(), slog.LevelError, "nil backend")
			continue
		}
		// TODO: need to check if only the Metadata has changed
		// If so, no need to reload

		err := h.client.BackendEdit(*updatedBE)
		if err != nil {
			h.logger.LogAttrs(
				context.Background(), slog.LevelError, "failed to edit backend",
				logging.LogAttrError(err),
			)
			errors.Add(err)
			continue
		}
	}

	// Global
	for _, global := range updated.Globals {
		err := h.client.GlobalEdit(global, mergeStrategies.Global)
		if err != nil {
			h.logger.LogAttrs(
				context.Background(), slog.LevelError, "failed to edit global",
				logging.LogAttrError(err),
			)
			errors.Add(err)
		}
	}

	// Defaults
	for _, defaults := range updated.Defaults {
		err := h.client.DefaultsSectionEdit(defaults, mergeStrategies.Defaults)
		if err != nil {
			h.logger.LogAttrs(
				context.Background(), slog.LevelError, "failed to edit defaults section",
				logging.LogAttrError(err),
			)
			errors.Add(err)
		}
	}

	return errors.Result()
}

func (h *AppManagerImpl) confUpdateProcessed(haproxyCfgDiffs diffs.HaproxyConfDiffs, err error) {
	h.client.APIDisposeTransaction()

	result := gatehaproxy.HaproxyConfUpdateResult{
		Error:                   err,
		UpdatedSectionsMetaData: make(map[string]any),
	}

	// Frontends
	for _, fe := range haproxyCfgDiffs.Created.Frontends {
		addFrontendMetadataToResult(result.UpdatedSectionsMetaData, fe)
	}
	for _, fe := range haproxyCfgDiffs.Updated.Frontends {
		addFrontendMetadataToResult(result.UpdatedSectionsMetaData, fe)
	}
	for _, fe := range haproxyCfgDiffs.Deleted.Frontends {
		addFrontendMetadataToResult(result.UpdatedSectionsMetaData, fe)
	}
	// Backends
	for _, be := range haproxyCfgDiffs.Created.Backends {
		addBackendMetadataToResult(result.UpdatedSectionsMetaData, be)
	}
	for _, be := range haproxyCfgDiffs.Updated.Backends {
		addBackendMetadataToResult(result.UpdatedSectionsMetaData, be)
	}
	for _, be := range haproxyCfgDiffs.Deleted.Backends {
		addBackendMetadataToResult(result.UpdatedSectionsMetaData, be)
	}

	h.logger.LogAttrs(context.Background(), slog.LevelInfo, "Haproxy configuration update result",
		slog.Any("result", result))

	if haproxyCfgDiffs.Done != nil {
		h.logger.LogAttrs(context.Background(), slog.LevelInfo, "[sending] HUG => CONTROLLER: DIFFS (Done)")
		close(haproxyCfgDiffs.Done)
	}

	if haproxyCfgDiffs.ResultCh != nil {
		confResult := diffs.HaproxyConfResult{
			Err:                        err,
			GatewayObservedGenerations: haproxyCfgDiffs.GatewayObservedGenerations(),
		}
		h.logger.LogAttrs(context.Background(), slog.LevelInfo, "[sending] HUG => CONTROLLER: haproxy conf update result", slog.Any("result", confResult))
		select {
		case haproxyCfgDiffs.ResultCh <- confResult:
		default:
			// TODO: check with the team
			h.logger.LogAttrs(
				context.Background(), slog.LevelError,
				"dropping haproxy conf result: ResultCh is full",
			)
		}
	}
}

func addFrontendMetadataToResult(meta map[string]any, fe *models.Frontend) {
	if fe != nil {
		meta[fe.Name] = fe.Metadata
	}
}

func addBackendMetadataToResult(meta map[string]any, be *models.Backend) {
	if be != nil {
		meta[be.Name] = be.Metadata
	}
}
