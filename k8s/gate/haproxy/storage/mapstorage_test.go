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

package storage

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMapsStorage(t *testing.T, mapsBaseDir string) MapsStorage {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))

	return NewMapsStorage(logger, mapsBaseDir)
}

func writeMapFile(t *testing.T, dir, name string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("key value\n"), 0o644))
}

func TestDeleteMapsDirectoryForFrontendIsIdempotent(t *testing.T) {
	mapsBaseDir := t.TempDir()
	storage := newTestMapsStorage(t, mapsBaseDir)

	// A frontend that never had a map file: nothing to remove, not an error.
	assert.NoError(t, storage.DeleteMapsDirectoryForFrontend("hug_http_31999"))

	storage.GetListenerExactMatchMapFile("hug_http_31080")
	dir := filepath.Join(mapsBaseDir, "hug_http_31080")
	writeMapFile(t, dir, "listener_exact_match.map")

	require.NoError(t, storage.DeleteMapsDirectoryForFrontend("hug_http_31080"))

	assert.NoDirExists(t, dir)
	assert.NotContains(t, storage.GetMaps(), dir)

	// A replayed deletion removes the same frontend twice.
	assert.NoError(t, storage.DeleteMapsDirectoryForFrontend("hug_http_31080"))
}
