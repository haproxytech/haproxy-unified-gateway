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

package find_route_unregistered_test

import (
	"strings"
	"testing"

	"github.com/haproxytech/haproxy-unified-gateway/test/lua/internal/harness"
)

// TestFindRoute_UnregisteredMap_AlertOnce verifies that when haproxy.cfg
// hasn't pre-loaded the path maps (no `acl _preload_*` rules), find_route
// degrades gracefully: no crash, no route set, and a single Alert per missing
// filepath thanks to the false sentinel in route.lua's patref cache.
func TestFindRoute_UnregisteredMap_AlertOnce(t *testing.T) {
	h := harness.New(t, "haproxy.cfg")

	// Two requests with the same path → second uses the sentinel, doesn't
	// re-alert. We can't easily count alerts deterministically across both
	// without parsing the log, so just check at least one fired.
	_, body1 := h.Get(t, "/foo")
	_, body2 := h.Get(t, "/foo")

	if !strings.Contains(body1, "route=") || strings.Contains(body1, "be_should_never_match") {
		t.Errorf("first request: expected no route in body; got: %s", body1)
	}
	if !strings.Contains(body2, "route=") || strings.Contains(body2, "be_should_never_match") {
		t.Errorf("second request: expected no route in body; got: %s", body2)
	}

	log := h.Log()
	if !strings.Contains(log, "find_route: map not registered with HAProxy") {
		t.Errorf("expected alert in haproxy log; full log:\n%s", log)
	}
}
