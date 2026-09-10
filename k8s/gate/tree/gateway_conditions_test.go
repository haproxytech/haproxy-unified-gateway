// Copyright 2026 HAProxy Technologies LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package tree

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
)

// newProtocolTestGateway builds a managed Gateway whose listeners carry only
// the CheckProtocol result, so BuildConditions exercises the protocol path.
func newProtocolTestGateway(protocols ...gatewayv1.ProtocolType) *Gateway {
	gw := &Gateway{
		K8sResource:            &gatewayv1.Gateway{},
		CheckValidGatewayClass: CheckResult{Valid: true},
		CheckParamsRef:         CheckResult{Valid: true},
		CheckConflict:          CheckResult{Valid: true},
	}
	for _, protocol := range protocols {
		valid := protocol == gatewayv1.HTTPProtocolType || protocol == gatewayv1.HTTPSProtocolType || protocol == gatewayv1.TLSProtocolType
		gw.Listeners = append(gw.Listeners, &Listener{
			K8sGatewayListener: &gatewayv1.Listener{},
			CheckProtocol:      CheckResult{Valid: valid},
		})
	}
	return gw
}

func getCondition(conds generic.Conditions, condType generic.ConditionType) (metav1.ConditionStatus, string) {
	c, ok := conds.GetCondition(condType)
	if !ok {
		return "", ""
	}
	return c.Status, c.Reason
}

func TestGatewayBuildConditions_UnsupportedProtocol(t *testing.T) {
	accepted := generic.ConditionType(gatewayv1.GatewayConditionAccepted)
	programmed := generic.ConditionType(gatewayv1.GatewayConditionProgrammed)

	tests := []struct {
		name                 string
		protocols            []gatewayv1.ProtocolType
		wantStatus           metav1.ConditionStatus
		wantReason           string
		wantProgrammedStatus metav1.ConditionStatus
		wantProgrammedReason string
	}{
		{
			name:                 "all listeners supported",
			protocols:            []gatewayv1.ProtocolType{gatewayv1.HTTPProtocolType, gatewayv1.HTTPSProtocolType},
			wantStatus:           metav1.ConditionTrue,
			wantReason:           "Accepted",
			wantProgrammedStatus: metav1.ConditionTrue,
			wantProgrammedReason: "Programmed",
		},
		{
			name:                 "some listeners unsupported",
			protocols:            []gatewayv1.ProtocolType{gatewayv1.HTTPProtocolType, "INVALID"},
			wantStatus:           metav1.ConditionTrue,
			wantReason:           "ListenersNotValid",
			wantProgrammedStatus: metav1.ConditionTrue,
			wantProgrammedReason: "Programmed",
		},
		{
			// Rejected Gateways must not look ready to Programmed waiters.
			name:                 "all listeners unsupported",
			protocols:            []gatewayv1.ProtocolType{"INVALID"},
			wantStatus:           metav1.ConditionFalse,
			wantReason:           "ListenersNotValid",
			wantProgrammedStatus: metav1.ConditionFalse,
			wantProgrammedReason: "ListenersNotValid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gw := newProtocolTestGateway(tt.protocols...)
			gw.BuildConditions()
			status, reason := getCondition(gw.Conditions, accepted)
			assert.Equal(t, tt.wantStatus, status)
			assert.Equal(t, tt.wantReason, reason)
			programmedStatus, programmedReason := getCondition(gw.Conditions, programmed)
			assert.Equal(t, tt.wantProgrammedStatus, programmedStatus)
			assert.Equal(t, tt.wantProgrammedReason, programmedReason)
		})
	}
}
