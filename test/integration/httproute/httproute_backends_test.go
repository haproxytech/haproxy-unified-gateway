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
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (s *HTTPRouteTestSuite) Test_HTTPRoute_Backend_1_route() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "backends"

	fixturePath := path.Join(fixtureDirPath, fixtureDir, "1_route")
	manifests := []string{"gateway.yaml", "gatewayclass.yaml", "http-echo-1.yaml", "http-echo-2.yaml", "http-echo-3.yaml", "route-1.yaml"}
	mapFileRelativePath := "hug_http_8080"
	s.CreateFixtures(fixturePath, manifests)
	defer s.CleanupFixturesCheckMapFiles(fixturePath, manifests, []string{mapFileRelativePath})

	// Expected Conditions
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "route-conditions.yaml")
	expectedConditions := s.YamlToRouteConditions(expectedCondPath)

	httpRouteName := "route-echo-1"
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, httpRouteName, expectedConditions)

	// Check AttachedRoutes on Gateway status
	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http", 1)

	// haproxy.cfg Backends
	backendsExpectationsPath := path.Join(expectationsPath, "backends")
	expectedBackends := []string{"hug_e2e-tests-httproute_http-echo-1_80__", "hug_e2e-tests-httproute_http-echo-2_80__", "hug_e2e-tests-httproute_http-echo-3_80__"}
	s.ExpectBackends(s.Test().Ctx, backendsExpectationsPath, expectedBackends)

	// Check Maps
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectMapContents(mapFileRelativePath, expectedMapsPath)
}

func (s *HTTPRouteTestSuite) Test_HTTPRoute_Backend_1_route_filter() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "backends"
	mapFileRelativePath := "hug_http_8080"
	fixturePath := path.Join(fixtureDirPath, fixtureDir, "1_route_filter")
	s.CreateFixtures(fixturePath, nil)
	defer s.CleanupFixturesCheckMapFiles(fixturePath, nil, []string{mapFileRelativePath})

	// Expected Conditions
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "route-conditions.yaml")
	expectedConditions := s.YamlToRouteConditions(expectedCondPath)

	httpRouteName := "route-echo-1"
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, httpRouteName, expectedConditions)

	// Check AttachedRoutes on Gateway status
	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http", 1)

	// haproxy.cfg Backends
	backendsExpectationsPath := path.Join(expectationsPath, "backends")
	expectedBackends := []string{
		"hug_e2e-tests-httproute_http-echo-1_80__",
		"hug_e2e-tests-httproute_http-echo-2_80_e771b6a447417b25dc95464a26a6cc87",
		"hug_e2e-tests-httproute_http-echo-3_80__",
	}
	s.ExpectBackends(s.Test().Ctx, backendsExpectationsPath, expectedBackends)

	// Check Maps
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectMapContents(mapFileRelativePath, expectedMapsPath)
}

func (s *HTTPRouteTestSuite) Test_HTTPRoute_Backend_1_route_response_header_filter() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "backends"
	mapFileRelativePath := "hug_http_8080"
	fixturePath := path.Join(fixtureDirPath, fixtureDir, "1_route_response_header_filter")
	s.CreateFixtures(fixturePath, nil)
	defer s.CleanupFixturesCheckMapFiles(fixturePath, nil, []string{mapFileRelativePath})

	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "route-conditions.yaml")
	expectedConditions := s.YamlToRouteConditions(expectedCondPath)

	httpRouteName := "route-echo-1"
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, httpRouteName, expectedConditions)

	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http", 1)

	backendsExpectationsPath := path.Join(expectationsPath, "backends")
	expectedBackends := []string{
		"hug_e2e-tests-httproute_http-echo-1_80_94509efedc7f21eaa984f3a51d5d6edc",
	}
	s.ExpectBackends(s.Test().Ctx, backendsExpectationsPath, expectedBackends)

	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectMapContents(mapFileRelativePath, expectedMapsPath)
}

func (s *HTTPRouteTestSuite) Test_HTTPRoute_Backend_1_route_url_rewrite() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "backends"
	mapFileRelativePath := "hug_http_8080"
	fixturePath := path.Join(fixtureDirPath, fixtureDir, "1_route_url_rewrite")
	s.CreateFixtures(fixturePath, nil)
	defer s.CleanupFixturesCheckMapFiles(fixturePath, nil, []string{mapFileRelativePath})

	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "route-conditions.yaml")
	expectedConditions := s.YamlToRouteConditions(expectedCondPath)

	httpRouteName := "route-echo-1"
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, httpRouteName, expectedConditions)

	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http", 1)

	backendsExpectationsPath := path.Join(expectationsPath, "backends")
	expectedBackends := []string{
		"hug_e2e-tests-httproute_http-echo-1_80_dabac5b6954afe29685ddd2d2e164b96",
	}
	s.ExpectBackends(s.Test().Ctx, backendsExpectationsPath, expectedBackends)

	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectMapContents(mapFileRelativePath, expectedMapsPath)
}

func (s *HTTPRouteTestSuite) Test_HTTPRoute_Backend_1_route_redirect() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "backends"
	mapFileRelativePath := "hug_http_8080"
	fixturePath := path.Join(fixtureDirPath, fixtureDir, "1_route_redirect")
	s.CreateFixtures(fixturePath, nil)
	defer s.CleanupFixturesCheckMapFiles(fixturePath, nil, []string{mapFileRelativePath})

	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "route-conditions.yaml")
	expectedConditions := s.YamlToRouteConditions(expectedCondPath)

	httpRouteName := "route-echo-1"
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, httpRouteName, expectedConditions)

	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http", 1)

	backendsExpectationsPath := path.Join(expectationsPath, "backends")
	expectedBackends := []string{
		"hug_redirect_0a19443037896884091efc4f20923e4d",
	}
	s.ExpectBackends(s.Test().Ctx, backendsExpectationsPath, expectedBackends)

	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectMapContents(mapFileRelativePath, expectedMapsPath)
}

func (s *HTTPRouteTestSuite) Test_HTTPRoute_Backend_1_route_dynamic_delete_1_backend() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "backends"
	fixturePath := path.Join(fixtureDirPath, fixtureDir, "1_route")
	manifests := []string{"gateway.yaml", "gatewayclass.yaml", "http-echo-1.yaml", "http-echo-2.yaml", "http-echo-3.yaml", "route-1.yaml"}
	s.CreateFixtures(fixturePath, manifests)
	mapFileRelativePath := "hug_http_8080"
	defer s.CleanupFixturesCheckMapFiles(fixturePath, manifests, []string{mapFileRelativePath})

	// Expected Conditions
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "route-conditions.yaml")
	expectedConditions := s.YamlToRouteConditions(expectedCondPath)

	httpRouteName := "route-echo-1"
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, httpRouteName, expectedConditions)

	// Check AttachedRoutes on Gateway status
	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http", 1)

	// haproxy.cfg Backends
	backendsExpectationsPath := path.Join(expectationsPath, "backends")
	expectedBackends := []string{"hug_e2e-tests-httproute_http-echo-1_80__", "hug_e2e-tests-httproute_http-echo-2_80__", "hug_e2e-tests-httproute_http-echo-3_80__"}
	s.ExpectBackends(s.Test().Ctx, backendsExpectationsPath, expectedBackends)

	// Check Maps
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectMapContents(mapFileRelativePath, expectedMapsPath)

	// - name: http-echo-2
	//   port: 80
	//   weight: 90
	// from route-1
	// Backend hug_e2e-tests-httproute_http-echo-2_80__ should be deleted
	manifests = []string{"route-1-v2.yaml"}
	s.CreateFixtures(fixturePath, manifests)
	backendsExpectationsPath = path.Join(backendsExpectationsPath, "v2")
	expectedBackends = []string{"hug_e2e-tests-httproute_http-echo-1_80__", "hug_e2e-tests-httproute_http-echo-3_80__"}
	s.ExpectBackends(s.Test().Ctx, backendsExpectationsPath, expectedBackends)
	s.ExpectBackendsDoNotExist(s.Test().Ctx, "hug_e2e-tests-httproute_http-echo-2_80__")

	// Check Maps
	expectedMapsPath = path.Join(expectedMapsPath, "v2")
	s.ExpectMapContents(mapFileRelativePath, expectedMapsPath)
}

func (s *HTTPRouteTestSuite) Test_HTTPRoute_Backend_1_route_1_backend_dynamic_delete_service() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "backends"

	fixturePath := path.Join(fixtureDirPath, fixtureDir, "1_route_1_backend")
	manifests := []string{"gateway.yaml", "gatewayclass.yaml", "http-echo-1.yaml", "route-1.yaml"}
	mapFileRelativePath := "hug_http_8080"
	s.CreateFixtures(fixturePath, manifests)
	defer s.CleanupFixturesCheckMapFiles(fixturePath, manifests, []string{mapFileRelativePath})

	// Expected Conditions
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "route-conditions.yaml")
	expectedConditions := s.YamlToRouteConditions(expectedCondPath)

	httpRouteName := "route-echo-1"
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, httpRouteName, expectedConditions)

	// Check AttachedRoutes on Gateway status
	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http", 1)

	// haproxy.cfg Backends
	backendsExpectationsPath := path.Join(expectationsPath, "backends")
	expectedBackends := []string{"hug_e2e-tests-httproute_http-echo-1_80__"}
	s.ExpectBackends(s.Test().Ctx, backendsExpectationsPath, expectedBackends)

	// Check Maps
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectMapContents(mapFileRelativePath, expectedMapsPath)

	// Now remove the Service http-echo-1
	// Backend hug_e2e-tests-httproute_http-echo-1_80__ should be deleted
	s.deleteService("http-echo-1")
	s.ExpectBackendsDoNotExist(s.Test().Ctx, "hug_e2e-tests-httproute_http-echo-1_80__")
	s.CreateFixtures(fixturePath, []string{"http-echo-1.yaml"}) // Recreate the service in order to please the defer  s.CleanupFixtures(fixturePath, manifests)
}

func (s *HTTPRouteTestSuite) deleteService(name string) *v1.Service {
	var service v1.Service
	err := s.Test().Client.Get(s.Test().Ctx, client.ObjectKey{Name: name, Namespace: s.Test().Namespace}, &service)
	s.Require().NoError(err)

	err = s.Test().Client.Delete(s.Test().Ctx, &service)
	s.Require().NoError(err)

	return &service
}
