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

package maps

import (
	"testing"
	"time"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

//go:fix inline
func ptr[T any](v T) *T { return new(v) }

func headerMatch(name, value string, matchType *gatewayv1.HeaderMatchType) gatewayv1.HTTPHeaderMatch {
	return gatewayv1.HTTPHeaderMatch{Name: gatewayv1.HTTPHeaderName(name), Value: value, Type: matchType}
}

func queryMatch(name, value string, matchType *gatewayv1.QueryParamMatchType) gatewayv1.HTTPQueryParamMatch {
	return gatewayv1.HTTPQueryParamMatch{Name: gatewayv1.HTTPHeaderName(name), Value: value, Type: matchType}
}

func TestNormalizeHTTPRouteMatch(t *testing.T) {
	tests := []struct {
		name      string
		match     gatewayv1.HTTPRouteMatch
		wantConds MatchConditions
		wantErr   bool
	}{
		{
			name:  "no conditions",
			match: gatewayv1.HTTPRouteMatch{},
		},
		{
			name: "header exact with default type",
			match: gatewayv1.HTTPRouteMatch{
				Headers: []gatewayv1.HTTPHeaderMatch{headerMatch("Version", "one", nil)},
			},
			wantConds: MatchConditions{
				Headers: []MatchCond{{Name: "version", Type: MatchCondExact, Value: "one"}},
			},
		},
		{
			name: "header names are case-insensitive, first entry wins",
			match: gatewayv1.HTTPRouteMatch{
				Headers: []gatewayv1.HTTPHeaderMatch{
					headerMatch("Version", "one", nil),
					headerMatch("VERSION", "two", nil),
				},
			},
			wantConds: MatchConditions{
				Headers: []MatchCond{{Name: "version", Type: MatchCondExact, Value: "one"}},
			},
		},
		{
			name: "header regex compiles",
			match: gatewayv1.HTTPRouteMatch{
				Headers: []gatewayv1.HTTPHeaderMatch{
					headerMatch("version", "^v[0-9]+$", ptr(gatewayv1.HeaderMatchRegularExpression)),
				},
			},
			wantConds: MatchConditions{
				Headers: []MatchCond{{Name: "version", Type: MatchCondRegexp, Value: "^v[0-9]+$"}},
			},
		},
		{
			name: "header regex using PCRE-only syntax is rejected",
			match: gatewayv1.HTTPRouteMatch{
				Headers: []gatewayv1.HTTPHeaderMatch{
					headerMatch("version", "one(?=two)", ptr(gatewayv1.HeaderMatchRegularExpression)),
				},
			},
			wantErr: true,
		},
		{
			name: "query names stay case-sensitive",
			match: gatewayv1.HTTPRouteMatch{
				QueryParams: []gatewayv1.HTTPQueryParamMatch{
					queryMatch("animal", "whale", nil),
				},
			},
			wantConds: MatchConditions{
				Query: []MatchCond{{Name: "animal", Type: MatchCondExact, Value: "whale"}},
			},
		},
		{
			name: "method is carried verbatim",
			match: gatewayv1.HTTPRouteMatch{
				Method: ptr(gatewayv1.HTTPMethod("POST")),
			},
			wantConds: MatchConditions{Method: "POST"},
		},
		{
			name: "conditions are canonically ordered for encoding",
			match: gatewayv1.HTTPRouteMatch{
				Headers: []gatewayv1.HTTPHeaderMatch{
					headerMatch("zeta", "1", nil),
					headerMatch("alpha", "2", nil),
				},
			},
			wantConds: MatchConditions{
				Headers: []MatchCond{
					{Name: "alpha", Type: MatchCondExact, Value: "2"},
					{Name: "zeta", Type: MatchCondExact, Value: "1"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeHTTPRouteMatch(tt.match)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Method != tt.wantConds.Method {
				t.Errorf("method: got %q want %q", got.Method, tt.wantConds.Method)
			}
			if len(got.Headers) != len(tt.wantConds.Headers) {
				t.Fatalf("headers: got %+v want %+v", got.Headers, tt.wantConds.Headers)
			}
			for i := range got.Headers {
				if got.Headers[i] != tt.wantConds.Headers[i] {
					t.Errorf("headers[%d]: got %+v want %+v", i, got.Headers[i], tt.wantConds.Headers[i])
				}
			}
			if len(got.Query) != len(tt.wantConds.Query) {
				t.Fatalf("query: got %+v want %+v", got.Query, tt.wantConds.Query)
			}
			for i := range got.Query {
				if got.Query[i] != tt.wantConds.Query[i] {
					t.Errorf("query[%d]: got %+v want %+v", i, got.Query[i], tt.wantConds.Query[i])
				}
			}
		})
	}
}

func TestMatchConditionsEncode(t *testing.T) {
	tests := []struct {
		name  string
		conds MatchConditions
		want  string
	}{
		{"empty", MatchConditions{}, ""},
		{"method", MatchConditions{Method: "POST"}, "m=POST"},
		{
			"headers and query",
			MatchConditions{
				Headers: []MatchCond{{Name: "version", Type: MatchCondExact, Value: "two"}},
				Query:   []MatchCond{{Name: "animal", Type: MatchCondExact, Value: "whale"}},
			},
			"h=version:e:two,q=animal:e:whale",
		},
		{
			"regex type",
			MatchConditions{
				Headers: []MatchCond{{Name: "version", Type: MatchCondRegexp, Value: "^v[0-9]+$"}},
			},
			"h=version:r:%5Ev%5B0-9%5D%2B%24",
		},
		{
			"values with delimiters are escaped",
			MatchConditions{
				Headers: []MatchCond{{Name: "x-a", Type: MatchCondExact, Value: "a,b;c>d"}},
			},
			"h=x-a:e:a%2Cb%3Bc%3Ed",
		},
		{
			"header value with a space is escaped",
			MatchConditions{
				Headers: []MatchCond{{Name: "accept", Type: MatchCondExact, Value: "audio/basic q=1"}},
			},
			"h=accept:e:audio%2Fbasic%20q%3D1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.conds.Encode(); got != tt.want {
				t.Errorf("Encode() = %q, want %q", got, tt.want)
			}
			// IsEmpty and Encode must agree so unconditional candidates keep
			// the legacy format.
			if tt.conds.IsEmpty() != (tt.want == "") {
				t.Errorf("IsEmpty() = %v for %q", tt.conds.IsEmpty(), tt.want)
			}
		})
	}
}

func TestBuildRouteValueCompositeKeysNeverLeak(t *testing.T) {
	// Regression: bucket keys are "sig \0 ruleIdx \0 backend" composites, but
	// every emitted value (bare name, wr JSON, plain list) must contain only
	// ValueName. A composite leaking into a runtime map value breaks the
	// 'set map' CLI (NUL bytes) and routes nowhere.
	m := newTestState()
	ek := EntryKey{Hostname: "l1/r1", Path: "/"}

	// Unconditional candidate (empty signature) on the legacy path.
	m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"}, map[string]*WeightedValue{
		"\x000\x00be1": {ValueName: "be1", Weight: new(int32(1))},
	})
	m.ProcessMapFiles()
	if got := BuildRouteValue(m.Entries[ek].DesiredValue); got != "be1" {
		t.Errorf("legacy single backend: got %q, want be1", got)
	}

	// Weighted candidates on the legacy path.
	m2 := newTestState()
	m2.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"}, map[string]*WeightedValue{
		"\x000\x00be1": {ValueName: "be1", Weight: new(int32(1))},
		"\x000\x00be2": {ValueName: "be2", Weight: new(int32(2))},
	})
	m2.ProcessMapFiles()
	want := `{"a":"wr","l":"be1:1,be2:2"}`
	if got := BuildRouteValue(m2.Entries[ek].DesiredValue); got != want {
		t.Errorf("legacy weighted: got %q, want %q", got, want)
	}

	// Plain values (listener maps) with composite keys.
	if got := BuildPlainRouteValue(map[string]*WeightedValue{
		"\x000\x00l1/r1": {ValueName: "l1/r1"},
		"\x000\x00l1/r2": {ValueName: "l1/r2"},
	}); got != "l1/r1,l1/r2" {
		t.Errorf("plain values: got %q", got)
	}
}

// The spec breaks cross-route ties by oldest route first, then ns/name, and
// listener-route map values are comma-joined in that order.
func TestBuildPlainRouteValueOrderedByRouteAge(t *testing.T) {
	older := time.Unix(100, 0)
	newer := time.Unix(200, 0)

	// Older route first regardless of name order.
	got := BuildPlainRouteValue(map[string]*WeightedValue{
		"a": {ValueName: "ns/a", CreatedAt: newer},
		"b": {ValueName: "ns/z", CreatedAt: older},
	})
	if got != "ns/z,ns/a" {
		t.Errorf("age ordering: got %q, want ns/z,ns/a", got)
	}

	// Equal timestamps fall back to ns/name order.
	got = BuildPlainRouteValue(map[string]*WeightedValue{
		"a": {ValueName: "ns/z", CreatedAt: older},
		"b": {ValueName: "ns/a", CreatedAt: older},
	})
	if got != "ns/a,ns/z" {
		t.Errorf("tie ordering: got %q, want ns/a,ns/z", got)
	}

	// Zero timestamps (maps that never carry several routes) stay
	// alphabetical for backward compatibility.
	got = BuildPlainRouteValue(map[string]*WeightedValue{
		"a": {ValueName: "ns/z"},
		"b": {ValueName: "ns/a"},
	})
	if got != "ns/a,ns/z" {
		t.Errorf("zero timestamps: got %q, want ns/a,ns/z", got)
	}
}

func TestBuildRouteValueConditional(t *testing.T) {
	mkConds := func(hs ...MatchCond) *MatchConditions {
		if len(hs) == 0 {
			return nil
		}
		return &MatchConditions{Headers: hs}
	}

	tests := []struct {
		name    string
		desired map[string]*WeightedValue
		want    string
	}{
		{
			// Legacy format is untouched when no conditions are present.
			name: "legacy multi-backend keeps wr json",
			desired: map[string]*WeightedValue{
				"be1": {ValueName: "be1", Weight: new(int32(1))},
				"be2": {ValueName: "be2", Weight: new(int32(2))},
			},
			want: `{"a":"wr","l":"be1:1,be2:2"}`,
		},
		{
			name: "single conditional candidate",
			desired: map[string]*WeightedValue{
				"h=version:e:one\x000\x00be1": {
					ValueName:  "be1",
					Weight:     new(int32(1)),
					Conditions: mkConds(MatchCond{Name: "version", Type: MatchCondExact, Value: "one"}),
					RuleIdx:    0,
				},
			},
			want: "~1;h=version:e:one>be1",
		},
		{
			name: "conformance header-matching shape: counts then rule order",
			desired: map[string]*WeightedValue{
				// rule 3: version=two AND color=orange -> v1
				"h=color:e:orange,h=version:e:two\x002\x00be1": {
					ValueName: "be1",
					Conditions: mkConds(
						MatchCond{Name: "color", Type: MatchCondExact, Value: "orange"},
						MatchCond{Name: "version", Type: MatchCondExact, Value: "two"},
					),
					RuleIdx: 2,
				},
				// rule 1: version=one -> v1
				"h=version:e:one\x000\x00be1": {
					ValueName:  "be1",
					Conditions: mkConds(MatchCond{Name: "version", Type: MatchCondExact, Value: "one"}),
					RuleIdx:    0,
				},
				// rule 2: version=two -> v2
				"h=version:e:two\x001\x00be2": {
					ValueName:  "be2",
					Conditions: mkConds(MatchCond{Name: "version", Type: MatchCondExact, Value: "two"}),
					RuleIdx:    1,
				},
			},
			// 2 headers first, then 1-header rules in rule order.
			want: "~1;h=color:e:orange,h=version:e:two>be1;h=version:e:one>be1;h=version:e:two>be2",
		},
		{
			name: "unconditional candidate is the fallback, last",
			desired: map[string]*WeightedValue{
				"\x000\x00be1": {ValueName: "be1", RuleIdx: 0},
				"h=version:e:one\x000\x00be2": {
					ValueName:  "be2",
					Conditions: mkConds(MatchCond{Name: "version", Type: MatchCondExact, Value: "one"}),
					RuleIdx:    0,
				},
			},
			want: "~1;h=version:e:one>be2;>be1",
		},
		{
			name: "same conditions from a later rule are dropped (first rule wins)",
			desired: map[string]*WeightedValue{
				"h=version:e:one\x000\x00be1": {
					ValueName:  "be1",
					Conditions: mkConds(MatchCond{Name: "version", Type: MatchCondExact, Value: "one"}),
					RuleIdx:    0,
				},
				"h=version:e:one\x002\x00be2": {
					ValueName:  "be2",
					Conditions: mkConds(MatchCond{Name: "version", Type: MatchCondExact, Value: "one"}),
					RuleIdx:    2,
				},
			},
			want: "~1;h=version:e:one>be1",
		},
		{
			name: "method outranks header count",
			desired: map[string]*WeightedValue{
				"m=POST\x001\x00be1": {
					ValueName:  "be1",
					Conditions: &MatchConditions{Method: "POST"},
					RuleIdx:    1,
				},
				"h=version:e:four\x002\x00be2": {
					ValueName:  "be2",
					Conditions: mkConds(MatchCond{Name: "version", Type: MatchCondExact, Value: "four"}),
					RuleIdx:    2,
				},
			},
			want: "~1;m=POST>be1;h=version:e:four>be2",
		},
		{
			name: "header count outranks query count",
			desired: map[string]*WeightedValue{
				"q=animal:e:hydra\x001\x00be2": {
					ValueName:  "be2",
					Conditions: &MatchConditions{Query: []MatchCond{{Name: "animal", Type: MatchCondExact, Value: "hydra"}}},
					RuleIdx:    1,
				},
				"h=version:e:four\x002\x00be3": {
					ValueName:  "be3",
					Conditions: mkConds(MatchCond{Name: "version", Type: MatchCondExact, Value: "four"}),
					RuleIdx:    2,
				},
			},
			want: "~1;h=version:e:four>be3;q=animal:e:hydra>be2",
		},
		{
			name: "conditional group with several backends emits wr json target",
			desired: map[string]*WeightedValue{
				"h=version:e:one\x000\x00be1": {
					ValueName:  "be1",
					Weight:     new(int32(1)),
					Conditions: mkConds(MatchCond{Name: "version", Type: MatchCondExact, Value: "one"}),
					RuleIdx:    0,
				},
				"h=version:e:one\x000\x00be2": {
					ValueName:  "be2",
					Weight:     new(int32(2)),
					Conditions: mkConds(MatchCond{Name: "version", Type: MatchCondExact, Value: "one"}),
					RuleIdx:    0,
				},
			},
			want: `~1;h=version:e:one>{"a":"wr","l":"be1:1,be2:2"}`,
		},
		{
			name: "same rule and signature weights add up (OR'ed matches)",
			desired: map[string]*WeightedValue{
				"h=a:e:1\x000\x00be1": {
					ValueName:  "be1",
					Weight:     new(int32(2)),
					Conditions: mkConds(MatchCond{Name: "a", Type: MatchCondExact, Value: "1"}),
					RuleIdx:    0,
				},
			},
			want: "~1;h=a:e:1>be1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BuildRouteValue(tt.desired); got != tt.want {
				t.Errorf("BuildRouteValue() =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}
