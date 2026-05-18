// Copyright 2025 HAProxy Technologies LLC
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

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/caps"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	objtypes "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/object-types"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func exampleSupportedKinds() map[gatewayv1.ProtocolType][]gatewayv1.RouteGroupKind {
	return map[gatewayv1.ProtocolType][]gatewayv1.RouteGroupKind{
		gatewayv1.HTTPProtocolType:  {objtypes.RouteKindHTTP, objtypes.RouteKindGRPC},
		gatewayv1.HTTPSProtocolType: {objtypes.RouteKindHTTP, objtypes.RouteKindGRPC},
		gatewayv1.TLSProtocolType:   {objtypes.RouteKindTLS, objtypes.RouteKindTCP},
		gatewayv1.UDPProtocolType:   {objtypes.RouteKindUDP},
		gatewayv1.TCPProtocolType:   {objtypes.RouteKindTCP},
	}
}

func TestSupportedKinds(t *testing.T) {
	tests := []struct {
		name          string
		listener      gatewayv1.Listener
		expectedKinds []gatewayv1.RouteGroupKind
	}{
		{
			name: "HTTP protocol, no allowed kinds specified",
			listener: gatewayv1.Listener{
				Protocol: gatewayv1.HTTPProtocolType,
			},
			expectedKinds: []gatewayv1.RouteGroupKind{objtypes.RouteKindGRPC, objtypes.RouteKindHTTP},
		},
		{
			name: "TLS protocol, no allowed kinds specified",
			listener: gatewayv1.Listener{
				Protocol: gatewayv1.TLSProtocolType,
			},
			expectedKinds: []gatewayv1.RouteGroupKind{objtypes.RouteKindTCP, objtypes.RouteKindTLS},
		},
		{
			name: "HTTP protocol, empty allowed kinds",
			listener: gatewayv1.Listener{
				Protocol: gatewayv1.HTTPProtocolType,
				AllowedRoutes: &gatewayv1.AllowedRoutes{
					Kinds: []gatewayv1.RouteGroupKind{},
				},
			},
			expectedKinds: []gatewayv1.RouteGroupKind{objtypes.RouteKindGRPC, objtypes.RouteKindHTTP},
		},
		{
			name: "HTTP protocol, nil allowed kinds",
			listener: gatewayv1.Listener{
				Protocol: gatewayv1.HTTPProtocolType,
				AllowedRoutes: &gatewayv1.AllowedRoutes{
					Kinds: nil,
				},
			},
			expectedKinds: []gatewayv1.RouteGroupKind{objtypes.RouteKindGRPC, objtypes.RouteKindHTTP},
		},
		{
			name: "HTTP protocol, with allowed kinds (intersection)",
			listener: gatewayv1.Listener{
				Protocol: gatewayv1.HTTPProtocolType,
				AllowedRoutes: &gatewayv1.AllowedRoutes{
					Kinds: []gatewayv1.RouteGroupKind{
						objtypes.RouteKindHTTP, // supported
						objtypes.RouteKindTCP,  // not supported for HTTP
					},
				},
			},
			expectedKinds: []gatewayv1.RouteGroupKind{objtypes.RouteKindHTTP},
		},
		{
			name: "HTTP protocol, with allowed kinds, none supported",
			listener: gatewayv1.Listener{
				Protocol: gatewayv1.HTTPProtocolType,
				AllowedRoutes: &gatewayv1.AllowedRoutes{
					Kinds: []gatewayv1.RouteGroupKind{
						objtypes.RouteKindTLS,
					},
				},
			},
			expectedKinds: []gatewayv1.RouteGroupKind{},
		},
		{
			name: "Unsupported protocol",
			listener: gatewayv1.Listener{
				Protocol: "foo",
			},
			expectedKinds: []gatewayv1.RouteGroupKind{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := supportedKinds(tt.listener, exampleSupportedKinds())
			assert.Equal(t, tt.expectedKinds, result)
		})
	}
}

func Test_hostnameConflicts(t *testing.T) {
	tests := []struct {
		name      string
		hostname1 string
		hostname2 string
		want      bool
	}{
		{
			name:      "exact match",
			hostname1: "example.com",
			hostname2: "example.com",
			want:      true,
		},
		{
			name:      "case-insensitive match",
			hostname1: "Example.com",
			hostname2: "example.com",
			want:      true,
		},
		{
			name:      "wildcards are identical",
			hostname1: "*.example.com",
			hostname2: "*.example.com",
			want:      true,
		},
		{
			name:      "both empty - two catch-all listeners conflict",
			hostname1: "",
			hostname2: "",
			want:      true,
		},
		// The following pairs are different hostname values: specificity rules (exact >
		// wildcard > empty) always produce an unambiguous winner, so no conflict.
		{
			name:      "wildcard vs matching specific - no conflict (specific beats wildcard)",
			hostname1: "*.example.com",
			hostname2: "foo.example.com",
			want:      false,
		},
		{
			name:      "specific vs matching wildcard - no conflict (specific beats wildcard)",
			hostname1: "foo.example.com",
			hostname2: "*.example.com",
			want:      false,
		},
		{
			name:      "wildcard vs parent domain - no conflict",
			hostname1: "*.example.com",
			hostname2: "example.com",
			want:      false,
		},
		{
			name:      "two different wildcards - no conflict",
			hostname1: "*.foo.com",
			hostname2: "*.bar.com",
			want:      false,
		},
		{
			name:      "two different exact hostnames - no conflict",
			hostname1: "one.com",
			hostname2: "two.com",
			want:      false,
		},
		{
			name:      "empty vs specific - no conflict (empty coexists with specific)",
			hostname1: "",
			hostname2: "one.com",
			want:      false,
		},
		{
			name:      "specific vs empty - no conflict (empty coexists with specific)",
			hostname1: "one.com",
			hostname2: "",
			want:      false,
		},
		{
			name:      "empty vs wildcard - no conflict (empty coexists with wildcard)",
			hostname1: "",
			hostname2: "*.example.com",
			want:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hostnameConflicts(tt.hostname1, tt.hostname2); got != tt.want {
				t.Errorf("hostnameConflicts() = %v, want %v", got, tt.want)
			}
		})
	}
}

// newListenerForPortTest builds a Listener + parent Gateway minimally wired
// for checkPort + BuildConditions. Other checks are left empty so the only
// failure mode under test is the port-permission check.
func newListenerForPortTest(port int32) (*Listener, *Gateway) {
	gw := &Gateway{
		K8sResource: &gatewayv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Name: "gw", Namespace: "default", Generation: 1},
		},
	}
	l := &Listener{
		K8sResource: gatewayv1.Listener{
			Name:     "http",
			Port:     gatewayv1.PortNumber(port),
			Protocol: gatewayv1.HTTPProtocolType,
		},
		Conditions: make(generic.Conditions),
	}
	return l, gw
}

func TestCheckPort_BuildConditions(t *testing.T) {
	const (
		acceptedT   = generic.ConditionType("Accepted")
		programmedT = generic.ConditionType("Programmed")
	)

	tests := []struct {
		name             string
		port             int32
		binder           caps.PortBinder
		wantValid        bool
		wantAccepted     bool
		wantAcceptedCond metav1.ConditionStatus
		wantProgCond     metav1.ConditionStatus
	}{
		{
			name:             "privileged port without cap rejected",
			port:             80,
			binder:           caps.Static(1024, false),
			wantValid:        false,
			wantAccepted:     false,
			wantAcceptedCond: metav1.ConditionFalse,
			wantProgCond:     metav1.ConditionFalse,
		},
		{
			name:             "privileged port with cap accepted",
			port:             80,
			binder:           caps.Static(1024, true),
			wantValid:        true,
			wantAccepted:     true,
			wantAcceptedCond: metav1.ConditionTrue,
			wantProgCond:     metav1.ConditionUnknown, // Pending
		},
		{
			name:             "unprivileged port without cap accepted",
			port:             8080,
			binder:           caps.Static(1024, false),
			wantValid:        true,
			wantAccepted:     true,
			wantAcceptedCond: metav1.ConditionTrue,
			wantProgCond:     metav1.ConditionUnknown, // Pending
		},
		{
			name:             "sysctl-relaxed allows port 80 without cap",
			port:             80,
			binder:           caps.Static(0, false),
			wantValid:        true,
			wantAccepted:     true,
			wantAcceptedCond: metav1.ConditionTrue,
			wantProgCond:     metav1.ConditionUnknown,
		},
		{
			name:             "nil binder is a no-op",
			port:             80,
			binder:           nil,
			wantValid:        true,
			wantAccepted:     true,
			wantAcceptedCond: metav1.ConditionTrue,
			wantProgCond:     metav1.ConditionUnknown,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, gw := newListenerForPortTest(tc.port)
			l.checkPort(tc.binder)
			l.BuildConditions(gw)

			assert.Equal(t, tc.wantValid, l.Valid, "Valid")
			assert.Equal(t, tc.wantAccepted, l.Accepted, "Accepted field")

			acc, ok := l.Conditions.GetCondition(acceptedT)
			if assert.True(t, ok, "Accepted condition missing") {
				assert.Equal(t, tc.wantAcceptedCond, acc.Status, "Accepted status")
			}
			prog, ok := l.Conditions.GetCondition(programmedT)
			if assert.True(t, ok, "Programmed condition missing") {
				assert.Equal(t, tc.wantProgCond, prog.Status, "Programmed status")
			}
			if !tc.wantAccepted {
				assert.Contains(t, acc.Message, "CAP_NET_BIND_SERVICE",
					"rejection message should explain the cause")
			}
		})
	}
}

// When an earlier check has already rejected the listener, checkPort must
// not run — otherwise its Reason=Invalid would overwrite the more specific
// reason (e.g. UnsupportedProtocol) during the merge in BuildConditions.
func TestCheckPort_SkipsWhenEarlierCheckFailed(t *testing.T) {
	l, _ := newListenerForPortTest(80)
	l.CheckProtocol = CheckResult{
		Valid:      false,
		Conditions: conditions.NewListenerAcceptedUnsupportedProtocol("unsupported"),
	}

	l.checkPort(caps.Static(1024, false))

	assert.Empty(t, l.CheckPort.Conditions, "checkPort should be a no-op when an earlier check already rejected the listener")
}
