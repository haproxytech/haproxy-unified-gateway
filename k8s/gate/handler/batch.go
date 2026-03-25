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
package handler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	hapi "github.com/haproxytech/haproxy-unified-gateway/hug/haproxy/api"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/events"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/certificate"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/diffs"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/storage"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/structured"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/hugservice"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/metrics"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/status"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"
	utilsk8s "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils-k8s"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// EventHandler handles a batch of events.
// Its builds a ClusterStore of k8s resources.
type EventHandler interface {
	// HandleEventBatch handles a batch of events.
	// EventBatch can include duplicated events.
	HandleEventBatch(ctx context.Context, batch events.EventBatch)
}

type GateTreeConfig struct {
	// k8sClient is a Kubernetes API client.
	K8sClient client.Client
	// k8sReader is a Kubernets API reader.
	K8sReader                  client.Reader
	CertificateStorage         storage.CertificateStorage
	MapsStorage                storage.MapsStorageEx
	BaseLogger                 *slog.Logger
	LogCategoryFilterHandler   *logging.CategoryFilterHandler
	ExtractGVK                 utilsk8s.ExtractGVK
	TransferHaproxyConfChannel chan diffs.HaproxyConfDiffs
	//  Namespace and name of the controller conf CRD
	ControllerConfNsName types.NamespacedName
	// ControllerName
	ControllerName string
	// StoreCertificatesOnDisk is a flag that indicates to the gate library to store certificates on disk
	StoreCertificateOnDisk bool
	// StoreMapsOnDisk is a flag that indicates to the gate library to store maps on disk
	StoreMapsOnDisk bool
	// RuntimeUpdateHaproxy
	RuntimeUpdateHaproxy bool
	// DisableIPv4 indicates whether IPv4 is disabled.
	DisableIPv4 bool
	// DisableIPv6 indicates whether IPv6 is disabled.
	DisableIPv6 bool
}

// eventHandlerImpl implements EventHandler.
// eventHandlerImpl is responsible for:
// - Reconciling the Gateway API and Kubernetes built-in resources with the HAProxy configuration.
// - building the GateTree
type eventHandlerImpl struct {
	clusterStoreUpdater  store.ClusterStoreUpdater
	haproxyConfBuilder   haproxy.HaproxyConfMgr
	statusUpdater        status.StatusUpdater
	hugServiceReconciler *hugservice.ServiceReconciler
	logger               *slog.Logger
	treeBuilder          GateTreeBuilder
	config               GateTreeConfig
	statusOnce           sync.Once
}

// NewEventHandlerImpl creates a new eventHandlerImpl.
func NewEventHandlerImpl(
	clusterStore *store.ClusterStore,
	gateTreeConfig GateTreeConfig,
	haproxyCfgManagerParams haproxy.HaproxyConfMgrParams,
	initialStructuredConf structured.Structured,
	haproxyClient hapi.HAProxyClient,
) EventHandler {
	clusterStoreUpdater := store.NewClusterStoreUpdaterImpl(
		clusterStore,
		gateTreeConfig.ExtractGVK,
		gateTreeConfig.BaseLogger,
	)

	gateTree := tree.NewGateTree()
	unmanagedGateTree := tree.NewGateTree()
	referencedObjects := tree.NewReferencedObjects(gateTreeConfig.ExtractGVK)

	controllerStore := tree.ControllerStore{
		ClusterStore:      clusterStore,
		GateTree:          gateTree,
		ReferencedObjects: referencedObjects,
		UnmanagedGateTree: unmanagedGateTree,
		ExtractGVK:        gateTreeConfig.ExtractGVK,
		Logger:            gateTreeConfig.BaseLogger.With(logging.LogAttrCategory(logging.LogCategoryGate)),
		InstalledGwAPIVersions: &tree.InstalledVersions{
			Versions: make(map[string]int),
		},
		CertUpdates: &tree.CertUpdates{
			Created: make(map[string]certificate.CertificateData),
			Updated: make(map[string]certificate.CertificateData),
			Deleted: make(map[string]certificate.CertificateData),
		},
		CrtListUpdates: &tree.CrtListUpdates{
			Created: make(map[string]certificate.CrtListData),
			Updated: make(map[string]certificate.CrtListData),
			Deleted: make(map[string]certificate.CrtListData),
		},
		ControllerName: gateTreeConfig.ControllerName,
	}

	treeBuilder := NewGateTreeBuilder(&controllerStore, gateTreeConfig)

	haproxyConfMgr := haproxy.NewHaproxyConfMgr(gateTreeConfig.BaseLogger, &controllerStore, initialStructuredConf,
		haproxyCfgManagerParams, haproxyClient, gateTreeConfig.K8sClient)

	handler := &eventHandlerImpl{
		treeBuilder:          treeBuilder,
		config:               gateTreeConfig,
		clusterStoreUpdater:  clusterStoreUpdater,
		haproxyConfBuilder:   haproxyConfMgr,
		hugServiceReconciler: hugservice.New(gateTreeConfig.K8sClient, gateTreeConfig.BaseLogger),
		statusUpdater: status.NewStatusUpdater(status.NewStatusUpdaterConf(
			gateTreeConfig.K8sClient,
			gateTreeConfig.ExtractGVK,
			gateTreeConfig.ControllerName,
			gateTreeConfig.BaseLogger,
			gateTreeConfig.DisableIPv4,
			gateTreeConfig.DisableIPv6,
		)),
		logger: gateTreeConfig.BaseLogger.With(logging.LogAttrCategory(logging.LogCategoryBatch)),
	}

	return handler
}

func (h *eventHandlerImpl) HandleEventBatch(ctx context.Context, batch events.EventBatch) {
	start := time.Now()

	h.logger.LogAttrs(context.Background(), slog.LevelInfo,
		"Started processing event batch",
		logging.LogAttrBatch(batch.BatchID, len(batch.Events)),
	)

	defer func() {
		duration := time.Since(start)
		metrics.EventBatchDuration.Observe(duration.Seconds())
		metrics.EventBatchSize.Observe(float64(len(batch.Events)))
		metrics.EventBatchTotal.Inc()
		h.logger.LogAttrs(context.Background(), slog.LevelInfo,
			"Finished processing event batch",
			logging.LogAttrBatch(batch.BatchID, len(batch.Events)),
			logging.LogAttrDuration(duration),
		)
	}()

	// Process each event in the batch
	_ = h.processBatch(batch)

	// Build the GateTree
	h.treeBuilder.buildGateTree()
	gatetree := h.treeBuilder.GetTree()

	// HAProxy Configuration building
	configStart := time.Now()
	err := h.haproxyConfBuilder.ComputeDiffs(ctx)
	metrics.ConfigGenerationDuration.Observe(time.Since(configStart).Seconds())
	if err != nil {
		metrics.EventBatchErrors.Inc()
		h.logger.LogAttrs(context.Background(), slog.LevelError,
			"error building HAProxy configuration",
			logging.LogAttrError(err),
		)
	}
	haproxyConfDiffs := h.haproxyConfBuilder.GetDiffs()
	h.recordDiffMetrics(haproxyConfDiffs)

	// -------------
	// Status updates
	// Note: status updates are performed asynchronously — they are prepared here
	// and dispatched to a background worker started by statusOnce.Do, so they
	// do not block the current reconciliation loop.
	// ---------------
	h.statusOnce.Do(func() { h.statusUpdater.Start(ctx) })
	// Prepare the status updates from the Tree
	statusUpdates := h.statusUpdater.PrepareStatusUpdate(ctx, gatetree.GatewayClasses, gatetree.Gateways, gatetree.HTTPRoutes, gatetree.TLSRoutes)
	// Now process them
	h.statusUpdater.UpdateStatus(ctx, statusUpdates)

	if !haproxyConfDiffs.IsEmpty() || haproxyConfDiffs.ReloadNeed {
		if h.config.TransferHaproxyConfChannel != nil {
			h.logger.LogAttrs(context.Background(), slog.LevelInfo, "[sending] CONTROLLER => HUG: DIFFS")
			haproxyConfDiffs.Done = make(chan struct{})
			haproxyConfDiffs.ResultCh = make(chan diffs.HaproxyConfResult, 1)
			transferStart := time.Now()
			h.config.TransferHaproxyConfChannel <- haproxyConfDiffs
			h.waitDone(haproxyConfDiffs.Done)
			metrics.ConfigTransferDuration.Observe(time.Since(transferStart).Seconds())
			h.logger.LogAttrs(context.Background(), slog.LevelInfo, "[received] HUG => CONTROLLER: DIFFS (Done)")

			// Forward the HUG result back as status feedback to the relevant Gateway/Route objects.
			h.forwardFeedback(ctx, haproxyConfDiffs.ResultCh)
		}
	}

	// Reconcile Hug service Ports
	h.hugServiceReconciler.ReconcilePorts(ctx, gatetree.VirtualListeners)
}

func (h *eventHandlerImpl) processBatch(batch events.EventBatch) bool {
	h.clusterStoreUpdater.ResetUpdates()

	for _, e := range batch.Events {
		h.updateClusterStore(e)
	}
	return true
}

func (h *eventHandlerImpl) updateClusterStore(event any) {
	switch obj := event.(type) {
	case *events.UpsertEvent:
		gvk := h.config.ExtractGVK(obj.Resource)
		h.logger.LogAttrs(context.Background(), slog.LevelDebug,
			"Processing event in batch",
			logging.LogAttrEventType("upsert"),
			logging.LogAttrResource(obj.Resource, gvk),
		)
		metrics.EventsProcessed.WithLabelValues("upsert").Inc()
		h.clusterStoreUpdater.Upsert(obj.Resource)

	case *events.DeleteEvent:
		gvk := h.config.ExtractGVK(obj.Type)

		h.logger.LogAttrs(context.Background(), slog.LevelDebug,
			"Processing event in batch",
			logging.LogAttrEventType("delete"),
			logging.LogAttrResource(obj.Type, gvk),
		)
		metrics.EventsProcessed.WithLabelValues("delete").Inc()
		h.clusterStoreUpdater.Delete(obj.Type, obj.NamespacedName)
	}
}

// waitDoneTimeout is the maximum time to wait for Done to be closed after
// sending diffs to HUG.
const waitDoneTimeout = 30 * time.Second

// waitDone blocks until done is closed or waitDoneTimeout elapses, logging a
// warning in the latter case.
func (h *eventHandlerImpl) waitDone(done <-chan struct{}) {
	select {
	case <-done:
	case <-time.After(waitDoneTimeout):
		h.logger.LogAttrs(context.Background(), slog.LevelWarn,
			"timed out waiting for Done signal from HUG",
		)
	}
}

// forwardFeedbackTimeout is the maximum time to wait for a result on ResultCh
// after Done has been closed. The result may arrive slightly after Done in some
// implementations, so we give a short grace period before giving up.
const forwardFeedbackTimeout = 5 * time.Second

// forwardFeedback waits up to forwardFeedbackTimeout for a result on resultCh,
// prepares the feedback status updates, and dispatches them through the shared
// statusUpdater channel.
func (h *eventHandlerImpl) forwardFeedback(ctx context.Context, resultCh chan diffs.HaproxyConfResult) {
	select {
	case result := <-resultCh:
		h.logger.LogAttrs(ctx, slog.LevelInfo, "[received] HUG => CONTROLLER: haproxy conf update result ", slog.Any("result", result))
		gatetree := h.treeBuilder.GetTree()
		for gwKey, gen := range result.GatewayObservedGenerations {
			gatetree.UpdateListenerProgrammedCondition(gwKey, gen, result.Err)
		}
		gateways := gatetree.RLockGateways()
		updates := h.statusUpdater.PrepareFeedbackStatusUpdate(result, gateways)
		gatetree.RUnlockGateways()
		h.statusUpdater.UpdateStatus(ctx, updates)
	case <-time.After(forwardFeedbackTimeout):
		// TODO: check with the team
		h.logger.LogAttrs(context.Background(), slog.LevelWarn,
			"timed out waiting for result from HUG on ResultCh",
		)
	}
}

func (*eventHandlerImpl) recordDiffMetrics(d diffs.HaproxyConfDiffs) {
	metrics.ConfigDiffs.WithLabelValues("created", "frontend").Add(float64(len(d.Created.Frontends)))
	metrics.ConfigDiffs.WithLabelValues("updated", "frontend").Add(float64(len(d.Updated.Frontends)))
	metrics.ConfigDiffs.WithLabelValues("deleted", "frontend").Add(float64(len(d.Deleted.Frontends)))
	metrics.ConfigDiffs.WithLabelValues("created", "backend").Add(float64(len(d.Created.Backends)))
	metrics.ConfigDiffs.WithLabelValues("updated", "backend").Add(float64(len(d.Updated.Backends)))
	metrics.ConfigDiffs.WithLabelValues("deleted", "backend").Add(float64(len(d.Deleted.Backends)))
	metrics.ConfigDiffs.WithLabelValues("created", "global").Add(float64(len(d.Created.Globals)))
	metrics.ConfigDiffs.WithLabelValues("updated", "global").Add(float64(len(d.Updated.Globals)))
	metrics.ConfigDiffs.WithLabelValues("deleted", "global").Add(float64(len(d.Deleted.Globals)))
	metrics.ConfigDiffs.WithLabelValues("created", "defaults").Add(float64(len(d.Created.Defaults)))
	metrics.ConfigDiffs.WithLabelValues("updated", "defaults").Add(float64(len(d.Updated.Defaults)))
	metrics.ConfigDiffs.WithLabelValues("deleted", "defaults").Add(float64(len(d.Deleted.Defaults)))
}
