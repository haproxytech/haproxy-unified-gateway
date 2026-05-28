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
	"os"
	"strings"
	"testing"

	"github.com/haproxytech/haproxy-unified-gateway/test/lua/internal/harness"
)

// TestReverseHost exercises the reverse_host converter registered in route.lua:
// it reverses a dot-separated hostname and prefixes a leading dot, which lets
// HAProxy use prefix matching against wildcard host patterns.
// Test cases are loaded from maps/cases.map (host → expected reversed hostname).
func TestReverseHost(t *testing.T) {
	h := harness.New(t, "haproxy.cfg")

	raw, err := os.ReadFile("maps/cases.map")
	if err != nil {
		t.Fatalf("read cases.map: %v", err)
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			t.Fatalf("malformed cases.map line: %q", line)
		}
		host, want := fields[0], fields[1]
		t.Run(host, func(t *testing.T) {
			status, body := h.Get(t, "/", "Host", host)
			if status != 200 {
				t.Fatalf("status=%d body=%q", status, body)
			}
			if body != want {
				t.Errorf("Host=%q got %q want %q", host, body, want)
			}
		})
	}
}
