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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const wrapped = `<?xml version="1.0" encoding="UTF-8"?>
<testsuites tests="2">
  <testsuite name="TestConformance" tests="2">
    <testcase classname="" name="TestConformance/HTTPRouteMatching" time="1.0"></testcase>
    <testcase classname="" name="TestConformance/TLSRouteSimpleSameNamespace" time="2.0"></testcase>
  </testsuite>
</testsuites>`

const bare = `<?xml version="1.0" encoding="UTF-8"?>
<testsuite name="TestConformance" tests="1">
  <testcase classname="" name="TestConformance/GatewayModifyListeners" time="3.0">
    <failure message="boom">details kept verbatim</failure>
  </testcase>
</testsuite>`

func writeShard(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// TestMergeKeepsEveryShardsTests is the property the report job relies on: the
// merged file must contain each shard's cases, failure text included.
func TestMergeKeepsEveryShardsTests(t *testing.T) {
	dir := t.TempDir()
	first := writeShard(t, dir, "shard-1.xml", wrapped)
	second := writeShard(t, dir, "shard-2.xml", bare)

	suites, err := merge([]string{first, second})
	require.NoError(t, err)
	require.Len(t, suites, 2)

	content, err := render(suites)
	require.NoError(t, err)
	merged := string(content)

	assert.Equal(t, 1, strings.Count(merged, "<testsuites>"))
	assert.Equal(t, 2, strings.Count(merged, "<testsuite "))
	for _, name := range []string{
		"TestConformance/HTTPRouteMatching",
		"TestConformance/TLSRouteSimpleSameNamespace",
		"TestConformance/GatewayModifyListeners",
	} {
		assert.Equal(t, 1, strings.Count(merged, `name="`+name+`"`), "case %s must appear once", name)
	}
	assert.Contains(t, merged, "details kept verbatim")
	assert.Contains(t, merged, `time="2.0"`, "suite attributes are preserved")
}

func TestMergeRejectsAnEmptyInput(t *testing.T) {
	_, err := merge(nil)
	require.NoError(t, err)
	content, err := render(nil)
	require.NoError(t, err)
	assert.Contains(t, string(content), "<testsuites>")
}

func TestReadSuitesReportsABrokenFile(t *testing.T) {
	dir := t.TempDir()
	_, err := readSuites(writeShard(t, dir, "broken.xml", "<testsuites>"))
	assert.Error(t, err)
}
