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
	"testing"
	"time"

	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"
	"github.com/stretchr/testify/suite"
)

const (
	timeout  = time.Second * 30
	interval = time.Second * 1
)

// Adding HTTPRouteTestSuite, just to be able to debug directly
type HTTPRouteTestSuite struct {
	HTTPRouteSuite
}

func TestHTTPRouteTestSuite(t *testing.T) {
	suite.Run(t, new(HTTPRouteTestSuite))
}

func (s *HTTPRouteTestSuite) Test_HTTPRoute_OK() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "basic"

	fixturePath := path.Join(fixtureDirPath, fixtureDir, "ok")
	s.CreateFixtures(fixturePath, nil)
	mapFilePath1 := "hug_http_8080"
	mapFilePath2 := "hug_http_8088"
	defer s.CleanupFixturesCheckMapFiles(fixturePath, nil, []string{mapFilePath1, mapFilePath2})

	// Expected Conditions
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "route-conditions.yaml")
	expectedConditions := s.YamlToRouteConditions(expectedCondPath)

	httpRouteName := "route-echo"
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, httpRouteName, expectedConditions)

	// Check AttachedRoutes on Gateway status
	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http", 1)
	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http2", 1)

	// haproxy.cfg Backends
	backendsExpectationsPath := path.Join(expectationsPath, "backends")
	expectedBackends := []string{"hug_e2e-tests-httproute_http-echo_80__"}
	s.ExpectBackends(s.Test().Ctx, backendsExpectationsPath, expectedBackends)

	// Check Maps
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectListenerRouteMapContents(expectedMapsPath)
	// For FE http
	s.ExpectMapContents(mapFilePath1, expectedMapsPath)

	// For FE https
	s.ExpectMapContents(mapFilePath2, expectedMapsPath)
}

func (s *HTTPRouteTestSuite) Test_HTTPRoute_1_parent_not_allowed() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "basic"
	fixturePath := path.Join(fixtureDirPath, fixtureDir, "1_parent_not_allowed")
	s.CreateFixtures(fixturePath, nil)
	mapFilePath1 := "hug_http_8080"
	defer s.CleanupFixturesCheckMapFiles(fixturePath, nil, []string{mapFilePath1})
	// Expected Conditions
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "route-conditions.yaml")
	expectedConditions := s.YamlToRouteConditions(expectedCondPath)

	httpRouteName := "route-echo"
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, httpRouteName, expectedConditions)

	// Check AttachedRoutes on Gateway status
	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http", 1)
	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http2", 0)

	// Check Maps
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectListenerRouteMapContents(expectedMapsPath)
	// For FE http
	s.ExpectMapContents(mapFilePath1, expectedMapsPath)
}

func (s *HTTPRouteTestSuite) Test_HTTPRoute_no_matching_parent() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "basic"

	fixturePath := path.Join(fixtureDirPath, fixtureDir, "no_matching_parent")
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
	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http2", 0)

	// Check Maps
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectListenerRouteMapContents(expectedMapsPath)
	// For FE http
	s.ExpectMapContents(mapFilePath, expectedMapsPath)
}

func (s *HTTPRouteTestSuite) Test_HTTPRoute_AttachedRoutes() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "attachedroutes"

	fixturePath := path.Join(fixtureDirPath, fixtureDir)
	mapFilePath := "hug_http_8080"
	manifests := []string{"gatewayclass.yaml", "gateway.yaml", "http-echo.yaml", "route.yaml"}
	s.CreateFixtures(fixturePath, manifests)
	defer s.CleanupFixturesCheckMapFiles(fixturePath, manifests, []string{mapFilePath})

	// Expected Conditions
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "route-conditions.yaml")
	expectedConditions := s.YamlToRouteConditions(expectedCondPath)

	httpRouteName := "route-echo"
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, httpRouteName, expectedConditions)

	// Check AttachedRoutes on Gateway status
	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http", 1)
	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http2", 1)

	// Check Maps
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectListenerRouteMapContents(expectedMapsPath)
	s.ExpectMapContents(mapFilePath, expectedMapsPath)

	// 2- Now create a 2nd route
	s.CreateFixtures(fixturePath, []string{"route-2.yaml"})
	expectedCondPath = path.Join(expectationsPath, "route-conditions-2.yaml")
	expectedConditions = s.YamlToRouteConditions(expectedCondPath)

	httpRouteName = "route-echo-2"
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, httpRouteName, expectedConditions)

	// Check AttachedRoutes on Gateway status
	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http", 2) // Now 2 routes are attached
	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http2", 1)

	// 3- Delete the route "route-echo-2"
	s.CleanupFixtures(fixturePath, []string{"route-2.yaml"})

	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http", 1) // Now back to 1 route attached
	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http2", 1)
}

func (s *HTTPRouteTestSuite) Test_HTTPRoute_KO_ResolvedRefs() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "basic"

	fixturePath := path.Join(fixtureDirPath, fixtureDir, "ko_resolvedRef")
	s.CreateFixtures(fixturePath, nil)
	mapFileRelativePath := "hug_http_8080"
	mapFileRelativePath2 := "hug_http_8088"
	defer s.CleanupFixturesCheckMapFiles(fixturePath, nil, []string{mapFileRelativePath, mapFileRelativePath2})

	// Expected Conditions
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "route-conditions.yaml")
	expectedConditions := s.YamlToRouteConditions(expectedCondPath)

	httpRouteName := "route-echo"
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, httpRouteName, expectedConditions)

	// Check AttachedRoutes on Gateway status
	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http", 1)
	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http2", 1)

	// Check Maps
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectMapContents(mapFileRelativePath, expectedMapsPath)
}

func (s *HTTPRouteTestSuite) Test_HTTPRoute_OK_Multiple_Listeners_One_Gateway() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "basic"

	fixturePath := path.Join(fixtureDirPath, fixtureDir, "ok_multiple_listeners_one_gateway")
	s.CreateFixtures(fixturePath, nil)
	mapFilePath1 := "hug_http_8080"
	mapFilePath2 := "hug_http_8088"
	defer s.CleanupFixturesCheckMapFiles(fixturePath, nil, []string{mapFilePath1, mapFilePath2})
	// Expected Conditions
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "route-conditions.yaml")
	expectedConditions := s.YamlToRouteConditions(expectedCondPath)

	httpRouteName := "route-echo"
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, httpRouteName, expectedConditions)

	// Check AttachedRoutes on Gateway status
	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http", 1)
	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http2", 1)

	// haproxy.cfg Backends
	backendsExpectationsPath := path.Join(expectationsPath, "backends")
	expectedBackends := []string{"hug_e2e-tests-httproute_http-echo_80__"}
	s.ExpectBackends(s.Test().Ctx, backendsExpectationsPath, expectedBackends)

	// Check Maps
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectListenerRouteMapContents(expectedMapsPath)
	// For FE http
	s.ExpectMapContents(mapFilePath1, expectedMapsPath)

	// For FE https
	s.ExpectMapContents(mapFilePath2, expectedMapsPath)
}

// Test_HTTPRoute_OK_No_reload_When_Modification_Only_Affect_Maps check that if we change a route and it's only affects
// maps (e.g. Path Prefix) it does not reload.
//
// the test scenario is the following:
//   - create a route with no endpoints and PathPrefix = /path1
//   - edit the route's path prefix to /path2 and check configuration is updated without reload and  route condition is updated. updating route w/o endpoints allows testing conner cases
//   - scale up to 3 endpoints and check configuration is updated without reload.
//   - scale down to 1 endpoints and check configuration is updated without reload.
//   - update route path's PathPrefix = /path1 and check configuration is updated without reload and route condition is updated.
func (s *HTTPRouteTestSuite) Test_HTTPRoute_OK_No_reload_When_Modification_Only_Affect_Maps() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "basic"

	fixturePath := path.Join(fixtureDirPath, fixtureDir, "ok_no_reload_when_modification_only_affect_maps")
	s.CreateFixtures(fixturePath, []string{"gatewayclass.yaml", "gateway.yaml", "http-echo.yaml", "route-path-prefix-1.yaml"})
	mapFilePath1 := "hug_http_8080"
	mapFilePath2 := "hug_http_8088"
	defer s.CleanupFixturesCheckMapFiles(fixturePath, []string{"gatewayclass.yaml", "gateway.yaml", "http-echo.yaml", "route-path-prefix-1.yaml"}, []string{mapFilePath1, mapFilePath2})

	// Expected Conditions
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "route-gen-1-conditions.yaml")
	expectedConditions := s.YamlToRouteConditions(expectedCondPath)

	httpRouteName := "route-echo"
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, httpRouteName, expectedConditions)

	// Check AttachedRoutes on Gateway status
	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http", 1)
	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http2", 1)

	// haproxy.cfg Backends
	const backendName = "hug_e2e-tests-httproute_http-echo_80__"
	expectedBackends := []string{backendName}
	s.ExpectBackends(s.Test().Ctx, path.Join(expectationsPath, "backends-gen-1"), expectedBackends)

	// Check Maps for route-prefix-1 (ie pathPrefix = /path1)
	expectedMapsPathV1 := path.Join(expectationsPath, "maps-path-prefix-1")
	s.ExpectListenerRouteMapContents(expectedMapsPathV1)
	s.ExpectMapContents(mapFilePath1, expectedMapsPathV1) // For FE http
	s.ExpectMapContents(mapFilePath2, expectedMapsPathV1) // For FE https

	// check Server
	s.ExpectServers(backendName, []string{})

	// From now we should not have any reloads
	oldPid := s.WaitForNoReloadsAnyMore(10*time.Second, 2*time.Second)

	s.T().Logf("====================================== Edit route to update Path Prefix (no endpoints) ======================================")
	s.CreateFixtures(fixturePath, []string{"route-path-prefix-2.yaml"}) // no need to clean up it the same obj as route-path-prefix-1.yaml

	expectedConditionsV2 := s.YamlToRouteConditions(path.Join(expectationsPath, "route-gen-2-conditions.yaml"))
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, httpRouteName, expectedConditionsV2)

	// haproxy.cfg Backends
	s.ExpectBackends(s.Test().Ctx, path.Join(expectationsPath, "backends-gen-2-ep-0"), expectedBackends)

	// Check Maps-prefix-2
	expectedMapsPathV2 := path.Join(expectationsPath, "maps-path-prefix-2")
	s.ExpectListenerRouteMapContents(expectedMapsPathV2)
	s.ExpectMapContents(mapFilePath1, expectedMapsPathV2) // For FE http
	s.ExpectMapContents(mapFilePath2, expectedMapsPathV2) // For FE https

	// check Server
	s.ExpectServers(backendName, []string{})

	s.ConsistentlyNoReload(oldPid, 4*time.Second)

	s.T().Logf("====================================== scale up endpoints to 3 ======================================")

	// scale deploy to 3 pods
	s.CreateFixtures(fixturePath, []string{"echo-endpoints-1.yaml"})
	defer s.CleanupFixtures(fixturePath, []string{"echo-endpoints-1.yaml"})
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, httpRouteName, expectedConditionsV2)

	// haproxy.cfg Backends
	s.ExpectBackends(s.Test().Ctx, path.Join(expectationsPath, "backends-gen-2-ep-3"), expectedBackends)

	// Check Maps-prefix-2 (should be the same)
	s.ExpectListenerRouteMapContents(expectedMapsPathV2)
	s.ExpectMapContents(mapFilePath1, expectedMapsPathV2) // For FE http
	s.ExpectMapContents(mapFilePath2, expectedMapsPathV2) // For FE https

	// check Server
	s.ExpectServers(backendName, []string{"SRV_4827409c6115096b8dde5db0092cea2f54213123", "SRV_8ec3713870978506a7ecded834e9907edcf2e619", "SRV_fe3f9ea252b531060fe66a137b37d38263502132"})

	s.ConsistentlyNoReload(oldPid, 4*time.Second)

	s.T().Logf("====================================== scale down endpoint to 1 ======================================")

	// scale deploy to 1 pods
	s.CreateFixtures(fixturePath, []string{"echo-endpoints-2.yaml"}) // no need to clean up it's the same obj as scale=3

	// haproxy.cfg Backends
	s.ExpectBackends(s.Test().Ctx, path.Join(expectationsPath, "backends-gen-2-ep-1"), expectedBackends)

	// Check Maps-prefix-2 (should be the same)
	s.ExpectListenerRouteMapContents(expectedMapsPathV2)
	s.ExpectMapContents(mapFilePath1, expectedMapsPathV2) // For FE http
	s.ExpectMapContents(mapFilePath2, expectedMapsPathV2) // For FE https

	// check Server
	s.ExpectServers(backendName, []string{"SRV_4827409c6115096b8dde5db0092cea2f54213123"})
	s.ConsistentlyNoReload(oldPid, 4*time.Second)

	s.T().Logf("====================================== edit PathPrefix = path1 ======================================")
	s.CreateFixtures(fixturePath, []string{"route-path-prefix-1.yaml"}) // no need to clean up it the same obj as route-path-prefix-1.yaml
	expectedConditionsV3 := s.YamlToRouteConditions(path.Join(expectationsPath, "route-gen-3-conditions.yaml"))
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, httpRouteName, expectedConditionsV3)

	// Check AttachedRoutes on Gateway status
	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http", 1)
	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http2", 1)

	// haproxy.cfg Backends
	s.ExpectBackends(s.Test().Ctx, path.Join(expectationsPath, "backends-gen-3"), expectedBackends)

	// Check Maps for route-path-prefix-1 (ie pathPrefix = /path1)
	s.ExpectListenerRouteMapContents(expectedMapsPathV1)
	s.ExpectMapContents(mapFilePath1, expectedMapsPathV1) // For FE http
	s.ExpectMapContents(mapFilePath2, expectedMapsPathV1) // For FE https

	s.ExpectServers(backendName, []string{"SRV_4827409c6115096b8dde5db0092cea2f54213123"})
	s.ConsistentlyNoReload(oldPid, 4*time.Second)
}
