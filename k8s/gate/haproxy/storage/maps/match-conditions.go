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
	"regexp/syntax"
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

	// Header names are case-insensitive, query names are exact.
	seenHeaders := map[string]bool{}
	for _, h := range match.Headers {
		name := strings.ToLower(string(h.Name))
		if name == "" {
			return mc, errors.New("invalid header match: empty name")
		}
		if seenHeaders[name] {
			continue
		}
		seenHeaders[name] = true
		condType := MatchCondExact
		if h.Type != nil && *h.Type == gatewayv1.HeaderMatchRegularExpression {
			condType = MatchCondRegexp
		}
		if condType == MatchCondRegexp {
			if _, err := regexp.Compile(h.Value); err != nil {
				return mc, fmt.Errorf("invalid header %q regex %q: %w", name, h.Value, err)
			}
			if err := validateRegexCost(h.Value); err != nil {
				return mc, fmt.Errorf("invalid header %q regex %q: %w", name, h.Value, err)
			}
		}
		mc.Headers = append(mc.Headers, MatchCond{Name: name, Type: condType, Value: h.Value})
	}

	// Spec: first entry per name wins, later equivalent names are ignored.
	seenQuery := map[string]bool{}
	for _, q := range match.QueryParams {
		name := string(q.Name)
		if name == "" {
			return mc, errors.New("invalid query param match: empty name")
		}
		if seenQuery[name] {
			continue
		}
		seenQuery[name] = true
		condType := MatchCondExact
		if q.Type != nil && *q.Type == gatewayv1.QueryParamMatchRegularExpression {
			condType = MatchCondRegexp
		}
		if condType == MatchCondRegexp {
			if _, err := regexp.Compile(q.Value); err != nil {
				return mc, fmt.Errorf("invalid query param %q regex %q: %w", name, q.Value, err)
			}
			if err := validateRegexCost(q.Value); err != nil {
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

// ValidateRegexPath checks a RegularExpression path value before it becomes
// part of a routing map key. The key is the route identity followed by the
// value, so the value must not be able to match outside its own route:
//
//   - it must compile in the Go RE2 dialect, which also guarantees balanced
//     groups, so the group the key wraps it in cannot be closed early;
//   - every alternative must start with a literal "/". The route identity is a
//     string prefix of a sibling route's identity (route-a, route-ab), and only
//     the "/" that starts every request path tells them apart.
//
// A leading "^" is accepted: the key is anchored already.
func ValidateRegexPath(value string) error {
	parsed, err := syntax.Parse(strings.TrimPrefix(value, "^"), syntax.Perl)
	if err != nil {
		return err
	}
	if !startsWithSlash(parsed) {
		return errors.New(`every alternative must start with "/"`)
	}
	return validateRegexCost(strings.TrimPrefix(value, "^"))
}

// startsWithSlash reports whether every string re matches starts with "/".
func startsWithSlash(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpLiteral:
		return len(re.Rune) > 0 && re.Rune[0] == '/'
	case syntax.OpCharClass:
		return len(re.Rune) == 2 && re.Rune[0] == '/' && re.Rune[1] == '/'
	case syntax.OpCapture, syntax.OpPlus:
		return startsWithSlash(re.Sub[0])
	case syntax.OpRepeat:
		return re.Min >= 1 && startsWithSlash(re.Sub[0])
	case syntax.OpConcat:
		for _, sub := range re.Sub {
			// Anchors consume nothing: the next element decides.
			if sub.Op == syntax.OpBeginLine || sub.Op == syntax.OpBeginText || sub.Op == syntax.OpEmptyMatch {
				continue
			}
			return startsWithSlash(sub)
		}
		return false
	case syntax.OpAlternate:
		for _, sub := range re.Sub {
			if !startsWithSlash(sub) {
				return false
			}
		}
		return true
	}
	return false
}

// Shape limits of a RegularExpression value. HAProxy runs it with a
// backtracking engine, so only the shape of the pattern bounds its cost.
const (
	maxRegexRepeats   = 3 // *, + and {n,m}
	maxRegexOptionals = 8 // ?
)

var strictQuantifier = regexp.MustCompile(`^\{\d+(,\d*)?\}`)

// validateRegexCost rejects a pattern whose match time can grow out of
// proportion to the request, and a pattern that Go and the HAProxy regex
// engine read differently:
//
//   - a "{" must be escaped or form {n}, {n,} or {n,m}. HAProxy also reads
//     {,n} and spaced forms as a quantifier, where Go sees plain text;
//   - a group must not be repeated with *, + or {n,m}: only a single
//     character, class or dot may be;
//   - the number of repetitions and of optional parts is capped.
//
// The value must already compile in the Go RE2 dialect.
func validateRegexCost(value string) error {
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '\\':
			i++
			if i >= len(value) {
				break
			}
			if value[i] == 'Q' {
				return errors.New(`\Q quoting is not supported`)
			}
			// In \p{..}, \P{..} and \x{..} the brace belongs to the escape.
			if strings.IndexByte("pPx", value[i]) >= 0 && strings.HasPrefix(value[i+1:], "{") {
				if end := strings.IndexByte(value[i:], '}'); end > 0 {
					i += end
				}
			}
		case '{':
			if !strictQuantifier.MatchString(value[i:]) {
				return errors.New(`"{" must be escaped or form {n}, {n,} or {n,m}`)
			}
		case ')':
			if i+1 < len(value) && strings.IndexByte("*+{", value[i+1]) >= 0 {
				return errors.New("a group must not be repeated")
			}
		}
	}
	parsed, err := syntax.Parse(value, syntax.Perl)
	if err != nil {
		return err
	}
	repeats, optionals := 0, 0
	if err := countRepeats(parsed, &repeats, &optionals); err != nil {
		return err
	}
	if repeats > maxRegexRepeats {
		return fmt.Errorf("more than %d repetitions (*, +, {n,m})", maxRegexRepeats)
	}
	if optionals > maxRegexOptionals {
		return fmt.Errorf("more than %d optional parts (?)", maxRegexOptionals)
	}
	return nil
}

// countRepeats walks the parsed pattern. It fails on a repetition of
// anything wider than one character.
func countRepeats(re *syntax.Regexp, repeats, optionals *int) error {
	switch re.Op {
	case syntax.OpQuest:
		*optionals++
	case syntax.OpStar, syntax.OpPlus, syntax.OpRepeat:
		sub := re.Sub[0]
		single := sub.Op == syntax.OpCharClass || sub.Op == syntax.OpAnyChar ||
			sub.Op == syntax.OpAnyCharNotNL || (sub.Op == syntax.OpLiteral && len(sub.Rune) == 1)
		if !single {
			return errors.New("only a single character, class or dot may be repeated")
		}
		// An exact count {n} never backtracks.
		if re.Op != syntax.OpRepeat || re.Min != re.Max {
			*repeats++
		}
	}
	for _, sub := range re.Sub {
		if err := countRepeats(sub, repeats, optionals); err != nil {
			return err
		}
	}
	return nil
}
