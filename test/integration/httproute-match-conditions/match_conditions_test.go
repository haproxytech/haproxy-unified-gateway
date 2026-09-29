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

package httproutematchconditions

import (
	"path"

	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"
)

// Test_MatchConditions programs a route whose rules carry header, method and
// query-param matches. All rules default to path prefix "/", so they all land
// in the same map entry and the golden value shows the conditional format
// with candidates ordered by the Gateway API precedence rules: method,
// header count, query count, then rule order, unconditional fallback last.
//
// A second route with a PCRE-only header regex is dropped from programming
// with a PartiallyInvalid condition, proving match validation reaches status.
// Both routes share the hostname, so the listener-route map also carries the
// comma-separated multi-candidate value exercised by lua.find_route.
func (s *MatchConditionsSuite) Test_MatchConditions() {
	fixtureDirPath := utils.GetCRDFixturePath()

	manifests := []string{
		"echo.yaml",
		"gateway.yaml",
		"gatewayclass.yaml",
		"route.yaml",
		"route-invalid-regex.yaml",
	}
	s.CreateFixtures(fixtureDirPath, manifests)
	defer s.CleanupFixtures(fixtureDirPath, manifests)

	expectationsPath := path.Join(fixtureDirPath, "expectations")

	// The valid route is Accepted with all references resolved.
	s.ExpectRouteConditionsUpdated(s.Test().Ctx, s.Test().Namespace, "route-match-conditions",
		s.YamlToRouteConditions(path.Join(expectationsPath, "route-conditions.yaml")))

	// The route with a PCRE-only header regex drops that rule and reports
	// PartiallyInvalid with the UnsupportedValue reason.
	s.ExpectRouteConditionsUpdated(s.Test().Ctx, s.Test().Namespace, "route-invalid-regex",
		s.YamlToRouteConditions(path.Join(expectationsPath, "route-invalid-conditions.yaml")))

	// Both routes attach to the http listener.
	s.ExpectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "hug-gateway", "http", 2)

	// Map goldens: conditional path value + multi-candidate listener-route.
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectMapContents("hug_http_31081", expectedMapsPath)
}
