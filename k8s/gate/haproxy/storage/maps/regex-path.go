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

package maps

import (
	"errors"
	"fmt"
	"regexp"
	"regexp/syntax"
	"strings"
)

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
