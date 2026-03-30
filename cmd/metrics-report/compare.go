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

//revive:disable:unhandled-error
package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// metricSummary holds the aggregated final values for a single metric across all tests in a job/suite.
type metricSummary struct {
	FinalSum float64 // sum of final values across all tests
	Count    int     // number of tests contributing
}

// comparisonResult holds the comparison for one metric in one job.
type comparisonResult struct {
	MetricKey string
	Title     string
	Unit      string
	JobName   string
	Status    string // "better", "worse", "same", "new"
	Baseline  float64
	Current   float64
	PctChange float64
}

// lowerIsBetter returns true for metrics where a decrease is an improvement.
func lowerIsBetter(key string) bool {
	switch key {
	case "hug_event_batch_total":
		return false // informational, not inherently good or bad
	default:
		return true
	}
}

const changeThreshold = 5.0 // percent change below this is "same"

func classifyChange(key string, baseline, current float64) (pctChange float64, status string) {
	if baseline == 0 {
		if current == 0 {
			return 0, "same"
		}
		return 0, "new"
	}
	pctChange = ((current - baseline) / math.Abs(baseline)) * 100

	if math.Abs(pctChange) < changeThreshold {
		return pctChange, "same"
	}

	increased := pctChange > 0
	if lowerIsBetter(key) {
		if increased {
			return pctChange, "worse"
		}
		return pctChange, "better"
	}
	// for "higher is not worse" metrics, just report the direction
	if increased {
		return pctChange, "increased"
	}
	return pctChange, "decreased"
}

// computeSummaries extracts per-metric, per-job final-value summaries from loaded job data.
func computeSummaries(jobs []jobData) map[string]map[string]metricSummary {
	// metric key -> job name -> summary
	result := make(map[string]map[string]metricSummary)
	for _, tracked := range trackedMetrics {
		result[tracked.Key] = make(map[string]metricSummary)
		for _, j := range jobs {
			var finalSum float64
			var count int
			for _, suite := range j.Suites {
				series := buildTestSeries(suite.Samples, tracked.Key, tracked.Unit)
				for _, ts := range series {
					if len(ts.Values) > 0 {
						finalSum += ts.Values[len(ts.Values)-1]
						count++
					}
				}
			}
			if count > 0 {
				result[tracked.Key][j.Name] = metricSummary{FinalSum: finalSum, Count: count}
			}
		}
	}
	return result
}

// compareMetrics produces a comparison between baseline and current job data.
func compareMetrics(baselineJobs, currentJobs []jobData) []comparisonResult {
	baselineSummaries := computeSummaries(baselineJobs)
	currentSummaries := computeSummaries(currentJobs)

	var results []comparisonResult
	for _, tracked := range trackedMetrics {
		baseJobMap := baselineSummaries[tracked.Key]
		currJobMap := currentSummaries[tracked.Key]

		// Collect all job names from both
		jobNames := make(map[string]struct{})
		for k := range baseJobMap {
			jobNames[k] = struct{}{}
		}
		for k := range currJobMap {
			jobNames[k] = struct{}{}
		}

		for jobName := range jobNames {
			baseSummary := baseJobMap[jobName]
			currSummary := currJobMap[jobName]

			baseAvg := 0.0
			if baseSummary.Count > 0 {
				baseAvg = baseSummary.FinalSum / float64(baseSummary.Count)
			}
			currAvg := 0.0
			if currSummary.Count > 0 {
				currAvg = currSummary.FinalSum / float64(currSummary.Count)
			}

			if baseSummary.Count == 0 && currSummary.Count == 0 {
				continue
			}

			pctChange, status := classifyChange(tracked.Key, baseAvg, currAvg)
			results = append(results, comparisonResult{
				MetricKey: tracked.Key,
				Title:     tracked.Title,
				Unit:      tracked.Unit,
				JobName:   jobName,
				Baseline:  baseAvg,
				Current:   currAvg,
				PctChange: pctChange,
				Status:    status,
			})
		}
	}
	return results
}

// formatComparisonReport generates a markdown section for the comparison.
func formatComparisonReport(results []comparisonResult, baselinePipelineID string) string {
	if len(results) == 0 {
		return ""
	}

	var better, worse, same int
	for _, r := range results {
		switch r.Status {
		case "better":
			better++
		case "worse":
			worse++
		default:
			same++
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## Metrics Comparison (vs pipeline #%s)\n\n", baselinePipelineID)

	// Global summary
	if worse > 0 {
		fmt.Fprintf(&b, "**%d worse**, %d better, %d unchanged\n\n", worse, better, same)
	} else if better > 0 {
		fmt.Fprintf(&b, "%d better, %d unchanged\n\n", better, same)
	} else {
		b.WriteString("No significant changes\n\n")
	}

	// Only show details if there are changes
	hasChanges := false
	for _, r := range results {
		if r.Status != "same" {
			hasChanges = true
			break
		}
	}
	if !hasChanges {
		return b.String()
	}

	b.WriteString("| Metric | Job | Baseline | Current | Change | Status |\n")
	b.WriteString("| ---:|:---:|---:|---:|---:|:--- |\n")

	for _, r := range results {
		statusIcon := ""
		switch r.Status {
		case "better":
			statusIcon = "OK"
		case "worse":
			statusIcon = "WORSE"
		case "same":
			continue // skip unchanged in detailed table
		case "new":
			statusIcon = "NEW"
		default:
			statusIcon = r.Status
		}

		unit := ""
		if r.Unit != "" {
			unit = " " + r.Unit
		}

		changeStr := fmt.Sprintf("%+.1f%%", r.PctChange)
		if r.Status == "new" {
			changeStr = "n/a"
		}

		fmt.Fprintf(&b, "| %s | %s | %.2f%s | %.2f%s | %s | %s |\n",
			r.Title, r.JobName, r.Baseline, unit, r.Current, unit, changeStr, statusIcon)
	}
	b.WriteRune('\n')

	return b.String()
}

// --- GitLab API helpers for fetching baseline ---

type glPipeline struct {
	Ref string `json:"ref"`
	ID  int    `json:"id"`
}

type glMergeRequest struct {
	IID int `json:"iid"`
}

type glJob struct {
	Name string `json:"name"`
	ID   int    `json:"id"`
}

func glAPIGet(path string) ([]byte, error) {
	base := os.Getenv("CI_API_V4_URL")
	token := os.Getenv("GITLAB_TOKEN")
	if base == "" || token == "" {
		return nil, errors.New("CI_API_V4_URL or GITLAB_TOKEN not set")
	}

	req, err := http.NewRequest("GET", base+path, nil) //nolint:noctx
	if err != nil {
		return nil, err
	}
	req.Header.Set("PRIVATE-TOKEN", token) //nolint:canonicalheader

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitLab API %s returned %d", path, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// findBaselinePipeline finds the pipeline from the last MR merged into the target branch.
// Test jobs only run on merge_request_event pipelines, so we look up the head pipeline
// of the most recently merged MR.
func findBaselinePipeline(projectID, targetBranch string) (int, error) {
	currentMRIID := os.Getenv("CI_MERGE_REQUEST_IID")

	data, err := glAPIGet(fmt.Sprintf(
		"/projects/%s/merge_requests?state=merged&target_branch=%s&per_page=10&order_by=updated_at&sort=desc",
		projectID, targetBranch,
	))
	if err != nil {
		return 0, err
	}

	var mrs []glMergeRequest
	if err := json.Unmarshal(data, &mrs); err != nil {
		return 0, err
	}

	for _, mr := range mrs {
		// Skip the current MR if it somehow appears.
		if currentMRIID != "" && fmt.Sprintf("%d", mr.IID) == currentMRIID {
			continue
		}

		// Look up pipelines for this MR.
		pData, err := glAPIGet(fmt.Sprintf(
			"/projects/%s/merge_requests/%d/pipelines?per_page=5&order_by=id&sort=desc",
			projectID, mr.IID,
		))
		if err != nil {
			continue
		}

		var pipelines []glPipeline
		if err := json.Unmarshal(pData, &pipelines); err != nil {
			continue
		}

		// Find the last successful pipeline with test jobs.
		for _, p := range pipelines {
			testJobs, err := findTestJobs(projectID, p.ID)
			if err != nil || len(testJobs) == 0 {
				continue
			}
			fmt.Printf("  baseline: MR !%d, pipeline %d\n", mr.IID, p.ID)
			return p.ID, nil
		}
	}

	return 0, errors.New("no merged MR with test job artifacts found")
}

// findTestJobs finds the test job IDs in a pipeline that match our test job names.
func findTestJobs(projectID string, pipelineID int) ([]glJob, error) {
	data, err := glAPIGet(fmt.Sprintf(
		"/projects/%s/pipelines/%d/jobs?per_page=100",
		projectID, pipelineID,
	))
	if err != nil {
		return nil, err
	}

	var allJobs []glJob
	if err := json.Unmarshal(data, &allJobs); err != nil {
		return nil, err
	}

	var matched []glJob
	for _, j := range allJobs {
		if strings.HasPrefix(j.Name, "tests-GW-API-") {
			matched = append(matched, j)
		}
	}
	return matched, nil
}

// downloadResult holds samples and the job name derived from the artifact directory.
type downloadResult struct {
	JobName string
	Samples []metricsSample
}

// downloadMetricsFromJob downloads job artifacts and extracts JSONL metric files.
// Returns the directory-based job name (e.g. "gwapi-1_3_0") to match loadJobs naming.
func downloadMetricsFromJob(projectID string, jobID int) (downloadResult, error) {
	data, err := glAPIGet(fmt.Sprintf("/projects/%s/jobs/%d/artifacts", projectID, jobID))
	if err != nil {
		return downloadResult{}, err
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return downloadResult{}, fmt.Errorf("reading artifact zip: %w", err)
	}

	var result downloadResult
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !strings.HasSuffix(f.Name, "hug_metrics_samples.jsonl") {
			continue
		}

		// Extract directory name (e.g. "metrics-output/gwapi-1_3_0/..." -> "gwapi-1_3_0")
		dirName := filepath.Base(filepath.Dir(f.Name))
		if dirName != "." {
			result.JobName = dirName
		}

		rc, err := f.Open()
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(rc)
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
		for scanner.Scan() {
			var s metricsSample
			if err := json.Unmarshal(scanner.Bytes(), &s); err != nil {
				continue
			}
			result.Samples = append(result.Samples, s)
		}
		rc.Close()
	}

	return result, nil
}

// loadBaselineJobs fetches metrics data from the last successful pipeline on the target branch.
func loadBaselineJobs(projectID, targetBranch string) ([]jobData, int, error) {
	pipelineID, err := findBaselinePipeline(projectID, targetBranch)
	if err != nil {
		return nil, 0, err
	}

	testJobs, err := findTestJobs(projectID, pipelineID)
	if err != nil {
		return nil, pipelineID, err
	}
	if len(testJobs) == 0 {
		return nil, pipelineID, fmt.Errorf("no test jobs found in pipeline %d", pipelineID)
	}

	// job name -> suite name -> samples
	jobMap := make(map[string]map[string][]metricsSample)

	for _, tj := range testJobs {
		dl, err := downloadMetricsFromJob(projectID, tj.ID)
		if err != nil {
			fmt.Printf("Warning: failed to download metrics from job %s (#%d): %v\n", tj.Name, tj.ID, err)
			continue
		}
		if len(dl.Samples) == 0 {
			continue
		}

		jobName := dl.JobName
		if jobName == "" {
			jobName = tj.Name
		}
		if jobMap[jobName] == nil {
			jobMap[jobName] = make(map[string][]metricsSample)
		}
		for _, s := range dl.Samples {
			jobMap[jobName][s.Suite] = append(jobMap[jobName][s.Suite], s)
		}
		fmt.Printf("  baseline: loaded %d samples from job %s (#%d)\n", len(dl.Samples), jobName, tj.ID)
	}

	var jobs []jobData
	for jobName, suiteMap := range jobMap {
		var suites []suiteData
		for suiteName, samples := range suiteMap {
			suites = append(suites, suiteData{Name: suiteName, Samples: samples})
		}
		jobs = append(jobs, jobData{Name: jobName, Suites: suites})
	}

	return jobs, pipelineID, nil
}
