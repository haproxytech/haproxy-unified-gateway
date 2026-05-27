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

package route_cli_test

import (
	"strings"
	"testing"

	"github.com/haproxytech/haproxy-unified-gateway/test/lua/internal/harness"
)

// TestCLI_RouteCache exercises the register_cli commands in route.lua:
// `route dump cache` and `route clear cache`. A request populates the cache;
// dump should mention the entry; clear should wipe it.
func TestCLI_RouteCache(t *testing.T) {
	h := harness.New(t, "haproxy.cfg")

	spec := `{"a":"wr","l":"be_one:1"}`
	if _, body := h.Get(t, "/", "X-Route", spec); !strings.Contains(body, "backend=be_one") {
		t.Fatalf("priming request failed: body=%s", body)
	}

	dump := h.Socket(t, "route dump cache")
	if !strings.Contains(dump, "be_one") {
		t.Errorf("expected dump to mention be_one; got:\n%s", dump)
	}

	cleared := h.Socket(t, "route clear cache")
	if !strings.Contains(cleared, "cleared") {
		t.Errorf("expected 'cleared' in clear response; got: %s", cleared)
	}

	dump2 := h.Socket(t, "route dump cache")
	if strings.Contains(dump2, "be_one") {
		t.Errorf("expected cache to be empty after clear; got:\n%s", dump2)
	}
}
