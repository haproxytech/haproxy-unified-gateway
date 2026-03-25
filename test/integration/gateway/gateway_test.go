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

package gateway

import (
	"fmt"
	"path"
	"testing"
	"time"

	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"
	"github.com/stretchr/testify/suite"
)

const (
	timeout  = time.Second * 10
	interval = time.Second * 1
)

// Adding GatewayTestSuite, just to be able to debug directly
type GatewayTestSuite struct {
	GatewaySuite
}

func TestGatewayTestSuite(t *testing.T) {
	suite.Run(t, new(GatewayTestSuite))
}

func (s *GatewayTestSuite) Test_Gateway_InvalidRef() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "invalidRef"

	fixturePath := path.Join(fixtureDirPath, fixtureDir)
	s.CreateFixtures(fixturePath, nil)
	defer s.CleanupFixtures(fixturePath, nil)

	// Expected Conditions
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "conditions.yaml")
	expectedConditions := s.YamlToConditions(expectedCondPath)
	expectedListenerStatusesPath := path.Join(expectationsPath, "listener_statuses.yaml")
	expectedListenerStatuses := s.YamlToListenerStatuses(expectedListenerStatusesPath)

	gwName := "gateway"
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, gwName, expectedConditions, expectedListenerStatuses)

	// Check Maps
	mapFileRelativePath := "hug_" + s.Test().Namespace + "_gateway_http"
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.Eventually(func() bool {
		return s.CheckMapContents(mapFileRelativePath, expectedMapsPath)
	}, timeout, interval, fmt.Sprintf("maps in %s did not match expected contents", mapFileRelativePath))
}

func (s *GatewayTestSuite) Test_Gateway_InvalidHTTPS() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "invalidHTTPS"

	fixturePath := path.Join(fixtureDirPath, fixtureDir)
	s.CreateFixtures(fixturePath, nil)
	defer s.CleanupFixtures(fixturePath, nil)

	// Expected Conditions
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "conditions.yaml")
	expectedConditions := s.YamlToConditions(expectedCondPath)
	expectedListenerStatusesPath := path.Join(expectationsPath, "listener_statuses.yaml")
	expectedListenerStatuses := s.YamlToListenerStatuses(expectedListenerStatusesPath)

	gwName := "gateway"
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, gwName, expectedConditions, expectedListenerStatuses)

	// Check Maps
	mapFileRelativePath := "hug_" + s.Test().Namespace + "_gateway_http"
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.Eventually(func() bool {
		return s.CheckMapContents(mapFileRelativePath, expectedMapsPath)
	}, timeout, interval, fmt.Sprintf("maps in %s did not match expected contents", mapFileRelativePath))
}

func (s *GatewayTestSuite) Test_Gateway_validRef() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "validRef"

	fixturePath := path.Join(fixtureDirPath, fixtureDir)
	s.CreateFixtures(fixturePath, nil)
	defer s.CleanupFixtures(fixturePath, nil)

	// Expected Conditions
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "conditions.yaml")
	expectedConditions := s.YamlToConditions(expectedCondPath)
	expectedListenerStatusesPath := path.Join(expectationsPath, "listener_statuses.yaml")
	expectedListenerStatuses := s.YamlToListenerStatuses(expectedListenerStatusesPath)

	gwName := "gateway"
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, gwName, expectedConditions, expectedListenerStatuses)

	// Check Maps
	mapFileRelativePath := "hug_" + s.Test().Namespace + "_gateway_http"
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.Eventually(func() bool {
		return s.CheckMapContents(mapFileRelativePath, expectedMapsPath)
	}, timeout, interval, fmt.Sprintf("maps in %s did not match expected contents", mapFileRelativePath))
}

func (s *GatewayTestSuite) Test_Gateway_conflict_at_least_1_listener_ok() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "conflict"
	subFixtureDir := "at_least_1_valid"

	fixturePath := path.Join(fixtureDirPath, fixtureDir, subFixtureDir)
	s.CreateFixtures(fixturePath, nil)
	defer s.CleanupFixtures(fixturePath, nil)

	// Expected Conditions: Gateway
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "conditions-gateway.yaml")
	expectedConditions := s.YamlToConditions(expectedCondPath)
	expectedListenerStatusesPath := path.Join(expectationsPath, "listener_statuses-gateway.yaml")
	expectedListenerStatuses := s.YamlToListenerStatuses(expectedListenerStatusesPath)

	gwName := "gateway"
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, gwName, expectedConditions, expectedListenerStatuses)

	// Expected Conditions: Gateway2
	expectedCondPath = path.Join(expectationsPath, "conditions-gateway2.yaml")
	expectedConditions = s.YamlToConditions(expectedCondPath)
	expectedListenerStatusesPath = path.Join(expectationsPath, "listener_statuses-gateway2.yaml")
	expectedListenerStatuses = s.YamlToListenerStatuses(expectedListenerStatusesPath)

	gwName = "gateway2"
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, gwName, expectedConditions, expectedListenerStatuses)

	// haproxy.cfg Frontends
	frontendsExpectationsPath := path.Join(expectationsPath, "frontends")
	expectedFrontends := []string{"hug_http_8080", "hug_http_8081", "hug_http_9090"}
	s.ExpectFrontends(s.Test().Ctx, frontendsExpectationsPath, expectedFrontends)

	// Check Maps
	mapFileRelativePath := "hug_http_8080"
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.Eventually(func() bool {
		return s.CheckMapContents(mapFileRelativePath, expectedMapsPath)
	}, timeout, interval, fmt.Sprintf("maps in %s did not match expected contents", mapFileRelativePath))

	mapFileRelativePath = "hug_http_8081"
	s.Eventually(func() bool {
		return s.CheckMapContents(mapFileRelativePath, expectedMapsPath)
	}, timeout, interval, fmt.Sprintf("maps in %s did not match expected contents", mapFileRelativePath))

	mapFileRelativePath = "hug_http_9090"
	s.Eventually(func() bool {
		return s.CheckMapContents(mapFileRelativePath, expectedMapsPath)
	}, timeout, interval, fmt.Sprintf("maps in %s did not match expected contents", mapFileRelativePath))
}

func (s *GatewayTestSuite) Test_Gateway_conflict_empty_hostname_no_conflict() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "conflict"
	subFixtureDir := "empty_hostname"

	fixturePath := path.Join(fixtureDirPath, fixtureDir, subFixtureDir)
	s.CreateFixtures(fixturePath, nil)
	defer s.CleanupFixtures(fixturePath, nil)

	// An empty hostname (catch-all) must not conflict with a specific hostname on the same port.
	// Both listeners should be accepted.
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "conditions-gateway.yaml")
	expectedConditions := s.YamlToConditions(expectedCondPath)
	expectedListenerStatusesPath := path.Join(expectationsPath, "listener_statuses-gateway.yaml")
	expectedListenerStatuses := s.YamlToListenerStatuses(expectedListenerStatusesPath)

	gwName := "gateway"
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, gwName, expectedConditions, expectedListenerStatuses)
}

func (s *GatewayTestSuite) Test_Gateway_conflict_0_listener_ok() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "conflict"
	subFixtureDir := "0_valid"

	fixturePath := path.Join(fixtureDirPath, fixtureDir, subFixtureDir)
	s.CreateFixtures(fixturePath, nil)
	defer s.CleanupFixtures(fixturePath, nil)

	// Expected Conditions: Gateway
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "conditions-gateway.yaml")
	expectedConditions := s.YamlToConditions(expectedCondPath)
	expectedListenerStatusesPath := path.Join(expectationsPath, "listener_statuses-gateway.yaml")
	expectedListenerStatuses := s.YamlToListenerStatuses(expectedListenerStatusesPath)

	gwName := "gateway"
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, gwName, expectedConditions, expectedListenerStatuses)

	// Expected Conditions: Gateway2
	expectedCondPath = path.Join(expectationsPath, "conditions-gateway2.yaml")
	expectedConditions = s.YamlToConditions(expectedCondPath)
	expectedListenerStatusesPath = path.Join(expectationsPath, "listener_statuses-gateway2.yaml")
	expectedListenerStatuses = s.YamlToListenerStatuses(expectedListenerStatusesPath)

	gwName = "gateway2"
	s.expectConditionsUpdated(s.Test().Ctx, s.Test().Namespace, gwName, expectedConditions, expectedListenerStatuses)

	// haproxy.cfg Frontends
	frontendsExpectationsPath := path.Join(expectationsPath, "frontends")
	expectedFrontends := []string{}
	s.ExpectFrontends(s.Test().Ctx, frontendsExpectationsPath, expectedFrontends)
}
