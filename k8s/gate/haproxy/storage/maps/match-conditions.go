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
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// MatchCondType selects how a header or query-param value is compared.
type MatchCondType string

const (
	// MatchCondExact compares the full field value.
	MatchCondExact MatchCondType = "e"
	// MatchCondRegexp searches the value with a regex, unanchored.
	MatchCondRegexp MatchCondType = "r"
)

// MatchCond is one header or query-param predicate. All conds of a match are
// ANDed.
type MatchCond struct {
	Name  string
	Type  MatchCondType
	Value string
}

// MatchConditions holds the non-path predicates of one HTTPRouteMatch. A nil
// or empty value means the candidate is unconditional.
type MatchConditions struct {
	Method  string
	Headers []MatchCond // lowercased names: HTTP header names are case-insensitive
	Query   []MatchCond // verbatim names: query param names are case-sensitive
}

// Encoded size guards: HAProxy truncates map values above ~16KB, so oversized
// rules must be rejected at admission instead of silently degrading.
const (
	MaxEncodedMatchSizePerRule  = 4096
	MaxEncodedMatchSizePerRoute = 12 * 1024
)

func (mc *MatchConditions) IsEmpty() bool {
	return mc == nil || (mc.Method == "" && len(mc.Headers) == 0 && len(mc.Query) == 0)
}

// Counts feeds precedence ordering: method first, then header count, then
// query count, as required by the Gateway API match precedence rules.
func (mc *MatchConditions) Counts() (hasMethod bool, nHeaders, nQuery int) {
	if mc == nil {
		return false, 0, 0
	}
	return mc.Method != "", len(mc.Headers), len(mc.Query)
}

// NormalizeHTTPRouteMatch extracts and validates the non-path predicates of a
// Gateway API HTTPRouteMatch. Header names are lowercased and later
// duplicates of a name (case-insensitively) are dropped, keeping only the
// first occurrence as the spec requires. RegularExpression values must
// compile in the Go RE2 dialect, the documented supported subset of the
// PCRE2 engine HAProxy uses at runtime.
func NormalizeHTTPRouteMatch(match gatewayv1.HTTPRouteMatch) (MatchConditions, error) {
	var mc MatchConditions

	if match.Method != nil {
		mc.Method = string(*match.Method)
	}

	seen := map[string]bool{}
	for _, h := range match.Headers {
		name := strings.ToLower(string(h.Name))
		if name == "" {
			return mc, errors.New("invalid header match: empty name")
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		condType := MatchCondExact
		if h.Type != nil && *h.Type == gatewayv1.HeaderMatchRegularExpression {
			condType = MatchCondRegexp
		}
		if condType == MatchCondRegexp {
			if _, err := regexp.Compile(h.Value); err != nil {
				return mc, fmt.Errorf("invalid header %q regex %q: %w", name, h.Value, err)
			}
		}
		mc.Headers = append(mc.Headers, MatchCond{Name: name, Type: condType, Value: h.Value})
	}

	for _, q := range match.QueryParams {
		name := string(q.Name)
		if name == "" {
			return mc, errors.New("invalid query param match: empty name")
		}
		condType := MatchCondExact
		if q.Type != nil && *q.Type == gatewayv1.QueryParamMatchRegularExpression {
			condType = MatchCondRegexp
		}
		if condType == MatchCondRegexp {
			if _, err := regexp.Compile(q.Value); err != nil {
				return mc, fmt.Errorf("invalid query param %q regex %q: %w", name, q.Value, err)
			}
		}
		mc.Query = append(mc.Query, MatchCond{Name: name, Type: condType, Value: q.Value})
	}

	// Canonical order so equal matches always encode identically.
	slices.SortFunc(mc.Headers, func(a, b MatchCond) int {
		return strings.Compare(a.Name+"\x00"+a.Value, b.Name+"\x00"+b.Value)
	})
	slices.SortFunc(mc.Query, func(a, b MatchCond) int {
		return strings.Compare(a.Name+"\x00"+a.Value, b.Name+"\x00"+b.Value)
	})
	return mc, nil
}

// Encode renders the canonical condition string. It doubles as dedup
// signature inside map values and as part of the weighted-value map key.
// Empty when unconditional.
func (mc *MatchConditions) Encode() string {
	if mc.IsEmpty() {
		return ""
	}
	parts := make([]string, 0, 1+len(mc.Headers)+len(mc.Query))
	if mc.Method != "" {
		parts = append(parts, "m="+percentEncode(mc.Method))
	}
	for _, h := range mc.Headers {
		parts = append(parts, "h="+percentEncode(h.Name)+":"+string(h.Type)+":"+percentEncode(h.Value))
	}
	for _, q := range mc.Query {
		parts = append(parts, "q="+percentEncode(q.Name)+":"+string(q.Type)+":"+percentEncode(q.Value))
	}
	return strings.Join(parts, ",")
}

// percentEncode keeps only unreserved bytes so encoded names and values can
// never contain the map-value structural delimiters or whitespace.
func percentEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			//revive:disable-next-line:unhandled-error
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
