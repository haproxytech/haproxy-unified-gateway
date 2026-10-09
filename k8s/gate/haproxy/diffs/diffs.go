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
	"encoding/json"
	"fmt"
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

// extractObservedGenerationsFromFrontend reads every kind a frontend records, keeping the
// highest generation seen for each resource.
func extractObservedGenerationsFromFrontend(fe *models.Frontend, into ObservedGenerations) {
	if fe == nil || fe.Metadata == nil {
		return
	}
	hugI, ok := fe.Metadata[metadata.UnifiedGatewayMetaDataKey]
	if !ok {
		return
	}
	by, err := json.Marshal(hugI)
	if err != nil {
		return
	}
	// After the JSON round-trip in FrontendMetaData(), the structure is:
	// { "<kind>": { "namespace/name": { "Generation": float64, "LinkID": string } } }
	var hugMeta map[string]map[string]map[string]any
	if err := json.Unmarshal(by, &hugMeta); err != nil {
		return
	}
	for kind, objects := range hugMeta {
		for nsName, info := range objects {
			genAny, ok := info["Generation"]
			if !ok {
				continue
			}
			genFloat, ok := genAny.(float64)
			if !ok {
				continue
			}
			nn := parseNamespacedName(nsName)
			if nn == (types.NamespacedName{}) {
				continue
			}
			owner := tree.ObservedOwner{Kind: gatewayv1.Kind(kind), Key: nn}
			if existing, ok := into[owner]; !ok || int64(genFloat) > existing {
				into[owner] = int64(genFloat)
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
