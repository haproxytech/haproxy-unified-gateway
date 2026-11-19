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
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/storage/maps"
	"github.com/stretchr/testify/assert"
)

// fakeMapsStorage records the maps directories removals and fails the ones the
// test asks for.
type fakeMapsStorage struct {
	failFor map[string]bool
	removed []string
}

func (f *fakeMapsStorage) DeleteMapsDirectoryForFrontend(frontendName string) error {
	if f.failFor[frontendName] {
		return fmt.Errorf("cannot remove the maps directory of %s", frontendName)
	}

	f.removed = append(f.removed, frontendName)

	return nil
}

func (*fakeMapsStorage) DeleteMapsDirectory() error { return nil }

func (*fakeMapsStorage) RemoveOrphanMapsDirectories([]string) error { return nil }

func (*fakeMapsStorage) GetPathExactMapFile(string) *maps.MapFileState { return nil }

func (*fakeMapsStorage) GetPathPrefixMapFile(string) *maps.MapFileState { return nil }

func (*fakeMapsStorage) GetPathRegexMapFile(string) *maps.MapFileState { return nil }

func (*fakeMapsStorage) GetSniMapFile(string) *maps.MapFileState { return nil }

func (*fakeMapsStorage) GetTCPRouteMapFile(string) *maps.MapFileState { return nil }

func (*fakeMapsStorage) GetListenerExactMatchMapFile(string) *maps.MapFileState { return nil }

func (*fakeMapsStorage) GetListenerWildcardMatchMapFile(string) *maps.MapFileState {
	return nil
}

func (*fakeMapsStorage) GetListenerRouteExactMatchMapFile(string) *maps.MapFileState {
	return nil
}

func (*fakeMapsStorage) GetListenerRouteWildcardMatchMapFile(string) *maps.MapFileState {
	return nil
}

func (*fakeMapsStorage) GetMaps() map[string]map[string]*maps.MapFileState { return nil }

func (*fakeMapsStorage) ProcessMapFiles() {}

func newTestAppManager(t *testing.T, storage *fakeMapsStorage) *AppManagerImpl {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))

	return &AppManagerImpl{
		logger:                      logger,
		mapsStorage:                 storage,
		frontendsPendingMapsRemoval: map[string]struct{}{},
	}
}

func TestFlushMapsDirectoriesRemovalKeepsFailuresQueued(t *testing.T) {
	storage := &fakeMapsStorage{failFor: map[string]bool{"hug_http_31080": true}}
	mgr := newTestAppManager(t, storage)

	// What processDelete returned for a committed configuration, merged into the
	// pending set the way applyCfgUpdates merges it.
	mgr.frontendsPendingMapsRemoval["hug_http_31080"] = struct{}{}
	mgr.frontendsPendingMapsRemoval["hug_https_31443"] = struct{}{}
	mgr.removePendingMapsDirectoriesFromFrontends()

	// The removal that failed stays queued: the diffs that carried the deletion
	// are reset once applied, so they cannot retry it.
	assert.Equal(t, []string{"hug_https_31443"}, storage.removed)
	assert.Contains(t, mgr.frontendsPendingMapsRemoval, "hug_http_31080")
	assert.NotContains(t, mgr.frontendsPendingMapsRemoval, "hug_https_31443")

	// The filesystem error is gone: the next successful commit retries it.
	storage.failFor = map[string]bool{}
	mgr.removePendingMapsDirectoriesFromFrontends()

	assert.Contains(t, storage.removed, "hug_http_31080")
	assert.Empty(t, mgr.frontendsPendingMapsRemoval)
}
