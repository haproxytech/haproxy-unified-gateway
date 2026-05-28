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

package find_route_test

import (
	"os"
	"strings"
	"testing"

	"github.com/haproxytech/haproxy-unified-gateway/test/lua/internal/harness"
)

// parseRoute pulls route= out of the response body produced by the lf-string
// "route=%[var(txn.route)] blr=...".
func parseRoute(body string) string {
	for part := range strings.FieldsSeq(body) {
		if after, ok := strings.CutPrefix(part, "route="); ok {
			return after
		}
	}
	return ""
}

// loadCases reads a 5-column space-separated testcase file.
// Columns: name  path  lr_exact  lr_wild  want
// "-" in any column means empty string.
func loadCases(t *testing.T, filename string) []struct{ name, path, exact, wild, want string } {
	t.Helper()
	raw, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("read %s: %v", filename, err)
	}
	var cases []struct{ name, path, exact, wild, want string }
	for line := range strings.SplitSeq(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 5 {
			t.Fatalf("malformed line in %s (want 5 fields): %q", filename, line)
		}
		denil := func(s string) string {
			if s == "-" {
				return ""
			}
			return s
		}
		cases = append(cases, struct{ name, path, exact, wild, want string }{
			f[0], f[1], denil(f[2]), denil(f[3]), denil(f[4]),
		})
	}
	return cases
}

func TestFindRoute_SingleSource(t *testing.T) {
	h := harness.New(t, "haproxy.cfg")
	for _, tc := range loadCases(t, "testcases/single_source.map") {
		t.Run(tc.name, func(t *testing.T) {
			_, body := h.Get(t, tc.path, "X-LR-Exact", tc.exact, "X-LR-Wild", tc.wild)
			if got := parseRoute(body); got != tc.want {
				t.Errorf("path=%s exact=%q wild=%q\n got route=%q\nwant route=%q\nbody: %s",
					tc.path, tc.exact, tc.wild, got, tc.want, body)
			}
		})
	}
}

func TestFindRoute_DualSource(t *testing.T) {
	h := harness.New(t, "haproxy.cfg")
	for _, tc := range loadCases(t, "testcases/dual_source.map") {
		t.Run(tc.name, func(t *testing.T) {
			_, body := h.Get(t, tc.path, "X-LR-Exact", tc.exact, "X-LR-Wild", tc.wild)
			if got := parseRoute(body); got != tc.want {
				t.Errorf("%s\n  path=%s exact=%q wild=%q\n  got route=%q\n  want route=%q\n  body: %s",
					tc.name, tc.path, tc.exact, tc.wild, got, tc.want, body)
			}
		})
	}
}

func TestFindRoute_BLRSetEvenOnMiss(t *testing.T) {
	// txn.base_listener_route is written even with no path match, so logs can
	// show what was attempted.
	h := harness.New(t, "haproxy.cfg")
	_, body := h.Get(t, "/totally-unknown", "X-LR-Exact", "lr1")
	if !strings.Contains(body, "blr=lr1/totally-unknown") {
		t.Errorf("expected blr to record the attempt; got: %s", body)
	}
	if got := parseRoute(body); got != "" {
		t.Errorf("expected no route; got %q", got)
	}
}

func TestFindRoute_NoInputs(t *testing.T) {
	// With neither listener-route var set, find_route returns early.
	h := harness.New(t, "haproxy.cfg")
	_, body := h.Get(t, "/anything")
	if got := parseRoute(body); got != "" {
		t.Errorf("expected no route; got %q", got)
	}
}

func TestFindRoute_RuntimeSocketUpdate(t *testing.T) {
	// `set map` over the runtime socket changes routing on the NEXT request,
	// proving find_route reads the live in-memory pat_ref (not the disk file).
	h := harness.New(t, "haproxy.cfg")

	_, body := h.Get(t, "/exact", "X-LR-Exact", "lr1")
	if got := parseRoute(body); got != "be_exact_r1" {
		t.Fatalf("baseline wrong: route=%q body=%s", got, body)
	}

	out := h.Socket(t, "set map "+h.Dir+"/maps/fe/path_exact.map lr1/exact be_runtime_overridden")
	if strings.Contains(out, "error") || strings.Contains(out, "Unknown") {
		t.Fatalf("set map failed: %s", out)
	}

	_, body = h.Get(t, "/exact", "X-LR-Exact", "lr1")
	if got := parseRoute(body); got != "be_runtime_overridden" {
		t.Errorf("after set map: got route=%q want be_runtime_overridden\nbody: %s", got, body)
	}

	out = h.Socket(t, "add map "+h.Dir+"/maps/fe/path_prefix.map lr1/runtime/ be_runtime_added")
	if strings.Contains(out, "error") {
		t.Fatalf("add map failed: %s", out)
	}
	_, body = h.Get(t, "/runtime/anything", "X-LR-Exact", "lr1")
	if got := parseRoute(body); got != "be_runtime_added" {
		t.Errorf("after add map: got route=%q want be_runtime_added\nbody: %s", got, body)
	}
}

func TestFindRoute_MultiCandidate(t *testing.T) {
	// A single listener-route variable can carry a comma-separated list when
	// multiple HTTPRoutes attach to the same listener+host.
	// Cases are loaded from testcases/multi_candidate.map:
	//   path  lr_exact_csv  want_route  want_blr
	h := harness.New(t, "haproxy.cfg")

	raw, err := os.ReadFile("testcases/multi_candidate.map")
	if err != nil {
		t.Fatalf("read multi_candidate.map: %v", err)
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 4 {
			t.Fatalf("malformed multi_candidate.map line (want 4 fields): %q", line)
		}
		path, lrCsv, wantRoute, wantBLR := f[0], f[1], f[2], f[3]
		t.Run(path, func(t *testing.T) {
			_, body := h.Get(t, path, "X-LR-Exact", lrCsv)
			if got := parseRoute(body); got != wantRoute {
				t.Errorf("lr=%q: got route=%q want %q; body: %s", lrCsv, got, wantRoute, body)
			}
			if !strings.Contains(body, "blr="+wantBLR) {
				t.Errorf("lr=%q: expected blr=%s; body: %s", lrCsv, wantBLR, body)
			}
		})
	}
}
