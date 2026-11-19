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

// trustBackendCRs lists the test namespace as trusted in HugConf, so its
// Backend CRs are applied in full. It returns the function removing the trust.
func (s *HTTPRouteTestSuite) trustBackendCRs() func() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "backend-crd", "trust")
	s.CreateFixturesInNamespace(fixturePath, hugConfNamespace, nil)
	return func() { s.CleanupFixturesInNamespace(fixturePath, hugConfNamespace, nil) }
}

// Test_HTTPRoute_BackendCRD_trust verifies that a Backend CR is reduced to
// tuning fields by default, and that listing or removing its namespace in
// HugConf rebuilds the backend without any change to the route.
func (s *HTTPRouteTestSuite) Test_HTTPRoute_BackendCRD_trust() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "backend-crd", "basic")
	s.CreateFixtures(fixturePath, nil)
	defer s.CleanupFixturesCheckMapFiles(fixturePath, nil, []string{"hug_http_8080"})

	expectationsPath := path.Join(fixturePath, "expectations")
	expectedBackends := []string{"hug_e2e-tests-httproute_http-echo_80_ef84fba452b3f62faf235c47b46c1e54"}

	// Untrusted: the header rules of the Backend CR are dropped.
	s.ExpectBackends(s.IntTest().Ctx, path.Join(expectationsPath, "backends-untrusted"), expectedBackends)

	removeTrust := s.trustBackendCRs()
	s.ExpectBackends(s.IntTest().Ctx, path.Join(expectationsPath, "backends"), expectedBackends)

	removeTrust()
	s.ExpectBackends(s.IntTest().Ctx, path.Join(expectationsPath, "backends-untrusted"), expectedBackends)
}

func (s *HTTPRouteTestSuite) Test_HTTPRoute_BackendCRD_basic() {
	removeTrust := s.trustBackendCRs()
	defer removeTrust()
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "backend-crd"

	fixturePath := path.Join(fixtureDirPath, fixtureDir, "basic")
	s.CreateFixtures(fixturePath, nil)
	mapFileRelativePath := "hug_http_8080"
	defer s.CleanupFixturesCheckMapFiles(fixturePath, nil, []string{mapFileRelativePath})
	// Expected Conditions
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "route-conditions.yaml")
	expectedConditions := s.YamlToRouteConditions(expectedCondPath)

	httpRouteName := "route-echo-backend-cr-basic"
	s.expectConditionsUpdated(s.IntTest().Ctx, s.IntTest().Namespace, httpRouteName, expectedConditions)

	// Check AttachedRoutes on Gateway status
	s.expectAttachedRoute(s.IntTest().Ctx, s.IntTest().Namespace, "gateway", "http", 1)

	// haproxy.cfg Backends
	backendsExpectationsPath := path.Join(expectationsPath, "backends")
	expectedBackends := []string{"hug_e2e-tests-httproute_http-echo_80_ef84fba452b3f62faf235c47b46c1e54"}
	s.ExpectBackends(s.IntTest().Ctx, backendsExpectationsPath, expectedBackends)

	// Check Maps
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectListenerRouteMapContents(expectedMapsPath)
	s.ExpectMapContents(mapFileRelativePath, expectedMapsPath)
}

func (s *HTTPRouteTestSuite) Test_HTTPRoute_BackendCRD_extended() {
	removeTrust := s.trustBackendCRs()
	defer removeTrust()
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "backend-crd"

	fixturePath := path.Join(fixtureDirPath, fixtureDir, "extended")
	s.CreateFixtures(fixturePath, nil)
	mapFileRelativePath := "hug_http_8080"
	defer s.CleanupFixturesCheckMapFiles(fixturePath, nil, []string{mapFileRelativePath})
	// Expected Conditions
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "route-conditions.yaml")
	expectedConditions := s.YamlToRouteConditions(expectedCondPath)

	httpRouteName := "route-echo-backend-cr-basic"
	s.expectConditionsUpdated(s.IntTest().Ctx, s.IntTest().Namespace, httpRouteName, expectedConditions)

	// Check AttachedRoutes on Gateway status
	s.expectAttachedRoute(s.IntTest().Ctx, s.IntTest().Namespace, "gateway", "http", 1)

	// haproxy.cfg Backends
	backendsExpectationsPath := path.Join(expectationsPath, "backends")
	expectedBackends := []string{"hug_e2e-tests-httproute_http-echo_80_870306e1a7d334a1eceb23475e6075aa"}
	s.ExpectBackends(s.IntTest().Ctx, backendsExpectationsPath, expectedBackends)

	// Check Maps
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectListenerRouteMapContents(expectedMapsPath)
	s.ExpectMapContents(mapFileRelativePath, expectedMapsPath)
}
