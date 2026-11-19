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
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/stretchr/testify/require"

	"github.com/haproxytech/haproxy-unified-gateway/hug/haproxy/api"
	"github.com/haproxytech/haproxy-unified-gateway/hug/haproxy/process"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/diffs"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/storage"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/structured"
)

// frontendRecordingClient keeps the frontends the diffs create and delete, so a
// test can tell which of them the applied configuration declares.
type frontendRecordingClient struct {
	api.HAProxyClient
	declared map[string]models.Frontend
}

func (*frontendRecordingClient) APIStartTransaction() error       { return nil }
func (*frontendRecordingClient) APIFinalCommitTransaction() error { return nil }
func (*frontendRecordingClient) APIDisposeTransaction()           {}

func (c *frontendRecordingClient) FrontendCreate(frontend models.Frontend) error {
	c.declared[frontend.Name] = frontend

	return nil
}

func (c *frontendRecordingClient) FrontendEdit(frontend models.Frontend) error {
	c.declared[frontend.Name] = frontend

	return nil
}

func (c *frontendRecordingClient) FrontendDelete(frontendName string) error {
	delete(c.declared, frontendName)

	return nil
}

// noReloadProcess stands in for the process manager: these tests never ask for a reload.
type noReloadProcess struct{ process.Process }

func (*noReloadProcess) Service(string) (string, error) { return "", nil }

// failingMapsStorageRefusesTheFrontendRemoval lets the directory of one frontend be
// removed once, the way a filesystem error does.
type failingMapsStorageRefusesTheFrontendRemoval struct {
	storage.MapsStorage
	frontendName string
	failuresLeft int
}

func (receiver *failingMapsStorageRefusesTheFrontendRemoval) DeleteMapsDirectoryForFrontend(frontendName string) error {
	if frontendName == receiver.frontendName && receiver.failuresLeft > 0 {
		receiver.failuresLeft--

		return errors.New("cannot remove the maps directory")
	}

	return receiver.MapsStorage.DeleteMapsDirectoryForFrontend(frontendName)
}

func newMapsRemovalTestAppManager(t *testing.T) (*AppManagerImpl, *frontendRecordingClient, string) {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mapsBaseDir := t.TempDir()
	mapsStorage := &failingMapsStorageRefusesTheFrontendRemoval{
		MapsStorage:  storage.NewMapsStorage(logger, mapsBaseDir),
		frontendName: "hug_http_31080",
	}

	client := &frontendRecordingClient{declared: map[string]models.Frontend{}}
	mgr := &AppManagerImpl{
		logger:                      logger,
		client:                      client,
		process:                     &noReloadProcess{},
		mapsStorage:                 mapsStorage,
		frontendsPendingMapsRemoval: map[string]struct{}{},
	}

	return mgr, client, mapsBaseDir
}

func newMapsRemovalTestDiffs() diffs.HaproxyConfDiffs {
	return diffs.HaproxyConfDiffs{
		Created: structured.NewStructuredConf(),
		Updated: structured.NewStructuredConf(),
		Deleted: structured.NewStructuredConf(),
	}
}

// A frontend whose deletion was committed can be re-created under the same name
// before the pending directory removal is retried -- the name is all a frontend
// keeps. The commit that declares it again has to cancel the removal: the files the
// cycle wrote for it are the ones HAProxy reads after the reload.
func TestMapsOfAFrontendTheCommitDeclaresAgainAreKept(t *testing.T) {
	mgr, _, mapsBaseDir := newMapsRemovalTestAppManager(t)

	frontendName := "hug_http_31080"
	mapFile := filepath.Join(mapsBaseDir, frontendName, "path_exact.map")
	require.NoError(t, os.MkdirAll(filepath.Dir(mapFile), 0o755))
	require.NoError(t, os.WriteFile(mapFile, []byte("/ previous_backend\n"), 0o644))

	// The deletion is committed and the directory cannot be removed yet.
	deletion := newMapsRemovalTestDiffs()
	deletion.Deleted.Frontends[frontendName] = &models.Frontend{Name: frontendName}
	mgr.mapsStorage.(*failingMapsStorageRefusesTheFrontendRemoval).failuresLeft = 1
	require.NoError(t, mgr.applyCfgUpdates(deletion))
	require.Contains(t, mgr.frontendsPendingMapsRemoval, frontendName,
		"a removal that failed has to stay queued")

	// The listener comes back on the same port: the controller writes its maps
	// during the cycle, then the creation reaches the API.
	require.NoError(t, os.MkdirAll(filepath.Dir(mapFile), 0o755))
	require.NoError(t, os.WriteFile(mapFile, []byte("/ recreated_backend\n"), 0o644))
	creation := newMapsRemovalTestDiffs()
	creation.Created.Frontends[frontendName] = &models.Frontend{Name: frontendName}
	require.NoError(t, mgr.applyCfgUpdates(creation))

	require.FileExists(t, mapFile,
		"the pending removal deleted the maps of the frontend this commit declares")
	require.NotContains(t, mgr.frontendsPendingMapsRemoval, frontendName)
}

// The removal of a frontend the commit does not declare again is still applied: the
// cancellation must not turn the queue into a leak.
func TestMapsOfAFrontendTheCommitNoLongerDeclaresAreRemoved(t *testing.T) {
	mgr, _, mapsBaseDir := newMapsRemovalTestAppManager(t)

	removedFrontend := "hug_https_31443"
	require.NoError(t, os.MkdirAll(filepath.Join(mapsBaseDir, removedFrontend), 0o755))
	mgr.frontendsPendingMapsRemoval[removedFrontend] = struct{}{}

	// A commit that declares another frontend only.
	creation := newMapsRemovalTestDiffs()
	creation.Created.Frontends["hug_http_31080"] = &models.Frontend{Name: "hug_http_31080"}
	require.NoError(t, mgr.applyCfgUpdates(creation))

	require.NoDirExists(t, filepath.Join(mapsBaseDir, removedFrontend))
	require.Empty(t, mgr.frontendsPendingMapsRemoval)
}
