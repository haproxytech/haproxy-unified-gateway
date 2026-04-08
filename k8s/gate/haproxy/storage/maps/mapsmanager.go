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

package maps

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	futils "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/fileutils"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
)

// NewMapFileState creates a new instance of MapFileState with the given filename and logger.
// It returns a pointer to the new instance.
func NewMapFileState(mapBaseDir, mapFileName string, logger *slog.Logger) *MapFileState {
	mylogger := logger.With(logging.LogAttrCategory(logging.LogCategoryMapsStorage))
	return &MapFileState{
		Path: futils.FilePath{
			FileName: mapFileName,
			Dir:      mapBaseDir,
		},
		Entries:           map[EntryKey]*EntryValue{},
		EntriesByResource: map[ResourceOrigin]map[EntryKey]struct{}{},
		logger:            mylogger,
	}
}

// ProcessMapFiles processes the map file and updates the desired values based on the intent operations stored in the map file state.
// It iterates over each entry/intent for the filename and collects the operations for each backend name.
// It then fills the desired values with the current values and updates the diff values with the desired operations.
// Finally, it prints the map file state after processing.
//
//revive:disable:function-length
func (m *MapFileState) ProcessMapFiles() {
	m.logger.LogAttrs(
		context.Background(),
		slog.LevelDebug, "Processing map file",
		logging.LogAttrMapFilePath(m.Path.FileName))
	// Iteration over each entry/intent for the filename
	for _, entryValue := range m.Entries {
		// We collect the operations for each backend, backend name -> weights + operations (create, update, delete, empty)
		// Operations are collected by backend name and gather all intent operations (orders) from all resource origins addressing the same backend
		backendsOp := map[string]*CollectedBackendIntents{}
		// We iterate over each intent from a resource origin in the two imbricated loops
		// Each intent concerns a backend name associated with an operation (create, update, delete, empty)
		for _, intentValueFromResourceOrigin := range entryValue.IntentsByBackendByResouceOrigin {
			for intentBackendName, intentValueForBackendName := range intentValueFromResourceOrigin {
				// If no previous intent for the same backend name, we create it
				if backendsOp[intentBackendName] == nil {
					backendsOp[intentBackendName] = &CollectedBackendIntents{}
				}
				backendsOpValue := backendsOp[intentBackendName]
				// We collect the operations
				switch intentValueForBackendName.Operation {
				case Create:
					backendsOpValue.Create = true
				case Update:
					backendsOpValue.Update = true
				case Delete:
					backendsOpValue.Delete = true
				case Empty:
					backendsOpValue.Empty = true
				}
				// We recalculate the weight from all intents for the same backend name if the operation is not delete
				weight := utils.PointerDefaultValueIfNil(backendsOpValue.Weight)
				if intentValueForBackendName.Operation != Delete {
					weight += utils.PointerDefaultValueIfNil(intentValueForBackendName.Weight)
				}
				backendsOpValue.Weight = &weight
			}
		}
		// fill desired values with current values
		entryValue.DesiredValue = map[string]*WeightedBackend{}
		for backendName, weightedBackend := range entryValue.CurrentValue {
			entryValue.DesiredValue[backendName] = weightedBackend.Copy()
		}

		for backendName, collectedIntents := range backendsOp {
			// DiffValue
			diffValue := &IntentValue{
				WeightedBackend: WeightedBackend{
					BackendName: backendName,
					Weight: func() *int32 {
						if collectedIntents.Weight != nil {
							weight := *collectedIntents.Weight
							return &weight
						}
						return nil
					}(),
				},
				Operation: func() Operation {
					// Deduced operation in ordered precedence
					// Ex: an update is prior to a delete
					switch {
					case collectedIntents.Update:
						return Update
					// Particular case: an empty intent is prior to a create but not a delete
					case collectedIntents.Empty && !collectedIntents.Delete:
						return Empty
					case collectedIntents.Create:
						return Create
					case collectedIntents.Delete:
						return Delete
					default:
						return ""
					}
				}(),
			}
			if diffValue.Operation == Empty {
				delete(entryValue.DiffValue, backendName)
				continue
			}
			entryValue.DiffValue[backendName] = diffValue
			// DesiredValue
			switch entryValue.DiffValue[backendName].Operation {
			case Create, Update:
				entryValue.DesiredValue[backendName] = &WeightedBackend{
					BackendName: backendName,
					Weight: func() *int32 {
						if diffValue.Weight != nil {
							weight := *diffValue.Weight
							return &weight
						}
						return nil
					}(),
				}
			case Delete:
				delete(entryValue.DesiredValue, backendName)
			}
		}
	}
	m.logger.LogAttrs(
		context.Background(),
		slog.LevelDebug, "Processed map file",
		logging.LogAttrMapFilePath(m.Path.FileName),
		logging.LogAttrMapFileContent(m.PrettyString()),
	)
}

// Reset resets the MapFileState to its initial state.
// It deletes all the entries with their corresponding functions and resources.
// It also resets the DesiredValue and diffValue of each entry to their initial state.
// It is intended to be used after a map file has been processed and its contents have been applied to the runtime maps.
func (m *MapFileState) Reset() {
	m.logger.LogAttrs(
		context.Background(),
		slog.LevelDebug, "Resetting map file",
		logging.LogAttrMapFilePath(m.Path.FileName))
	for entryKey, entryValue := range m.Entries {
		if entryValue == nil {
			delete(m.Entries, entryKey)
			continue
		}
		for resourceOrigin, intentByBackendByResouceOrigin := range entryValue.IntentsByBackendByResouceOrigin {
			for backendName, intent := range intentByBackendByResouceOrigin {
				if intent.Operation == Delete {
					delete(intentByBackendByResouceOrigin, backendName)
				} else {
					intent.Operation = ""
				}
			}
			if len(intentByBackendByResouceOrigin) == 0 {
				delete(entryValue.IntentsByBackendByResouceOrigin, resourceOrigin)
			}
		}
		entryValue.CurrentValue = map[string]*WeightedBackend{}
		for backendName, weightedBackend := range entryValue.DesiredValue {
			entryValue.CurrentValue[backendName] = weightedBackend.Copy()
		}
		entryValue.DiffValue = map[string]*IntentValue{}
		if len(entryValue.IntentsByBackendByResouceOrigin) == 0 && len(entryValue.CurrentValue) == 0 {
			delete(m.Entries, entryKey)
		}
	}
}

type EntryKey struct {
	Hostname string
	Path     string
}

func (ek EntryKey) String() string {
	return fmt.Sprintf("EntryKey {Hostname: %s, Path: %s}", ek.Hostname, ek.Path)
}

type ResourceOrigin struct {
	Namespace string
	Name      string
}

func (ro ResourceOrigin) String() string {
	return fmt.Sprintf("ResourceOrigin {Namespace: %s, Name: %s}", ro.Namespace, ro.Name)
}

type IntentValue struct {
	WeightedBackend
	Operation
}

func (i IntentValue) String() string {
	return fmt.Sprintf("IntentValue {WeightedBackend: %+v, Operation: %+v}", i.WeightedBackend, i.Operation)
}

func (i IntentValue) Copy() *IntentValue {
	weight := utils.PointerDefaultValueIfNil(i.WeightedBackend.Weight)
	return &IntentValue{
		WeightedBackend: WeightedBackend{
			BackendName: i.WeightedBackend.BackendName,
			Weight:      &weight,
		},
		Operation: i.Operation,
	}
}

type Operation string

const (
	Create Operation = "create"
	Update Operation = "update"
	Delete Operation = "delete"
	Empty  Operation = ""
)

// EntryValue represents all the information necessary to compute the desired state for a given key in the map file
type EntryValue struct {
	// IntentsByBackendByResouceOrigin map[ResourceOrigin]*IntentValue
	IntentsByBackendByResouceOrigin map[ResourceOrigin]map[string]*IntentValue // resource origin -> backend name -> backend + weight
	CurrentValue                    map[string]*WeightedBackend                // backend name -> backend + weight
	DiffValue                       map[string]*IntentValue                    // backend name -> backend + weight + operation (useful for sockets orders)
	DesiredValue                    map[string]*WeightedBackend                // backend name -> backend + weight
}

type WeightedBackend struct {
	Weight      *int32
	BackendName string
}

func (wb WeightedBackend) String() string {
	return fmt.Sprintf("BackendName: %s, Weight: %d", wb.BackendName, utils.PointerDefaultValueIfNil(wb.Weight))
}

func (wb WeightedBackend) Copy() *WeightedBackend {
	weight := utils.PointerDefaultValueIfNil(wb.Weight)
	return &WeightedBackend{
		BackendName: wb.BackendName,
		Weight:      &weight,
	}
}

func (ev EntryValue) CopyDesiredValue() map[string]*WeightedBackend {
	result := map[string]*WeightedBackend{}
	for backendName, weightedBackend := range ev.DesiredValue {
		result[backendName] = weightedBackend.Copy()
	}
	return result
}

type MapFileState struct {
	// The contents of the map file.
	// Could be in the format: hostname+path ou sni-> backend
	Entries           map[EntryKey]*EntryValue
	EntriesByResource map[ResourceOrigin]map[EntryKey]struct{}
	logger            *slog.Logger
	// FileName          string
	// RelativeFileName  string
	Path futils.FilePath
}

// Collect all intentions for each backend
// Each order has a precedence order Create & Update > Delete
type PresentOperations struct {
	Create bool
	Update bool
	Delete bool
	Empty  bool
}

type CollectedBackendIntents struct {
	Weight *int32
	PresentOperations
}

// ApplyRoute applies a set of new entries to the MapFileState and updates the EntriesByResource accordingly.
// It handles three cases:
// 1. Apply / update for all new entries
// 2. Handle removed entries (previous but not in new)
// 3. Update EntriesByResource
func (m *MapFileState) ApplyRoute(
	ro ResourceOrigin,
	newEntries map[EntryKey]map[string]*WeightedBackend,
) {
	previous := m.EntriesByResource[ro]
	if previous == nil {
		previous = map[EntryKey]struct{}{}
	}

	current := map[EntryKey]struct{}{}

	// 1 Apply / update for all new entries
	for ek, backends := range newEntries {
		current[ek] = struct{}{}
		m.ApplyDesiredBackends(ek, ro, backends)
	}

	// 2 Handle removed entries (previous but not in new)
	for ek := range previous {
		if _, stillPresent := current[ek]; !stillPresent {
			// force deletion of all backends for this resource on this entry
			m.ApplyDesiredBackends(ek, ro, map[string]*WeightedBackend{})
		}
	}

	// 3 Update EntriesByResource
	if len(current) == 0 {
		delete(m.EntriesByResource, ro)
	} else {
		m.EntriesByResource[ro] = current
	}
}

// ApplyDesiredBackends applies the desired backends for an entry and resource origin to the map file state.
// It handles four cases:
// 1. Get/Create EntryValue
// 2. Get the intents for all backends from this resource origin
// 3. DELETE : present before, absent now
// 4. CREATE / UPDATE / NOOP
func (m *MapFileState) ApplyDesiredBackends(
	entryKey EntryKey,
	resourceOrigin ResourceOrigin,
	desired map[string]*WeightedBackend,
) {
	m.logger.LogAttrs(
		context.Background(),
		slog.LevelDebug, "Applying desired backends",
		logging.LogAttrMapFilePath(m.Path.FileName),
		slog.String("entry key", m.Path.FileName),
		slog.Any("desired backends", desired),
	)

	m.logger.LogAttrs(
		context.Background(),
		slog.LevelDebug, "Before applying desired backends",
		logging.LogAttrMapFilePath(m.Path.FileName),
		logging.LogAttrMapFileContent(m.PrettyString()),
	)

	entriesForResource := m.EntriesByResource[resourceOrigin]
	if entriesForResource == nil {
		entriesForResource = map[EntryKey]struct{}{}
	}
	entriesForResource[entryKey] = struct{}{}
	// 1. Get/Create EntryValue
	entry := m.Entries[entryKey]
	if entry == nil {
		entry = &EntryValue{
			IntentsByBackendByResouceOrigin: map[ResourceOrigin]map[string]*IntentValue{},
			CurrentValue:                    map[string]*WeightedBackend{},
			DiffValue:                       map[string]*IntentValue{},
			DesiredValue:                    map[string]*WeightedBackend{},
		}
		m.Entries[entryKey] = entry
	}

	// 2. Get the intents for all backends from this resource origin
	currentByBackend := entry.IntentsByBackendByResouceOrigin[resourceOrigin]
	if currentByBackend == nil {
		currentByBackend = map[string]*IntentValue{}
		entry.IntentsByBackendByResouceOrigin[resourceOrigin] = currentByBackend
	}

	// 3. DELETE : present before, absent now
	for backendName, currentIntent := range currentByBackend {
		if _, stillDesired := desired[backendName]; !stillDesired {
			currentByBackend[backendName] = &IntentValue{
				WeightedBackend: WeightedBackend{
					BackendName: backendName,
					Weight:      currentIntent.Weight,
				},
				Operation: Delete,
			}
		}
	}

	// 4. CREATE / UPDATE / NOOP
	for backendName, desiredBackend := range desired {
		currentIntent, exists := currentByBackend[backendName]

		switch {
		case !exists:
			// CREATE
			currentByBackend[backendName] = &IntentValue{
				WeightedBackend: WeightedBackend{
					BackendName: backendName,
					Weight:      copyWeight(desiredBackend.Weight),
				},
				Operation: Create,
			}

		case weightsEqual(currentIntent.Weight, desiredBackend.Weight):
			// NOOP -> no operation
			currentIntent.Operation = Empty

		default:
			// UPDATE
			currentIntent.Weight = copyWeight(desiredBackend.Weight)
			currentIntent.Operation = Update
		}
	}
	m.logger.LogAttrs(
		context.Background(),
		slog.LevelDebug, "After applying desired backends",
		logging.LogAttrMapFilePath(m.Path.FileName),
		logging.LogAttrMapFileContent(m.PrettyString()),
	)
}

// WriteOnDiskIfChanged writes the MapFileState to disk if there are any differences between the desired state and the current state.
// It iterates over the entries in the map file state and checks if there are any differences between the desired state and the current state.
// If there are differences, it writes the desired state to disk in the format "key value\n"
func (m *MapFileState) WriteOnDiskIfChanged() error {
	var f *os.File
	var err error
	dir := filepath.Dir(m.Path.FullPath())
	if _, err = os.Stat(dir); os.IsNotExist(err) {
		err = os.MkdirAll(dir, 0o755)
		if err != nil {
			return err
		}
	}
	if _, err = os.Stat(m.Path.FullPath()); os.IsNotExist(err) {
		f, err = os.Create(m.Path.FullPath())
		if err != nil {
			return err
		}
	}
	defer f.Close()

	var hasDiff bool
	for _, entryValue := range m.Entries {
		hasDiff = hasDiff || (len(entryValue.DiffValue) > 0)
		if hasDiff {
			break
		}
	}

	if !hasDiff {
		return nil
	}

	m.logger.LogAttrs(
		context.Background(),
		slog.LevelDebug, "Writing map file to disk",
		logging.LogAttrMapFilePath(m.Path.FileName),
		logging.LogAttrMapFileContent(m.PrettyString()),
	)

	f, err = os.Create(m.Path.FullPath())
	if err != nil {
		return err
	}

	orderedEntries := []EntryKey{}
	for entryKey := range m.Entries {
		orderedEntries = append(orderedEntries, entryKey)
	}
	slices.SortFunc(orderedEntries, compareEntryKeys)

	for _, entryKey := range orderedEntries {
		entryValue := m.Entries[entryKey]
		if entryValue == nil {
			continue
		}
		key := entryKey.Hostname
		if entryKey.Path != "" {
			key += entryKey.Path
		}
		value := BuildRouteValue(entryValue.DesiredValue)
		if value == "" {
			continue
		}
		m.logger.LogAttrs(
			context.Background(),
			slog.LevelDebug,
			"Map file entry",
			slog.String("key", key),
			slog.String("value", value),
		)

		_, err := fmt.Fprintf(f, "%s %s\n", key, value)
		if err != nil {
			return err
		}
	}
	return nil
}

func BuildRouteValue(desired map[string]*WeightedBackend) string {
	if len(desired) == 0 {
		return ""
	}

	if len(desired) == 1 {
		for name := range desired {
			return name
		}
	}

	backendNames := make([]string, 0, len(desired))
	for name := range desired {
		backendNames = append(backendNames, name)
	}
	slices.Sort(backendNames)

	var b strings.Builder
	b.WriteString(`{"a":"wr","l":"`)

	for i, name := range backendNames {
		if i > 0 {
			b.WriteString(",")
		}
		w := desired[name].Weight
		weight := int32(0)
		if w != nil {
			weight = *w
		}
		_, _ = fmt.Fprintf(&b, "%s:%d", name, weight)
	}

	b.WriteString(`"}`)
	return b.String()
}

func copyWeight(w *int32) *int32 {
	if w == nil {
		return nil
	}
	v := *w
	return &v
}

func weightsEqual(a, b *int32) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func (cbi CollectedBackendIntents) String() string {
	return fmt.Sprintf("CollectedBackendIntents {PresentOperations: %+v, Weight: %d}",
		cbi.PresentOperations, utils.PointerDefaultValueIfNil(cbi.Weight))
}

func sortedEntryKeys(m map[EntryKey]*EntryValue) []EntryKey {
	keys := make([]EntryKey, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, compareEntryKeys)
	return keys
}

func sortedStringKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func (m *MapFileState) PrettyString() string {
	var b strings.Builder

	_, _ = fmt.Fprintf(&b, "MapFileState: %s\n", m.Path.FileName)

	for _, key := range sortedEntryKeys(m.Entries) {
		_, _ = fmt.Fprintf(&b, "└─ %s\n", key.String())
		b.WriteString(m.Entries[key].PrettyString("   "))
	}

	return b.String()
}

func (ev *EntryValue) PrettyString(indent string) string {
	var b strings.Builder

	// -------- Intents --------
	_, _ = fmt.Fprintf(&b, "%s├─ Intents\n", indent)
	for ro, backends := range ev.IntentsByBackendByResouceOrigin {
		_, _ = fmt.Fprintf(&b, "%s│  ├─ %s\n", indent, ro.String())
		for _, backend := range sortedStringKeys(backends) {
			intent := backends[backend]
			_, _ = fmt.Fprintf(
				&b,
				"%s│  │  └─ %s weight=%d op=%s\n",
				indent,
				intent.BackendName,
				utils.PointerDefaultValueIfNil(intent.Weight),
				intent.Operation,
			)
		}
	}

	// -------- Current --------
	_, _ = fmt.Fprintf(&b, "%s├─ CurrentValue\n", indent)
	for _, backend := range sortedStringKeys(ev.CurrentValue) {
		wb := ev.CurrentValue[backend]
		_, _ = fmt.Fprintf(
			&b,
			"%s│  └─ %s weight=%d\n",
			indent,
			wb.BackendName,
			utils.PointerDefaultValueIfNil(wb.Weight),
		)
	}

	// -------- Diff --------
	_, _ = fmt.Fprintf(&b, "%s├─ DiffValue\n", indent)
	for _, backend := range sortedStringKeys(ev.DiffValue) {
		diff := ev.DiffValue[backend]
		_, _ = fmt.Fprintf(
			&b,
			"%s│  └─ %s weight=%d op=%s\n",
			indent,
			diff.BackendName,
			utils.PointerDefaultValueIfNil(diff.Weight),
			diff.Operation,
		)
	}

	// -------- Desired --------
	_, _ = fmt.Fprintf(&b, "%s└─ DesiredValue\n", indent)
	for _, backend := range sortedStringKeys(ev.DesiredValue) {
		wb := ev.DesiredValue[backend]
		_, _ = fmt.Fprintf(
			&b,
			"%s   └─ %s weight=%d\n",
			indent,
			wb.BackendName,
			utils.PointerDefaultValueIfNil(wb.Weight),
		)
	}

	return b.String()
}

func compareEntryKeys(a, b EntryKey) int {
	switch {
	// TODO: sort by longest path first (longest subdomain)
	case a.Hostname < b.Hostname:
		return -1
	case a.Hostname > b.Hostname:
		return 1
	}
	switch {
	case a.Path < b.Path:
		return -1
	case a.Path > b.Path:
		return 1
	}
	return 0
}
