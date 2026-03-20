// Package main compares today's conformance JUnit results with the previous
// scheduled run and posts any differences to Slack.
package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

type junitTestSuites struct {
	XMLName xml.Name         `xml:"testsuites"`
	Suites  []junitTestSuite `xml:"testsuite"`
}

type junitTestSuite struct {
	TestCases []junitTestCase `xml:"testcase"`
}

type junitTestCase struct {
	Classname string        `xml:"classname,attr"`
	Name      string        `xml:"name,attr"`
	Failure   *junitFailure `xml:"failure"`
	Error     *junitError   `xml:"error"`
	Skipped   *junitSkipped `xml:"skipped"`
}

type junitFailure struct{}
type junitError struct{}
type junitSkipped struct{}

type pipeline struct {
	ID int `json:"id"`
}

type job struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func parseJUnit(data []byte) (map[string]string, error) {
	var suites junitTestSuites
	if err := xml.Unmarshal(data, &suites); err != nil {
		// Try single testsuite
		var suite junitTestSuite
		if err2 := xml.Unmarshal(data, &suite); err2 != nil {
			return nil, fmt.Errorf("parsing JUnit XML: %w", err)
		}
		suites.Suites = []junitTestSuite{suite}
	}

	results := make(map[string]string)
	for _, suite := range suites.Suites {
		for _, tc := range suite.TestCases {
			fullName := tc.Name
			if tc.Classname != "" {
				fullName = tc.Classname + " " + tc.Name
			}

			switch {
			case tc.Skipped != nil:
				results[fullName] = "SKIP"
			case tc.Failure != nil || tc.Error != nil:
				results[fullName] = "FAIL"
			default:
				results[fullName] = "PASS"
			}
		}
	}
	return results, nil
}

func gitlabAPI(path string) ([]byte, error) {
	base := os.Getenv("CI_API_V4_URL")
	token := os.Getenv("CI_JOB_TOKEN")
	url := base + path

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("JOB-TOKEN", token)

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

func findPreviousJob() (int, int, error) {
	projectID := os.Getenv("CI_PROJECT_ID")
	currentPipeline := os.Getenv("CI_PIPELINE_ID")
	ref := os.Getenv("CI_COMMIT_REF_NAME")

	data, err := gitlabAPI(fmt.Sprintf(
		"/projects/%s/pipelines?source=schedule&ref=%s&per_page=5&order_by=id&sort=desc",
		projectID, ref,
	))
	if err != nil {
		return 0, 0, err
	}

	var pipelines []pipeline
	if err := json.Unmarshal(data, &pipelines); err != nil {
		return 0, 0, err
	}

	for _, p := range pipelines {
		if fmt.Sprintf("%d", p.ID) == currentPipeline {
			continue
		}

		jobsData, err := gitlabAPI(fmt.Sprintf(
			"/projects/%s/pipelines/%d/jobs?per_page=100",
			projectID, p.ID,
		))
		if err != nil {
			continue
		}

		var jobs []job
		if err := json.Unmarshal(jobsData, &jobs); err != nil {
			continue
		}

		for _, j := range jobs {
			if j.Name == "conformance-GW-API-1_3_0" {
				return j.ID, p.ID, nil
			}
		}
	}
	return 0, 0, fmt.Errorf("no previous scheduled conformance job found")
}

func downloadJUnit(jobID int) ([]byte, error) {
	projectID := os.Getenv("CI_PROJECT_ID")
	junitFile := os.Getenv("JUNIT_FILE")
	if junitFile == "" {
		junitFile = "conformance-junit-1.3.0.xml"
	}

	data, err := gitlabAPI(fmt.Sprintf("/projects/%s/jobs/%d/artifacts", projectID, jobID))
	if err != nil {
		return nil, err
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}

	for _, f := range zr.File {
		if f.Name == junitFile {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("%s not found in artifacts", junitFile)
}

func shortenTestName(name string) string {
	parts := strings.Fields(name)
	testPath := parts[len(parts)-1]

	if strings.Contains(testPath, "/") {
		segments := strings.Split(testPath, "/")
		if strings.HasPrefix(segments[0], "TestConformance") {
			segments = segments[1:]
		}
		for i, s := range segments {
			segments[i] = strings.ReplaceAll(s, "_", " ")
		}
		return strings.Join(segments, " / ")
	}
	return strings.ReplaceAll(testPath, "_", " ")
}

func postToSlack(message string) error {
	webhookURL := os.Getenv("SLACK_CONFORMANCE_WEBHOOK")
	if webhookURL == "" {
		fmt.Println("SLACK_CONFORMANCE_WEBHOOK not set, skipping Slack notification")
		fmt.Println(message)
		return nil
	}

	payload, _ := json.Marshal(map[string]string{"text": message})
	resp, err := http.Post(webhookURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	fmt.Printf("Slack response: %d\n", resp.StatusCode)
	return nil
}

func main() {
	junitFile := os.Getenv("JUNIT_FILE")
	if junitFile == "" {
		junitFile = "conformance-junit-1.3.0.xml"
	}

	todayData, err := os.ReadFile(junitFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", junitFile, err)
		os.Exit(1)
	}

	today, err := parseJUnit(todayData)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing today's JUnit: %v\n", err)
		os.Exit(1)
	}

	prevJobID, prevPipelineID, err := findPreviousJob()
	if err != nil {
		fmt.Printf("No previous scheduled pipeline found, skipping comparison: %v\n", err)
		os.Exit(0)
	}

	fmt.Printf("Comparing with job %d from pipeline %d\n", prevJobID, prevPipelineID)

	prevData, err := downloadJUnit(prevJobID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error downloading previous JUnit: %v\n", err)
		os.Exit(1)
	}

	previous, err := parseJUnit(prevData)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing previous JUnit: %v\n", err)
		os.Exit(1)
	}

	type change struct {
		test       string
		prevStatus string
	}

	var newFailures []change
	var newPasses []string

	for test, status := range today {
		prevStatus, exists := previous[test]
		if status == "FAIL" && exists && prevStatus != "FAIL" {
			newFailures = append(newFailures, change{test, prevStatus})
		} else if status == "PASS" && prevStatus == "FAIL" {
			newPasses = append(newPasses, test)
		}
	}

	if len(newFailures) == 0 && len(newPasses) == 0 {
		fmt.Println("No conformance test status changes detected")
		os.Exit(0)
	}

	projectURL := os.Getenv("CI_PROJECT_URL")
	pipelineID := os.Getenv("CI_PIPELINE_ID")
	pipelineURL := fmt.Sprintf("%s/-/pipelines/%s", projectURL, pipelineID)

	var pass, fail, skip int
	for _, v := range today {
		switch v {
		case "PASS":
			pass++
		case "FAIL":
			fail++
		case "SKIP":
			skip++
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "*Conformance Test Report* (<%s|pipeline #%s>)\n", pipelineURL, pipelineID)
	fmt.Fprintf(&sb, "Results: %d passed, %d failed, %d skipped\n", pass, fail, skip)

	if len(newFailures) > 0 {
		fmt.Fprintf(&sb, "\n:red_circle: *%d New Failure(s):*\n", len(newFailures))
		for _, f := range newFailures {
			fmt.Fprintf(&sb, "  • %s (was %s)\n", shortenTestName(f.test), f.prevStatus)
		}
	}

	if len(newPasses) > 0 {
		fmt.Fprintf(&sb, "\n:large_green_circle: *%d New Pass(es):*\n", len(newPasses))
		for _, t := range newPasses {
			fmt.Fprintf(&sb, "  • %s\n", shortenTestName(t))
		}
	}

	message := sb.String()
	fmt.Println(message)

	if err := postToSlack(message); err != nil {
		fmt.Fprintf(os.Stderr, "Error posting to Slack: %v\n", err)
		os.Exit(1)
	}
}
