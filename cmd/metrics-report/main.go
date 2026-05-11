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

// metrics-report reads HUG integration test metrics samples (JSONL files),
// produces a self-contained HTML artifact with inline SVG charts, and posts
// an MR comment containing only the baseline comparison summary and a link
// to the HTML artifact.
//
//revive:disable:unhandled-error
package main

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// metricsSample mirrors the struct in test/integration/base/base.go.
type metricsSample struct {
	Metrics map[string]metricValues `json:"metrics"`
	Suite   string                  `json:"suite"`
	Test    string                  `json:"test"`
	Elapsed float64                 `json:"elapsed_s"`
}

type metricValues struct {
	Value *float64 `json:"value,omitempty"`
	Count *uint64  `json:"count,omitempty"`
	Sum   *float64 `json:"sum,omitempty"`
	Type  string   `json:"type"`
}

// testTimeSeries holds the time-series data for a single metric from one test.
type testTimeSeries struct {
	Test   string
	Times  []float64
	Values []float64
}

// suiteData holds all samples for one test suite.
type suiteData struct {
	Name    string
	Samples []metricsSample
}

// jobData holds suites grouped by job (directory name).
type jobData struct {
	Name   string
	Suites []suiteData
}

// Charts to generate: metric key pattern -> chart title.
var trackedMetrics = []struct {
	Key   string
	Title string
	Unit  string
}{
	{"go_goroutines", "Goroutines", ""},
	{"go_memstats_alloc_bytes", "Heap Alloc", "MB"},
	{"go_memstats_sys_bytes", "Sys Memory", "MB"},
	{"process_resident_memory_bytes", "RSS", "MB"},
	{"process_cpu_seconds_total", "CPU Time", "s"},
	{"hug_event_batch_total", "Event Batches", ""},
	{"hug_event_batch_errors_total", "Batch Errors", ""},
	{"hug_haproxy_reload_total", "HAProxy Reloads", ""},
}

func main() {
	metricsDir := os.Getenv("METRICS_OUTPUT_DIR")
	if metricsDir == "" {
		metricsDir = "metrics-output"
	}

	mrIID := os.Getenv("CI_MERGE_REQUEST_IID")
	if mrIID == "" {
		fmt.Println("Not a merge request context. Generating report to stdout only.")
	}

	jobs, err := loadJobs(metricsDir)
	if err != nil {
		fmt.Printf("Error loading samples: %v\n", err)
		os.Exit(1)
	}
	if len(jobs) == 0 {
		fmt.Println("No metrics samples found.")
		os.Exit(0)
	}

	var commentComparison, htmlComparison string
	if targetBranch := os.Getenv("CI_MERGE_REQUEST_TARGET_BRANCH_NAME"); targetBranch != "" {
		projectID := os.Getenv("CI_PROJECT_ID")
		if projectID != "" {
			fmt.Printf("Fetching baseline metrics from branch %s...\n", targetBranch)
			baselineJobs, pipelineID, err := loadBaselineJobs(projectID, targetBranch)
			if err != nil {
				fmt.Printf("Warning: could not load baseline: %v\n", err)
			} else if len(baselineJobs) > 0 {
				results := compareMetrics(baselineJobs, jobs)
				pipelineStr := fmt.Sprintf("%d", pipelineID)
				commentComparison = formatComparisonSummary(results, pipelineStr)
				htmlComparison = renderComparisonHTML(results, pipelineStr)
			}
		}
	}

	htmlPath, err := renderHTMLReport(jobs, metricsDir, htmlComparison)
	if err != nil {
		fmt.Printf("Error generating HTML report: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("HTML report written to %s\n", htmlPath)

	comment := buildMRComment(commentComparison)

	reportFile := filepath.Join(metricsDir, "metrics-report.md")
	if err := os.WriteFile(reportFile, []byte(comment), 0o644); err != nil {
		fmt.Printf("Error writing report: %v\n", err)
	} else {
		fmt.Printf("Comment written to %s\n", reportFile)
	}

	if mrIID != "" {
		if err := postMRComment(comment, noteMarker); err != nil {
			fmt.Printf("Error posting MR comment: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("MR comment posted successfully.")
	} else {
		fmt.Println(comment)
	}
}

// buildMRComment assembles the MR comment: a heading, the comparison block
// (if any), and a link to the HTML artifact for the full charts.
func buildMRComment(comparison string) string {
	var b strings.Builder
	b.WriteString("## HUG Integration Test Metrics\n\n")
	if comparison != "" {
		b.WriteString(comparison)
	} else {
		b.WriteString("_No baseline comparison available._\n\n")
	}
	if link := htmlArtifactLink(); link != "" {
		fmt.Fprintf(&b, "Full charts: [metrics-report.html](%s)\n\n", link)
	} else {
		b.WriteString("Full charts: see job artifact `metrics-output/metrics-report.html`.\n\n")
	}
	b.WriteString("---\n_Generated by HUG metrics-report_\n")
	return b.String()
}

// htmlArtifactLink builds the GitLab artifact-browser URL for the HTML report,
// using CI env vars. Returns "" when not in a GitLab job context.
func htmlArtifactLink() string {
	projectURL := os.Getenv("CI_PROJECT_URL")
	jobID := os.Getenv("CI_JOB_ID")
	if projectURL == "" || jobID == "" {
		return ""
	}
	return fmt.Sprintf("%s/-/jobs/%s/artifacts/raw/metrics-output/metrics-report.html", projectURL, jobID)
}

// loadJobs walks the metrics directory, groups samples by job (parent dir)
// and then by suite within each job.
func loadJobs(dir string) ([]jobData, error) {
	// job name -> suite name -> samples
	jobMap := make(map[string]map[string][]metricsSample)

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || d.Name() != "hug_metrics_samples.jsonl" {
			return nil
		}
		samples, readErr := readJSONL(path)
		if readErr != nil {
			fmt.Printf("Warning: skipping %s: %v\n", path, readErr)
			return nil
		}

		jobName := filepath.Base(filepath.Dir(path))
		if jobName == "." || jobName == filepath.Base(dir) {
			jobName = "default"
		}

		if jobMap[jobName] == nil {
			jobMap[jobName] = make(map[string][]metricsSample)
		}
		for _, s := range samples {
			jobMap[jobName][s.Suite] = append(jobMap[jobName][s.Suite], s)
		}

		fmt.Printf("  loaded %d samples from %s (job: %s)\n", len(samples), path, jobName)
		return nil
	})
	if err != nil {
		return nil, err
	}

	var jobs []jobData
	for jobName, suiteMap := range jobMap {
		var suites []suiteData
		for suiteName, samples := range suiteMap {
			slices.SortFunc(samples, func(a, b metricsSample) int {
				return cmp.Compare(a.Elapsed, b.Elapsed)
			})
			suites = append(suites, suiteData{Name: suiteName, Samples: samples})
		}
		slices.SortFunc(suites, func(a, b suiteData) int {
			return cmp.Compare(a.Name, b.Name)
		})
		jobs = append(jobs, jobData{Name: jobName, Suites: suites})
	}
	slices.SortFunc(jobs, func(a, b jobData) int {
		return cmp.Compare(a.Name, b.Name)
	})

	return jobs, nil
}

func readJSONL(path string) ([]metricsSample, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var samples []metricsSample
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for scanner.Scan() {
		var s metricsSample
		if err := json.Unmarshal(scanner.Bytes(), &s); err != nil {
			continue
		}
		samples = append(samples, s)
	}
	return samples, scanner.Err()
}

// buildTestSeries groups samples by test name and builds one time series per test.
func buildTestSeries(samples []metricsSample, key, unit string) []testTimeSeries {
	// Preserve discovery order of tests
	testOrder := []string{}
	testSamples := make(map[string][]metricsSample)
	for _, s := range samples {
		if _, seen := testSamples[s.Test]; !seen {
			testOrder = append(testOrder, s.Test)
		}
		testSamples[s.Test] = append(testSamples[s.Test], s)
	}

	var result []testTimeSeries
	for _, test := range testOrder {
		ts := testTimeSeries{Test: test}
		for _, s := range testSamples[test] {
			mv, ok := s.Metrics[key]
			if !ok {
				continue
			}

			var val float64
			switch {
			case mv.Value != nil:
				val = *mv.Value
			case mv.Count != nil:
				val = float64(*mv.Count)
			default:
				continue
			}

			if unit == "MB" {
				val /= 1024 * 1024
			}

			ts.Times = append(ts.Times, s.Elapsed)
			ts.Values = append(ts.Values, val)
		}
		if len(ts.Values) >= 2 {
			result = append(result, ts)
		}
	}
	return result
}

// maxParallel limits concurrency for I/O-bound operations (uploads, deletions).
const maxParallel = 10

// shortTestName strips the suite prefix from a test name for display.
func shortTestName(testName, suiteName string) string {
	if after, ok := strings.CutPrefix(testName, suiteName+"/"); ok {
		return after
	}
	return testName
}

func stats(values []float64) (minVal, maxVal, lastVal float64) {
	minVal = math.Inf(1)
	maxVal = math.Inf(-1)
	for _, v := range values {
		if v < minVal {
			minVal = v
		}
		if v > maxVal {
			maxVal = v
		}
	}
	lastVal = values[len(values)-1]
	return minVal, maxVal, lastVal
}

func formatAxisValue(v float64) string {
	switch {
	case v >= 1_000_000:
		return fmt.Sprintf("%.1fM", v/1_000_000)
	case v >= 1000:
		return fmt.Sprintf("%.1fK", v/1000)
	case v == float64(int(v)):
		return fmt.Sprintf("%.0f", v)
	default:
		return fmt.Sprintf("%.2f", v)
	}
}

// uploadToGitLab is retained for potential image-in-comment workflows but is
// currently unused since charts live in the HTML artifact.
//
//lint:ignore U1000 retained for future image-in-comment use
func uploadToGitLab(filePath, filename string) (string, error) {
	apiURL := os.Getenv("CI_API_V4_URL")
	projectID := os.Getenv("CI_PROJECT_ID")
	token := os.Getenv("GITLAB_TOKEN")

	if apiURL == "" || projectID == "" || token == "" {
		return "", errors.New("missing GitLab CI environment variables for upload")
	}

	fileData, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("reading file: %w", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("creating form file: %w", err)
	}
	if _, err := part.Write(fileData); err != nil {
		return "", fmt.Errorf("writing form data: %w", err)
	}
	writer.Close()

	url := fmt.Sprintf("%s/projects/%s/uploads", apiURL, projectID)
	req, err := http.NewRequest("POST", url, &body) //nolint:noctx,usestdlibvars
	if err != nil {
		return "", err
	}
	req.Header.Set("PRIVATE-TOKEN", token) //nolint:canonicalheader
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("upload failed: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		URL      string `json:"url"`
		Markdown string `json:"markdown"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding upload response: %w", err)
	}
	fmt.Printf("  uploaded %s -> %s\n", filename, result.URL)

	return result.URL, nil
}

func postMRComment(body, marker string) error {
	apiURL := os.Getenv("CI_API_V4_URL")
	projectID := os.Getenv("CI_PROJECT_ID")
	mrIID := os.Getenv("CI_MERGE_REQUEST_IID")
	token := os.Getenv("GITLAB_TOKEN")

	if apiURL == "" || projectID == "" || mrIID == "" {
		return errors.New("missing GitLab CI environment variables")
	}
	if token == "" {
		return errors.New("GITLAB_TOKEN not set")
	}

	existingNote, err := findExistingNote(apiURL, projectID, mrIID, token, marker)
	if err != nil {
		fmt.Printf("Warning: could not check for existing note: %v\n", err)
	}

	// Delete previously uploaded images before replacing the comment.
	if existingNote.id > 0 {
		deleteUploads(apiURL, projectID, token, existingNote.body)
	}

	body = marker + "\n" + body
	if existingNote.id > 0 {
		return updateNote(apiURL, projectID, mrIID, token, existingNote.id, body)
	}
	return createNote(apiURL, projectID, mrIID, token, body)
}

const noteMarker = "<!-- hug-metrics-report -->"

type existingNoteResult struct {
	body string
	id   int
}

func findExistingNote(apiURL, projectID, mrIID, token, marker string) (existingNoteResult, error) {
	url := fmt.Sprintf("%s/projects/%s/merge_requests/%s/notes?per_page=100", apiURL, projectID, mrIID)
	req, err := http.NewRequest("GET", url, nil) //nolint:noctx,usestdlibvars
	if err != nil {
		return existingNoteResult{}, err
	}
	req.Header.Set("PRIVATE-TOKEN", token) //nolint:canonicalheader

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return existingNoteResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return existingNoteResult{}, fmt.Errorf("list notes: status %d", resp.StatusCode)
	}

	var notes []struct {
		ID   int    `json:"id"`
		Body string `json:"body"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&notes); err != nil {
		return existingNoteResult{}, err
	}

	for _, n := range notes {
		if strings.Contains(n.Body, marker) {
			return existingNoteResult{id: n.ID, body: n.Body}, nil
		}
	}
	return existingNoteResult{}, nil
}

func createNote(apiURL, projectID, mrIID, token, body string) error {
	payload, _ := json.Marshal(map[string]string{"body": body})
	url := fmt.Sprintf("%s/projects/%s/merge_requests/%s/notes", apiURL, projectID, mrIID)
	req, err := http.NewRequest("POST", url, bytes.NewReader(payload)) //nolint:noctx,usestdlibvars
	if err != nil {
		return err
	}
	req.Header.Set("PRIVATE-TOKEN", token) //nolint:canonicalheader
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create note: status %d, body: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func updateNote(apiURL, projectID, mrIID, token string, noteID int, body string) error {
	payload, _ := json.Marshal(map[string]string{"body": body})
	url := fmt.Sprintf("%s/projects/%s/merge_requests/%s/notes/%d", apiURL, projectID, mrIID, noteID)
	req, err := http.NewRequest("PUT", url, bytes.NewReader(payload)) //nolint:noctx,usestdlibvars
	if err != nil {
		return err
	}
	req.Header.Set("PRIVATE-TOKEN", token) //nolint:canonicalheader
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("update note: status %d, body: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// deleteUploads parses image URLs from the old note body and deletes them
// in parallel via the GitLab project uploads API.
func deleteUploads(apiURL, projectID, token, noteBody string) {
	// Extract upload paths: ![...]( /uploads/secret/file.png )
	var paths []string
	for line := range strings.SplitSeq(noteBody, "\n") {
		idx := strings.Index(line, "(/uploads/")
		if idx == -1 {
			continue
		}
		rest := line[idx+1:]
		before, _, ok := strings.Cut(rest, ")")
		if ok {
			paths = append(paths, before)
		}
	}

	if len(paths) == 0 {
		return
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, maxParallel)

	for _, uploadPath := range paths {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			deleteOneUpload(apiURL, projectID, token, uploadPath)
		})
	}
	wg.Wait()
}

func deleteOneUpload(apiURL, projectID, token, uploadPath string) {
	url := fmt.Sprintf("%s/projects/%s%s", apiURL, projectID, uploadPath)
	req, err := http.NewRequest("DELETE", url, nil) //nolint:noctx,usestdlibvars
	if err != nil {
		return
	}
	req.Header.Set("PRIVATE-TOKEN", token) //nolint:canonicalheader

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		fmt.Printf("  Warning: failed to delete upload %s: %v\n", uploadPath, err)
		return
	}
	resp.Body.Close()
	fmt.Printf("  deleted old upload %s (status: %d)\n", uploadPath, resp.StatusCode)
}
