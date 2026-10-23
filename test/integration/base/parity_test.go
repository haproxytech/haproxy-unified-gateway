// Copyright 2025 HAProxy Technologies LLC
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

package base

import (
	"strconv"
	"strings"
	"testing"
)

// The parity assertions decide pass or fail from a string rewrite, so the rewrite is the
// part worth testing without a cluster: what it erases is the naming a case tolerates,
// and what it must not erase is everything else.

func TestCanonicalizeParityDocumentErasesNaming(t *testing.T) {
	ports := map[int]int{8088: 23456, 8081: 21234}
	differences := []NamingDifference{{"ns/parity-gateway", "ns/parity-listenerset"}}

	// One side is written with the derived port everywhere but keeps a map path written with
	// the template port, which is what a document half-substituted looks like; the other
	// side carries its own derived port throughout. Both must canonicalise to the same
	// document, port, origin kind and declaring resource included.
	gatewaySide := "name: hug_tls_23456\n" +
		"binds:\n  v4:\n    port: 23456\n" +
		"tcp_request_rule_list:\n- expr: var(sess.sni),map(/tmp/hug/e2e/maps/hug_tls_8088/sni.map)\n" +
		"metadata:\n  hug:\n    Gateway:\n      ns/parity-gateway:\n        Generation: 1\n" +
		"backend: hug_e2e_http-echo_443_\n"
	entrySide := "name: hug_tls_21234\n" +
		"binds:\n  v4:\n    port: 21234\n" +
		"tcp_request_rule_list:\n- expr: var(sess.sni),map(/tmp/hug/e2e/maps/hug_tls_21234/sni.map)\n" +
		"metadata:\n  hug:\n    ListenerSet:\n      ns/parity-listenerset:\n        Generation: 1\n" +
		"backend: hug_e2e_http-echo_443_\n"

	got := canonicalizeParityDocument(gatewaySide, ports, differences)
	want := canonicalizeParityDocument(entrySide, ports, differences)
	if got != want {
		t.Fatalf("the two sides are not the same document modulo naming\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestCanonicalizeParityDocumentKeepsBehaviour(t *testing.T) {
	ports := map[int]int{8088: 23456}
	differences := []NamingDifference{{"ns/gw", "ns/ls"}}

	withReject := "name: hug_tls_23456\n- action: reject\n"
	withoutReject := "name: hug_tls_23456\n"

	if canonicalizeParityDocument(withReject, ports, differences) ==
		canonicalizeParityDocument(withoutReject, ports, differences) {
		t.Fatal("a rule difference was canonicalised away: the comparison would pass on two different frontends")
	}
}

// A contributed listener's identity carries the ListenerSet on top of the Gateway's, so a
// Gateway listener's identity is a prefix of it. The rewrite has to consume the long form
// first: applying the short one first leaves the long identity half rewritten, which the
// comparison then reports as a difference the case does not tolerate.
func TestCanonicalizeParityDocumentPrefersTheLongestName(t *testing.T) {
	differences := []NamingDifference{
		{"ns/gw", "ns/ls"},
		{"ns/gw", "ns/gw_ns_ls"},
	}

	gatewayListener := "listener_exact_match.map: parity.haproxy ns/gw_tls\n"
	contributedListener := "listener_exact_match.map: parity.haproxy ns/gw_ns_ls_tls\n"

	got := canonicalizeParityDocument(gatewayListener, nil, differences)
	want := canonicalizeParityDocument(contributedListener, nil, differences)
	if got != want {
		t.Fatalf("the two listener identities are not the same modulo naming\ngot:  %q\nwant: %q", got, want)
	}
}

// A backend port is below the template range and is the same on both sides: erasing it
// would stop the comparison from asserting that both frontends resolve to the same
// backend, which is half of what the cases are for.
func TestCanonicalizeParityDocumentKeepsBackendPorts(t *testing.T) {
	backend := "hug_e2e_http-echo_" + strconv.Itoa(443) + "_"
	got := canonicalizeParityDocument(backend, map[int]int{8088: 23456, 8081: 21234}, nil)
	if !strings.Contains(got, "_443_") {
		t.Fatalf("the backend port was rewritten: %q", got)
	}
}
