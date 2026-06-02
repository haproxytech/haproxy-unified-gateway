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

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	docs "github.com/haproxytech/haproxy-unified-gateway/documentation"
)

//revive:disable:deep-exit

type Note struct {
	CreatedAt time.Time `json:"created_at"`
	Body      string    `json:"body"`
	ID        int       `json:"id"`
}

type GitlabLabel struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Color       string `json:"color"`
	ID          int    `json:"id"`
}

type MergeRequest struct {
	Description string `json:"description"`
}

var baseURL string

//revive:disable-next-line:var-naming
const LABEL_COLOR = "#8fbc8f"

// autoBackportMarker records that AUTO_BACKPORT labels were applied once, so
// reruns won't re-add labels the user has since removed manually.
const autoBackportMarker = `<!-- MR AUTO BACKPORT -->`

//revive:disable-next-line:function-length
func main() {
	fmt.Print(hello) //nolint:forbidigo

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		AddSource: true,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == "source" {
				x := a.Value
				//revive:disable-next-line:unchecked-type-assertion
				src := x.Any().(*slog.Source)
				path := strings.Split(src.File, "/")
				src.File = path[len(path)-1]
				return slog.Attr{
					Key:   "source",
					Value: slog.AnyValue(src),
				}
			}
			return a
		},
	}))
	slog.SetDefault(logger)

	baseURL = os.Getenv("CI_API_V4_URL")
	if baseURL == "" {
		slog.Error("CI_API_V4_URL not set")
		os.Exit(1)
	}

	docsData, err := docs.GetLifecycle()
	if err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}

	token := os.Getenv("GITLAB_TOKEN")

	//revive:disable-next-line:var-naming,unexported-naming
	CI_MERGE_REQUEST_IID_STR := os.Getenv("CI_MERGE_REQUEST_IID")
	if CI_MERGE_REQUEST_IID_STR == "" {
		slog.Error("CI_MERGE_REQUEST_IID not set")
		os.Exit(1)
	}
	//revive:disable-next-line:var-naming,unexported-naming
	CI_MERGE_REQUEST_IID, err := strconv.Atoi(CI_MERGE_REQUEST_IID_STR)
	if err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}

	//revive:disable-next-line:var-naming,unexported-naming
	CI_PROJECT_ID := os.Getenv("CI_PROJECT_ID")
	if CI_PROJECT_ID == "" {
		slog.Error("CI_PROJECT_ID not set")
		os.Exit(1)
	}

	var table strings.Builder
	table.WriteString("| Version | EOL | label |\n|:--:|:---|:--:|")
	backportLabels := map[string]struct{}{}
	for _, version := range docsData.Versions {
		if !version.Maintained {
			continue
		}
		table.WriteString("\n" + "| " + version.Version + " | ~ " + version.EOLHuman + " | " + "backport-" + version.Version + " |")
		backportLabels["backport-"+version.Version] = struct{}{}
	}
	question := `<!-- MR BACKPORT QUESTION -->` + "\n" + "Does this need a backport? \n" + table.String() + "\n\n" + "please add labels for backporting."

	// AUTO_BACKPORT short-circuits the question: apply its labels to the MR directly.
	if autoLabels := parseLabels(os.Getenv("AUTO_BACKPORT")); len(autoLabels) > 0 {
		mr, err := getMergeRequest(baseURL, token, CI_PROJECT_ID, CI_MERGE_REQUEST_IID)
		if err != nil {
			slog.Error(err.Error())
			os.Exit(1)
		}
		// Marker in the description means we already ran once: respect labels the
		// user may have removed since.
		if strings.Contains(mr.Description, autoBackportMarker) {
			slog.Info("AUTO_BACKPORT already applied, skipping to preserve manual label changes")
			os.Exit(0)
		}
		labelSet := make(map[string]struct{}, len(autoLabels))
		for _, l := range autoLabels {
			labelSet[l] = struct{}{}
		}
		// Errors are non-fatal: labels may already exist or be set on the MR.
		if err = getProjectlabels(labelSet, CI_PROJECT_ID); err != nil {
			slog.Warn("ensuring project labels exist failed", "error", err.Error())
		}
		// Add the labels and stamp the marker into the description in one update.
		if err = updateMergeRequest(baseURL, token, CI_PROJECT_ID, CI_MERGE_REQUEST_IID, autoLabels, mr.Description+"\n\n"+autoBackportMarker); err != nil {
			slog.Warn("applying AUTO_BACKPORT failed", "error", err.Error())
		}
		// Record that labels were applied automatically and resolve the thread,
		// since there is nothing for a human to answer.
		note := autoBackportMarker + "\n" +
			"Backport labels applied automatically via `AUTO_BACKPORT`: " + strings.Join(autoLabels, ", ") + "\n\n" +
			table.String() + "\n\n" +
			"You can still add or remove any label manually."
		discussionID, err := startThreadOnMergeRequest(baseURL, token, CI_PROJECT_ID, CI_MERGE_REQUEST_IID, note)
		if err != nil {
			slog.Warn("creating backport note failed", "error", err.Error())
		} else if err = resolveDiscussion(baseURL, token, CI_PROJECT_ID, CI_MERGE_REQUEST_IID, discussionID); err != nil {
			slog.Warn("resolving backport note failed", "error", err.Error())
		}
		slog.Info("AUTO_BACKPORT set, applied labels", "labels", strings.Join(autoLabels, ","))
		os.Exit(0)
	}

	mr, err := getMergeRequest(baseURL, token, CI_PROJECT_ID, CI_MERGE_REQUEST_IID)
	if err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}

	if strings.Contains(mr.Description, "<!-- BOT DEPENDABOT -->") {
		slog.Info("Dependabot MR detected, skipping backport check.")
		os.Exit(0)
	}

	notes, err := getMergeRequestComments(baseURL, token, CI_PROJECT_ID, CI_MERGE_REQUEST_IID)
	if err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
	index := slices.IndexFunc(notes, func(note Note) bool {
		return strings.Contains(note.Body, "<!-- MR BACKPORT QUESTION -->")
	})
	if index == -1 {
		// Ensure the backport labels exist before asking.
		err = getProjectlabels(backportLabels, CI_PROJECT_ID)
		if err != nil {
			slog.Error(err.Error())
			os.Exit(1)
		}
		slog.Info("No backport question found, creating one as thread")
		if _, err = startThreadOnMergeRequest(baseURL, token, CI_PROJECT_ID, CI_MERGE_REQUEST_IID, question); err != nil {
			slog.Error(err.Error())
			os.Exit(1)
		}
	}
}

// parseLabels splits a comma-delimited label list, trimming blanks.
func parseLabels(raw string) []string {
	var labels []string
	for part := range strings.SplitSeq(raw, ",") {
		if label := strings.TrimSpace(part); label != "" {
			labels = append(labels, label)
		}
	}
	return labels
}

// updateMergeRequest adds labels (without removing existing ones) and sets the
// description in a single request.
func updateMergeRequest(baseURL, token, projectID string, mergeRequestIID int, labels []string, description string) error {
	client := &http.Client{}
	payload := map[string]string{
		"add_labels":  strings.Join(labels, ","),
		"description": description,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut,
		fmt.Sprintf("%s/projects/%s/merge_requests/%d", baseURL, url.PathEscape(projectID), mergeRequestIID), bytes.NewBuffer(payloadBytes))
	if err != nil {
		return err
	}
	req.Header.Add("PRIVATE-TOKEN", token) //nolint:canonicalheader
	req.Header.Add("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to update merge request: status %s, body: %s", resp.Status, string(body))
	}
	return nil
}

// startThreadOnMergeRequest opens a discussion and returns its ID.
func startThreadOnMergeRequest(baseURL, token, projectID string, mergeRequestIID int, threadBody string) (string, error) {
	client := &http.Client{}
	threadData := map[string]any{
		"body": threadBody,
	}
	threadDataBytes, err := json.Marshal(threadData)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		fmt.Sprintf("%s/projects/%s/merge_requests/%d/discussions", baseURL, url.PathEscape(projectID), mergeRequestIID), bytes.NewBuffer(threadDataBytes))
	if err != nil {
		return "", err
	}
	req.Header.Add("PRIVATE-TOKEN", token) //nolint:canonicalheader
	req.Header.Add("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("failed to create discussion: status %s, body: %s", resp.Status, string(body))
	}

	var discussion struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal(body, &discussion); err != nil {
		return "", err
	}
	return discussion.ID, nil
}

// resolveDiscussion marks a merge request discussion as resolved.
func resolveDiscussion(baseURL, token, projectID string, mergeRequestIID int, discussionID string) error {
	client := &http.Client{}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut,
		fmt.Sprintf("%s/projects/%s/merge_requests/%d/discussions/%s?resolved=true", baseURL, url.PathEscape(projectID), mergeRequestIID, url.PathEscape(discussionID)), nil)
	if err != nil {
		return err
	}
	req.Header.Add("PRIVATE-TOKEN", token) //nolint:canonicalheader

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to resolve discussion: status %s, body: %s", resp.Status, string(body))
	}
	return nil
}

func getMergeRequest(baseURL, token, projectID string, mergeRequestIID int) (*MergeRequest, error) {
	client := &http.Client{}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		fmt.Sprintf("%s/projects/%s/merge_requests/%d", baseURL, url.PathEscape(projectID), mergeRequestIID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("PRIVATE-TOKEN", token) //nolint:canonicalheader

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get merge request: status %s, body: %s", resp.Status, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var mr MergeRequest
	err = json.Unmarshal(body, &mr)
	if err != nil {
		return nil, err
	}

	return &mr, nil
}

func getMergeRequestComments(baseURL, token, projectID string, mergeRequestIID int) ([]Note, error) {
	client := &http.Client{}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		fmt.Sprintf("%s/projects/%s/merge_requests/%d/notes", baseURL, url.PathEscape(projectID), mergeRequestIID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("PRIVATE-TOKEN", token) //nolint:canonicalheader

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var notes []Note
	err = json.Unmarshal(body, &notes)
	if err != nil {
		return nil, err
	}

	return notes, nil
}

func getProjectlabels(backportLabels map[string]struct{}, projectID string) error {
	client := &http.Client{}
	token := os.Getenv("GITLAB_TOKEN")
	if token == "" {
		return errors.New("GITLAB_TOKEN not set")
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		fmt.Sprintf("%s/projects/%s/labels", baseURL, url.PathEscape(projectID)), nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Add("PRIVATE-TOKEN", token) //nolint:canonicalheader
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to get project labels: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to get project labels: status %s, body: %s", resp.Status, string(body))
	}

	var projectLabels []GitlabLabel
	err = json.Unmarshal(body, &projectLabels)
	if err != nil {
		return fmt.Errorf("failed to unmarshal response body (status %s): %w. Body: %s", resp.Status, err, string(body))
	}

	for _, label := range projectLabels {
		_, ok := backportLabels[label.Name]
		if ok {
			delete(backportLabels, label.Name)
		}
	}
	for label := range backportLabels {
		labelData := map[string]string{
			"name":        label,
			"color":       LABEL_COLOR,
			"description": "Label for backporting to " + label + " branch",
		}
		labelDataBytes, err := json.Marshal(labelData)
		if err != nil {
			return fmt.Errorf("failed to marshal label data: %w", err)
		}
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
			fmt.Sprintf("%s/projects/%s/labels", baseURL, url.PathEscape(projectID)), bytes.NewBuffer(labelDataBytes))
		if err != nil {
			return fmt.Errorf("failed to create request to create label: %w", err)
		}
		req.Header.Add("PRIVATE-TOKEN", token) //nolint:canonicalheader
		req.Header.Add("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("failed to create label %s: %w", label, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			return fmt.Errorf("failed to create label %s, status code: %d", label, resp.StatusCode)
		}
	}

	return nil
}

const hello = `
 __  __ ____         _               _
|  \/  |  _ \    ___| |__   ___  ___| | _____ _ __
| |\/| | |_) |  / __| '_ \ / _ \/ __| |/ / _ \ '__|
| |  | |  _ <  | (__| | | |  __/ (__|   <  __/ |
|_|  |_|_| \_\  \___|_| |_|\___|\___|_|\_\___|_|

`
