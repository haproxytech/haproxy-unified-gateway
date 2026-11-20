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

import "testing"

func TestValidateRegexPath(t *testing.T) {
	valid := []string{
		"/foo",
		"^/foo",
		"/api/.*",
		"/(a|b)/x",
		"/x|/y",
		"(/a|/b)c",
		"/+x",
		`/a\.b`,
		"(?i)/Foo",
	}
	for _, value := range valid {
		t.Run("valid "+value, func(t *testing.T) {
			if err := ValidateRegexPath(value); err != nil {
				t.Errorf("ValidateRegexPath(%q) = %v, want nil", value, err)
			}
		})
	}

	invalid := []struct{ name, value string }{
		{"alternative matching everything", "/x|.*"},
		{"no leading slash", ".*"},
		{"empty", ""},
		{"plain word", "x"},
		{"sibling route suffix", "2/secret"},
		{"optional slash", "/?x"},
		{"slash as one choice of a class", "[/a]x"},
		{"unbalanced group closing the key group", "/x)|(.*"},
		{"unclosed group", "(unclosed"},
		{"construct outside the supported dialect", "/one(?=two)"},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateRegexPath(tt.value); err == nil {
				t.Errorf("ValidateRegexPath(%q) = nil, want an error", tt.value)
			}
		})
	}
}

func TestValidateRegexCost(t *testing.T) {
	valid := []string{
		"^v[0-9]+$",
		"/api/.*",
		"/v[0-9]+/users/[0-9]+/.*",
		"(foo|bar)baz",
		"(abc)?d",
		"a{2,5}",
		`\{,5}`,
		`\p{Greek}+`,
		`\x{41}`,
		"(?i)abc+",
		"a?b?c?d?e?f?g?h?",
	}
	for _, value := range valid {
		t.Run("valid "+value, func(t *testing.T) {
			if err := validateRegexCost(value); err != nil {
				t.Errorf("validateRegexCost(%q) = %v, want nil", value, err)
			}
		})
	}

	invalid := []struct{ name, value string }{
		{"repeated alternation", "(a|aa)+$"},
		{"nested repetition", "(a+)+"},
		{"repeated duplicate alternation", "(a|a)+"},
		{"repeated non-capturing group", "(?:a+)+c$"},
		{"repeated group with star", "(ab)*"},
		{"counted group", "(a|aa){2,30}"},
		{"brace form the runtime engine reads as a quantifier", "^(a|aa){,1000}$"},
		{"spaced brace quantifier", "a{ 1,2}"},
		{"quoted text", `\Q(a+)+\E`},
		{"too many repetitions", "a*a*a*a*c$"},
		{"too many optional parts", "a?a?a?a?a?a?a?a?a?"},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateRegexCost(tt.value); err == nil {
				t.Errorf("validateRegexCost(%q) = nil, want an error", tt.value)
			}
		})
	}
}

func TestExpensiveRegexPathIsRejected(t *testing.T) {
	if err := ValidateRegexPath("/(a|aa)+$"); err == nil {
		t.Error("path regex /(a|aa)+$ accepted")
	}
}
