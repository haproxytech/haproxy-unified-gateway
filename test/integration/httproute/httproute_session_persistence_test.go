//
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

package httproute

import (
	"path"

	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"
)

func (s *HTTPRouteTestSuite) Test_HTTPRoute_SessionPersistence_Unnamed() {
	s.RunHTTPRouteSessionPersistenceTest("session-persistence-unnamed", true)
}

func (s *HTTPRouteTestSuite) Test_HTTPRoute_SessionPersistence_Named() {
	s.RunHTTPRouteSessionPersistenceTest("session-persistence-named", true)
}

func (s *HTTPRouteTestSuite) Test_HTTPRoute_SessionPersistence_Absolutetimeout() {
	s.RunHTTPRouteSessionPersistenceTest("session-persistence-absolutetimeout", true)
}

// Not implemented yet
// func (s *HTTPRouteTestSuite) Test_HTTPRoute_SessionPersistence_Header() {
// 	s.RunHTTPRouteSessionPersistenceTest("session-persistence-header", true)
// }

// Not implemented yet
// func (s *HTTPRouteTestSuite) Test_HTTPRoute_SessionPersistence_CookieConfig_Permanent_Absolutetimeout() {
// 	s.RunHTTPRouteSessionPersistenceTest("session-persistence-absolutetimeout-cookieconfig-permanent", true)
// }

// Not implemented yet
// func (s *HTTPRouteTestSuite) Test_HTTPRoute_SessionPersistence_CookieConfig_Session_Absolutetimeout() {
// 	s.RunHTTPRouteSessionPersistenceTest("session-persistence-absolutetimeout-cookieconfig-session", true)
// }

func (s *HTTPRouteTestSuite) Test_HTTPRoute_SessionPersistence_Idletimeout() {
	s.RunHTTPRouteSessionPersistenceTest("session-persistence-idletimeout", true)
}

func (s *HTTPRouteTestSuite) Test_HTTPRoute_SessionPersistence_Named_Removed() {
	fixtureTestDir := "session-persistence-named-removed"
	s.RunHTTPRouteSessionPersistenceTest(fixtureTestDir, false)

	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "session_persistence"
	fixturePath := path.Join(fixtureDirPath, fixtureDir, fixtureTestDir, "route-session-persistence-removed")
	s.CreateFixtures(fixturePath, nil)
	mapFileRelativePath := "hug_http_8080"
	defer s.CleanupFixturesCheckMapFiles(fixturePath, nil, []string{mapFileRelativePath})
	// Clean up parent fixtures (gateway, http-echo) before the route-update cleanup runs,
	// so the gateway is gone before ExpectListenerRouteMapContents("") is checked.
	// Route is intentionally excluded here: it is deleted by CleanupFixturesCheckMapFiles above.
	defer s.CleanupFixtures(path.Join(fixtureDirPath, fixtureDir, fixtureTestDir), []string{"gatewayclass.yaml", "gateway.yaml", "http-echo.yaml"})

	expectationsPath := path.Join(fixtureDirPath, fixtureDir, fixtureTestDir, "expectations")
	expectedCondPath := path.Join(expectationsPath, "route-conditions.yaml")
	s.YamlToRouteConditions(expectedCondPath)

	backendsRemovedExpectationsPath := path.Join(fixtureDirPath, fixtureDir, fixtureTestDir, "expectations", "backends-session-persistence-removed")
	expectedBackends := []string{
		"hug_e2e-tests-httproute_http-echo_80__",
	}

	s.ExpectBackends(s.Test().Ctx, backendsRemovedExpectationsPath, expectedBackends)
}

//revive:disable:flag-parameter
func (s *HTTPRouteTestSuite) RunHTTPRouteSessionPersistenceTest(fixtureTestDir string, cleanup bool) {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "session_persistence"

	fixturePath := path.Join(fixtureDirPath, fixtureDir, fixtureTestDir)
	s.CreateFixtures(fixturePath, nil)
	mapFileRelativePath := "hug_http_8080"
	if cleanup {
		defer s.CleanupFixturesCheckMapFiles(fixturePath, nil, []string{mapFileRelativePath})
	}
	// Expected Conditions
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "route-conditions.yaml")
	expectedConditions := s.YamlToRouteConditions(expectedCondPath)

	httpRouteName := "route-echo"
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, httpRouteName, expectedConditions)

	// Check AttachedRoutes on Gateway status
	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http", 1)

	// haproxy.cfg Backends
	backendsExpectationsPath := path.Join(expectationsPath, "backends")
	expectedBackends := []string{
		"hug_e2e-tests-httproute_http-echo_80__",
	}

	s.ExpectBackends(s.Test().Ctx, backendsExpectationsPath, expectedBackends)

	// Check Maps
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectListenerRouteMapContents(expectedMapsPath)
	s.ExpectMapContents(mapFileRelativePath, expectedMapsPath)
}
