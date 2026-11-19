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

func TestCheckFiltersRejectsUnsafePaths(t *testing.T) {
	tests := []struct {
		name      string
		pathType  gatewayv1.PathMatchType
		path      string
		wantValid bool
	}{
		{"plain path is valid", gatewayv1.PathMatchPathPrefix, "/", true},
		{"percent-encoded space is valid", gatewayv1.PathMatchExact, "/my%20file", true},
		{"space in regex path is rejected", gatewayv1.PathMatchRegularExpression, "/x hug_other_vsvc_80__", false},
		{"newline in prefix path is rejected", gatewayv1.PathMatchPathPrefix, "/y\nzz", false},
		{"tab in exact path is rejected", gatewayv1.PathMatchExact, "/a\tb", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := &HTTPRouteRule{
				K8sResource: gatewayv1.HTTPRouteRule{Matches: []gatewayv1.HTTPRouteMatch{
					{Path: &gatewayv1.HTTPPathMatch{Type: &tt.pathType, Value: &tt.path}},
				}},
				Valid: true,
			}
			rule.checkFilters()
			if rule.CheckFilters.Valid != tt.wantValid || rule.Valid != tt.wantValid {
				t.Fatalf("CheckFilters.Valid = %v, rule.Valid = %v, want %v", rule.CheckFilters.Valid, rule.Valid, tt.wantValid)
			}
			if tt.wantValid {
				return
			}
			msg := rule.CheckFilters.Conditions.GetMessage("Accepted")
			if !strings.Contains(msg, "invalid path") {
				t.Errorf("condition message %q does not contain %q", msg, "invalid path")
			}
		})
	}
}
