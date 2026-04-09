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

package httprouteisolation

import (
	"path"
	"testing"

	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"
	"github.com/stretchr/testify/suite"
)

// Adding HTTPRouteTestSuite, just to be able to debug directly
type HTTPRouteIsolationWithHostnameIntersectionTestSuite struct {
	HTTPRouteIsolationSuite
}

func TestHTTPRouteIsolationWithHostnameIntersectionTestSuite(t *testing.T) {
	suite.Run(t, new(HTTPRouteIsolationWithHostnameIntersectionTestSuite))
}

func (s *HTTPRouteIsolationWithHostnameIntersectionTestSuite) Test_HTTPRoute_ListenerIsolation_1_route_attaches_to_empty_hostname() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "listener-isolation-with-hostname-intersection"

	fixturePath := path.Join(fixtureDirPath, fixtureDir, "1-route-attaches-to-empty-hostname")
	s.CreateFixtures(fixturePath, nil)
	mapFilePath := "hug_http_8080"
	defer s.CleanupFixturesCheckMapFiles(fixturePath, nil, []string{mapFilePath})

	// Expected Conditions
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "route-conditions.yaml")
	expectedConditions := s.YamlToRouteConditions(expectedCondPath)

	httpRouteName := "route-attaches-to-empty-hostname"
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, httpRouteName, expectedConditions)

	// Check Maps
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectMapContents(mapFilePath, expectedMapsPath)
}

func (s *HTTPRouteIsolationWithHostnameIntersectionTestSuite) Test_HTTPRoute_ListenerIsolation_2_route_attaches_to_wildcard_example_com() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "listener-isolation-with-hostname-intersection"

	fixturePath := path.Join(fixtureDirPath, fixtureDir, "2-route-attaches-to-wildcard-example-com")
	s.CreateFixtures(fixturePath, nil)
	mapFilePath := "hug_http_8080"
	defer s.CleanupFixturesCheckMapFiles(fixturePath, nil, []string{mapFilePath})

	// Expected Conditions
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "route-conditions.yaml")
	expectedConditions := s.YamlToRouteConditions(expectedCondPath)

	httpRouteName := "route-attaches-to-wildcard-example-com"
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, httpRouteName, expectedConditions)

	// Check Maps
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectMapContents(mapFilePath, expectedMapsPath)
}

func (s *HTTPRouteIsolationWithHostnameIntersectionTestSuite) Test_HTTPRoute_ListenerIsolation_3_route_attaches_to_wildcard_foo_example_com() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "listener-isolation-with-hostname-intersection"

	fixturePath := path.Join(fixtureDirPath, fixtureDir, "3-route-attaches-to-wildcard-foo-example-com")
	s.CreateFixtures(fixturePath, nil)
	mapFilePath := "hug_http_8080"
	defer s.CleanupFixturesCheckMapFiles(fixturePath, nil, []string{mapFilePath})

	// Expected Conditions
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "route-conditions.yaml")
	expectedConditions := s.YamlToRouteConditions(expectedCondPath)

	httpRouteName := "route-attaches-to-wildcard-foo-example-com"
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, httpRouteName, expectedConditions)

	// Check Maps
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectMapContents(mapFilePath, expectedMapsPath)
}

func (s *HTTPRouteIsolationWithHostnameIntersectionTestSuite) Test_HTTPRoute_ListenerIsolation_4_route_attaches_to_abc_foo_example_com() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "listener-isolation-with-hostname-intersection"

	fixturePath := path.Join(fixtureDirPath, fixtureDir, "4-route-attaches-to-abc-foo-example-com")
	s.CreateFixtures(fixturePath, nil)
	mapFilePath := "hug_http_8080"
	defer s.CleanupFixturesCheckMapFiles(fixturePath, nil, []string{mapFilePath})

	// Expected Conditions
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "route-conditions.yaml")
	expectedConditions := s.YamlToRouteConditions(expectedCondPath)

	httpRouteName := "route-attaches-to-abc-foo-example-com"
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, httpRouteName, expectedConditions)

	// Check Maps
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectMapContents(mapFilePath, expectedMapsPath)
}

func (s *HTTPRouteIsolationWithHostnameIntersectionTestSuite) Test_HTTPRoute_ListenerIsolation_5_all() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "listener-isolation-with-hostname-intersection"

	fixturePath := path.Join(fixtureDirPath, fixtureDir, "5-all")
	s.CreateFixtures(fixturePath, nil)
	mapFilePath := "hug_http_8080"
	defer s.CleanupFixturesCheckMapFiles(fixturePath, nil, []string{mapFilePath})

	// Check Maps
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectMapContents(mapFilePath, expectedMapsPath)
}
