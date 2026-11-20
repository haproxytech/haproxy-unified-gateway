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
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/stretchr/testify/require"

	v3 "github.com/haproxytech/haproxy-unified-gateway/api/gate/v3"
	"github.com/haproxytech/haproxy-unified-gateway/hug/reload"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/certificate"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/diffs"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/storage"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/structured"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"
)

// newTestHaproxyConfMgr builds a manager over an empty tree, so the cycles run by
// these tests reach the replay state machine without producing diffs of their own.
func newTestHaproxyConfMgr(t *testing.T) *HaproxyConfMgrImpl {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	controllerStore := &tree.ControllerStore{
		ClusterStore:      store.NewClusterStore(),
		GateTree:          tree.NewGateTree(),
		UnmanagedGateTree: tree.NewGateTree(),
		Logger:            logger,
		CertUpdates: &tree.CertUpdates{
			Created: map[string]certificate.CertificateData{},
			Updated: map[string]certificate.CertificateData{},
			Deleted: map[string]certificate.CertificateData{},
		},
		CrtListUpdates: &tree.CrtListUpdates{
			Created: map[string]certificate.CrtListData{},
			Updated: map[string]certificate.CrtListData{},
			Deleted: map[string]certificate.CrtListData{},
		},
	}

	built := NewHaproxyConfMgr(logger, controllerStore, structured.NewStructuredConf(), HaproxyConfMgrParams{
		mapsStorage: storage.NewMapsStorage(logger, t.TempDir()),
	}, nil, nil)
	mgr, builtType := built.(*HaproxyConfMgrImpl)
	require.True(t, builtType, "NewHaproxyConfMgr returns the implementation these tests drive")

	// The startup cleanup and the first sync are out of scope here.
	mgr.firstSync.flag = false
	// The diff set is allocated by the first cycle in production; these tests write
	// to it before running one.
	mgr.configuration.resetDiffs()

	reload.Instance().SetLogger(logger)
	reload.Instance().Reset()
	t.Cleanup(reload.Instance().Reset)

	return mgr
}

func testFrontend(name string) *models.Frontend {
	return &models.Frontend{Name: name, Mode: "http"}
}

// A configuration that reached the file must not be sent again: the cycle after a
// refused reload re-applies nothing, it only asks for the reload.
func TestCommittedCycleIsNotReplayed(t *testing.T) {
	mgr := newTestHaproxyConfMgr(t)

	require.NoError(t, mgr.configuration.upsertFrontend(mgr.logger, testFrontend("hug_http_8080"), false))
	mgr.RecordDiffApplicationOutcome(diffs.HaproxyConfResult{
		Err:          errors.New("HAProxy refused the configuration"),
		ReloadFailed: true,
	})

	require.NoError(t, mgr.ComputeDiffs(context.Background()))

	got := mgr.GetDiffs()
	require.True(t, got.ReloadNeed, "the reload has to be retried")
	require.Empty(t, got.Created.Frontends, "a committed creation must not be replayed")
	require.Empty(t, got.Updated.Frontends, "a committed update must not be replayed")
	require.Empty(t, got.Deleted.Frontends, "a committed deletion must not be replayed")
}

// A section the committed cycle deleted can be re-created before the reload is
// retried. Replaying the deletion would remove it right after creating it, and the
// apply would report success over a frontend that is gone.
func TestCommittedDeletionIsNotReplayedOverTheRecreation(t *testing.T) {
	mgr := newTestHaproxyConfMgr(t)

	frontend := testFrontend("hug_http_8080")
	require.NoError(t, mgr.configuration.upsertFrontend(mgr.logger, frontend, false))
	mgr.configuration.resetDiffs() // the creation was applied by an earlier cycle
	require.NoError(t, mgr.configuration.deleteFrontend(mgr.logger, frontend.Name))
	mgr.RecordDiffApplicationOutcome(diffs.HaproxyConfResult{
		Err:          errors.New("HAProxy refused the configuration"),
		ReloadFailed: true,
	})

	mgr.startCycleReplayingPendingDiffsOrResettingThem()
	// The listener is back on the same port during this cycle.
	require.NoError(t, mgr.configuration.upsertFrontend(mgr.logger, frontend, false))

	got := mgr.GetDiffs()
	require.Empty(t, got.Deleted.Frontends,
		"the committed deletion would delete the frontend this cycle re-creates")
	require.Contains(t, got.Created.Frontends, frontend.Name)
}

// The retry deadline is armed once: a cycle that keeps finding the replay suspended
// must not push it back, or a cluster producing cycles would never reach it.
func TestSuspendedReplayKeepsItsDeadline(t *testing.T) {
	mgr := newTestHaproxyConfMgr(t)

	mgr.pendingDiffsAwaitingReplay = true
	mgr.consecutiveFailuresOfTheSameInput = maxConsecutiveFailuresOfAnUnchangedInput
	deadline := time.Now().Add(suspendedReplayRetryInterval / 2)
	mgr.suspendedReplayResumesAt = deadline

	mgr.startCycleReplayingPendingDiffsOrResettingThem()

	require.True(t, mgr.replaySuspendedBecauseTheSameInputKeepsFailing)
	require.WithinDuration(t, deadline, mgr.suspendedReplayResumesAt, 0,
		"the next cycle postponed an existing retry deadline")
}

// A replay suspended after reload failures must resume when the configuration
// changes: the fix is the only thing that can make the reload pass.
func TestSuspendedReplayResumesWhenTheInputChangesAfterAReloadFailure(t *testing.T) {
	mgr := newTestHaproxyConfMgr(t)

	// What three refused reloads leave behind: nothing to replay, a reload to
	// retry, and the input that failed once the committed diffs were dropped.
	mgr.committedConfigurationAwaitingReload = true
	mgr.consecutiveFailuresOfTheSameInput = maxConsecutiveFailuresOfAnUnchangedInput
	mgr.fingerprintOfTheInputThatFailed = diffs.HaproxyConfDiffs{}.ResourceFingerprint()
	mgr.suspendedReplayResumesAt = time.Now().Add(suspendedReplayRetryInterval)

	// The operator corrects the Global the configuration failed on.
	mgr.controllerStore.GateTree.Global = tree.NewGlobal(&v3.Global{
		Spec: v3.GlobalSpec{Global: models.Global{GlobalBase: models.GlobalBase{Daemon: true}}},
	})

	require.NoError(t, mgr.ComputeDiffs(context.Background()))

	require.False(t, mgr.replaySuspendedBecauseTheSameInputKeepsFailing,
		"a corrected configuration stays hidden by GetDiffs until something else resumes it")
	require.False(t, mgr.GetDiffs().IsEmpty(), "the correction has to be sent")
}

// A reload asked for by a cycle the API never committed is not part of the diff
// set: it must survive the reload manager reset of the next cycle.
func TestReloadRequestSurvivesAFailedTransaction(t *testing.T) {
	mgr := newTestHaproxyConfMgr(t)

	// A certificate or crt-list change asks for a reload without touching a section.
	mgr.configuration.diffs.ReloadNeed = true
	mgr.RecordDiffApplicationOutcome(diffs.HaproxyConfResult{Err: errors.New("cannot start transaction")})
	reload.Instance().Reset() // what the next cycle starts with

	require.NoError(t, mgr.ComputeDiffs(context.Background()))

	require.True(t, mgr.GetDiffs().ReloadNeed, "the reload requested by the failed cycle is lost")
	require.True(t, mgr.HasConfigurationToRetry(), "the failed cycle has nothing left to retry")
}

// The event loop polls HasConfigurationToRetry on every sync period: it must stay
// true while a retry is pending, and only once the suspension elapsed while the
// replay is suspended.
func TestHasConfigurationToRetry(t *testing.T) {
	tests := []struct {
		name                         string
		pendingDiffsAwaitingReplay   bool
		committedAwaitingReload      bool
		reloadRequestedByFailedCycle bool
		suspended                    bool
		deadline                     time.Time
		want                         bool
	}{
		{
			name: "nothing pending after a successful apply",
		},
		{
			name:                       "diffs left pending by a refused apply",
			pendingDiffsAwaitingReplay: true,
			want:                       true,
		},
		{
			name:                    "committed configuration waiting for its reload",
			committedAwaitingReload: true,
			want:                    true,
		},
		{
			name:                         "reload requested by a cycle that was never committed",
			reloadRequestedByFailedCycle: true,
			want:                         true,
		},
		{
			name:                       "suspended, the retry interval has not elapsed",
			pendingDiffsAwaitingReplay: true,
			suspended:                  true,
			deadline:                   time.Now().Add(time.Minute),
		},
		{
			name:                       "suspended, the retry interval elapsed",
			pendingDiffsAwaitingReplay: true,
			suspended:                  true,
			deadline:                   time.Now().Add(-time.Second),
			want:                       true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mgr := newTestHaproxyConfMgr(t)
			mgr.pendingDiffsAwaitingReplay = test.pendingDiffsAwaitingReplay
			mgr.committedConfigurationAwaitingReload = test.committedAwaitingReload
			mgr.reloadRequestedByUnappliedCycle = test.reloadRequestedByFailedCycle
			mgr.replaySuspendedBecauseTheSameInputKeepsFailing = test.suspended
			mgr.suspendedReplayResumesAt = test.deadline

			require.Equal(t, test.want, mgr.HasConfigurationToRetry())
		})
	}
}

// A successful apply forgets the retry state, so the loop stops running cycles for it.
func TestSuccessfulApplyForgetsTheRetry(t *testing.T) {
	mgr := newTestHaproxyConfMgr(t)

	mgr.RecordDiffApplicationOutcome(diffs.HaproxyConfResult{Err: errors.New("cannot start transaction")})
	require.True(t, mgr.HasConfigurationToRetry())

	mgr.RecordDiffApplicationOutcome(diffs.HaproxyConfResult{})

	require.False(t, mgr.HasConfigurationToRetry())
	require.Zero(t, mgr.consecutiveFailuresOfTheSameInput)
}
