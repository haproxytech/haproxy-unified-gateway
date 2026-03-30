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
// generates PNG charts, uploads them to GitLab, and posts an MR comment
// with embedded chart images — one section per test suite.
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
	"image"
	"image/color"
	"image/png"
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

	report, err := generateReport(jobs, metricsDir, mrIID != "")
	if err != nil {
		fmt.Printf("Error generating report: %v\n", err)
		os.Exit(1)
	}

	// Compare with baseline from target branch when in MR context.
	if targetBranch := os.Getenv("CI_MERGE_REQUEST_TARGET_BRANCH_NAME"); targetBranch != "" {
		projectID := os.Getenv("CI_PROJECT_ID")
		if projectID != "" {
			fmt.Printf("Fetching baseline metrics from branch %s...\n", targetBranch)
			baselineJobs, pipelineID, err := loadBaselineJobs(projectID, targetBranch)
			if err != nil {
				fmt.Printf("Warning: could not load baseline: %v\n", err)
			} else if len(baselineJobs) > 0 {
				results := compareMetrics(baselineJobs, jobs)
				comparison := formatComparisonReport(results, fmt.Sprintf("%d", pipelineID))
				report = comparison + report
			}
		}
	}

	reportFile := filepath.Join(metricsDir, "metrics-report.md")
	if err := os.WriteFile(reportFile, []byte(report), 0o644); err != nil {
		fmt.Printf("Error writing report: %v\n", err)
	} else {
		fmt.Printf("Report written to %s\n", reportFile)
	}

	if mrIID != "" {
		if err := postMRComment(report, noteMarker); err != nil {
			fmt.Printf("Error posting MR comment: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("MR comment posted successfully.")
	} else {
		fmt.Println(report)
	}
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

// generateReport builds a single markdown report grouped by suite.
// For each metric, one chart per GWAPI version is shown stacked vertically.
func generateReport(jobs []jobData, outputDir string, useUpload bool) (string, error) {
	// Collect all unique suite names across jobs, in sorted order.
	suiteSet := make(map[string]struct{})
	for _, job := range jobs {
		for _, suite := range job.Suites {
			suiteSet[suite.Name] = struct{}{}
		}
	}
	suiteNames := make([]string, 0, len(suiteSet))
	for name := range suiteSet {
		suiteNames = append(suiteNames, name)
	}
	slices.Sort(suiteNames)

	// Index: job name -> suite name -> samples
	jobSuiteMap := make(map[string]map[string][]metricsSample)
	for _, job := range jobs {
		jobSuiteMap[job.Name] = make(map[string][]metricsSample)
		for _, suite := range job.Suites {
			jobSuiteMap[job.Name][suite.Name] = suite.Samples
		}
	}

	var b strings.Builder
	b.WriteString("## HUG Integration Test Metrics\n\n")

	for _, suiteName := range suiteNames {
		if err := writeSuiteSection(&b, suiteName, jobs, jobSuiteMap, outputDir, useUpload); err != nil {
			return "", err
		}
	}

	b.WriteString("---\n_Generated by HUG metrics-report_\n")
	return b.String(), nil
}

// maxParallel limits concurrency for I/O-bound operations (PNG generation, uploads, deletions).
const maxParallel = 10

// chartEntry holds a generated chart ready for upload and markdown assembly.
type chartEntry struct {
	Err       error // filled if PNG generation fails
	SuiteName string
	JobName   string
	MetricKey string
	Title     string
	UnitLabel string
	Stats     string // pre-formatted stats lines
	PNGPath   string
	Filename  string
	ImageURL  string           // filled after upload
	Series    []testTimeSeries // used for PNG generation
}

func writeSuiteSection(
	b *strings.Builder, suiteName string,
	jobs []jobData, jobSuiteMap map[string]map[string][]metricsSample,
	outputDir string, useUpload bool,
) error {
	// Phase 1: collect chart entries with their series data
	var charts []chartEntry
	for _, tracked := range trackedMetrics {
		unitLabel := ""
		if tracked.Unit != "" {
			unitLabel = " (" + tracked.Unit + ")"
		}

		for _, job := range jobs {
			samples := jobSuiteMap[job.Name][suiteName]
			if len(samples) == 0 {
				continue
			}

			series := buildTestSeries(samples, tracked.Key, tracked.Unit)
			if len(series) == 0 {
				continue
			}

			var statsBuf strings.Builder
			for _, ts := range series {
				testLabel := shortTestName(ts.Test, suiteName)
				minV, maxV, lastV := stats(ts.Values)
				fmt.Fprintf(&statsBuf, "- %s — Min: %.2f | Max: %.2f | Final: %.2f\n", testLabel, minV, maxV, lastV)
			}

			safeSuite := strings.NewReplacer("/", "_", " ", "_").Replace(suiteName)
			pngFilename := fmt.Sprintf("%s_%s_%s.png", job.Name, safeSuite, tracked.Key)
			pngPath := filepath.Join(outputDir, pngFilename)

			charts = append(charts, chartEntry{
				SuiteName: suiteName,
				JobName:   job.Name,
				MetricKey: tracked.Key,
				Title:     tracked.Title,
				UnitLabel: unitLabel,
				Stats:     statsBuf.String(),
				PNGPath:   pngPath,
				Filename:  pngFilename,
				Series:    series,
			})
		}
	}

	if len(charts) == 0 {
		return nil
	}

	// Phase 2: generate PNGs in parallel
	if err := generateChartPNGs(charts); err != nil {
		return err
	}

	// Phase 3: upload PNGs in parallel
	if useUpload {
		uploadCharts(charts)
	}

	// Phase 4: assemble markdown
	fmt.Fprintf(b, "<details>\n<summary>%s</summary>\n\n", suiteName)

	prevMetric := ""
	for _, c := range charts {
		if c.MetricKey != prevMetric {
			fmt.Fprintf(b, "**%s%s**\n\n", c.Title, c.UnitLabel)
			prevMetric = c.MetricKey
		}

		b.WriteString(c.Stats)
		b.WriteString("\n")

		if useUpload && c.ImageURL != "" {
			fmt.Fprintf(b, "**%s**\n\n![%s%s](%s)\n\n", c.JobName, c.Title, c.UnitLabel, c.ImageURL)
		} else if !useUpload {
			fmt.Fprintf(b, "**%s** — Chart saved to: `%s`\n\n", c.JobName, c.PNGPath)
		}
	}

	b.WriteString("</details>\n\n")
	return nil
}

func generateChartPNGs(charts []chartEntry) error {
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxParallel)

	for i := range charts {
		c := &charts[i]
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			c.Err = generatePNG(c.Series, c.SuiteName, c.Title+c.UnitLabel, c.PNGPath)
		})
	}
	wg.Wait()

	for _, c := range charts {
		if c.Err != nil {
			return fmt.Errorf("writing PNG %s: %w", c.PNGPath, c.Err)
		}
	}
	return nil
}

func uploadCharts(charts []chartEntry) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxParallel)

	for i := range charts {
		c := &charts[i]
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			imgURL, err := uploadToGitLab(c.PNGPath, c.Filename)
			if err != nil {
				fmt.Printf("Warning: failed to upload %s: %v\n", c.Filename, err)
				return
			}
			c.ImageURL = imgURL
		})
	}
	wg.Wait()
}

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

const (
	imgWidth  = 900
	imgHeight = 200
	padLeft   = 60
	padRight  = 10
	padTop    = 10
	padBottom = 20
)

var (
	colorBg   = color.RGBA{R: 250, G: 250, B: 250, A: 255}
	colorGrid = color.RGBA{R: 220, G: 220, B: 220, A: 255}
	colorAxis = color.RGBA{R: 180, G: 180, B: 180, A: 255}

	// Palette for distinguishing tests within a suite.
	linePalette = []color.RGBA{
		{R: 37, G: 99, B: 235, A: 255},  // blue
		{R: 220, G: 38, B: 38, A: 255},  // red
		{R: 22, G: 163, B: 74, A: 255},  // green
		{R: 168, G: 85, B: 247, A: 255}, // purple
		{R: 234, G: 88, B: 12, A: 255},  // orange
		{R: 14, G: 165, B: 233, A: 255}, // cyan
		{R: 161, G: 98, B: 7, A: 255},   // amber
		{R: 219, G: 39, B: 119, A: 255}, // pink
	}
)

const (
	legendRowHeight = 16
	fontScale       = 2
)

func generatePNG(series []testTimeSeries, suiteName, yAxisTitle, path string) error {
	if len(series) == 0 {
		return nil
	}

	var allValues []float64
	maxElapsed := 0.0
	for _, ts := range series {
		allValues = append(allValues, ts.Values...)
		if last := ts.Times[len(ts.Times)-1]; last > maxElapsed {
			maxElapsed = last
		}
	}
	if len(allValues) < 2 {
		return nil
	}
	if maxElapsed == 0 {
		maxElapsed = 1
	}

	minV, maxV, _ := stats(allValues)
	vRange := maxV - minV
	if vRange == 0 {
		vRange = 1
	}
	minV -= vRange * 0.05
	maxV += vRange * 0.05
	if minV < 0 {
		minV = 0
	}

	plotW := float64(imgWidth - padLeft - padRight)
	plotH := float64(imgHeight - padTop - padBottom)

	scaleX := func(t float64) int { return padLeft + int((t/maxElapsed)*plotW) }
	scaleY := func(v float64) int { return padTop + int(plotH-(v-minV)/(maxV-minV)*plotH) }

	legendH := len(series)*legendRowHeight + padTop
	totalHeight := imgHeight + legendH

	img := image.NewRGBA(image.Rect(0, 0, imgWidth, totalHeight))
	fillBackground(img, totalHeight)
	drawGrid(img, plotH)
	drawAxisLabels(img, plotH, minV, maxV, maxElapsed, yAxisTitle)

	for i, ts := range series {
		lineColor := linePalette[i%len(linePalette)]
		drawSeriesLine(img, ts, scaleX, scaleY, lineColor)
	}

	drawLegend(img, series, suiteName)

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func drawLegend(img *image.RGBA, series []testTimeSeries, suiteName string) {
	for i, ts := range series {
		lineColor := linePalette[i%len(linePalette)]
		ly := imgHeight + 4 + i*legendRowHeight
		// Color swatch line, centered vertically in the row
		swatchY := ly + (fontScale*5)/2 - 1
		for dx := range 24 {
			for dy := range 3 {
				img.Set(padLeft+dx, swatchY+dy, lineColor)
			}
		}
		label := shortTestName(ts.Test, suiteName)
		drawLabel(img, padLeft+28, ly, label, lineColor)
	}
}

// drawLabel draws text using a 3x5 bitmap font scaled by fontScale.
func drawLabel(img *image.RGBA, x, y int, text string, c color.RGBA) {
	cx := x
	for _, ch := range strings.ToUpper(text) {
		glyph, ok := tinyFont[ch]
		if !ok {
			cx += 4 * fontScale
			continue
		}
		for row, bits := range glyph {
			for col := range 3 {
				if bits>>(2-col)&1 == 1 {
					for dy := range fontScale {
						for dx := range fontScale {
							img.Set(cx+col*fontScale+dx, y+row*fontScale+dy, c)
						}
					}
				}
			}
		}
		cx += 4 * fontScale
	}
}

var tinyFont = map[rune][5]byte{
	'A': {0b111, 0b101, 0b111, 0b101, 0b101},
	'B': {0b110, 0b101, 0b110, 0b101, 0b110},
	'C': {0b111, 0b100, 0b100, 0b100, 0b111},
	'D': {0b110, 0b101, 0b101, 0b101, 0b110},
	'E': {0b111, 0b100, 0b110, 0b100, 0b111},
	'F': {0b111, 0b100, 0b110, 0b100, 0b100},
	'G': {0b111, 0b100, 0b101, 0b101, 0b111},
	'H': {0b101, 0b101, 0b111, 0b101, 0b101},
	'I': {0b111, 0b010, 0b010, 0b010, 0b111},
	'J': {0b001, 0b001, 0b001, 0b101, 0b010},
	'K': {0b101, 0b101, 0b110, 0b101, 0b101},
	'L': {0b100, 0b100, 0b100, 0b100, 0b111},
	'M': {0b101, 0b111, 0b111, 0b101, 0b101},
	'N': {0b101, 0b111, 0b111, 0b111, 0b101},
	'O': {0b111, 0b101, 0b101, 0b101, 0b111},
	'P': {0b111, 0b101, 0b111, 0b100, 0b100},
	'Q': {0b111, 0b101, 0b101, 0b111, 0b001},
	'R': {0b111, 0b101, 0b111, 0b110, 0b101},
	'S': {0b111, 0b100, 0b111, 0b001, 0b111},
	'T': {0b111, 0b010, 0b010, 0b010, 0b010},
	'U': {0b101, 0b101, 0b101, 0b101, 0b111},
	'V': {0b101, 0b101, 0b101, 0b101, 0b010},
	'W': {0b101, 0b101, 0b111, 0b111, 0b101},
	'X': {0b101, 0b101, 0b010, 0b101, 0b101},
	'Y': {0b101, 0b101, 0b010, 0b010, 0b010},
	'Z': {0b111, 0b001, 0b010, 0b100, 0b111},
	'0': {0b111, 0b101, 0b101, 0b101, 0b111},
	'1': {0b010, 0b110, 0b010, 0b010, 0b111},
	'2': {0b111, 0b001, 0b111, 0b100, 0b111},
	'3': {0b111, 0b001, 0b111, 0b001, 0b111},
	'4': {0b101, 0b101, 0b111, 0b001, 0b001},
	'5': {0b111, 0b100, 0b111, 0b001, 0b111},
	'6': {0b111, 0b100, 0b111, 0b101, 0b111},
	'7': {0b111, 0b001, 0b001, 0b001, 0b001},
	'8': {0b111, 0b101, 0b111, 0b101, 0b111},
	'9': {0b111, 0b101, 0b111, 0b001, 0b111},
	' ': {0b000, 0b000, 0b000, 0b000, 0b000},
	'-': {0b000, 0b000, 0b111, 0b000, 0b000},
	'_': {0b000, 0b000, 0b000, 0b000, 0b111},
	'/': {0b001, 0b001, 0b010, 0b100, 0b100},
	'.': {0b000, 0b000, 0b000, 0b000, 0b010},
	'(': {0b010, 0b100, 0b100, 0b100, 0b010},
	')': {0b010, 0b001, 0b001, 0b001, 0b010},
}

func fillBackground(img *image.RGBA, height int) {
	for y := range height {
		for x := range imgWidth {
			img.Set(x, y, colorBg)
		}
	}
}

func drawGrid(img *image.RGBA, plotH float64) {
	for i := range 6 {
		y := padTop + int(plotH*float64(i)/5)
		drawHLine(img, padLeft, imgWidth-padRight, y, colorGrid)
	}
	drawHLine(img, padLeft, imgWidth-padRight, padTop+int(plotH), colorAxis)
	drawVLine(img, padLeft, padTop, padTop+int(plotH), colorAxis)
}

func drawAxisLabels(img *image.RGBA, plotH, minV, maxV, maxElapsed float64, yAxisTitle string) {
	plotW := float64(imgWidth - padLeft - padRight)
	labelColor := color.RGBA{R: 100, G: 100, B: 100, A: 255}

	// Y-axis title (top-left, above the plot area)
	drawLabelSmall(img, 2, 2, yAxisTitle, labelColor)

	// Y-axis: 6 tick labels (matching grid lines)
	for i := range 6 {
		frac := float64(5-i) / 5
		val := minV + frac*(maxV-minV)
		y := padTop + int(plotH*float64(i)/5)
		label := formatAxisValue(val)
		drawLabelSmall(img, 2, y-2, label, labelColor)
	}

	// X-axis: ~5 tick labels
	for i := range 6 {
		frac := float64(i) / 5
		t := frac * maxElapsed
		x := padLeft + int(frac*plotW)
		label := fmt.Sprintf("%.0fs", t)
		drawLabelSmall(img, x-len(label)*2, padTop+int(plotH)+4, label, labelColor)
	}

	// X-axis title (bottom-right, below the tick labels)
	xTitle := "ELAPSED (S)"
	xTitleWidth := len(xTitle) * 4 // 3px glyph + 1px gap per char at 1x scale
	drawLabelSmall(img, imgWidth-padRight-xTitleWidth, padTop+int(plotH)+13, xTitle, labelColor)
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

// drawLabelSmall draws text at 1x scale (3x5 pixels) for axis labels.
func drawLabelSmall(img *image.RGBA, x, y int, text string, c color.RGBA) {
	cx := x
	for _, ch := range strings.ToUpper(text) {
		glyph, ok := tinyFont[ch]
		if !ok {
			cx += 4
			continue
		}
		for row, bits := range glyph {
			for col := range 3 {
				if bits>>(2-col)&1 == 1 {
					img.Set(cx+col, y+row, c)
				}
			}
		}
		cx += 4
	}
}

func drawSeriesLine(
	img *image.RGBA, ts testTimeSeries,
	scaleX, scaleY func(float64) int,
	lineColor color.RGBA,
) {
	for i := 1; i < len(ts.Times); i++ {
		x0, y0 := scaleX(ts.Times[i-1]), scaleY(ts.Values[i-1])
		x1, y1 := scaleX(ts.Times[i]), scaleY(ts.Values[i])
		drawLine(img, x0, y0, x1, y1, lineColor)
		drawLine(img, x0, y0-1, x1, y1-1, lineColor)
		drawLine(img, x0, y0+1, x1, y1+1, lineColor)
	}
}

func drawHLine(img *image.RGBA, x0, x1, y int, c color.RGBA) {
	for x := x0; x <= x1; x++ {
		img.Set(x, y, c)
	}
}

func drawVLine(img *image.RGBA, x, y0, y1 int, c color.RGBA) {
	for y := y0; y <= y1; y++ {
		img.Set(x, y, c)
	}
}

// drawLine draws a line using Bresenham's algorithm.
func drawLine(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA) {
	dx := abs(x1 - x0)
	dy := -abs(y1 - y0)
	sx, sy := 1, 1
	if x0 >= x1 {
		sx = -1
	}
	if y0 >= y1 {
		sy = -1
	}
	err := dx + dy
	for {
		img.Set(x0, y0, c)
		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

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
