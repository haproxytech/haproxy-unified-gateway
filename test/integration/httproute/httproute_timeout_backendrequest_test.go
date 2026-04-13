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

func (s *HTTPRouteTestSuite) Test_Timeout_OK() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "timeout"

	fixturePath := path.Join(fixtureDirPath, fixtureDir, "backendrequest_ok")
	s.CreateFixtures(fixturePath, nil)
	mapFilePath := "hug_http_8080"
	defer s.CleanupFixturesCheckMapFiles(fixturePath, nil, []string{mapFilePath})

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
	expectedBackends := []string{"hug_e2e-tests-httproute_http-echo_80__"}
	s.ExpectBackends(s.Test().Ctx, backendsExpectationsPath, expectedBackends)

	// Check Maps
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectListenerRouteMapContents(expectedMapsPath)
	// For FE http
	s.ExpectMapContents(mapFilePath, expectedMapsPath)
}

func (s *HTTPRouteTestSuite) Test_Timeout_Update() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "timeout"

	fixturePath := path.Join(fixtureDirPath, fixtureDir, "backendrequest_ok")
	s.CreateFixtures(fixturePath, nil)
	mapFilePath := "hug_http_8080"
	defer s.CleanupFixturesCheckMapFiles(fixturePath, nil, []string{mapFilePath})

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
	expectedBackends := []string{"hug_e2e-tests-httproute_http-echo_80__"}
	s.ExpectBackends(s.Test().Ctx, backendsExpectationsPath, expectedBackends)

	// Check Maps
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectListenerRouteMapContents(expectedMapsPath)
	// For FE http
	s.ExpectMapContents(mapFilePath, expectedMapsPath)

	fixturePath = path.Join(fixtureDirPath, fixtureDir, "backendrequest_update")
	expectationsPath = path.Join(fixturePath, "expectations")
	backendsExpectationsPath = path.Join(expectationsPath, "backends")
	expectedBackends = []string{"hug_e2e-tests-httproute_http-echo_80__"}
	s.CreateFixtures(fixturePath, []string{"route.yaml"})
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, httpRouteName, expectedConditions)

	// Check AttachedRoutes on Gateway status
	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http", 1)
	s.ExpectBackends(s.Test().Ctx, backendsExpectationsPath, expectedBackends)
}

func (s *HTTPRouteTestSuite) Test_Timeout_Removal() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "timeout"

	fixturePath := path.Join(fixtureDirPath, fixtureDir, "backendrequest_ok")
	s.CreateFixtures(fixturePath, nil)
	mapFilePath := "hug_http_8080"
	defer s.CleanupFixturesCheckMapFiles(fixturePath, nil, []string{mapFilePath})

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
	expectedBackends := []string{"hug_e2e-tests-httproute_http-echo_80__"}
	s.ExpectBackends(s.Test().Ctx, backendsExpectationsPath, expectedBackends)

	// Check Maps
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectListenerRouteMapContents(expectedMapsPath)
	// For FE http
	s.ExpectMapContents(mapFilePath, expectedMapsPath)

	fixturePath = path.Join(fixtureDirPath, fixtureDir, "backendrequest_removal")
	expectationsPath = path.Join(fixturePath, "expectations")
	backendsExpectationsPath = path.Join(expectationsPath, "backends")
	expectedBackends = []string{"hug_e2e-tests-httproute_http-echo_80__"}
	s.CreateFixtures(fixturePath, []string{"route.yaml"})
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, httpRouteName, expectedConditions)

	// Check AttachedRoutes on Gateway status
	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http", 1)
	s.ExpectBackends(s.Test().Ctx, backendsExpectationsPath, expectedBackends)
}
