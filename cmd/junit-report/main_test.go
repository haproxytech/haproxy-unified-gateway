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

// A test that contains others aggregates them: `go test` marks it failed as soon as one of its
// subtests is, so counting it shows a single real failure twice -- and at every depth, not only at
// the root. Such a test is in the JUnit file and is not a test of its own.
func TestSummarizeDropsAggregateTests(t *testing.T) {
	const junit = `<testsuites>
  <testsuite name="github.com/haproxytech/haproxy-unified-gateway/test/conformance">
    <testcase classname="pkg" name="TestConformance"/>
    <testcase classname="pkg" name="TestConformance/HTTPRouteSimpleSameNamespace"/>
    <testcase classname="pkg" name="TestConformance/HTTPRouteWeight"><failure/></testcase>
    <testcase classname="pkg" name="TestConformance/ListenerSetHTTPRouting"><skipped/></testcase>
    <testcase classname="pkg" name="TestConformance/TLSRouteMixedTerminationNotSupported"><failure/></testcase>
    <testcase classname="pkg" name="TestConformance/TLSRouteMixedTerminationNotSupported/one_of_its_own"><failure/></testcase>
  </testsuite>
</testsuites>`

	passed, skipped, failed, err := summarize([]byte(junit))

	require.NoError(t, err)
	require.Equal(t, []string{"HTTPRouteSimpleSameNamespace"}, passed)
	require.Equal(t, []string{"ListenerSetHTTPRouting"}, skipped)
	require.Equal(t,
		[]string{
			"HTTPRouteWeight",
			"TLSRouteMixedTerminationNotSupported/one_of_its_own",
		},
		failed,
		"the failing subtest is reported once, and the parent that aggregates it not at all")
}

// A test with no subtest is a test, including the root: a run that fails before creating any
// subtest leaves it alone in the file, and that is a failure the reader has to see.
func TestSummarizeKeepsARootFailureWithoutSubtests(t *testing.T) {
	const junit = `<testsuites>
  <testsuite name="github.com/haproxytech/haproxy-unified-gateway/test/conformance">
    <testcase classname="pkg" name="TestConformance"><failure/></testcase>
  </testsuite>
</testsuites>`

	_, _, failed, err := summarize([]byte(junit))

	require.NoError(t, err)
	require.Equal(t, []string{"TestConformance"}, failed)
}
