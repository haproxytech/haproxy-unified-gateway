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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFindPreviousJobLooksInsideTheChildPipeline is the lookup that decides
// which run to compare with: the report job lives in the child pipeline a
// scheduled run triggers, not in the scheduled pipeline itself.
func TestFindPreviousJobLooksInsideTheChildPipeline(t *testing.T) {
	bodies := map[string]string{
		"/projects/7/pipelines":             `[{"id": 200}, {"id": 100}]`, // 200 is the run to skip
		"/projects/7/pipelines/100/bridges": `[{"downstream_pipeline": {"id": 101}}]`,
		"/projects/7/pipelines/101/jobs":    `[{"id": 555, "name": "conformance-report"}, {"id": 556, "name": "conformance-GW-API-1_7_0:1/6"}]`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := bodies[r.URL.Path]
		if !ok {
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	t.Setenv("CI_API_V4_URL", server.URL)
	t.Setenv("CI_PROJECT_ID", "7")
	t.Setenv("CI_COMMIT_REF_NAME", "dev")
	t.Setenv("PARENT_PIPELINE_ID", "200")

	jobID, pipelineID, err := findPreviousJob()
	require.NoError(t, err)
	assert.Equal(t, 555, jobID)
	assert.Equal(t, 100, pipelineID)
}

func TestFindPreviousJobReportsWhenThereIsNoHistory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := "[]"
		if r.URL.Path == "/projects/7/pipelines" {
			body = `[{"id": 200}]`
		}
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	t.Setenv("CI_API_V4_URL", server.URL)
	t.Setenv("CI_PROJECT_ID", "7")
	t.Setenv("CI_COMMIT_REF_NAME", "dev")
	t.Setenv("PARENT_PIPELINE_ID", "200")

	_, _, err := findPreviousJob()
	assert.Error(t, err)
}
