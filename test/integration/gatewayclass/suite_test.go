// Copyright 2019 HAProxy Technologies LLC
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
	"context"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions"
	"github.com/haproxytech/haproxy-unified-gateway/test/integration/base"
	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"

	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type GatewayClassSuite struct {
	base.BaseSuite
}

func (s *GatewayClassSuite) SetupSuite() {
	s.BaseSuite.SetupSuite("", 0)
}

func (s *GatewayClassSuite) TearDownSuite() {
	s.BaseSuite.TearDownSuite()
}

func (s *GatewayClassSuite) expectConditionsUpdated(ctx context.Context, namespace, name string, expectedConditions conditions.Conditions) {
	gwc := &gatewayv1.GatewayClass{}
	if !utils.WaitFor(ctx, interval, timeout, func() bool {
		if err := s.Test().Client.Get(
			s.Test().Ctx,
			types.NamespacedName{Name: name, Namespace: namespace}, gwc,
		); err != nil {
			return false
		}

		gotConditions := conditions.NewConditionsFromMetav1Conditions(gwc.Status.Conditions)

		res := gotConditions.Equal(expectedConditions)

		return res
	}) {
		s.T().Fatal("conditions not correct")
	}
}
