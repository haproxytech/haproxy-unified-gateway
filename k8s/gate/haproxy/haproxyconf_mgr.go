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
	"log/slog"
	"sync"

	"github.com/haproxytech/haproxy-unified-gateway/hug/haproxy/api"
	"github.com/haproxytech/haproxy-unified-gateway/hug/reload"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/diffs"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/metadata"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/structured"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

type HaproxyConfMgr interface {
	// ComputeDiffs computes the HAProxy configuration diffs.
	ComputeDiffs(ctx context.Context) error
	GetDiffs() diffs.HaproxyConfDiffs
}

var _ HaproxyConfMgr = &HaproxyConfMgrImpl{}

type HaproxyConfMgrImpl struct {
	// backendsImpactedInCycle are all the upserted/deleted backends in the refresh cycle
	backendsImpactedInCycle BackendsImpactedInCycle
	metadataManager         metadata.Manager
	// haproxyClient is set if HaproxyConfMgrParams.UpdateHaproxyThroughRuntime is true
	haproxyClient api.HAProxyClient
	k8sClient     client.Client
	// Backend owners
	backendOwners   BackendReferencedBy // map[backendName] -> map[ownerType] -> map[ownerName] -> struct{}
	logger          *slog.Logger
	mu              *sync.Mutex
	controllerStore *tree.ControllerStore
	configuration   Configuration
	firstSync       FirstSync
	params          HaproxyConfMgrParams
}

type FirstSync struct {
	// frontends that are present at startup, used to cleanup after the first sync
	// the frontends that are not anymore in the cluster
	frontends map[string]struct{}
	// Same for backends
	backends map[string]struct{}

	// local managers
	routeManager RouteMgrImpl
	// If this is the initial sync, we will add to the diffs Deleted all items that are not upserted
	flag bool // True if this is the initial sync
}

func NewHaproxyConfMgr(logger *slog.Logger, controllerStore *tree.ControllerStore, startupStructured structured.Structured,
	params HaproxyConfMgrParams, haproxyClient api.HAProxyClient, k8sClient client.Client,
) HaproxyConfMgr {
	impl := HaproxyConfMgrImpl{
		controllerStore: controllerStore,
		configuration: Configuration{
			structured: startupStructured,
		},
		params: params,
		logger: logger.With(logging.LogAttrCategory(logging.LogCategoryHaproxyCfgMgr)),
		firstSync: FirstSync{
			frontends: make(map[string]struct{}),
			backends:  make(map[string]struct{}),
			flag:      true,
		},
		backendOwners: NewBackendOwners(),
		backendsImpactedInCycle: BackendsImpactedInCycle{
			Upserted:     make(map[string]map[client.ObjectKey]BackendImpactedInCycle),
			Deleted:      make(map[string]struct{}),
			Unreferenced: make(map[string]struct{}),
		},
		metadataManager: metadata.NewManager(params.extractGVK, controllerStore, params.LinkID),
		haproxyClient:   haproxyClient,
		k8sClient:       k8sClient,
		mu:              &sync.Mutex{},
	}
	impl.firstSync.routeManager = RouteMgrImpl{topManager: &impl}

	return &impl
}

func (b *HaproxyConfMgrImpl) ComputeDiffs(ctx context.Context) error {
	logger := b.logger
	logger.LogAttrs(context.Background(), slog.LevelDebug, "Start computing HAProxy configuration diffs")
	defer logger.LogAttrs(context.Background(), slog.LevelDebug, "Finished computing HAProxy configuration diffs")

	// Clear the previous configuration diffs
	// This is important to ensure that we only transfer the current configuration changes.
	b.configuration.resetDiffs()
	// Refresh the backends impacted in the refresh cycle
	b.backendsImpactedInCycle = BackendsImpactedInCycle{
		Upserted:     make(map[string]map[client.ObjectKey]BackendImpactedInCycle),
		Deleted:      make(map[string]struct{}),
		Unreferenced: make(map[string]struct{}),
	}

	// Build HAProxy configuration for the Gateways
	if err := b.processVirtualListener(); err != nil {
		logger.LogAttrs(context.Background(), slog.LevelError, "Failed to build Gateways",
			logging.LogAttrError(err))
	}

	// ----------
	// Global CR
	if err := b.processGlobal(); err != nil {
		logger.LogAttrs(context.Background(), slog.LevelInfo, "Error processing globalCR",
			logging.LogAttrError(err))
	}

	// -----------
	// Defaults CR
	if err := b.processDefaults(); err != nil {
		logger.LogAttrs(context.Background(), slog.LevelInfo, "Error processing defaultsCR",
			logging.LogAttrError(err))
	}

	// -----------
	// Certificates
	if err := b.processCertificates(); err != nil {
		logger.LogAttrs(context.Background(), slog.LevelInfo, "Error processing certificates",
			logging.LogAttrError(err))
	}

	// ------------
	// HTTPRoutes
	if err := b.processHTTPRoutes(); err != nil {
		logger.LogAttrs(context.Background(), slog.LevelInfo, "Error processing HTTPRoutes",
			logging.LogAttrError(err))
	}

	// ------------
	// TLSRoutes
	if err := b.processTLSRoutes(); err != nil {
		logger.LogAttrs(context.Background(), slog.LevelInfo, "Error processing TLSRoutes",
			logging.LogAttrError(err))
	}

	// -----------
	// Routes (All types)
	if err := b.firstSync.routeManager.processRoutes(); err != nil {
		logger.LogAttrs(context.Background(), slog.LevelInfo, "Error processing routes",
			logging.LogAttrError(err))
	}

	b.configuration.diffs.ReloadNeed = reload.Instance().NeedReload()

	// Perform the needed cleanup after the first sync
	// Remove frontends and backends that were present at startup but not anymore in the cluster
	b.cleanupAfterFirstSync()

	// Now handle Servers (EndpointSlices)
	if err := b.processEndpointSlices(ctx); err != nil {
		logger.LogAttrs(context.Background(), slog.LevelInfo, "Error processing EndpointSlices",
			logging.LogAttrError(err))
	}

	return nil
}

func (b *HaproxyConfMgrImpl) GetDiffs() diffs.HaproxyConfDiffs {
	return b.configuration.diffs
}

func (b *HaproxyConfMgrImpl) cleanupAfterFirstSync() {
	if !b.firstSync.flag {
		return
	}

	// Check all frontends that are in the configuration but not any more in the cluster
	// If the initial sync period was too short, the impact is that we would send a Delete on the frontend
	// to the application.
	// But we would receive an Upsert on the next sync
	// The state will be eventually consistent
	for feName := range b.configuration.structured.Frontends {
		_, toKeep := b.firstSync.frontends[feName]
		if !toKeep {
			b.logger.LogAttrs(
				context.Background(), slog.LevelDebug, "Frontend [DELETE STARTUP]",
				logging.LogAttrFrontendName(feName),
			)
			if err := b.configuration.deleteFrontend(b.logger, feName); err != nil {
				slog.LogAttrs(context.Background(), slog.LevelError, "Failed to delete frontend [startup]",
					logging.LogAttrFrontendName(feName))
				// continue to delete the rest of the frontends
			}
		}
	}

	// Same for Backends
	for beName := range b.configuration.structured.Backends {
		_, toKeep := b.firstSync.backends[beName]
		if !toKeep {
			b.logger.LogAttrs(
				context.Background(), slog.LevelDebug, "Backend [DELETE STARTUP]",
				logging.LogAttrBackendName(beName),
			)
			if err := b.configuration.deleteBackend(b.logger, beName); err != nil {
				slog.LogAttrs(context.Background(), slog.LevelError, "Failed to delete backend [startup]",
					logging.LogAttrBackendName(beName))
				// continue to delete the rest of the frontends
			}
		}
	}

	b.firstSync.frontends = make(map[string]struct{})
	b.firstSync.backends = make(map[string]struct{})
	b.firstSync.flag = false
}
