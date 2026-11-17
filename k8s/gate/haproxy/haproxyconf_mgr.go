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
	"time"

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
	// RecordDiffApplicationOutcome decides what the next cycle does with the
	// diffs, from the result of applying them: replay them, retry only the reload,
	// or forget them because they went through.
	RecordDiffApplicationOutcome(applicationResult diffs.HaproxyConfResult)
	// HasConfigurationToRetry reports whether a cycle that was not applied is still
	// waiting for another one.
	HasConfigurationToRetry() bool
}

var _ HaproxyConfMgr = &HaproxyConfMgrImpl{}

type HaproxyConfMgrImpl struct {
	// backendsImpactedInCycle are all the upserted/deleted backends in the refresh cycle
	backendsImpactedInCycle BackendsImpactedInCycle
	// suspendedReplayResumesAt is when a suspended replay tries again, alone. The
	// budget stops the repetition of an input that keeps failing, but the input is
	// not the only thing that can change: an environment repaired (a full disk, a
	// runtime socket back), or a change the fingerprint cannot see, would otherwise
	// leave the replay suspended until someone touches a resource or restarts HUG.
	suspendedReplayResumesAt time.Time
	metadataManager          metadata.Manager
	// haproxyClient is set if HaproxyConfMgrParams.UpdateHaproxyThroughRuntime is true
	haproxyClient api.HAProxyClient
	k8sClient     client.Client
	// Backend owners
	backendOwners   BackendReferencedBy // map[backendName] -> map[ownerType] -> map[ownerName] -> struct{}
	logger          *slog.Logger
	mu              *sync.Mutex
	controllerStore *tree.ControllerStore
	firstSync       FirstSync
	// fingerprintOfTheInputThatFailed is the fingerprint the diff set had when it
	// failed, used to tell a repetition of the same failure from a new one.
	fingerprintOfTheInputThatFailed string
	configuration                   Configuration
	params                          HaproxyConfMgrParams
	// consecutiveFailuresOfTheSameInput counts how many times in a row the same
	// input failed to apply. "Same input" means the resources the diff set is built
	// from, fingerprinted with their generations: a changed input resets the count,
	// an unchanged one is retried a bounded number of times and then suspended.
	// Without it, a failure that persists would re-run the apply -- and its HAProxy
	// configuration check -- on every sync period, forever.
	consecutiveFailuresOfTheSameInput int
	// pendingDiffsAwaitingReplay holds the diff set of a cycle that was not applied.
	// The API refused it, so it is not in the configuration HAProxy runs: it is kept
	// and re-sent at the next cycle instead of being reset. Without it a failed
	// cycle is lost for good, because the diff base (configuration.structured)
	// already holds its values: the recomputation finds nothing left to emit and
	// the changes are never re-emitted.
	pendingDiffsAwaitingReplay bool
	// committedConfigurationAwaitingReload means the configuration reached the file
	// but HAProxy refused to reload it: the next cycle must ask for a reload again,
	// and must not replay the diffs, which are already written.
	committedConfigurationAwaitingReload bool
	// reloadRequestedByUnappliedCycle carries the reload intent of a cycle the API
	// refused. The reload manager is reset at the start of every cycle, so an
	// intent it recorded for a cycle that was never committed is gone by the next
	// one: without this, a cycle whose only effect was to ask for a reload would
	// lose it, and the pending failure would look like an empty, satisfied cycle.
	reloadRequestedByUnappliedCycle bool
	// replaySuspendedBecauseTheSameInputKeepsFailing means the same input has failed
	// more times than the budget allows: the diffs stay pending but GetDiffs reports
	// nothing, so the apply is not retried on every sync period. Two things end the
	// suspension -- the input changing (a new generation, a certificate, a Global or
	// Defaults edit) or suspendedReplayResumesAt elapsing -- which is why the name
	// states the cause and not a condition of its own. The diffs are not dropped,
	// because dropping them would leave the applied configuration behind for good:
	// the change is gone from the tree and from structured, and a deletion in
	// particular can never be regenerated.
	replaySuspendedBecauseTheSameInputKeepsFailing bool
}

// maxConsecutiveFailuresOfAnUnchangedInput is the number of times the same input
// may fail in a row before the replay is suspended. A changed input resets the
// count, and a fixed or deleted resource regenerates the diffs by itself, so
// stopping loses nothing but the retry of an input that fails deterministically.
const maxConsecutiveFailuresOfAnUnchangedInput = 3

// suspendedReplayRetryInterval is how long a suspended replay waits before trying
// once more, alone. The budget bounds the repetition of a failing input; this
// bounded retry covers what the input cannot express, and costs one configuration
// check per interval until it goes through or the input changes on its own.
const suspendedReplayRetryInterval = time.Minute

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

	// A cycle that failed to apply is replayed, not recomputed.
	pendingDiffsAwaitingReplay, committedConfigurationAwaitingReload := b.startCycleReplayingPendingDiffsOrResettingThem()
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

	b.configuration.diffs.ReloadNeed = reload.Instance().NeedReload() ||
		committedConfigurationAwaitingReload || b.reloadRequestedByUnappliedCycle

	// Perform the needed cleanup after the first sync
	// Remove frontends and backends that were present at startup but not anymore in the cluster
	b.cleanupAfterFirstSync()

	// Now handle Servers (EndpointSlices)
	if err := b.processEndpointSlices(ctx); err != nil {
		logger.LogAttrs(context.Background(), slog.LevelInfo, "Error processing EndpointSlices",
			logging.LogAttrError(err))
	}

	if pendingDiffsAwaitingReplay {
		b.reconcilePendingDiffs()
	}

	// The input can change while the replay is suspended: a changed or deleted
	// resource updates the pending diffs (their fingerprint tells), and a
	// certificate or crt-list change is an input the diff set does not carry at
	// all. Either must resume, or a fix could never unblock the changes it made
	// the configuration fail on.
	b.resumeSuspendedReplayIfTheInputChanged()

	return nil
}

// reconcilePendingDiffs keeps the direction of the pending diffs that matches the
// last write of the cycle, and forgets the retry altogether when nothing is left
// to replay.
func (b *HaproxyConfMgrImpl) reconcilePendingDiffs() {
	b.configuration.reconcilePendingDiffs()

	if b.configuration.diffs.IsEmpty() && !b.configuration.diffs.ReloadNeed {
		// Nothing left to replay: the pending changes were reconciled away.
		b.clearRetryState()
	}
}

// resumeSuspendedReplayIfTheInputChanged lifts the suspension when the input the
// next attempt would send is not the one that failed. It runs in both failure
// states: a configuration that was committed but not reloaded has no pending
// diffs, and a correction arriving there would otherwise stay hidden.
func (b *HaproxyConfMgrImpl) resumeSuspendedReplayIfTheInputChanged() {
	if !b.replaySuspendedBecauseTheSameInputKeepsFailing {
		return
	}

	fingerprint := b.configuration.diffs.ResourceFingerprint()
	switch {
	case fingerprint != b.fingerprintOfTheInputThatFailed:
		b.fingerprintOfTheInputThatFailed = fingerprint
		b.resumeSuspendedReplay()
	case b.aCertificateOrCrtListInputChanged():
		b.resumeSuspendedReplay()
	}
}

// resumeSuspendedReplay gives the replay a fresh budget, and drops the deadline a
// previous suspension armed: the retry is no longer waiting for it.
func (b *HaproxyConfMgrImpl) resumeSuspendedReplay() {
	b.replaySuspendedBecauseTheSameInputKeepsFailing = false
	b.consecutiveFailuresOfTheSameInput = 1
	b.suspendedReplayResumesAt = time.Time{}
}

func (b *HaproxyConfMgrImpl) GetDiffs() diffs.HaproxyConfDiffs {
	if b.replaySuspendedBecauseTheSameInputKeepsFailing {
		// The replay is suspended: reporting nothing keeps the apply (and its
		// HAProxy configuration check) from running again until the input
		// changes. The diffs stay pending in the configuration.
		return diffs.HaproxyConfDiffs{}
	}

	return b.configuration.diffs
}

// HasConfigurationToRetry reports whether a previous cycle left a configuration
// that must be applied again. The event loop polls it on every sync period: a
// retry that waited for a resource to change would never run in a cluster that
// simply stayed broken for a while.
func (b *HaproxyConfMgrImpl) HasConfigurationToRetry() bool {
	if b.replaySuspendedBecauseTheSameInputKeepsFailing {
		// Suspended: try once the interval elapses, and not before. Polling is what
		// makes that interval reachable without any event to wait for.
		return !b.suspendedReplayResumesAt.IsZero() && !time.Now().Before(b.suspendedReplayResumesAt)
	}

	return b.pendingDiffsAwaitingReplay || b.committedConfigurationAwaitingReload ||
		b.reloadRequestedByUnappliedCycle
}

// clearRetryState forgets everything a failed cycle left behind: there is nothing
// left to replay and nothing left to reload.
func (b *HaproxyConfMgrImpl) clearRetryState() {
	b.pendingDiffsAwaitingReplay = false
	b.committedConfigurationAwaitingReload = false
	b.reloadRequestedByUnappliedCycle = false
	b.consecutiveFailuresOfTheSameInput = 0
	b.fingerprintOfTheInputThatFailed = ""
	b.replaySuspendedBecauseTheSameInputKeepsFailing = false
	b.suspendedReplayResumesAt = time.Time{}
}

// RecordDiffApplicationOutcome decides what the next ComputeDiffs does with the
// diff set, from the result of applying it. On failure the diffs stay pending
// and are replayed -- unless the configuration was committed, in which case only
// the reload is retried.
func (b *HaproxyConfMgrImpl) RecordDiffApplicationOutcome(applicationResult diffs.HaproxyConfResult) {
	if applicationResult.Err == nil {
		b.clearRetryState()
		return
	}

	// A configuration that was committed but not reloaded must not be replayed:
	// its diffs are already in the file, and replaying them would run the
	// creations and the deletions a second time, which client-native refuses for a
	// section that is already gone. Only the reload is retried in that case.
	b.pendingDiffsAwaitingReplay = !applicationResult.ReloadFailed
	b.committedConfigurationAwaitingReload = applicationResult.ReloadFailed
	// The reload manager is reset at the start of every cycle, so the reload
	// intent of a cycle the API never committed has to survive on its own: it is
	// not carried by the diff set the replay keeps.
	b.reloadRequestedByUnappliedCycle = !applicationResult.ReloadFailed && b.configuration.diffs.ReloadNeed

	// Only count a failure against the budget when the input did not change: a
	// replay that fails on a new resource is a new failure, not a repetition. A
	// committed cycle has its diffs dropped before the next one, so the input that
	// cycle will compare is the empty set unless a resource changes.
	fingerprint := b.configuration.diffs.ResourceFingerprint()
	if applicationResult.ReloadFailed {
		fingerprint = diffs.HaproxyConfDiffs{}.ResourceFingerprint()
	}
	if fingerprint == b.fingerprintOfTheInputThatFailed {
		b.consecutiveFailuresOfTheSameInput++
	} else {
		b.fingerprintOfTheInputThatFailed = fingerprint
		b.consecutiveFailuresOfTheSameInput = 1
	}

	if applicationResult.ReloadFailed {
		b.logger.LogAttrs(context.Background(), slog.LevelWarn,
			"HAProxy configuration was committed but not reloaded: the reload is retried on the next cycle",
			logging.LogAttrError(applicationResult.Err),
			slog.Int("attempt", b.consecutiveFailuresOfTheSameInput))
		return
	}

	b.logger.LogAttrs(context.Background(), slog.LevelWarn,
		"HAProxy configuration apply failed: the cycle's diffs stay pending and are replayed on the next cycle",
		logging.LogAttrError(applicationResult.Err),
		slog.Int("attempt", b.consecutiveFailuresOfTheSameInput))
}

// startCycleReplayingPendingDiffsOrResettingThem decides whether the previous
// cycle's pending diffs are replayed or reset.
//
// A cycle that failed to apply is replayed, not recomputed: its diffs were never
// applied, while the diff base (configuration.structured) already holds their
// values. Resetting them here would make the recomputation produce nothing and
// the changes would be lost for good.
//
// It returns whether the cycle is a replay and whether the reload must be
// retried (a configuration that was committed but not reloaded).
func (b *HaproxyConfMgrImpl) startCycleReplayingPendingDiffsOrResettingThem() (
	pendingDiffsAwaitingReplay, committedConfigurationAwaitingReload bool,
) {
	pendingDiffsAwaitingReplay = b.pendingDiffsAwaitingReplay
	committedConfigurationAwaitingReload = b.committedConfigurationAwaitingReload

	if !pendingDiffsAwaitingReplay && !committedConfigurationAwaitingReload {
		// Clear the previous configuration diffs
		// This is important to ensure that we only transfer the current configuration changes.
		b.configuration.resetDiffs()
		b.clearRetryState()
		return false, false
	}

	if committedConfigurationAwaitingReload {
		// The sections of that cycle are in the file HAProxy runs: replaying them
		// would create and delete them a second time, and a section the committed
		// cycle deleted can have been re-created since. Only the reload is retried,
		// on the diffs the current cycle computes.
		b.configuration.resetDiffs()
	}

	b.replaySuspendedBecauseTheSameInputKeepsFailing = b.consecutiveFailuresOfTheSameInput >= maxConsecutiveFailuresOfAnUnchangedInput
	if b.replaySuspendedBecauseTheSameInputKeepsFailing && !b.suspendedReplayResumesAt.IsZero() && time.Now().After(b.suspendedReplayResumesAt) {
		// The interval elapsed: try once more even though the input did not
		// change. The environment may have been repaired, or the change may be
		// one the fingerprint cannot see.
		b.logger.LogAttrs(context.Background(), slog.LevelInfo,
			"Retrying a suspended HAProxy configuration replay: the retry interval elapsed",
			slog.Int("attempts", b.consecutiveFailuresOfTheSameInput))
		b.replaySuspendedBecauseTheSameInputKeepsFailing = false
		b.suspendedReplayResumesAt = time.Time{}
	}

	if b.replaySuspendedBecauseTheSameInputKeepsFailing {
		// Suspend, do not drop: the same input keeps failing, so the apply (and
		// its HAProxy configuration check) is not retried again on every sync
		// period. The diffs stay pending; GetDiffs reports nothing until the
		// input changes or the retry interval elapses.
		//
		// The deadline is armed once, when the replay is suspended: re-arming it on
		// every cycle would push it back by a whole interval each time, and a
		// cluster that keeps producing cycles would never reach it.
		if b.suspendedReplayResumesAt.IsZero() {
			b.suspendedReplayResumesAt = time.Now().Add(suspendedReplayRetryInterval)
		}
		b.logger.LogAttrs(context.Background(), slog.LevelError,
			"HAProxy configuration replay suspended: the same input keeps failing, "+
				"it will be replayed when the configuration changes",
			slog.Int("attempts", b.consecutiveFailuresOfTheSameInput))
		return pendingDiffsAwaitingReplay, committedConfigurationAwaitingReload
	}

	b.logger.LogAttrs(context.Background(), slog.LevelInfo,
		"Retrying the previous HAProxy configuration failure",
		slog.Bool("reloadOnly", !pendingDiffsAwaitingReplay),
		slog.Int("attempt", b.consecutiveFailuresOfTheSameInput), slog.Int("maxAttempts", maxConsecutiveFailuresOfAnUnchangedInput))

	return pendingDiffsAwaitingReplay, committedConfigurationAwaitingReload
}

// aCertificateOrCrtListInputChanged reports whether the current cycle carries a
// certificate or crt-list change. Certificates are written and pushed to the
// runtime during ComputeDiffs, outside the diff set, so their state is not part
// of ResourceFingerprint: this is the only signal that a fixed Secret is a new
// input for a suspended replay.
func (b *HaproxyConfMgrImpl) aCertificateOrCrtListInputChanged() bool {
	certs := b.controllerStore.CertUpdates
	if certs != nil && len(certs.Created)+len(certs.Updated)+len(certs.Deleted) > 0 {
		return true
	}

	crtLists := b.controllerStore.CrtListUpdates

	return crtLists != nil && len(crtLists.Created)+len(crtLists.Updated)+len(crtLists.Deleted) > 0
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
