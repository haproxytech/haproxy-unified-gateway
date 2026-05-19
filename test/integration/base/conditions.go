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

package base

import (
	"os"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions"
	rc "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/routes"
	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/yaml"
)

func (b *BaseSuite) YamlToConditions(yamlPath string) conditions.Conditions {
	yamlFile, err := os.ReadFile(yamlPath)
	assert.NoError(b.T(), err, "Failed to read YAML file")
	yamlFile = utils.SubstitutePortsInContent(yamlFile, b.test.PortMap)

	var expectedConditionsMetaV1 []metav1.Condition
	err = yaml.Unmarshal(yamlFile, &expectedConditionsMetaV1)
	assert.NoError(b.T(), err, "Failed to unmarshal YAML")
	return conditions.NewConditionsFromMetav1Conditions(expectedConditionsMetaV1)
}

func (b *BaseSuite) YamlToRouteConditions(yamlPath string) rc.RouteConditions {
	yamlFile, err := os.ReadFile(yamlPath)
	assert.NoError(b.T(), err, "Failed to read YAML file")
	yamlFile = utils.SubstitutePortsInContent(yamlFile, b.test.PortMap)

	var expectedConditionsV1 gatewayv1.HTTPRouteStatus
	err = yaml.Unmarshal(yamlFile, &expectedConditionsV1)
	assert.NoError(b.T(), err, "Failed to unmarshal YAML")
	return rc.NewRouteConditionsFromV1RouteConditions(expectedConditionsV1.Parents, TestControllerName)
}

func (b *BaseSuite) YamlToListenerStatuses(yamlPath string) []gatewayv1.ListenerStatus {
	yamlFile, err := os.ReadFile(yamlPath)
	assert.NoError(b.T(), err, "Failed to read YAML file")
	yamlFile = utils.SubstitutePortsInContent(yamlFile, b.test.PortMap)

	var expectedListenerStatuses []gatewayv1.ListenerStatus
	err = yaml.Unmarshal(yamlFile, &expectedListenerStatuses)
	assert.NoError(b.T(), err, "Failed to unmarshal YAML")
	return expectedListenerStatuses
}
