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

	"github.com/haproxytech/client-native/v6/models"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/storage"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/protocols"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"
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

// httpFastPathRules is a shared fixture for the fast-path rule-set tests.
func httpFastPathRules(t *testing.T) ([]*models.HTTPRequestRule, map[string]*models.ACL) {
	t.Helper()
	httpRules, aclList := httpFrontendRules(
		"/maps/fe/listener_exact_match.map",
		"/maps/fe/listener_wildcard_match.map",
		"/maps/fe/listener_route_exact_match.map",
		"/maps/fe/listener_route_wildcard_match.map",
		"/maps/fe/path_exact.map",
		"/maps/fe/path_prefix.map",
		"/maps/fe/path_regex.map",
		"/maps/fe",
	)
	acls := map[string]*models.ACL{}
	for _, a := range aclList {
		acls[a.ACLName] = a
	}
	return httpRules, acls
}

func TestHTTPFrontendRulesFastPathACLs(t *testing.T) {
	_, acls := httpFastPathRules(t)
	want := map[string]string{
		"lr_exact_single": "-m reg ^[^,]+$",
		"lr_wild_single":  "-m reg ^[^,]+$",
		"lr_exact_found":  "-m found",
		"lr_wild_found":   "-m found",
		"fast_path":       "-m found",
	}
	for name, value := range want {
		acl, ok := acls[name]
		if !ok {
			t.Fatalf("missing ACL %q", name)
		}
		if acl.Value != value {
			t.Errorf("ACL %q value = %q, want %q", name, acl.Value, value)
		}
	}
	for _, name := range []string{"route_is_json", "_preload_path_exact", "_preload_path_prefix", "_preload_path_regex"} {
		if _, ok := acls[name]; !ok {
			t.Fatalf("missing ACL %q", name)
		}
	}
}

// The two candidate-pick rules must be mutually exclusive: each requires its
// own source to be a single entry and the other source to be absent, so
// txn.lr being set marks the fast path as active.
func TestHTTPFrontendRulesFastPathCandidatePick(t *testing.T) {
	httpRules, _ := httpFastPathRules(t)
	want := map[string]string{
		"lr_exact_single !lr_wild_found": "var(txn.selected_listener_route)",
		"lr_wild_single !lr_exact_found": "var(txn.selected_listener_route_wildcard)",
	}
	seen := map[string]bool{}
	for _, r := range httpRules {
		if r.Type != "set-var" || r.VarName != "lr" {
			continue
		}
		expr, ok := want[r.CondTest]
		if !ok {
			t.Errorf("unexpected condition on txn.lr rule: %q", r.CondTest)
			continue
		}
		if r.VarExpr != expr {
			t.Errorf("txn.lr rule with condition %q has expr %q, want %q", r.CondTest, r.VarExpr, expr)
		}
		seen[r.CondTest] = true
	}
	for cond := range want {
		if !seen[cond] {
			t.Errorf("missing candidate-pick rule with condition %q", cond)
		}
	}
}

// The lookup chain must mirror lua find_route specificity: exact first, then
// longest prefix (map_beg), then regex — with base_listener_route kept for
// logging parity with the Lua fallback.
func TestHTTPFrontendRulesFastPathLookupChain(t *testing.T) {
	httpRules, _ := httpFastPathRules(t)
	var chain []*models.HTTPRequestRule
	var blr bool
	for _, r := range httpRules {
		switch {
		case r.Type == "set-var" && r.VarName == "base_listener_route" && r.CondTest == "fast_path":
			blr = true
		case r.Type == "set-var" && r.CondTest == "fast_path" && strings.HasPrefix(r.VarName, "route"):
			chain = append(chain, r)
		}
	}
	if !blr {
		t.Error("fast path must set txn.base_listener_route")
	}
	if len(chain) != 3 {
		t.Fatalf("expected 3 fast-path lookup rules, got %d", len(chain))
	}
	wantExpr := []string{"map(", "map_beg(", "map_reg("}
	wantVar := []string{"route", "route,ifnotexists", "route,ifnotexists"}
	for i, r := range chain {
		if r.VarName != wantVar[i] || !strings.Contains(r.VarExpr, wantExpr[i]) {
			t.Errorf("lookup rule %d: var=%q expr=%q, want var=%q containing %q",
				i, r.VarName, r.VarExpr, wantVar[i], wantExpr[i])
		}
	}
}

// find_route must only run off the fast path; lua.route stays gated on
// route_is_json; and the whole fast-path block must sit between the
// listener-route selection and lua.route.
func TestHTTPFrontendRulesFastPathOrderingAndFallback(t *testing.T) {
	httpRules, _ := httpFastPathRules(t)
	var wildSet, lrPick, routeLua int = -1, -1, -1
	for i, r := range httpRules {
		switch {
		case r.Type == "lua" && r.LuaAction == "find_route":
			if r.Cond != "if" || r.CondTest != "!fast_path" {
				t.Errorf("find_route fallback must run only when the fast path is inactive, got cond=%q test=%q", r.Cond, r.CondTest)
			}
		case r.Type == "lua" && r.LuaAction == "route":
			if r.CondTest != "route_is_json" {
				t.Errorf("lua.route must stay gated on route_is_json, got %q", r.CondTest)
			}
			routeLua = i
		case r.Type == "set-var" && r.VarName == "selected_listener_route_wildcard":
			wildSet = i
		case r.Type == "set-var" && r.VarName == "lr" && lrPick == -1:
			lrPick = i
		}
	}
	if wildSet == -1 || lrPick == -1 || routeLua == -1 {
		t.Fatal("missing listener-route selection, txn.lr pick or lua.route rules")
	}
	if wildSet > lrPick {
		t.Errorf("txn.lr pick (rule %d) must come after listener-route selection (rule %d)", lrPick, wildSet)
	}
	if routeLua < lrPick {
		t.Errorf("lua.route (rule %d) must come after the fast-path rules (rule %d)", routeLua, lrPick)
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

// TestBindParamsAcceptProxy checks that AcceptProxy reaches the binds of every
// listener frontend whatever its protocol category, and leaves the TLS
// termination parameters alone.
func TestBindParamsAcceptProxy(t *testing.T) {
	certStorage := &storage.CertificateStorageDefault{
		CertFilesBaseDir: "/etc/haproxy/crt-lists",
		LinkID:           "hug",
	}
	tests := []struct {
		name        string
		category    protocols.ProtocolCategory
		acceptProxy bool
		wantSSL     bool
	}{
		{name: "http", category: protocols.ProtocolCategoryInsecure},
		{name: "http accept-proxy", category: protocols.ProtocolCategoryInsecure, acceptProxy: true},
		{name: "https", category: protocols.ProtocolCategorySecure, wantSSL: true},
		{name: "https accept-proxy", category: protocols.ProtocolCategorySecure, acceptProxy: true, wantSSL: true},
		{name: "tls passthrough", category: protocols.ProtocolCategoryTLS},
		{name: "tls passthrough accept-proxy", category: protocols.ProtocolCategoryTLS, acceptProxy: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &HaproxyConfMgrImpl{
				params: HaproxyConfMgrParams{
					certificateStorage: certStorage,
					HaproxyConfParams:  HaproxyConfParams{AcceptProxy: tt.acceptProxy},
				},
			}
			vl := tree.NewVirtualListener(tt.category, 443)
			got := b.bindParams("hug_"+vl.Name, vl.Name, vl)

			if got.AcceptProxy != tt.acceptProxy {
				t.Errorf("AcceptProxy = %v, want %v", got.AcceptProxy, tt.acceptProxy)
			}
			if got.Ssl != tt.wantSSL {
				t.Errorf("Ssl = %v, want %v", got.Ssl, tt.wantSSL)
			}
			wantCrtList := ""
			if tt.wantSSL {
				wantCrtList = "/etc/haproxy/crt-lists/hug_" + vl.Name + ".list"
			}
			if got.CrtList != wantCrtList {
				t.Errorf("CrtList = %q, want %q", got.CrtList, wantCrtList)
			}
		})
	}
}
