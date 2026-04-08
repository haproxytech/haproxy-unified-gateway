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

package gatewaytls

import (
	"path"
	"testing"

	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"
	"github.com/stretchr/testify/suite"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Adding GatewayTLSTestSuite, just to be able to debug directly
type GatewayTLSTestSuite struct {
	GatewayTLSSuite
}

func TestGatewayTLSTestSuite(t *testing.T) {
	suite.Run(t, new(GatewayTLSTestSuite))
}

func (s *GatewayTLSTestSuite) Test_Gateway_TLS_missingSecret() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "tls"
	fixtures := []string{"gatewayclass.yaml", "gateway.yaml"}

	fixturePath := path.Join(fixtureDirPath, fixtureDir)
	s.CreateFixtures(fixturePath, fixtures)
	defer s.CleanupFixtures(fixturePath, fixtures)

	// Expected Conditions
	expectationsPath := path.Join(fixturePath, "missingSecret", "expectations")
	expectedCondPath := path.Join(expectationsPath, "gw-conditions.yaml")
	expectedConditions := s.YamlToConditions(expectedCondPath)
	expectedListenerStatusesPath := path.Join(expectationsPath, "gw-listener_statuses.yaml")
	expectedListenerStatuses := s.YamlToListenerStatuses(expectedListenerStatusesPath)

	gwName := "gateway"
	s.expectGwConditionsUpdated(s.Test().Ctx, s.Test().Namespace, gwName, expectedConditions, expectedListenerStatuses)

	// Check Maps
	mapFileRelativePath := "hug_" + s.Test().Namespace + "_gateway_http"
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectMapContents(mapFileRelativePath, expectedMapsPath)

	mapFileRelativePath = "hug_" + s.Test().Namespace + "_gateway_https"
	s.ExpectMapContents(mapFileRelativePath, expectedMapsPath)
}

func (s *GatewayTLSTestSuite) Test_Gateway_TLS_okSecret() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "tls"

	fixturePath := path.Join(fixtureDirPath, fixtureDir)
	s.CreateFixtures(fixturePath, nil)
	defer s.CleanupFixtures(fixturePath, nil)

	// Expected Conditions
	expectationsPath := path.Join(fixturePath, "okSecret", "expectations")
	expectedCondPath := path.Join(expectationsPath, "gw-conditions.yaml")
	expectedConditions := s.YamlToConditions(expectedCondPath)
	expectedListenerStatusesPath := path.Join(expectationsPath, "gw-listener_statuses.yaml")
	expectedListenerStatuses := s.YamlToListenerStatuses(expectedListenerStatusesPath)

	gwName := "gateway"
	s.expectGwConditionsUpdated(s.Test().Ctx, s.Test().Namespace, gwName, expectedConditions, expectedListenerStatuses)

	// Check Maps
	mapFileRelativePath := "hug_" + s.Test().Namespace + "_gateway_http"
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectMapContents(mapFileRelativePath, expectedMapsPath)

	mapFileRelativePath = "hug_" + s.Test().Namespace + "_gateway_https"
	s.ExpectMapContents(mapFileRelativePath, expectedMapsPath)
}

func (s *GatewayTLSTestSuite) Test_Gateway_TLS_Dynamic_ok_missing_ok_Secret() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "tls"

	fixturePath := path.Join(fixtureDirPath, fixtureDir)
	s.CreateFixtures(fixturePath, nil)
	defer s.CleanupFixtures(fixturePath, nil)

	// Expected Conditions
	// 1 - Secret is present OK
	expectationsPath := path.Join(fixturePath, "dynamicSecret", "expectations")
	expectedCondPath := path.Join(expectationsPath, "gw-conditions.yaml")
	expectedConditions := s.YamlToConditions(expectedCondPath)
	expectedListenerStatusesPath := path.Join(expectationsPath, "gw-listener_statuses_ok_1.yaml")
	expectedListenerStatuses := s.YamlToListenerStatuses(expectedListenerStatusesPath)

	gwName := "gateway"
	s.expectGwConditionsUpdated(s.Test().Ctx, s.Test().Namespace, gwName, expectedConditions, expectedListenerStatuses)

	// 2 - Delete the secret
	secret := s.deleteSecret("offload")
	expectedListenerStatusesPath = path.Join(expectationsPath, "gw-listener_statuses_ko_2.yaml")
	expectedListenerStatuses = s.YamlToListenerStatuses(expectedListenerStatusesPath)
	s.expectGwConditionsUpdated(s.Test().Ctx, s.Test().Namespace, gwName, expectedConditions, expectedListenerStatuses)

	// // 3 - Re-create the secret
	s.createSecret(secret)
	expectedListenerStatusesPath = path.Join(expectationsPath, "gw-listener_statuses_ok_3.yaml")
	expectedListenerStatuses = s.YamlToListenerStatuses(expectedListenerStatusesPath)
	s.expectGwConditionsUpdated(s.Test().Ctx, s.Test().Namespace, gwName, expectedConditions, expectedListenerStatuses)

	// Check Maps
	mapFileRelativePath := "hug_" + s.Test().Namespace + "_gateway_http"
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectMapContents(mapFileRelativePath, expectedMapsPath)

	mapFileRelativePath = "hug_" + s.Test().Namespace + "_gateway_https"
	s.ExpectMapContents(mapFileRelativePath, expectedMapsPath)
}

func (s *GatewayTLSTestSuite) deleteSecret(name string) *v1.Secret {
	var secret v1.Secret
	err := s.Test().Client.Get(s.Test().Ctx, client.ObjectKey{Name: name, Namespace: s.Test().Namespace}, &secret)
	s.Require().NoError(err)

	err = s.Test().Client.Delete(s.Test().Ctx, &secret)
	s.Require().NoError(err)

	return &secret
}

func (s *GatewayTLSTestSuite) createSecret(secret *v1.Secret) {
	secret.ResourceVersion = ""
	err := s.Test().Client.Create(s.Test().Ctx, secret)
	s.Require().NoError(err)
}
