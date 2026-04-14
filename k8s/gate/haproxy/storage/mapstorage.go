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
	f := m.getMapFile(frontendName, MAP_LISTENER_EXACT_MATCH)
	f.PlainValues = true
	return f
}

func (m *MapsStorageDefault) GetListenerWildcardMatchMapFile(frontendName string) *maps.MapFileState {
	f := m.getMapFile(frontendName, MAP_LISTENER_WILDCARD_MATCH)
	f.PlainValues = true
	return f
}

func (m *MapsStorageDefault) GetListenerRouteExactMatchMapFile(frontendName string) *maps.MapFileState {
	f := m.getMapFile(frontendName, MAP_LISTENER_ROUTE_EXACT_MATCH)
	f.PlainValues = true
	return f
}

func (m *MapsStorageDefault) GetListenerRouteWildcardMatchMapFile(frontendName string) *maps.MapFileState {
	f := m.getMapFile(frontendName, MAP_LISTENER_ROUTE_WILDCARD_MATCH)
	f.PlainValues = true
	return f
}

func (m *MapsStorageDefault) GetPathExactMapFile(frontendName string) *maps.MapFileState {
	return m.getMapFile(frontendName, PATH_EXACT_MAP)
}

func (m *MapsStorageDefault) GetPathPrefixMapFile(frontendName string) *maps.MapFileState {
	return m.getMapFile(frontendName, PATH_PREFIX_MAP)
}

func (m *MapsStorageDefault) GetPathRegexMapFile(frontendName string) *maps.MapFileState {
	return m.getMapFile(frontendName, PATH_REGEX_MAP)
}

func (m *MapsStorageDefault) GetSniMapFile(frontendName string) *maps.MapFileState {
	return m.getMapFile(frontendName, SNI_MAP)
}

// getMapFile returns the MapFileState for the given frontend name and map name.
// If the map file does not exist, it creates a new instance of MapFileState,
// reads the map file from disk, and stores the map file in the mapFiles map.
// If there is an error reading the map file from disk, it logs an error message.
// It returns a pointer to the MapFileState.
func (m *MapsStorageDefault) getMapFile(frontendName string, mapName string) *maps.MapFileState {
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

func (m *MapsStorageDefault) DeleteMapsDirectoryForFrontend(frontendName string) error {
	mapBaseDir := filepath.Join(m.MapsBaseDir, frontendName)
	_, exists := m.mapFiles[mapBaseDir]
	if !exists {
		return errors.New("Maps directory for frontend " + frontendName + "does not exist. Can't be removed.")
	}
	delete(m.mapFiles, mapBaseDir)
	return os.RemoveAll(mapBaseDir)
}

func (m *MapsStorageDefault) DeleteMapsDirectory() error {
	return os.RemoveAll(m.MapsBaseDir)
}
