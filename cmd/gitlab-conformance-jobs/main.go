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

// Command gitlab-conformance-jobs renders the child pipeline that runs the
// conformance suite split across several jobs. Each job gets its own kind
// cluster and is told which suite tests to skip, because the suite has no
// "run only these" switch; the union of the jobs still covers every test.
//
// The test list is read from the gateway-api conformance package itself, so a
// test upstream adds lands in exactly one shard without an edit here.
package main

import (
	"log"
	"os"
)

const (
	defaultManifest = ".gitlab/conformance-jobs.yml"
	defaultOutput   = "generated-conformance.yml"
)

func main() {
	log.SetFlags(0)
	manifestPath := envOr("CONFORMANCE_JOBS_MANIFEST", defaultManifest)
	outputPath := envOr("CONFORMANCE_JOBS_OUTPUT", defaultOutput)

	manifest, err := loadManifest(manifestPath)
	if err != nil {
		log.Fatalf("cannot read %s: %v", manifestPath, err)
	}

	names := suiteTestNames()
	jobs := manifest.selectedJobs(triggerFromEnv())
	pipeline := buildPipeline(jobs, names)
	for _, shard := range pipeline.Legs {
		log.Printf("%s: %d test(s)", shard.Name, shard.Tests)
	}
	log.Printf("%d suite test(s), %d job set(s) selected, %d shard job(s) to place",
		len(names), len(jobs), len(pipeline.Legs))

	content, err := render(pipeline)
	if err != nil {
		log.Fatalf("cannot render the child pipeline: %v", err)
	}
	if err = os.WriteFile(outputPath, []byte(content), 0o644); err != nil {
		log.Fatalf("cannot write %s: %v", outputPath, err)
	}
	log.Printf("wrote %s", outputPath)
}

// envOr returns the environment value, or the fallback when it is unset.
func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
