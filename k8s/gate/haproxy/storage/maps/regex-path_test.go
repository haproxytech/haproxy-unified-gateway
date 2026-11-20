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
