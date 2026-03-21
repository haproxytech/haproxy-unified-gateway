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

package gatewaytls

import (
	"context"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/status"
	"github.com/haproxytech/haproxy-unified-gateway/test/integration/base"
	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const (
	timeout  = time.Second * 30
	interval = time.Second * 1
)

type GatewayTLSSuite struct {
	base.BaseSuite
}

func (s *GatewayTLSSuite) SetupSuite() {
	s.BaseSuite.SetupSuite("", 0)
}

func (s *GatewayTLSSuite) TearDownSuite() {
	s.BaseSuite.TearDownSuite()
}

func (s *GatewayTLSSuite) expectGwConditionsUpdated(ctx context.Context, namespace, name string,
	expectedConditions conditions.Conditions,
	expectedListenerStatuses []gatewayv1.ListenerStatus,
) {
	gw := &gatewayv1.Gateway{}
	var gotConditions conditions.Conditions

	if !utils.WaitFor(ctx, interval, timeout, func() bool {
		if err := s.Test().Client.Get(
			s.Test().Ctx,
			types.NamespacedName{Name: name, Namespace: namespace}, gw); err != nil {
			return false
		}

		gotConditions := conditions.NewConditionsFromMetav1Conditions(gw.Status.Conditions)

		if resGwConds := gotConditions.Equal(expectedConditions); !resGwConds {
			return false
		}

		return status.ListenerStatusesEqual(gw.Status.Listeners, expectedListenerStatuses)
	}) {
		// Define the option
		opts := cmpopts.IgnoreTypes(v1.Time{})
		diffConds := cmp.Diff(expectedConditions, gotConditions, opts)
		utils.SortListenerStatus(expectedListenerStatuses)
		utils.SortListenerStatus(gw.Status.Listeners)
		diffListeners := cmp.Diff(expectedListenerStatuses, gw.Status.Listeners, opts)
		s.T().Fatalf("conditions not correct for Gateway [%s/%s]. Diff conds: \n%v. Diff listener conds \n%v", namespace, name, diffConds, diffListeners)
	}
}
