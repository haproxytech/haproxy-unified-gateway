// Copyright 2026 HAProxy Technologies LLC
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

package tree

import (
	"strings"
	"testing"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestCheckFiltersRejectsUnsafeValues(t *testing.T) {
	injected := gatewayv1.HTTPRouteFilter{
		Type: gatewayv1.HTTPRouteFilterRequestHeaderModifier,
		RequestHeaderModifier: &gatewayv1.HTTPHeaderFilter{
			Remove: []string{"x\nfrontend injected\n  bind 127.0.0.1:28999"},
		},
	}
	safe := gatewayv1.HTTPRouteFilter{
		Type: gatewayv1.HTTPRouteFilterRequestHeaderModifier,
		RequestHeaderModifier: &gatewayv1.HTTPHeaderFilter{
			Set: []gatewayv1.HTTPHeader{{Name: "X-Foo", Value: "%[src] is literal"}},
		},
	}
	prefixRedirect := gatewayv1.HTTPRouteFilter{
		Type: gatewayv1.HTTPRouteFilterRequestRedirect,
		RequestRedirect: &gatewayv1.HTTPRequestRedirectFilter{Path: &gatewayv1.HTTPPathModifier{
			Type: gatewayv1.PrefixMatchHTTPPathModifier, ReplacePrefixMatch: new("/new"),
		}},
	}
	prefixMatch := func(p string) []gatewayv1.HTTPRouteMatch {
		return []gatewayv1.HTTPRouteMatch{{Path: &gatewayv1.HTTPPathMatch{Type: new(gatewayv1.PathMatchPathPrefix), Value: &p}}}
	}
	tests := []struct {
		name      string
		rule      gatewayv1.HTTPRouteRule
		wantValid bool
	}{
		{"safe rule filter", gatewayv1.HTTPRouteRule{Filters: []gatewayv1.HTTPRouteFilter{safe}}, true},
		{"unsafe rule filter", gatewayv1.HTTPRouteRule{Filters: []gatewayv1.HTTPRouteFilter{injected}}, false},
		{"unsafe backendRef filter", gatewayv1.HTTPRouteRule{BackendRefs: []gatewayv1.HTTPBackendRef{
			{Filters: []gatewayv1.HTTPRouteFilter{injected}},
		}}, false},
		{"prefix redirect on a plain prefix", gatewayv1.HTTPRouteRule{
			Matches: prefixMatch("/old"), Filters: []gatewayv1.HTTPRouteFilter{prefixRedirect},
		}, true},
		{"prefix redirect on a prefix closing the converter", gatewayv1.HTTPRouteRule{
			Matches: prefixMatch("/a,b)"), Filters: []gatewayv1.HTTPRouteFilter{prefixRedirect},
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := &HTTPRouteRule{K8sResource: tt.rule, Valid: true}
			rule.checkFilters()
			if rule.CheckFilters.Valid != tt.wantValid || rule.Valid != tt.wantValid {
				t.Fatalf("CheckFilters.Valid = %v, rule.Valid = %v, want %v", rule.CheckFilters.Valid, rule.Valid, tt.wantValid)
			}
			if tt.wantValid {
				return
			}
			cond, _ := rule.CheckFilters.Conditions.GetCondition("Accepted")
			if cond.Reason != string(gatewayv1.RouteReasonIncompatibleFilters) {
				t.Errorf("reason = %q, want %q", cond.Reason, gatewayv1.RouteReasonIncompatibleFilters)
			}
			if !strings.Contains(cond.Message, "invalid filter") {
				t.Errorf("message %q does not contain %q", cond.Message, "invalid filter")
			}
		})
	}
}
