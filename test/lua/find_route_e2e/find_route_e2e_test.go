// Copyright 2026 HAProxy Technologies LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package find_route_e2e tests the full routing pipeline driven entirely by
// map fixtures — no listener-route values are injected via HTTP headers.
// The Host header drives listener selection (listener_exact_match.map /
// listener_wildcard_match.map), HAProxy then resolves txn.selected_listener_route
// via concat+map, and lua.find_route scores candidates against the path maps.
//
// Each sub-scenario lives in its own directory with its own map fixtures and
// a testcases/cases.map file:
//
//	1_listener_1_route         — one no-hostname listener, one route, all path types
//	1_listener_N_routes        — one listener, multiple routes for same host (path picks winner)
//	exact_listener_only        — multiple exact listeners, no wildcard fallback
//	exact_vs_wildcard_listener — exact listener takes priority over wildcard (ifnotexists)
//	wildcard_route             — route with wildcard hostname (*.wild.org) via listener_route_wildcard_match.map
package find_route_e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/haproxytech/haproxy-unified-gateway/test/lua/internal/harness"
)

var scenarios = []string{
	"1_listener_1_route",
	"1_listener_N_routes",
	"exact_listener_only",
	"exact_vs_wildcard_listener",
	"wildcard_route",
}

func parseRoute(body string) string {
	for part := range strings.FieldsSeq(body) {
		if after, ok := strings.CutPrefix(part, "route="); ok {
			return after
		}
	}
	return ""
}

func TestFindRoute_E2E(t *testing.T) {
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			h := harness.New(t, filepath.Join(scenario, "haproxy.cfg"))
			runCases(t, h, filepath.Join(scenario, "testcases", "cases.map"))
		})
	}
}

// runCases reads a 4-column testcases file (name host path want_route) and
// runs one sub-test per line. "-" in want_route means no route expected.
func runCases(t *testing.T, h *harness.Harness, casesFile string) {
	t.Helper()
	raw, err := os.ReadFile(casesFile)
	if err != nil {
		t.Fatalf("read %s: %v", casesFile, err)
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 4 {
			t.Fatalf("malformed line in %s (want 4 fields): %q", casesFile, line)
		}
		name, host, path, wantRoute := f[0], f[1], f[2], f[3]
		if wantRoute == "-" {
			wantRoute = ""
		}
		t.Run(name, func(t *testing.T) {
			_, body := h.Get(t, path, "Host", host)
			if got := parseRoute(body); got != wantRoute {
				t.Errorf("host=%q path=%q\n got  route=%q\n want route=%q\n body: %s",
					host, path, got, wantRoute, body)
			}
		})
	}
}
