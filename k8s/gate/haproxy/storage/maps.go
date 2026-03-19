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
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	futils "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/fileutils"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/storage/maps"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	utilsk8s "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils-k8s"
)

//revive:enable:var-naming

var _ MapsStorage = &MapsStorageDefault{}

type MapsStorageDefault struct {
	logger     *slog.Logger
	extractGVK utilsk8s.ExtractGVK
	Maps       map[string]*maps.MapData
	// MapsBaseDir the base directory to store maps
	MapsBaseDir string
}

func NewMapsStorage(logger *slog.Logger, extractGVK utilsk8s.ExtractGVK, structureType StructureType, mapsBaseDir string) (MapsStorage, error) {
	mylogger := logger.With(logging.LogAttrCategory(logging.LogCategoryMapsStorage))

	if mapsBaseDir == "" {
		return nil, errors.New("maps directory is not set")
	}

	switch structureType {
	case StructureTypeMapsDefault:
		cs := MapsStorageDefault{
			logger:      mylogger,
			extractGVK:  extractGVK,
			MapsBaseDir: mapsBaseDir,
			Maps:        map[string]*maps.MapData{},
		}
		cs.empty(mapsBaseDir)
		return &cs, nil
	default:
		return nil, fmt.Errorf("unknown structure type: %s", structureType)
	}
}

// MapPath returns the FilePath for the map
// Default algorithm for Maps Storage
// - For directory: maps are grouped in directories based on:
//   - frontend_name
//   - /etc/unified.../maps/<frontend>/
func (m *MapsStorageDefault) MapPath(frontendName string, mapName string) futils.FilePath {
	return futils.FilePath{
		Dir:      filepath.Join(m.MapsBaseDir, frontendName),
		FileName: mapName + ".map",
	}
}

func (m *MapsStorageDefault) GetMaps() map[string]*maps.MapData {
	return m.Maps
}

func (m *MapsStorageDefault) GetMapData(filePath futils.FilePath) *maps.MapData {
	m.EnsureMapData(filePath)
	return m.Maps[filePath.FullPath()]
}

func (m *MapsStorageDefault) EnsureMapData(filePath futils.FilePath) {
	name := filePath.FullPath()
	_, ok := m.Maps[name]
	if ok {
		return
	}

	m.Maps[name] = &maps.MapData{
		DynamicUpdates: maps.DynamicMapUpdates{
			Add:    map[string]string{},
			Update: map[string]string{},
			Delete: []string{},
		},
		Path: filePath,
	}

	err := m.readFromDisk(filePath)
	if err != nil {
		m.logger.LogAttrs(
			context.Background(),
			slog.LevelError,
			"Error reading map from disk",
			slog.String("map", name),
			slog.String("error", err.Error()))
	}
}

func (m *MapsStorageDefault) readFromDisk(filePath futils.FilePath) error {
	// data is key-value pairs, there is one space between key and value
	// this func presumes that the map data initialization is done
	name := filePath.FullPath()

	data, err := futils.ReadKeyValueFile(name, ' ')
	if err != nil {
		return err
	}

	memoryMap := m.Maps[name]
	memoryMap.SetData(data)

	return nil
}

func (MapsStorageDefault) WriteOnDisk(data maps.MapData) error {
	var f *os.File
	var err error
	if _, err = os.Stat(data.Path.Dir); os.IsNotExist(err) {
		err = os.MkdirAll(data.Path.Dir, 0o755)
		if err != nil {
			return err
		}
	}
	f, err = os.Create(data.Path.FullPath())
	if err != nil {
		return err
	}
	defer f.Close()
	// TODO sort this maybe
	for k, v := range data.Data() {
		_, err = f.WriteString(fmt.Sprintf("%s %s\n", k, v))
		if err != nil {
			return err
		}
	}
	return nil
}

func (MapsStorageDefault) DeleteFromDisk(data maps.MapData) error {
	return os.Remove(data.Path.FullPath())
}

func (m *MapsStorageDefault) DeleteEmptyMapsDir() error {
	entries, err := os.ReadDir(m.MapsBaseDir)
	if err != nil {
		return err
	}

	// Iterates over namespace directories
	for _, entry := range entries {
		if !entry.IsDir() {
			continue // We only care about directories
		}

		subdirPath := filepath.Join(m.MapsBaseDir, entry.Name())
		subEntries, err := os.ReadDir(subdirPath)
		if err != nil {
			return err
		}

		if len(subEntries) == 0 {
			m.logger.LogAttrs(context.Background(), slog.LevelDebug, "Deleting empty directory",
				slog.String("dir", subdirPath))
			err := os.RemoveAll(subdirPath)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (m *MapsStorageDefault) empty(path string) {
	// TODO
	_ = path
	_ = m.logger
}
