// Copyright 2026 HAProxy Technologies LLC
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
package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func sevenTests() []string {
	return []string{"A1", "A2", "A3", "A4", "A5", "A6", "A7"}
}

func mrJobSet(shards int) []JobSet {
	return []JobSet{{
		Name:   "conformance-MR-GW-API-1_7_0",
		Stage:  "conformance-tests",
		When:   whenMROrPush,
		Shards: shards,
	}}
}

func skippedBy(t *testing.T, legs []leg) map[string]int {
	t.Helper()
	skipped := map[string]int{}
	for _, l := range legs {
		for name := range strings.SplitSeq(l.SkipTests, ",") {
			if name != "" {
				skipped[name]++
			}
		}
	}
	return skipped
}

// TestBuildPipelineCoversEveryTestExactlyOnce is the safety net for the split:
// a test no shard runs would silently narrow what the pipeline checks.
func TestBuildPipelineCoversEveryTestExactlyOnce(t *testing.T) {
	p := buildPipeline(mrJobSet(3), sevenTests())
	require.Len(t, p.Legs, 3)

	skipped := skippedBy(t, p.Legs)
	for _, name := range sevenTests() {
		assert.Equal(t, 2, skipped[name], "test %s must be skipped by the two shards that do not own it", name)
	}
}

func TestShardTestsDealsRoundRobin(t *testing.T) {
	assert.Equal(t, [][]string{{"A1", "A4", "A7"}, {"A2", "A5"}, {"A3", "A6"}}, shardTests(sevenTests(), 3))
}

func TestShardTestsDropsEmptyShards(t *testing.T) {
	shards := shardTests(sevenTests(), 10)
	assert.Len(t, shards, 7)
	assert.Equal(t, [][]string{{"A1"}, {"A2"}, {"A3"}, {"A4"}, {"A5"}, {"A6"}, {"A7"}}, shards)
}

func TestRenderOneJobPerShardAsValidYAML(t *testing.T) {
	p := buildPipeline(mrJobSet(3), sevenTests())
	content, err := render(p)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(content), &parsed), "rendered pipeline must be valid YAML")
	assert.Equal(t, []any{"conformance-tests"}, parsed["stages"])
	assert.Equal(t, []any{map[string]any{"local": ".gitlab/.conformance.yml"}}, parsed["include"])

	for _, l := range p.Legs {
		job, ok := parsed[l.Name].(map[string]any)
		require.True(t, ok, "job %q missing from the rendered pipeline", l.Name)
		assert.Equal(t, ".conformance-mr", job["extends"])
		variables, ok := job["variables"].(map[string]any)
		require.True(t, ok, "job %q has no variables", l.Name)
		assert.Equal(t, l.SkipTests, variables["CONFORMANCE_SKIP_TESTS"])
	}
	assert.Len(t, parsed, 3+2, "three jobs plus include and stages")
}

func TestRenderWithoutJobsKeepsThePipelineValid(t *testing.T) {
	content, err := render(buildPipeline(nil, sevenTests()))
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(content), &parsed))
	_, ok := parsed["conformance-no-jobs"].(map[string]any)
	assert.True(t, ok, "a child pipeline with no jobs is invalid, so a no-op job must be rendered")
}

func TestSuiteTestsArePresentAndUnique(t *testing.T) {
	names := suiteTestNames()
	require.NotEmpty(t, names)

	seen := map[string]bool{}
	for _, name := range names {
		assert.False(t, seen[name], "test %s appears twice in the suite; sharding keys on the short name", name)
		seen[name] = true
	}
}
