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
	"testing"

	"github.com/stretchr/testify/require"
)

// The root test aggregates its subtests: `go test` marks it failed as soon as one of them is, so
// counting it shows a single real failure as two, and a clean run as one extra pass. It is in the
// JUnit file -- `tests="141"` in the artifact that first carried one counts it -- and it is not a
// test of its own.
func TestSummarizeDropsTheAggregateRootTest(t *testing.T) {
	const junit = `<testsuites>
  <testsuite name="github.com/haproxytech/haproxy-unified-gateway/test/conformance">
    <testcase classname="pkg" name="TestConformance"/>
    <testcase classname="pkg" name="TestConformance/HTTPRouteSimpleSameNamespace"/>
    <testcase classname="pkg" name="TestConformance/HTTPRouteWeight"><failure/></testcase>
    <testcase classname="pkg" name="TestConformance/ListenerSetHTTPRouting"><skipped/></testcase>
  </testsuite>
</testsuites>`

	passed, skipped, failed, err := summarize([]byte(junit))

	require.NoError(t, err)
	require.Equal(t, []string{"HTTPRouteSimpleSameNamespace"}, passed)
	require.Equal(t, []string{"ListenerSetHTTPRouting"}, skipped)
	require.Equal(t, []string{"HTTPRouteWeight"}, failed)
}
