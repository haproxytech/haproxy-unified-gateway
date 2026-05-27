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

func TestFindRoute_SingleSource(t *testing.T) {
	h := harness.New(t, "haproxy.cfg")

	cases := []struct {
		name, path, exact, wild, want string
	}{
		{"exact_path_wins", "/exact", "lr1", "", "be_exact_r1"},
		{"longest_prefix", "/api/v2/foo", "lr1", "", "be_r1_apiv2"},
		{"shorter_prefix", "/api/foo", "lr1", "", "be_r1_api"},
		{"regex_when_nothing_else_matches", "/123", "lr1", "", "be_r1_digits"},
		{"no_match_returns_no_route", "/nowhere", "lr1", "", ""},
		{"wildcard_source_only", "/special/x", "", "lr2", "be_r2_special"},
		{"wildcard_default_prefix", "/anything", "", "lr2", "be_r2_default"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, body := h.Get(t, tc.path, "X-LR-Exact", tc.exact, "X-LR-Wild", tc.wild)
			if got := parseRoute(body); got != tc.want {
				t.Errorf("path=%s exact=%q wild=%q\n got route=%q\nwant route=%q\nbody: %s", tc.path, tc.exact, tc.wild, got, tc.want, body)
			}
		})
	}
}

func TestFindRoute_DualSource(t *testing.T) {
	h := harness.New(t, "haproxy.cfg")

	cases := []struct {
		name, path, exact, wild, want, why string
	}{
		{
			name: "exact_path_tie_exact_host_wins", path: "/exact",
			exact: "lr1", wild: "lr2", want: "be_exact_r1",
			why: "both maps have /exact at SCORE_EXACT; exact-host tiebreaker → r1",
		},
		{
			name: "path_specificity_beats_host_rank", path: "/special/foo",
			exact: "lr1", wild: "lr2", want: "be_r2_special",
			why: "r1 has no matching prefix; r2 has /special/ → wins on path score even though wildcard",
		},
		{
			name: "prefix_tie_exact_host_wins", path: "/tie",
			exact: "lr1", wild: "lr2", want: "be_r1_tie",
			why: "both have /tie at equal length → host-specificity tiebreaker",
		},
		{
			name: "wildcard_with_longer_prefix_wins", path: "/api/v2/foo",
			exact: "lr1", wild: "lr2", want: "be_r1_apiv2",
			why: "r1's /api/v2/ beats r2's / on length",
		},
		{
			name: "fallback_to_wildcard_default", path: "/foo",
			exact: "lr1", wild: "lr2", want: "be_r2_default",
			why: "r1 has no matching prefix; only r2/ matches",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, body := h.Get(t, tc.path, "X-LR-Exact", tc.exact, "X-LR-Wild", tc.wild)
			if got := parseRoute(body); got != tc.want {
				t.Errorf("%s\n  path=%s exact=%q wild=%q\n  got route=%q\n  want route=%q (%s)\n  body: %s",
					tc.name, tc.path, tc.exact, tc.wild, got, tc.want, tc.why, body)
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
	h := harness.New(t, "haproxy.cfg")

	// Path matches only the 2nd candidate's prefix → we must iterate past the
	// 1st (no early-exit).
	_, body := h.Get(t, "/special/foo", "X-LR-Exact", "lr1,lr2")
	if got := parseRoute(body); got != "be_r2_special" {
		t.Errorf("expected be_r2_special (only 2nd candidate matches); got %q body=%s", got, body)
	}
	if !strings.Contains(body, "blr=lr1/special/foo,lr2/special/foo") {
		t.Errorf("expected blr to list both candidates; got: %s", body)
	}

	// And the 1st candidate is honoured when ITS prefix matches.
	_, body = h.Get(t, "/api/x", "X-LR-Exact", "lr1,lr2")
	if got := parseRoute(body); got != "be_r1_api" {
		t.Errorf("expected be_r1_api (1st candidate hit); got %q body=%s", got, body)
	}
}
