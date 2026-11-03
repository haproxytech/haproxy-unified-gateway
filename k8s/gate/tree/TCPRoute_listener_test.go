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

	objtypes "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/object-types"
	"github.com/stretchr/testify/assert"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// The production table must keep TCP managed: this is the switch that turned a
// TCP listener from UnsupportedProtocol into a servable one.
func TestTCPProtocolIsManaged(t *testing.T) {
	assert.True(t, gateManagedRouteGroupKindsByProtocol.manages(gatewayv1.TCPProtocolType))
	assert.Equal(t, []gatewayv1.RouteGroupKind{objtypes.RouteKindTCP},
		gateManagedRouteGroupKindsByProtocol.sortedFor(gatewayv1.TCPProtocolType))
}
