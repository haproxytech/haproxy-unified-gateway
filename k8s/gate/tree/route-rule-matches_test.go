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

	genericconditions "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	rc "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/routes"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

//go:fix inline
func mptr[T any](v T) *T { return new(v) }

func routeConditionsKO(msg string) genericconditions.Conditions {
	return rc.ConditionKOAcceptedUnsupportedValue(msg)
}

func newTestRouteConditions(t *testing.T) rc.RouteConditions {
	t.Helper()
	conds := rc.RouteConditions{
		ControllerName: "test-controller",
		Conditions:     utils.NewKeyMap[gatewayv1.ParentReference, genericconditions.Conditions](utils.ParentRefToKey),
	}
	conds.MergeOverrideConditionsForParentRef(
		gatewayv1.ParentReference{Name: "gw"}, rc.ConditionAccepted())
	return conds
}

func collectConditionMessages(route *HTTPRoute) string {
	var sb strings.Builder
	route.Conditions.Conditions.Iterate(func(_ string, conds genericconditions.Conditions) bool {
		sb.WriteString(conds.GetMessage(genericconditions.ConditionType(gatewayv1.RouteConditionAccepted)))
		sb.WriteString("|")
		sb.WriteString(conds.GetMessage(genericconditions.ConditionType(gatewayv1.RouteConditionPartiallyInvalid)))
		return true
	})
	return sb.String()
}

func TestCheckMatches(t *testing.T) {
	tests := []struct {
		name      string
		matches   []gatewayv1.HTTPRouteMatch
		wantValid bool
		wantInMsg string
	}{
		{
			name: "no conditions is valid",
			matches: []gatewayv1.HTTPRouteMatch{
				{Path: &gatewayv1.HTTPPathMatch{Type: mptr(gatewayv1.PathMatchPathPrefix), Value: new("/")}},
			},
			wantValid: true,
		},
		{
			name: "valid header regex",
			matches: []gatewayv1.HTTPRouteMatch{
				{Headers: []gatewayv1.HTTPHeaderMatch{{
					Name:  "version",
					Value: "^v[0-9]+$",
					Type:  mptr(gatewayv1.HeaderMatchRegularExpression),
				}}},
			},
			wantValid: true,
		},
		{
			name: "invalid header regex is rejected",
			matches: []gatewayv1.HTTPRouteMatch{
				{Headers: []gatewayv1.HTTPHeaderMatch{{
					Name:  "version",
					Value: "one(?=two)",
					Type:  mptr(gatewayv1.HeaderMatchRegularExpression),
				}}},
			},
			wantValid: false,
			wantInMsg: "invalid header",
		},
		{
			name: "invalid query regex is rejected",
			matches: []gatewayv1.HTTPRouteMatch{
				{QueryParams: []gatewayv1.HTTPQueryParamMatch{{
					Name:  "animal",
					Value: "(unclosed",
					Type:  mptr(gatewayv1.QueryParamMatchRegularExpression),
				}}},
			},
			wantValid: false,
			wantInMsg: "invalid query param",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := &HTTPRouteRule{
				K8sResource: gatewayv1.HTTPRouteRule{Matches: tt.matches},
				Valid:       true,
			}
			rule.checkMatches()
			if rule.CheckMatches.Valid != tt.wantValid {
				t.Fatalf("CheckMatches.Valid = %v, want %v (conds %+v)", rule.CheckMatches.Valid, tt.wantValid, rule.CheckMatches.Conditions)
			}
			if tt.wantValid {
				if !rule.Valid {
					t.Error("valid matches must not invalidate the rule")
				}
				return
			}
			if rule.Valid {
				t.Error("invalid matches must invalidate the rule")
			}
			msg := rule.CheckMatches.Conditions.GetMessage("Accepted")
			if !strings.Contains(msg, tt.wantInMsg) {
				t.Errorf("condition message %q does not contain %q", msg, tt.wantInMsg)
			}
		})
	}
}

func TestCheckMatchesSizeGuard(t *testing.T) {
	// A single rule whose encoded conditions exceed the per-rule cap.
	longValue := strings.Repeat("a", 5000)
	rule := &HTTPRouteRule{
		K8sResource: gatewayv1.HTTPRouteRule{
			Matches: []gatewayv1.HTTPRouteMatch{
				{Headers: []gatewayv1.HTTPHeaderMatch{{Name: "x-long", Value: longValue}}},
			},
		},
		Valid: true,
	}
	rule.checkMatches()
	if rule.CheckMatches.Valid {
		t.Fatal("oversized match conditions must be rejected")
	}
	msg := rule.CheckMatches.Conditions.GetMessage("Accepted")
	if !strings.Contains(msg, "above the") {
		t.Errorf("unexpected message: %q", msg)
	}
}

func TestMergeMatchConditionsPartiallyInvalid(t *testing.T) {
	route := &HTTPRoute{
		Rules: []*HTTPRouteRule{
			{Valid: true, CheckMatches: CheckResult{Valid: true}},
			{Valid: false, CheckMatches: CheckResult{
				Valid:      false,
				Conditions: routeConditionsKO("invalid header \"version\" regex \"one(?=two)\""),
			}},
		},
	}
	route.Conditions = newTestRouteConditions(t)

	route.mergeMatchConditions()

	msg := collectConditionMessages(route)
	if !strings.Contains(msg, "Dropped Rule") {
		t.Errorf("partially invalid route must surface 'Dropped Rule', got %q", msg)
	}
	if !strings.Contains(msg, "rule 1") {
		t.Errorf("message must name the dropped rule, got %q", msg)
	}
}

func TestMergeMatchConditionsAllInvalid(t *testing.T) {
	route := &HTTPRoute{
		Rules: []*HTTPRouteRule{
			{Valid: false, CheckMatches: CheckResult{
				Valid:      false,
				Conditions: routeConditionsKO("invalid header regex"),
			}},
		},
	}
	route.Conditions = newTestRouteConditions(t)

	route.mergeMatchConditions()

	msg := collectConditionMessages(route)
	if strings.Contains(msg, "Dropped Rule") {
		t.Errorf("fully invalid route must not claim partial, got %q", msg)
	}
	if !strings.Contains(msg, "rule 0") {
		t.Errorf("message must name the invalid rule, got %q", msg)
	}
}

func TestMergeMatchConditionsNoFailureNoCondition(t *testing.T) {
	route := &HTTPRoute{
		Rules: []*HTTPRouteRule{
			{Valid: true, CheckMatches: CheckResult{Valid: true}},
		},
	}
	route.Conditions = newTestRouteConditions(t)

	route.mergeMatchConditions()

	msg := collectConditionMessages(route)
	if msg != "Route Accepted|" {
		t.Errorf("valid matches must not change conditions, got %q", msg)
	}
}
