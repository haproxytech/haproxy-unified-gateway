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

// Package select_route_test exercises lua.select_route against the production
// fast-path rule chain: native map lookups fill txn.route with a conditional
// "~1;..." value, which select_route resolves against the request (headers,
// method, query params), falling through to less specific entries and ending
// with no route (404) when nothing matches.
//
// The candidate ordering in maps/fe/path_prefix.map mirrors what the
// controller emits: method first, then header count, then query count, then
// rule order. The catch-all entry has no unconditional candidate, so requests
// whose headers match nothing 404, like the conformance header-matching test.
//
// Sub-test names must avoid parentheses and commas: they end up in the
// per-test tempdir path, and HAProxy config parsing cannot handle those
// characters inside a map() argument.
package select_route_test

import (
	"slices"
	"testing"

	"github.com/haproxytech/haproxy-unified-gateway/test/lua/internal/harness"
)

type tc struct {
	name       string
	method     string
	path       string
	hdrs       []string // alternating name/value
	wantStatus int
	wantBody   string // exact body, or one of wantAny
	wantAny    []string
}

func TestSelectRoute(t *testing.T) {
	cases := []tc{
		// Header matching.
		{name: "exact header", path: "/", hdrs: []string{"Version", "one"}, wantBody: "be_one"},
		{name: "header names case-insensitive", path: "/", hdrs: []string{"version", "one"}, wantBody: "be_one"},
		{name: "AND of two headers beats one", path: "/", hdrs: []string{"Version", "two", "Color", "orange"}, wantBody: "be_orange"},
		{name: "rule order breaks count ties", path: "/", hdrs: []string{"Version", "two", "Color", "blue"}, wantBody: "be_two"},
		{name: "OR across matches blue", path: "/", hdrs: []string{"Color", "blue"}, wantBody: "be_blue"},
		{name: "OR across matches green", path: "/", hdrs: []string{"Color", "green"}, wantBody: "be_green"},
		{name: "percent-encoded value", path: "/", hdrs: []string{"Color", "orange extra"}, wantBody: "be_spaced"},
		{name: "repeated header matches any occurrence", path: "/", hdrs: []string{"Version", "two", "Version", "one"}, wantBody: "be_one"},
		{name: "no headers 404s", path: "/", wantStatus: 404, wantBody: "NOT FOUND"},
		{name: "irrelevant header 404s", path: "/", hdrs: []string{"Some-Other", "one"}, wantStatus: 404, wantBody: "NOT FOUND"},
		{name: "no passing candidate 404s", path: "/", hdrs: []string{"Color", "orange"}, wantStatus: 404, wantBody: "NOT FOUND"},

		// Regex matching: unanchored search (users anchor with ^...$).
		{name: "header regex matches", path: "/", hdrs: []string{"Version", "v42"}, wantBody: "be_vregex"},
		{name: "header regex non-match 404s", path: "/", hdrs: []string{"Version", "v4x"}, wantStatus: 404, wantBody: "NOT FOUND"},

		// Method matching: method outranks header count.
		{name: "method exact", method: "POST", path: "/", wantBody: "be_post"},
		{name: "method and header", method: "POST", path: "/", hdrs: []string{"Version", "one"}, wantBody: "be_post_ver"},
		{name: "failed method and header falls to plain method", method: "POST", path: "/", hdrs: []string{"Version", "two"}, wantBody: "be_post"},
		{name: "method outranks two headers", method: "POST", path: "/", hdrs: []string{"Version", "two", "Color", "orange"}, wantBody: "be_post"},

		// Query matching: names case-sensitive, values exact, headers outrank.
		{name: "query exact", path: "/?animal=whale", wantBody: "be_whale"},
		{name: "header count outranks query count", path: "/?animal=whale", hdrs: []string{"Version", "one"}, wantBody: "be_one"},
		{name: "query name is case-sensitive", path: "/?ANIMAL=whale", wantStatus: 404, wantBody: "NOT FOUND"},
		{name: "query value is exact", path: "/?animal=whaledolphin", wantStatus: 404, wantBody: "NOT FOUND"},

		// Path specificity beats conditions: the longer /api prefix wins
		// when its own condition passes, and falls through to the catch-all
		// when it does not.
		{name: "longer prefix passes its condition", path: "/api", hdrs: []string{"admin", "yes"}, wantBody: "be_admin"},
		{name: "failed conditional falls to shorter prefix", path: "/api", hdrs: []string{"Version", "one"}, wantBody: "be_one"},
		{name: "failed conditional with no fallback 404s", path: "/api", wantStatus: 404, wantBody: "NOT FOUND"},

		// Regex fallback after a failed conditional prefix. The alternation
		// pattern also proves map regexes run on the HAProxy engine: Lua
		// patterns cannot express "|".
		{name: "regex fallback after failed prefix", path: "/old/x", wantBody: "be_old"},
		{name: "regex alternation matches", path: "/ancient/x", wantBody: "be_old"},
		{name: "regex alternation second branch", path: "/vintage/x", wantBody: "be_old"},

		// Exact-map conditional with unconditional fallback candidate.
		{name: "exact map fallback", path: "/ping", wantBody: "be_ping"},
		{name: "exact map conditional", path: "/ping", hdrs: []string{"Version", "one"}, wantBody: "be_ping_one"},

		// Weighted target: select_route leaves the wr JSON in txn.route and
		// lua.route picks a backend from it.
		{name: "weighted candidate target", path: "/", hdrs: []string{"Version", "split"}, wantAny: []string{"be_one", "be_two"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := harness.New(t, "haproxy.cfg")
			method := c.method
			if method == "" {
				method = "GET"
			}
			if c.wantStatus == 0 {
				c.wantStatus = 200
			}
			status, body := h.Do(t, method, c.path, append([]string{"Host", "example.org"}, c.hdrs...)...)
			if status != c.wantStatus {
				t.Errorf("status = %d, want %d (body %q)", status, c.wantStatus, body)
			}
			if c.wantAny != nil {
				if !slices.Contains(c.wantAny, body) {
					t.Errorf("body %q not in %v", body, c.wantAny)
				}
				return
			}
			if body != c.wantBody {
				t.Errorf("body = %q, want %q", body, c.wantBody)
			}
		})
	}
}
