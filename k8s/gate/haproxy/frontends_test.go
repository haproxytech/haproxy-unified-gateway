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

import (
	"regexp"
	"strings"
	"testing"
)

// TestTLSPassthroughRulesScopeConsistency guards against scope drift between
// the TCP set-var rules and the ACL that reads those variables. A mismatch
// (e.g. writing sess.sni_match but reading var(txn.sni_match)) leaves the
// route_is_json ACL permanently dead, silently disabling the lua-JSON
// backend routing branch.
func TestTLSPassthroughRulesScopeConsistency(t *testing.T) {
	tcpRules, _, aclList := tlsPassthroughRules(
		"/etc/haproxy/listener_exact_match.map",
		"/etc/haproxy/listener_wildcard_match.map",
		"/etc/haproxy/listener_route_exact_match.map",
		"/etc/haproxy/listener_route_wildcard_match.map",
		"/etc/haproxy/sni.map",
	)

	// Collect the scope each variable is written in by the set-var rules.
	writeScope := map[string]string{}
	for _, r := range tcpRules {
		if r.Action != "set-var" {
			continue
		}
		// Strip the ",ifnotexists" flag from names like "selected_listener_route,ifnotexists".
		name, _, _ := strings.Cut(r.VarName, ",")
		writeScope[name] = r.VarScope
	}

	if got := writeScope["sni_match"]; got != "sess" {
		t.Fatalf("sni_match must be written in sess scope, got %q", got)
	}

	// Every var(scope.name) reference in every ACL criterion must match the
	// scope the variable was set in.
	varRef := regexp.MustCompile(`var\(([a-z]+)\.([A-Za-z_][A-Za-z0-9_]*)\)`)
	for _, acl := range aclList {
		for _, m := range varRef.FindAllStringSubmatch(acl.Criterion, -1) {
			refScope, varName := m[1], m[2]
			wantScope, ok := writeScope[varName]
			if !ok {
				// Variable not written by this rule set (e.g. txn.backend set
				// by lua). Skip — invariant only covers locally-written vars.
				continue
			}
			if refScope != wantScope {
				t.Errorf("ACL %q criterion %q references var(%s.%s) but the variable is written in %s scope",
					acl.ACLName, acl.Criterion, refScope, varName, wantScope)
			}
		}
	}
}

// TestTLSPassthroughRulesRouteIsJSONReadsSessScope is an explicit regression
// test for the blocker where the route_is_json ACL read var(txn.sni_match)
// while the rule set writes sess.sni_match.
func TestTLSPassthroughRulesRouteIsJSONReadsSessScope(t *testing.T) {
	_, _, aclList := tlsPassthroughRules("a", "b", "c", "d", "e")
	var found bool
	for _, acl := range aclList {
		if acl.ACLName != "route_is_json" {
			continue
		}
		found = true
		if !regexp.MustCompile(`var\(sess\.sni_match\)`).MatchString(acl.Criterion) {
			t.Errorf("route_is_json must read var(sess.sni_match); got criterion %q", acl.Criterion)
		}
		if regexp.MustCompile(`var\(txn\.sni_match\)`).MatchString(acl.Criterion) {
			t.Errorf("route_is_json still reads var(txn.sni_match); criterion %q", acl.Criterion)
		}
	}
	if !found {
		t.Fatal("route_is_json ACL not found in TLS passthrough rule set")
	}
}
