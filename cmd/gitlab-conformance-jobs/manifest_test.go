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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "conformance-jobs.yml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestLoadManifestReadsJobSets(t *testing.T) {
	manifest, err := loadManifest(writeManifest(t, `
jobs:
  - name: conformance-MR-GW-API-1_7_0
    stage: conformance-tests
    when: mr_or_push
    shards: 6
`))
	require.NoError(t, err)
	require.Len(t, manifest.Jobs, 1)
	assert.Equal(t, mrJobSet(6), manifest.Jobs)
}

func TestLoadManifestRejectsWhatCannotBeRendered(t *testing.T) {
	for name, body := range map[string]string{
		"no name":      "jobs:\n  - stage: s\n    when: mr_or_push\n    shards: 2\n",
		"no stage":     "jobs:\n  - name: j\n    when: mr_or_push\n    shards: 2\n",
		"no shards":    "jobs:\n  - name: j\n    stage: s\n    when: mr_or_push\n    shards: 0\n",
		"unknown when": "jobs:\n  - name: j\n    stage: s\n    when: schedule\n    shards: 2\n",
		"missing when": "jobs:\n  - name: j\n    stage: s\n    shards: 2\n",
		"broken yaml":  "jobs: [",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadManifest(writeManifest(t, body))
			assert.Error(t, err)
		})
	}
}

func TestSelectedJobsSplitsOnPipelineSource(t *testing.T) {
	jobs := mrJobSet(6)
	for source, want := range map[string]int{sourceMR: 1, sourcePush: 1, "schedule": 0, "web": 0} {
		assert.Len(t, Manifest{Jobs: jobs}.selectedJobs(Trigger{Source: source}), want, "source %q", source)
	}
}

func TestNightlyJobRunsOnlyForTheNightlySchedule(t *testing.T) {
	manifest := Manifest{Jobs: []JobSet{{
		Name:   "conformance-GW-API-1_7_0",
		Stage:  "conformance-tests",
		When:   whenScheduleNightly,
		Shards: 2,
	}}}

	assert.Len(t, manifest.selectedJobs(Trigger{Source: sourceMR}), 0)
	assert.Len(t, manifest.selectedJobs(Trigger{Source: sourcePush}), 0)
	assert.Len(t, manifest.selectedJobs(Trigger{Source: "schedule"}), 0,
		"a schedule without the nightly type runs nothing")
	assert.Len(t, manifest.selectedJobs(Trigger{Source: "schedule", ScheduleType: scheduleNightly}), 1)
	assert.Len(t, manifest.selectedJobs(Trigger{Source: "web", ScheduleType: scheduleNightly}), 1,
		"keyed on the type so a manual pipeline can rehearse it")
}

// TestLoadManifestRejectsTwoReportSets guards the merged report's name, which
// the diff and its history rely on.
func TestLoadManifestRejectsTwoReportSets(t *testing.T) {
	_, err := loadManifest(writeManifest(t, `
jobs:
  - name: conformance-MR-GW-API-1_7_0
    stage: conformance-tests
    when: mr_or_push
    shards: 6
    report: true
  - name: conformance-GW-API-1_7_0
    stage: conformance-tests
    when: schedule_nightly
    shards: 6
    report: true
`))
	assert.Error(t, err)
}
