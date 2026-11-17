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
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/storage/maps"
)

//revive:disable:var-naming
const (
	PATH_EXACT_MAP                    = "path_exact"
	PATH_PREFIX_MAP                   = "path_prefix"
	PATH_REGEX_MAP                    = "path_regex"
	SNI_MAP                           = "sni"
	TCP_ROUTE_MAP                     = "tcp_route"
	MAP_LISTENER_EXACT_MATCH          = "listener_exact_match"
	MAP_LISTENER_WILDCARD_MATCH       = "listener_wildcard_match"
	MAP_LISTENER_ROUTE_EXACT_MATCH    = "listener_route_exact_match"
	MAP_LISTENER_ROUTE_WILDCARD_MATCH = "listener_route_wildcard_match"
)

//revive:enable:var-naming
var _ MapsStorage = &MapsStorageDefault{}

type MapsStorageDefault struct {
	logger      *slog.Logger
	mapFiles    map[string]map[string]*maps.MapFileState // map file dir -> map file name -> map contents
	MapsBaseDir string
}

// NewMapsStorage creates a new instance of MapsStorageEx with the given logger and maps base directory.
// It returns a pointer to the new instance.
// The logger is used to log messages related to the MapsStorageEx instance.
// The maps base directory is the directory where the maps storage will store the maps files.
func NewMapsStorage(logger *slog.Logger, mapsBaseDir string) MapsStorage {
	return &MapsStorageDefault{
		logger:      logger,
		MapsBaseDir: mapsBaseDir,
		mapFiles:    map[string]map[string]*maps.MapFileState{},
	}
}

func (m *MapsStorageDefault) GetListenerExactMatchMapFile(frontendName string) *maps.MapFileState {
	return m.getMapFile(frontendName, MAP_LISTENER_EXACT_MATCH, true)
}

func (m *MapsStorageDefault) GetListenerWildcardMatchMapFile(frontendName string) *maps.MapFileState {
	return m.getMapFile(frontendName, MAP_LISTENER_WILDCARD_MATCH, true)
}

func (m *MapsStorageDefault) GetListenerRouteExactMatchMapFile(frontendName string) *maps.MapFileState {
	return m.getMapFile(frontendName, MAP_LISTENER_ROUTE_EXACT_MATCH, true)
}

func (m *MapsStorageDefault) GetListenerRouteWildcardMatchMapFile(frontendName string) *maps.MapFileState {
	return m.getMapFile(frontendName, MAP_LISTENER_ROUTE_WILDCARD_MATCH, true)
}

func (m *MapsStorageDefault) GetPathExactMapFile(frontendName string) *maps.MapFileState {
	return m.getMapFile(frontendName, PATH_EXACT_MAP, false)
}

func (m *MapsStorageDefault) GetPathPrefixMapFile(frontendName string) *maps.MapFileState {
	return m.getMapFile(frontendName, PATH_PREFIX_MAP, false)
}

func (m *MapsStorageDefault) GetPathRegexMapFile(frontendName string) *maps.MapFileState {
	return m.getMapFile(frontendName, PATH_REGEX_MAP, false)
}

func (m *MapsStorageDefault) GetSniMapFile(frontendName string) *maps.MapFileState {
	return m.getMapFile(frontendName, SNI_MAP, false)
}

// GetTCPRouteMapFile returns the per-frontend map holding the port's selected
// TCPRoute target: a backend name, or the weighted-random JSON.
func (m *MapsStorageDefault) GetTCPRouteMapFile(frontendName string) *maps.MapFileState {
	return m.getMapFile(frontendName, TCP_ROUTE_MAP, false)
}

// getMapFile returns the MapFileState for the given frontend name and map name.
// If the map file does not exist, it creates a new instance of MapFileState,
// reads the map file from disk, and stores the map file in the mapFiles map.
// If there is an error reading the map file from disk, it logs an error message.
// It returns a pointer to the MapFileState.
func (m *MapsStorageDefault) getMapFile(frontendName string, mapName string, plainValues bool) *maps.MapFileState {
	mapBaseDir := filepath.Join(m.MapsBaseDir, frontendName)
	mapFileName := mapName + ".map"

	mapFilesInDir := m.mapFiles[mapBaseDir]
	if mapFilesInDir == nil {
		mapFilesInDir = map[string]*maps.MapFileState{}
		m.mapFiles[mapBaseDir] = mapFilesInDir
	}
	mapFile := mapFilesInDir[mapFileName]
	if mapFile == nil {
		mapFile = maps.NewMapFileState(mapBaseDir, mapFileName, m.logger)
		mapFile.PlainValues = plainValues
		m.mapFiles[mapBaseDir][mapFileName] = mapFile
	}
	return mapFile
}

// GetMaps returns a map of all MapFileState objects currently stored in MapsStorageExDefault.
// It returns a map of string (map file path) to MapFileState objects.
// The map file path is the full path to the map file including the base directory.
// The MapFileState objects contain the current state of the map file including the entries,
// desired values, and diff values.
// The map is read-only and should not be modified directly.
func (m *MapsStorageDefault) GetMaps() map[string]map[string]*maps.MapFileState {
	return m.mapFiles
}

// ProcessMapFiles processes all the map files stored in MapsStorageExDefault.
// It iterates over each map file and calls ProcessMapFiles on each map file.
// ProcessMapFiles is a blocking call and should be called in a goroutine to avoid blocking the application.
func (m *MapsStorageDefault) ProcessMapFiles() {
	for _, mapDir := range m.mapFiles {
		if mapDir == nil {
			continue
		}
		for _, mapFile := range mapDir {
			mapFile.ProcessMapFiles()
		}
	}
}

// DeleteMapsDirectoryForFrontend removes the maps directory of a frontend.
//
// It is idempotent: the directory of a frontend that never had a map file, or
// that was already removed, is simply absent. Callers retry a failed removal, and
// a deleted frontend may be replayed, so an absent directory is not an error.
func (m *MapsStorageDefault) DeleteMapsDirectoryForFrontend(frontendName string) error {
	mapBaseDir := filepath.Join(m.MapsBaseDir, frontendName)

	// Remove from disk before dropping the in-memory state. Dropping the state
	// first makes a filesystem failure unrecoverable: the entry is gone, so the
	// directory is never retried, and only the orphan removal collects it.
	if err := os.RemoveAll(mapBaseDir); err != nil {
		m.logger.LogAttrs(
			context.Background(), slog.LevelWarn,
			"failed to remove maps directory",
			slog.String("dir", mapBaseDir),
			slog.Any("error", err),
		)
		return err
	}

	delete(m.mapFiles, mapBaseDir)

	return nil
}

// RemoveOrphanMapsDirectories removes the maps directory of every frontend the
// configuration does not declare, and leaves the others untouched.
//
// It is called once at startup, with the frontends of the configuration loaded
// from disk.
//
// The parameter is what to keep, not what to remove: every directory it names is
// left in place. It is the frontends the configuration declares, which is not the
// same set as the frontends the cluster declares: a frontend that no resource owns
// any more is still declared by the file HAProxy is started with, so its map files
// are still read, and it is the deletion of that frontend — once committed — that
// removes its directory. An orphan here is a directory whose frontend is absent
// from the configuration altogether: the leftover of a removal that did not
// complete (a crash between the commit and the removal, or a filesystem error on a
// removal that was never retried).
//
// The base directory is not wiped: the configuration HAProxy starts with
// references map files, and nothing guarantees they would be written again before
// the next reload, so a blanket removal would take the running configuration's
// files away. DeleteMapsDirectory is the full reset, for a caller that knows the
// whole directory can go.
func (m *MapsStorageDefault) RemoveOrphanMapsDirectories(referencedFrontends []string) error {
	referencedMapsDirectories := make(map[string]struct{}, len(referencedFrontends))
	for _, frontendName := range referencedFrontends {
		referencedMapsDirectories[filepath.Join(m.MapsBaseDir, frontendName)] = struct{}{}
	}

	mapsBaseDirEntries, err := os.ReadDir(m.MapsBaseDir)
	if err != nil {
		if os.IsNotExist(err) {
			// No map file was ever written.
			return nil
		}
		return err
	}

	var removalErrors []error

	for _, entry := range mapsBaseDirEntries {
		if !entry.IsDir() {
			continue
		}

		orphanMapsDirectory := filepath.Join(m.MapsBaseDir, entry.Name())
		if _, ok := referencedMapsDirectories[orphanMapsDirectory]; ok {
			continue
		}

		if err := os.RemoveAll(orphanMapsDirectory); err != nil {
			m.logger.LogAttrs(
				context.Background(), slog.LevelWarn,
				"failed to remove orphan maps directory",
				slog.String("dir", orphanMapsDirectory),
				slog.Any("error", err),
			)
			removalErrors = append(removalErrors, err)
			continue
		}

		delete(m.mapFiles, orphanMapsDirectory)
	}

	return errors.Join(removalErrors...)
}

// DeleteMapsDirectory removes the whole maps base directory, for a full reset of
// the maps storage. It is not the frontend deletion path: see
// DeleteMapsDirectoryForFrontend and RemoveOrphanMapsDirectories.
func (m *MapsStorageDefault) DeleteMapsDirectory() error {
	return os.RemoveAll(m.MapsBaseDir)
}
