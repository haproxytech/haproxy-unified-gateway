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
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// JobSet is one conformance job definition, fanned out over its shards.
type JobSet struct {
	Name         string `yaml:"name"`
	Stage        string `yaml:"stage"`
	When         string `yaml:"when"`
	Shards       int    `yaml:"shards"`
	AllowFailure bool   `yaml:"allow_failure"`
	// Report adds one job after the shards that merges their JUnit files and
	// diffs them against the previous scheduled run.
	Report bool `yaml:"report"`
}

// Manifest is the parsed .gitlab/conformance-jobs.yml.
type Manifest struct {
	Jobs []JobSet `yaml:"jobs"`
}

// Trigger describes the pipeline the generator runs in.
type Trigger struct {
	Source       string
	ScheduleType string
}

const (
	whenMROrPush        = "mr_or_push"
	whenScheduleNightly = "schedule_nightly"
	sourceMR            = "merge_request_event"
	sourcePush          = "push"
	scheduleNightly     = "nightly"
)

var errUnknownWhen = errors.New("unknown when value")

// loadManifest reads and validates the job manifest at path.
func loadManifest(path string) (Manifest, error) {
	var manifest Manifest
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest, err
	}
	if err = yaml.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, err
	}
	reports := 0
	for _, job := range manifest.Jobs {
		if err = job.validate(); err != nil {
			return Manifest{}, err
		}
		if job.Report {
			reports++
		}
	}
	// The merged report keeps the name the diff and its history use.
	if reports > 1 {
		return Manifest{}, fmt.Errorf("at most one job set may set report: true, found %d", reports)
	}
	return manifest, nil
}

// validate rejects a job set the generator cannot render. Shards is not bounded
// above: more shards than tests only leaves empty shards, which are dropped.
func (j JobSet) validate() error {
	if j.Name == "" || j.Stage == "" {
		return fmt.Errorf("job %q needs a name and a stage", j.Name)
	}
	if j.Shards < 1 {
		return fmt.Errorf("job %q needs at least one shard", j.Name)
	}
	if j.When != whenMROrPush && j.When != whenScheduleNightly {
		return fmt.Errorf("%w %q for job %q", errUnknownWhen, j.When, j.Name)
	}
	return nil
}

// selected reports whether this job set runs in the given pipeline.
func (j JobSet) selected(t Trigger) bool {
	switch j.When {
	case whenMROrPush:
		return t.Source == sourceMR || t.Source == sourcePush
	case whenScheduleNightly:
		// Keyed on the schedule type rather than the source, so the same set can
		// be rehearsed from a manual pipeline.
		return t.ScheduleType == scheduleNightly
	}
	return false
}

// selectedJobs returns the job sets that run in the given pipeline.
func (m Manifest) selectedJobs(t Trigger) []JobSet {
	jobs := make([]JobSet, 0, len(m.Jobs))
	for _, job := range m.Jobs {
		if job.selected(t) {
			jobs = append(jobs, job)
		}
	}
	return jobs
}

// triggerFromEnv reads the pipeline context from the GitLab CI environment.
func triggerFromEnv() Trigger {
	return Trigger{
		Source:       os.Getenv("CI_PIPELINE_SOURCE"),
		ScheduleType: os.Getenv("SCHEDULE_TYPE"),
	}
}
