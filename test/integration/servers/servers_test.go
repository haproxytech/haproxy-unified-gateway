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

package servers

import (
	"fmt"
	"path"
	"time"

	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"
)

func (s *ServersSuite) Test_Scale_up() {
	fixtureDirPath := utils.GetCRDFixturePath()

	fixturePath := fixtureDirPath
	manifests := []string{
		"echo.yaml",
		"echo-endpoints-1.yaml",
		"gateway.yaml",
		"gatewayclass.yaml",
		"offload-secret.yaml",
		"route.yaml",
	}
	s.CreateFixtures(fixturePath, manifests)
	defer s.CleanupFixtures(fixturePath, manifests)

	// Expected Conditions
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "route-conditions.yaml")
	expectedConditions := s.YamlToRouteConditions(expectedCondPath)

	route := "route-echo-http"
	s.ExpectRouteConditionsUpdated(s.Test().Ctx, s.Test().Namespace, route, expectedConditions)

	// Check AttachedRoutes on Gateway status
	s.ExpectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "hug-gateway", "http", 1)
	s.ExpectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "hug-gateway", "https", 1)

	// Check Maps
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectListenerRouteMapContents(expectedMapsPath)

	mapFilePath := "hug_http_31081"
	s.ExpectMapContents(mapFilePath, expectedMapsPath)

	mapFilePath = "hug_https_31444"
	s.ExpectMapContents(mapFilePath, expectedMapsPath)

	// Check Servers: runtime

	backend := fmt.Sprintf("hug_%s_http-echo_80__", s.Test().Namespace)
	expectedServers := []string{
		"SRV_4827409c6115096b8dde5db0092cea2f54213123",
	}
	s.ExpectServers(backend, expectedServers)

	// -------------------------------------
	// Make sure to wait for no more reloads
	// -------------------------------------
	oldPid := s.WaitForNoReloadsAnyMore(10*time.Second, 2*time.Second)

	// 2- Now scale up the endpoints
	epsManifest2 := []string{
		"echo-endpoints-2.yaml",
	}
	s.CreateFixtures(fixturePath, epsManifest2)
	// No need to defer cleanup of the endpoints manifest, as it's the same EndpointSlice resource as in the first manifest, so it will be cleaned up by the first defer statement

	// Check Servers again
	expectedServers = []string{
		"SRV_4827409c6115096b8dde5db0092cea2f54213123",
		"SRV_fe3f9ea252b531060fe66a137b37d38263502132",
		"SRV_8ec3713870978506a7ecded834e9907edcf2e619",
	}
	s.ExpectServers(backend, expectedServers)

	// -------------------------------------
	// Check there are no reloads
	// -------------------------------------
	s.ConsistentlyNoReload(oldPid, 4*time.Second)

	// 3- Now scale down the endpoints
	epsManifest3 := []string{
		"echo-endpoints-3.yaml",
	}
	s.CreateFixtures(fixturePath, epsManifest3)
	// No need to defer cleanup of the endpoints manifest, as it's the same EndpointSlice resource as in the first manifest, so it will be cleaned up by the first defer statement

	// Check Servers again
	expectedServers = []string{
		"SRV_8ec3713870978506a7ecded834e9907edcf2e619",
	}
	s.ExpectServers(backend, expectedServers)

	// -------------------------------------
	// Check there are no reloads
	// -------------------------------------
	s.ConsistentlyNoReload(oldPid, 4*time.Second)
}
