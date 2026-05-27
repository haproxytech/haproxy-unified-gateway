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

package reverse_host_test

import (
	"testing"

	"github.com/haproxytech/haproxy-unified-gateway/test/lua/internal/harness"
)

// TestReverseHost exercises the reverse_host converter registered in route.lua:
// it reverses a dot-separated hostname and prefixes a leading dot, which lets
// HAProxy use prefix matching against wildcard host patterns.
func TestReverseHost(t *testing.T) {
	h := harness.New(t, "haproxy.cfg")

	cases := []struct {
		name, host, want string
	}{
		{"three_labels", "www.example.com", ".com.example.www"},
		{"two_labels", "a.b", ".b.a"},
		{"single_label", "foo", ".foo"},
		{"with_port_stripped", "host.example.com:8080", ".com.example.host"},
		{"trailing_dot", "a.b.c.", ".c.b.a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := h.Get(t, "/", "Host", tc.host)
			if status != 200 {
				t.Fatalf("status=%d body=%q", status, body)
			}
			if body != tc.want {
				t.Errorf("Host=%q got %q want %q", tc.host, body, tc.want)
			}
		})
	}
}
