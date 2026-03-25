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
	"k8s.io/apimachinery/pkg/types"
)

type MergeStrategies struct {
	Global   string // override or append
	Defaults string // override or append
}

// GatewayObservedGenerations maps each gateway (namespace/name) to the highest
// observed generation seen in the processed frontends' metadata.
type GatewayObservedGenerations map[types.NamespacedName]int64

// HaproxyConfResult is the outcome of a single HaproxyConfDiffs processing
// cycle, sent back to the controller via HaproxyConfDiffs.ResultCh.
type HaproxyConfResult struct {
	Err                        error
	GatewayObservedGenerations GatewayObservedGenerations
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

// GatewayObservedGenerations builds a map of gateway → max generation from
// the metadata of all Created and Updated frontends. Deleted frontends are
// excluded because their config is no longer active.
func (c HaproxyConfDiffs) GatewayObservedGenerations() GatewayObservedGenerations {
	result := make(GatewayObservedGenerations)
	for _, fe := range c.Created.Frontends {
		for key, gen := range extractGatewayGenerationsFromFrontend(fe) {
			if existing, ok := result[key]; !ok || gen > existing {
				result[key] = gen
			}
		}
	}
	for _, fe := range c.Updated.Frontends {
		for key, gen := range extractGatewayGenerationsFromFrontend(fe) {
			if existing, ok := result[key]; !ok || gen > existing {
				result[key] = gen
			}
		}
	}
	return result
}

func extractGatewayGenerationsFromFrontend(fe *models.Frontend) map[types.NamespacedName]int64 {
	result := make(map[types.NamespacedName]int64)
	if fe == nil || fe.Metadata == nil {
		return result
	}
	hugI, ok := fe.Metadata[metadata.UnifiedGatewayMetaDataKey]
	if !ok {
		return result
	}
	by, err := json.Marshal(hugI)
	if err != nil {
		return result
	}
	// After the JSON round-trip in FrontendMetadata(), the structure is:
	// { "Gateway": { "namespace/name": { "Generation": float64, "LinkID": string } } }
	var hugMeta map[string]map[string]map[string]any
	if err := json.Unmarshal(by, &hugMeta); err != nil {
		return result
	}
	gatewayMeta, ok := hugMeta["Gateway"]
	if !ok {
		return result
	}
	for nsName, info := range gatewayMeta {
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
		result[nn] = int64(genFloat)
	}
	return result
}

func parseNamespacedName(s string) types.NamespacedName {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return types.NamespacedName{}
	}
	return types.NamespacedName{Namespace: parts[0], Name: parts[1]}
}

func (c HaproxyConfDiffs) Stats() string {
	return fmt.Sprintf("Created/Updated/Deleted FE:[%d/%d/%d] BE[%d/%d/%d] Global[%d/%d/%d] Defaults[%d/%d/%d] Reload[%t]",
		len(c.Created.Frontends), len(c.Updated.Frontends), len(c.Deleted.Frontends),
		len(c.Created.Backends), len(c.Updated.Backends), len(c.Deleted.Backends),
		len(c.Created.Globals), len(c.Updated.Globals), len(c.Deleted.Globals),
		len(c.Created.Defaults), len(c.Updated.Defaults), len(c.Deleted.Defaults),
		c.ReloadNeed,
	)
}
