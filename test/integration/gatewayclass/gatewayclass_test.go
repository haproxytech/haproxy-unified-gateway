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

package gatewayclass

import (
	"path"
	"testing"
	"time"

	v3 "github.com/haproxytech/haproxy-unified-gateway/api/gate/v3"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/constants"
	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"
	"github.com/stretchr/testify/suite"
	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	timeout  = time.Second * 15
	interval = time.Second * 1
)

// Adding GatewayClassTestSuite, just to be able to debug directly
type GatewayClassTestSuite struct {
	GatewayClassSuite
}

func TestGatewayClassTestSuite(t *testing.T) {
	suite.Run(t, new(GatewayClassTestSuite))
}

func (s *GatewayClassTestSuite) Test_GatewayClass_MissingNamespace() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "nsMissing"

	fixturePath := path.Join(fixtureDirPath, fixtureDir)
	s.CreateFixtures(fixturePath, nil)
	defer s.CleanupFixtures(fixturePath, nil)

	// Expected Conditions
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "conditions.yaml")
	expectedConditions := s.YamlToConditions(expectedCondPath)

	gwcName := "haproxy"
	s.expectConditionsUpdated(s.IntTest().Ctx, s.IntTest().Namespace, gwcName, expectedConditions)
}

func (s *GatewayClassTestSuite) Test_GatewayClass_InvalidRef() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "invalidRef"

	fixturePath := path.Join(fixtureDirPath, fixtureDir)
	s.CreateFixtures(fixturePath, nil)
	defer s.CleanupFixtures(fixturePath, nil)

	// Expected Conditions
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "conditions.yaml")
	expectedConditions := s.YamlToConditions(expectedCondPath)

	gwcName := "haproxy"
	s.expectConditionsUpdated(s.IntTest().Ctx, s.IntTest().Namespace, gwcName, expectedConditions)
}

func (s *GatewayClassTestSuite) Test_GatewayClass_ValidRef() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "validRef"

	fixturePath := path.Join(fixtureDirPath, fixtureDir)
	s.CreateFixtures(fixturePath, nil)
	defer s.CleanupFixtures(fixturePath, nil)

	// Expected Conditions
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "conditions.yaml")
	expectedConditions := s.YamlToConditions(expectedCondPath)

	gwcName := "haproxy"
	s.expectConditionsUpdated(s.IntTest().Ctx, s.IntTest().Namespace, gwcName, expectedConditions)
}

func (s *GatewayClassTestSuite) Test_GatewayClass_Dynamic_InstalledVersions() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "dynamic-installedversions"
	fixturePath := path.Join(fixtureDirPath, fixtureDir)
	s.CreateFixtures(fixturePath, nil)
	defer s.CleanupFixtures(fixturePath, nil)

	currentVersion := s.setGatewayClassCRToUnsupportedVersion("v1.1")

	// Expected Conditions
	// unsupported version
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "conditions-ko.yaml")
	expectedConditions := s.YamlToConditions(expectedCondPath)
	gwcName := "haproxy"
	s.expectConditionsUpdated(s.IntTest().Ctx, s.IntTest().Namespace, gwcName, expectedConditions)

	// Expected Conditions
	// supported version
	s.resetGatewayClassCRToSupportedVersion(currentVersion)
	expectedCondPath = path.Join(expectationsPath, "conditions-ok.yaml")
	expectedConditions = s.YamlToConditions(expectedCondPath)
	s.expectConditionsUpdated(s.IntTest().Ctx, s.IntTest().Namespace, gwcName, expectedConditions)
}

func (s *GatewayClassTestSuite) setGatewayClassCRToUnsupportedVersion(newVersion string) string {
	var gatewayClassCRD apiext.CustomResourceDefinition
	err := s.IntTest().Client.Get(s.IntTest().Ctx, client.ObjectKey{Name: "gatewayclasses.gateway.networking.k8s.io"}, &gatewayClassCRD)
	s.Require().NoError(err)
	currentVersion := gatewayClassCRD.Annotations[constants.BundleVersionAnnotation]

	gatewayClassCRD.Annotations[constants.BundleVersionAnnotation] = newVersion
	err = s.IntTest().Client.Update(s.IntTest().Ctx, &gatewayClassCRD)
	s.Require().NoError(err)

	return currentVersion
}

func (s *GatewayClassTestSuite) resetGatewayClassCRToSupportedVersion(version string) {
	var gatewayClassCRD apiext.CustomResourceDefinition
	err := s.IntTest().Client.Get(s.IntTest().Ctx, client.ObjectKey{Name: "gatewayclasses.gateway.networking.k8s.io"}, &gatewayClassCRD)
	s.Require().NoError(err)

	gatewayClassCRD.Annotations[constants.BundleVersionAnnotation] = version
	err = s.IntTest().Client.Update(s.IntTest().Ctx, &gatewayClassCRD)
	s.Require().NoError(err)
}

func (s *GatewayClassTestSuite) Test_GatewayClass_Dynamic_Gate() {
	fixtureDirPath := utils.GetCRDFixturePath()
	fixtureDir := "dynamic-gate"

	fixturePath := path.Join(fixtureDirPath, fixtureDir)
	s.CreateFixtures(fixturePath, nil)
	defer s.CleanupFixtures(fixturePath, nil)

	// Expected Conditions
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "conditions-ok.yaml")
	expectedConditions := s.YamlToConditions(expectedCondPath)

	gwcName := "haproxy"
	s.expectConditionsUpdated(s.IntTest().Ctx, s.IntTest().Namespace, gwcName, expectedConditions)

	// Remove gate
	gate := s.removeGate("huggate")

	// Expected Conditions
	expectationsPath = path.Join(fixturePath, "expectations")
	expectedCondPath = path.Join(expectationsPath, "conditions-ko.yaml")
	expectedConditions = s.YamlToConditions(expectedCondPath)

	s.expectConditionsUpdated(s.IntTest().Ctx, s.IntTest().Namespace, gwcName, expectedConditions)

	// Re-add Gate
	s.createGate(gate)
	expectationsPath = path.Join(fixturePath, "expectations")
	expectedCondPath = path.Join(expectationsPath, "conditions-ok.yaml")
	expectedConditions = s.YamlToConditions(expectedCondPath)

	s.expectConditionsUpdated(s.IntTest().Ctx, s.IntTest().Namespace, gwcName, expectedConditions)
}

func (s *GatewayClassTestSuite) removeGate(name string) *v3.HugGate {
	var gate v3.HugGate
	err := s.IntTest().Client.Get(s.IntTest().Ctx, client.ObjectKey{Name: name, Namespace: s.IntTest().Namespace}, &gate)
	s.Require().NoError(err)

	err = s.IntTest().Client.Delete(s.IntTest().Ctx, &gate)
	s.Require().NoError(err)

	return &gate
}

func (s *GatewayClassTestSuite) createGate(gate *v3.HugGate) {
	gate.ResourceVersion = ""
	err := s.IntTest().Client.Create(s.IntTest().Ctx, gate)
	s.Require().NoError(err)
}
