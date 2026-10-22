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

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// Only Passthrough is served on a TLS listener. The predicate is written as an
// allowlist, so a mode added to the API later is rejected rather than admitted by
// default -- the denylist form would need a code change to notice it.
func TestCheckProtocolTLSModeIsAnAllowlist(t *testing.T) {
	mode := func(m gatewayv1.TLSModeType) *gatewayv1.TLSModeType { return &m }

	tests := []struct {
		name     string
		protocol gatewayv1.ProtocolType
		tls      *gatewayv1.ListenerTLSConfig
		valid    bool
	}{
		{
			name:     "TLS passthrough is served",
			protocol: gatewayv1.TLSProtocolType,
			tls:      &gatewayv1.ListenerTLSConfig{Mode: mode(gatewayv1.TLSModePassthrough)},
			valid:    true,
		},
		{
			name:     "TLS terminate is rejected",
			protocol: gatewayv1.TLSProtocolType,
			tls:      &gatewayv1.ListenerTLSConfig{Mode: mode(gatewayv1.TLSModeTerminate)},
			valid:    false,
		},
		{
			name:     "TLS without a tls block is rejected",
			protocol: gatewayv1.TLSProtocolType,
			tls:      nil,
			valid:    false,
		},
		{
			name:     "TLS without a mode is rejected",
			protocol: gatewayv1.TLSProtocolType,
			tls:      &gatewayv1.ListenerTLSConfig{},
			valid:    false,
		},
		{
			name:     "a TLS mode added later is rejected, not admitted by default",
			protocol: gatewayv1.TLSProtocolType,
			tls:      &gatewayv1.ListenerTLSConfig{Mode: mode("SomeFutureMode")},
			valid:    false,
		},
		{
			name:     "HTTPS terminate is untouched",
			protocol: gatewayv1.HTTPSProtocolType,
			tls:      &gatewayv1.ListenerTLSConfig{Mode: mode(gatewayv1.TLSModeTerminate)},
			valid:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			listener := &Listener{K8sGatewayListener: &gatewayv1.Listener{
				Name:     "listener",
				Port:     443,
				Protocol: tc.protocol,
				TLS:      tc.tls,
			}}

			listener.checkProtocol(gateManagedRouteGroupKindsByProtocol)

			assert.Equal(t, tc.valid, listener.CheckProtocol.Valid)
		})
	}
}

// The two vocabularies do not hold the same word for a TLS mode this controller does not
// serve: a Gateway's listener has UnsupportedValue, an entry has UnsupportedProtocol alone.
// The check therefore concludes the verdict its own vocabulary can spell, because a verdict
// that vocabulary cannot spell renders as Unreportable -- the listener is then refused with
// Invalid, on an internal gap, instead of with the reason its reader can act on.
func TestCheckProtocolTLSModeVerdictFollowsTheVocabulary(t *testing.T) {
	mode := gatewayv1.TLSModeType(gatewayv1.TLSModeTerminate)
	tlsConfig := &gatewayv1.ListenerTLSConfig{Mode: &mode}

	gatewayListener := &Listener{
		K8sGatewayListener: &gatewayv1.Listener{
			Name: "tls", Port: 443, Protocol: gatewayv1.TLSProtocolType, TLS: tlsConfig,
		},
		OwnerKind: kindGateway,
	}
	entry := &Listener{
		ListenerKey: listenerSetListenerKey(
			client.ObjectKey{Namespace: "infra", Name: "gw"},
			client.ObjectKey{Namespace: "tenant", Name: "ls"},
			gatewayv1.SectionName("tls"),
		),
		K8sListenerSetListenerEntry: &gatewayv1.ListenerEntry{
			Name: "tls", Port: 443, Protocol: gatewayv1.TLSProtocolType, TLS: tlsConfig,
		},
		OwnerKind: kindListenerSet,
	}

	tests := []struct {
		name       string
		listener   *Listener
		wantReason string
	}{
		{
			name:       "a Gateway listener reports UnsupportedValue",
			listener:   gatewayListener,
			wantReason: string(gatewayv1.ListenerReasonUnsupportedValue),
		},
		{
			name:       "an entry reports UnsupportedProtocol, the only word it has",
			listener:   entry,
			wantReason: string(gatewayv1.ListenerEntryReasonUnsupportedProtocol),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.listener.checkProtocol(gateManagedRouteGroupKindsByProtocol)

			require.False(t, tc.listener.CheckProtocol.Valid)
			_, reason := getCondition(tc.listener.CheckProtocol.Conditions,
				generic.ConditionType(gatewayv1.ListenerConditionAccepted))
			assert.Equal(t, tc.wantReason, reason)
		})
	}
}
