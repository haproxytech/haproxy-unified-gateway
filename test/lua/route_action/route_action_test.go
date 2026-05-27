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

package route_action_test

import (
	"strings"
	"testing"

	"github.com/haproxytech/haproxy-unified-gateway/test/lua/internal/harness"
)

// The route() action in route.lua dispatches on the algorithm encoded in the
// JSON in txn.route:
//   wr  weighted random  → picks via cumulative weight + txn.f:rand()
//   fo  failover         → picks first backend with srv_act >= min
// These tests exercise both, plus the default-algorithm fallback.

func parseBackend(body string) string {
	for p := range strings.FieldsSeq(body) {
		if after, ok := strings.CutPrefix(p, "backend="); ok {
			return after
		}
	}
	return ""
}

func TestRoute_WeightedRandom_AllWeightOnOne(t *testing.T) {
	// 100/0 split: random_val is in [0, total_weight) and only the first
	// cumulative segment is reachable, so be_only must always win.
	h := harness.New(t, "haproxy.cfg")
	spec := `{"a":"wr","l":"be_only:100,be_never:0"}`
	for i := range 20 {
		_, body := h.Get(t, "/", "X-Route", spec)
		if got := parseBackend(body); got != "be_only" {
			t.Fatalf("iteration %d: got backend=%q want be_only (body=%s)", i, got, body)
		}
	}
}

func TestRoute_WeightedRandom_Distribution(t *testing.T) {
	// 50/50 split: across many requests both backends should appear at least
	// once. We don't assert the exact distribution (random).
	h := harness.New(t, "haproxy.cfg")
	spec := `{"a":"wr","l":"be_a:50,be_b:50"}`
	seen := map[string]int{}
	for range 100 {
		_, body := h.Get(t, "/", "X-Route", spec)
		seen[parseBackend(body)]++
	}
	if seen["be_a"] == 0 || seen["be_b"] == 0 {
		t.Errorf("expected both backends to appear; got %v", seen)
	}
	for k := range seen {
		if k != "be_a" && k != "be_b" {
			t.Errorf("unexpected backend %q in distribution %v", k, seen)
		}
	}
}

func TestRoute_Failover_SkipsEmptyBackend(t *testing.T) {
	// be_empty has 0 active servers → fo skips it and falls through.
	h := harness.New(t, "haproxy.cfg")
	spec := `{"a":"fo","l":"be_empty:1,be_with_server:1"}`
	_, body := h.Get(t, "/", "X-Route", spec)
	if got := parseBackend(body); got != "be_with_server" {
		t.Errorf("expected fo to skip be_empty; got %q body=%s", got, body)
	}
}

func TestRoute_Failover_FirstMatchWins(t *testing.T) {
	// Both backends qualify → fo picks the FIRST listed.
	h := harness.New(t, "haproxy.cfg")
	spec := `{"a":"fo","l":"be_first:1,be_second:1"}`
	_, body := h.Get(t, "/", "X-Route", spec)
	if got := parseBackend(body); got != "be_first" {
		t.Errorf("expected fo to pick first; got %q body=%s", got, body)
	}
}

func TestRoute_Failover_AllUnavailable(t *testing.T) {
	// Neither backend has servers → fo sets nothing.
	h := harness.New(t, "haproxy.cfg")
	spec := `{"a":"fo","l":"be_a:1,be_b:1"}`
	_, body := h.Get(t, "/", "X-Route", spec)
	if got := parseBackend(body); got != "" {
		t.Errorf("expected no backend set; got %q body=%s", got, body)
	}
}

func TestRoute_DefaultAlgorithm_IsWR(t *testing.T) {
	// Missing "a" → algorithms.default ("wr").
	h := harness.New(t, "haproxy.cfg")
	spec := `{"l":"be_default:1"}`
	_, body := h.Get(t, "/", "X-Route", spec)
	if got := parseBackend(body); got != "be_default" {
		t.Errorf("expected default algorithm to pick be_default; got %q body=%s", got, body)
	}
}
