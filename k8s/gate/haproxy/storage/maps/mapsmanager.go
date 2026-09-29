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
	"strconv"
	"strings"
	"time"

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
		logging.LogAttrMapFilePath(m.Path.FileName),
	)
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
					backendsOp[intentBackendName] = &CollectedBackendIntents{
						ValueName:  intentValueForBackendName.ValueName,
						Conditions: intentValueForBackendName.Conditions,
						RuleIdx:    intentValueForBackendName.RuleIdx,
						CreatedAt:  intentValueForBackendName.CreatedAt,
					}
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
		entryValue.DesiredValue = map[string]*WeightedValue{}
		for backendName, weightedBackend := range entryValue.CurrentValue {
			entryValue.DesiredValue[backendName] = weightedBackend.Copy()
		}

		for backendName, collectedIntents := range backendsOp {
			// DiffValue
			diffValue := &IntentValue{
				ValueName:  collectedIntents.ValueName,
				Weight:     copyWeight(collectedIntents.Weight),
				Conditions: collectedIntents.Conditions,
				RuleIdx:    collectedIntents.RuleIdx,
				CreatedAt:  collectedIntents.CreatedAt,
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
				entryValue.DesiredValue[backendName] = &WeightedValue{
					ValueName:  collectedIntents.ValueName,
					Weight:     copyWeight(collectedIntents.Weight),
					Conditions: collectedIntents.Conditions,
					RuleIdx:    collectedIntents.RuleIdx,
					CreatedAt:  collectedIntents.CreatedAt,
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
		logging.LogAttrMapFilePath(m.Path.FileName),
	)
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
		entryValue.CurrentValue = map[string]*WeightedValue{}
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
	Operation
	WeightedValue
}

func (i IntentValue) String() string {
	return fmt.Sprintf("IntentValue {WeightedBackend: %+v, Operation: %+v}", i.WeightedValue, i.Operation)
}

func (i IntentValue) Copy() *IntentValue {
	return &IntentValue{
		WeightedValue: *i.WeightedValue.Copy(),
		Operation:     i.Operation,
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
	// in the following lines:
	// value can be:
	// - a backend name with a weight (for path-based maps) or just a backend name (for sni-based maps) that we want to create/update/delete in the runtime map file (desired state)
	// - a listener name
	// - a listener + route name
	IntentsByBackendByResouceOrigin map[ResourceOrigin]map[string]*IntentValue // resource origin -> value name -> value + weight
	CurrentValue                    map[string]*WeightedValue                  // value name -> value + weight
	DiffValue                       map[string]*IntentValue                    // value name -> value + weight + operation (useful for sockets orders)
	DesiredValue                    map[string]*WeightedValue                  // value name -> value + weight
}

type WeightedValue struct {
	// CreatedAt orders comma-joined listener-route values: the spec breaks
	// cross-route ties by oldest route first, then ns/name. Zero for maps
	// that never hold several routes per value.
	CreatedAt time.Time
	Weight    *int32
	// Conditions, when non-empty, restrict this candidate to requests
	// satisfying them. Two rules of the same route can share one map entry,
	// so the key of the enclosing map carries the encoded conditions and the
	// rule index to keep their candidates apart; ValueName stays the real
	// backend name.
	Conditions *MatchConditions
	ValueName  string
	// RuleIdx ranks candidates from the same route: more conditions first,
	// then lower rule index (spec: first matching rule wins ties).
	RuleIdx int
}

func (wb WeightedValue) String() string {
	return fmt.Sprintf("BackendName: %s, Weight: %d", wb.ValueName, utils.PointerDefaultValueIfNil(wb.Weight))
}

func (wb WeightedValue) Copy() *WeightedValue {
	weight := utils.PointerDefaultValueIfNil(wb.Weight)
	var conditions *MatchConditions
	if wb.Conditions != nil {
		conditions = wb.Conditions.copy()
	}
	return &WeightedValue{
		ValueName:  wb.ValueName,
		Weight:     &weight,
		Conditions: conditions,
		RuleIdx:    wb.RuleIdx,
		CreatedAt:  wb.CreatedAt,
	}
}

// copy returns a shallow copy: condition slices are treated as read-only.
func (mc *MatchConditions) copy() *MatchConditions {
	if mc == nil {
		return nil
	}
	c := *mc
	return &c
}

func (ev EntryValue) CopyDesiredValue() map[string]*WeightedValue {
	result := map[string]*WeightedValue{}
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
	// PlainValues disables the JSON weighted format: values are written as plain
	// comma-separated names (no weights, no JSON wrapper). Use this for maps that
	// are looked up directly by HAProxy (listener_exact_match, listener_wildcard_match,
	// listener_route_exact_match, listener_route_wildcard_match).
	PlainValues bool
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
	CreatedAt  time.Time
	Weight     *int32
	Conditions *MatchConditions
	// Carried from the intents so DesiredValue keeps the real backend name and
	// the candidate ranking fields, even when the map key is a composite of
	// conditions, rule index and backend name.
	ValueName string
	RuleIdx   int
	PresentOperations
}

// ApplyRoute applies a set of new entries to the MapFileState and updates the EntriesByResource accordingly.
// It handles three cases:
// 1. Apply / update for all new entries
// 2. Handle removed entries (previous but not in new)
// 3. Update EntriesByResource
func (m *MapFileState) ApplyRoute(
	ro ResourceOrigin,
	newEntries map[EntryKey]map[string]*WeightedValue,
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
			m.ApplyDesiredBackends(ek, ro, map[string]*WeightedValue{})
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
	desired map[string]*WeightedValue,
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
			CurrentValue:                    map[string]*WeightedValue{},
			DiffValue:                       map[string]*IntentValue{},
			DesiredValue:                    map[string]*WeightedValue{},
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
				ValueName: currentIntent.ValueName,
				Weight:    currentIntent.Weight,
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
				ValueName:  desiredBackend.ValueName,
				Weight:     copyWeight(desiredBackend.Weight),
				Conditions: desiredBackend.Conditions,
				RuleIdx:    desiredBackend.RuleIdx,
				CreatedAt:  desiredBackend.CreatedAt,
				Operation:  Create,
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
	dir := filepath.Dir(m.Path.FullPath())
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	// The map file must exist on disk even when empty, because the HAProxy
	// configuration references it at load time. Create an empty placeholder
	// on first sync if it is missing.
	if _, err := os.Stat(m.Path.FullPath()); os.IsNotExist(err) {
		placeholder, err := os.Create(m.Path.FullPath())
		if err != nil {
			return err
		}
		if err := placeholder.Close(); err != nil {
			return err
		}
	}

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

	f, err := os.Create(m.Path.FullPath())
	if err != nil {
		return err
	}
	defer f.Close()

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

		value := m.BuildValue(entryValue.DesiredValue)
		if value == "" {
			continue
		}
		m.logger.LogAttrs(context.Background(), slog.LevelDebug, "Map file entry",
			slog.String("key", key), slog.String("value", value))
		if _, err := fmt.Fprintf(f, "%s %s\n", key, value); err != nil {
			return err
		}
	}
	return nil
}

// BuildValue returns the map-file value string for the given desired state,
// choosing between the plain and JSON-weighted formats based on m.PlainValues.
func (m *MapFileState) BuildValue(desired map[string]*WeightedValue) string {
	if m.PlainValues {
		return BuildPlainRouteValue(desired)
	}
	return BuildRouteValue(desired)
}

// BuildRouteValue renders the map-file value for the given desired state.
//
// Legacy entries (no conditions anywhere) keep the historical format: a bare
// backend name, or the weighted-random JSON.
//
// Entries with at least one conditional candidate use the conditional format,
// parsed by lua select_route / find_route:
//
//	~1;<conds>>target;<conds>>target;...
//
// Candidates are ordered by the Gateway API precedence rules after path
// (already the map key): method, header count, query count, then rule order —
// so the first passing candidate is the spec-correct one. An unconditional
// candidate sorts last and acts as fallback. Duplicate signatures from later
// rules are dropped (spec: first matching rule wins ties).
func BuildRouteValue(desired map[string]*WeightedValue) string {
	if len(desired) == 0 {
		return ""
	}

	hasCond := false
	for _, v := range desired {
		if !v.Conditions.IsEmpty() {
			hasCond = true
			break
		}
	}
	if !hasCond {
		return buildWeightedValue(desired)
	}

	type candGroup struct {
		conds    *MatchConditions
		backends map[string]*WeightedValue
		sig      string
		ruleIdx  int
	}
	groups := map[string]*candGroup{}
	for _, v := range desired {
		sig := v.Conditions.Encode()
		groupKey := sig + "\x00" + strconv.Itoa(v.RuleIdx)
		group := groups[groupKey]
		if group == nil {
			group = &candGroup{
				sig:      sig,
				conds:    v.Conditions,
				ruleIdx:  v.RuleIdx,
				backends: map[string]*WeightedValue{},
			}
			groups[groupKey] = group
		}
		existing := group.backends[v.ValueName]
		if existing == nil {
			weight := copyWeight(v.Weight)
			group.backends[v.ValueName] = &WeightedValue{ValueName: v.ValueName, Weight: weight}
			continue
		}
		// Same rule + signature: OR'ed matches, weights add up.
		newWeight := utils.PointerDefaultValueIfNil(existing.Weight) + utils.PointerDefaultValueIfNil(v.Weight)
		existing.Weight = &newWeight
	}

	ordered := make([]*candGroup, 0, len(groups))
	for _, g := range groups {
		ordered = append(ordered, g)
	}
	slices.SortFunc(ordered, func(a, b *candGroup) int {
		aMethod, aHdrs, aQuery := a.conds.Counts()
		bMethod, bHdrs, bQuery := b.conds.Counts()
		if aMethod != bMethod {
			if aMethod {
				return -1
			}
			return 1
		}
		if aHdrs != bHdrs {
			return bHdrs - aHdrs
		}
		if aQuery != bQuery {
			return bQuery - aQuery
		}
		if a.ruleIdx != b.ruleIdx {
			return a.ruleIdx - b.ruleIdx
		}
		// Two matches of one rule can differ only by their conditions while
		// sharing a rank. Break the tie on the signature so the emitted value
		// does not depend on map iteration order.
		return strings.Compare(a.sig, b.sig)
	})

	var b strings.Builder
	b.WriteString("~1")
	seen := map[string]bool{}
	for _, g := range ordered {
		if seen[g.sig] {
			// Same conditions from a later rule: shadowed by the first rule.
			continue
		}
		seen[g.sig] = true
		target := buildWeightedValue(g.backends)
		if target == "" {
			continue
		}
		b.WriteString(";")
		b.WriteString(g.sig)
		b.WriteString(">")
		b.WriteString(target)
	}
	return b.String()
}

// buildWeightedValue renders one candidate target: a bare backend name when
// the group has a single backend, the weighted-random JSON otherwise. It
// always uses ValueName: map keys may be condition/rule composites.
func buildWeightedValue(desired map[string]*WeightedValue) string {
	if len(desired) == 0 {
		return ""
	}

	type namedWeight struct {
		name   string
		weight int32
	}
	backends := make([]namedWeight, 0, len(desired))
	for _, v := range desired {
		backends = append(backends, namedWeight{name: v.ValueName, weight: utils.PointerDefaultValueIfNil(v.Weight)})
	}
	slices.SortFunc(backends, func(a, b namedWeight) int {
		return strings.Compare(a.name, b.name)
	})

	if len(backends) == 1 {
		return backends[0].name
	}

	var b strings.Builder
	b.WriteString(`{"a":"wr","l":"`)

	for i, bw := range backends {
		if i > 0 {
			b.WriteString(",")
		}
		_, _ = fmt.Fprintf(&b, "%s:%d", bw.name, bw.weight)
	}

	b.WriteString(`"}`)
	return b.String()
}

// BuildPlainRouteValue returns names as a plain comma-separated string (no weights, no JSON).
// Used for listener maps where the value must be a literal string, not a weighted backend list.
// It uses ValueName rather than the map key so composite keys cannot leak. The
// Gateway API tie-break orders cross-route candidates by oldest route first,
// then "{namespace}/{name}", so equal timestamps fall back to the name order.
func BuildPlainRouteValue(desired map[string]*WeightedValue) string {
	if len(desired) == 0 {
		return ""
	}
	type named struct {
		createdAt time.Time
		name      string
	}
	values := make([]named, 0, len(desired))
	for _, v := range desired {
		values = append(values, named{name: v.ValueName, createdAt: v.CreatedAt})
	}
	slices.SortFunc(values, func(a, b named) int {
		if !a.createdAt.Equal(b.createdAt) {
			if a.createdAt.Before(b.createdAt) {
				return -1
			}
			return 1
		}
		return strings.Compare(a.name, b.name)
	})
	names := make([]string, 0, len(values))
	for _, v := range values {
		names = append(names, v.name)
	}
	return strings.Join(names, ",")
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
				intent.ValueName,
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
			wb.ValueName,
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
			diff.ValueName,
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
			wb.ValueName,
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
