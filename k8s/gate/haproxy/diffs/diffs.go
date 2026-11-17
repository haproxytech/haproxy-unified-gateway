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
package diffs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/metadata"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/structured"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type MergeStrategies struct {
	Global   string // override or append
	Defaults string // override or append
}

// ObservedGenerations maps each resource a frontend was programmed against to the highest
// generation it was programmed from. It is the tree's type because the feedback that stamps a
// listener's Programmed condition routes by the same (kind, key) pair.
type ObservedGenerations = tree.ObservedGenerations

// HaproxyConfResult is the outcome of a single HaproxyConfDiffs processing
// cycle, sent back to the controller via HaproxyConfDiffs.ResultCh.
type HaproxyConfResult struct {
	Err                 error
	ObservedGenerations ObservedGenerations
	// ReloadFailed is true when the configuration was committed but HAProxy
	// refused to reload it: the diffs were applied and must not be replayed, but
	// the reload has to be retried.
	ReloadFailed bool
}

type HaproxyConfDiffs struct {
	Created structured.Structured
	Updated structured.Structured
	Deleted structured.Structured
	// If not nil, it has to be closed
	// If the gate controller is setup to use runtime commands, it has to be closed after applying the diffs.
	// If the gate controller is setup to not use runtime commands, it can be closed immediately after having received the diffs.
	// h.applyCfgUpdates(haproxyCfg)
	// if haproxyCfg.Done != nil {
	//  close(haproxyCfg.Done)
	// }
	Done chan struct{}
	// ResultCh, when non-nil, receives exactly one HaproxyConfResult after
	// the diffs have been applied. The channel must be buffered (size >= 1).
	ResultCh chan HaproxyConfResult

	MergeStrategies MergeStrategies
	ReloadNeed      bool
}

func (c HaproxyConfDiffs) IsEmpty() bool {
	return c.Created.IsEmpty() && c.Updated.IsEmpty() && c.Deleted.IsEmpty()
}

// ObservedGenerations builds the per-resource generations from the metadata of all Created
// and Updated frontends. Deleted frontends are excluded because their config is no longer
// active.
func (c HaproxyConfDiffs) ObservedGenerations() ObservedGenerations {
	result := make(ObservedGenerations)
	for _, fe := range c.Created.Frontends {
		extractObservedGenerationsFromFrontend(fe, result)
	}
	for _, fe := range c.Updated.Frontends {
		extractObservedGenerationsFromFrontend(fe, result)
	}
	return result
}

// ResourceFingerprint identifies the resources a diff set is built from, with
// the generation each was read at. It answers "is this the same input?" for a
// replay budget: an input that changed (a resource added, removed or edited) gets
// a fresh budget, while an unchanged one is replayed a bounded number of times
// and then dropped.
//
// It covers every section of the set, whatever the direction of the diff --
// created, updated or deleted -- because all of them carry the metadata that names
// their owners (FrontendMetaData / BackendMetaData).
func (c HaproxyConfDiffs) ResourceFingerprint() string {
	// Every section the set carries contributes its owners, whichever direction the
	// diff goes: a section to create, to update and to delete are all part of the
	// input this fingerprint identifies. ObservedGenerations is not reused
	// here on purpose: it feeds the status stamping, where a deleted section must
	// not count, and that rule has nothing to do with the identity of the input.
	owners := make(ObservedGenerations)
	for _, frontendsOfOneDirection := range []map[string]*models.Frontend{
		c.Created.Frontends, c.Updated.Frontends, c.Deleted.Frontends,
	} {
		for _, fe := range frontendsOfOneDirection {
			extractObservedGenerationsFromFrontend(fe, owners)
		}
	}
	for _, backendsOfOneDirection := range []map[string]*models.Backend{
		c.Created.Backends, c.Updated.Backends, c.Deleted.Backends,
	} {
		for _, be := range backendsOfOneDirection {
			extractObservedGenerationsFromBackend(be, owners)
		}
	}

	entriesOfTheFingerprint := make([]string, 0, len(owners))
	for owner, generation := range owners {
		entriesOfTheFingerprint = append(entriesOfTheFingerprint,
			fmt.Sprintf("%s|%s|%d", owner.Kind, owner.Key.String(), generation))
	}

	// Global and Defaults carry no owner metadata: their own content is what
	// changes when their CR is edited, so it must be part of the fingerprint. A fix
	// to a Defaults CR has to resume a suspended replay, and it is the typical cause
	// of a configuration HAProxy refuses. Only the created and updated directions
	// contribute: a deleted Global or Defaults carries no content, a key being all
	// its removal needs.
	for _, globalsOfOneDirection := range []map[string]*models.Global{
		c.Created.Globals, c.Updated.Globals,
	} {
		for sectionKey, globalSection := range globalsOfOneDirection {
			entriesOfTheFingerprint = append(entriesOfTheFingerprint,
				"global|"+sectionKey+"|"+renderForFingerprint(globalSection))
		}
	}
	for _, defaultsOfOneDirection := range []map[string]*models.Defaults{
		c.Created.Defaults, c.Updated.Defaults,
	} {
		for sectionKey, defaultsSection := range defaultsOfOneDirection {
			entriesOfTheFingerprint = append(entriesOfTheFingerprint,
				"defaults|"+sectionKey+"|"+renderForFingerprint(defaultsSection))
		}
	}

	slices.Sort(entriesOfTheFingerprint)

	digest := sha256.Sum256([]byte(strings.Join(entriesOfTheFingerprint, "\n")))
	return hex.EncodeToString(digest[:])
}

// renderForFingerprint renders one section for the fingerprint. A marshal failure
// yields an empty string rather than dropping the entry, so the presence of the
// section still counts.
func renderForFingerprint(section any) string {
	by, err := json.Marshal(section)
	if err != nil {
		return ""
	}
	return string(by)
}

// extractObservedGenerationsFromFrontend reads every kind a frontend records, keeping the
// highest generation seen for each resource.
func extractObservedGenerationsFromFrontend(frontend *models.Frontend, generationsSeenSoFar ObservedGenerations) {
	if frontend == nil {
		return
	}
	extractObservedGenerationsFromMetadata(frontend.Metadata, generationsSeenSoFar)
}

// extractObservedGenerationsFromBackend does the same for a backend, whose metadata records
// the routes it serves (BackendMetaData).
func extractObservedGenerationsFromBackend(backend *models.Backend, generationsSeenSoFar ObservedGenerations) {
	if backend == nil {
		return
	}
	extractObservedGenerationsFromMetadata(backend.Metadata, generationsSeenSoFar)
}

// extractObservedGenerationsFromMetadata parses the HUG metadata of a section. After the
// JSON round-trip in the metadata builder, the structure is:
// { "<kind>": { "namespace/name": { "Generation": float64, "LinkID": string } } }.
func extractObservedGenerationsFromMetadata(
	sectionMetadata map[string]any, generationsSeenSoFar ObservedGenerations,
) {
	if sectionMetadata == nil {
		return
	}
	hugMetadata, ok := sectionMetadata[metadata.UnifiedGatewayMetaDataKey]
	if !ok {
		return
	}
	serializedMetadata, err := json.Marshal(hugMetadata)
	if err != nil {
		return
	}
	// <kind> -> "namespace/name" -> fields of the owner record
	var ownersByKind map[string]map[string]map[string]any
	if err := json.Unmarshal(serializedMetadata, &ownersByKind); err != nil {
		return
	}
	for ownerKind, ownersOfThatKind := range ownersByKind {
		for ownerKey, ownerFields := range ownersOfThatKind {
			generationAsNumber, ok := ownerFields["Generation"]
			if !ok {
				continue
			}
			generationAsFloat, ok := generationAsNumber.(float64)
			if !ok {
				continue
			}
			namespacedName := parseNamespacedName(ownerKey)
			if namespacedName == (types.NamespacedName{}) {
				continue
			}
			owner := tree.ObservedOwner{Kind: gatewayv1.Kind(ownerKind), Key: namespacedName}
			generationSeenBefore, alreadyRecorded := generationsSeenSoFar[owner]
			if !alreadyRecorded || int64(generationAsFloat) > generationSeenBefore {
				generationsSeenSoFar[owner] = int64(generationAsFloat)
			}
		}
	}
}

func parseNamespacedName(s string) types.NamespacedName {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return types.NamespacedName{}
	}
	return types.NamespacedName{Namespace: parts[0], Name: parts[1]}
}

func (c HaproxyConfDiffs) Stats() string {
	return fmt.Sprintf(
		"Created/Updated/Deleted FE:[%d/%d/%d] BE[%d/%d/%d] Global[%d/%d/%d] Defaults[%d/%d/%d] Reload[%t]",
		len(c.Created.Frontends), len(c.Updated.Frontends), len(c.Deleted.Frontends),
		len(c.Created.Backends), len(c.Updated.Backends), len(c.Deleted.Backends),
		len(c.Created.Globals), len(c.Updated.Globals), len(c.Deleted.Globals),
		len(c.Created.Defaults), len(c.Updated.Defaults), len(c.Deleted.Defaults),
		c.ReloadNeed,
	)
}
