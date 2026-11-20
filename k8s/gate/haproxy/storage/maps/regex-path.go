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
	return nil
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
