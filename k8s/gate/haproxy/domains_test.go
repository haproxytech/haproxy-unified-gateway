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
package haproxy

import "testing"

func TestReverseDomain(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{".example.com", ".com.example."},
		{"example.com", ".com.example"},
		{"foo.bar.example.com", ".com.example.bar.foo"},
		{".foo.bar.example.com", ".com.example.bar.foo."},
		{"com", ".com"},
		{"", "."},
		{".", "."},
	}
	for _, tc := range tests {
		got := reverseDomain(tc.input)
		if got != tc.want {
			t.Errorf("reverseDomain(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
