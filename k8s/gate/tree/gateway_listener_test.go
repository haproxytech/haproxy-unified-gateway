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
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func exampleSupportedKinds() managedRouteGroupKindsByProtocol {
	return managedRouteGroupKindsByProtocol{
		gatewayv1.HTTPProtocolType:  {objtypes.RouteKindHTTP, objtypes.RouteKindGRPC},
		gatewayv1.HTTPSProtocolType: {objtypes.RouteKindHTTP, objtypes.RouteKindGRPC},
		gatewayv1.TLSProtocolType:   {objtypes.RouteKindTLS, objtypes.RouteKindTCP},
		gatewayv1.UDPProtocolType:   {objtypes.RouteKindUDP},
		gatewayv1.TCPProtocolType:   {objtypes.RouteKindTCP},
	}
}

func TestPartitionRouteKinds(t *testing.T) {
	tests := []struct {
		name             string
		listener         gatewayv1.Listener
		expectedKinds    []gatewayv1.RouteGroupKind
		expectedRejected []gatewayv1.RouteGroupKind
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
			expectedKinds:    []gatewayv1.RouteGroupKind{objtypes.RouteKindHTTP},
			expectedRejected: []gatewayv1.RouteGroupKind{objtypes.RouteKindTCP},
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
			expectedKinds:    []gatewayv1.RouteGroupKind{},
			expectedRejected: []gatewayv1.RouteGroupKind{objtypes.RouteKindTLS},
		},
		{
			// Every rejected kind must be reported, not just one of them: the
			// message names them all so a user does not fix them one round-trip
			// at a time.
			name: "HTTP protocol, several unsupported kinds are all rejected",
			listener: gatewayv1.Listener{
				Protocol: gatewayv1.HTTPProtocolType,
				AllowedRoutes: &gatewayv1.AllowedRoutes{
					Kinds: []gatewayv1.RouteGroupKind{
						objtypes.RouteKindTLS,
						objtypes.RouteKindHTTP,
						objtypes.RouteKindUDP,
					},
				},
			},
			expectedKinds:    []gatewayv1.RouteGroupKind{objtypes.RouteKindHTTP},
			expectedRejected: []gatewayv1.RouteGroupKind{objtypes.RouteKindTLS, objtypes.RouteKindUDP},
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
			kept, rejected := partitionRouteKinds(tt.listener.Protocol, tt.listener.AllowedRoutes, exampleSupportedKinds())
			assert.Equal(t, tt.expectedKinds, kept, "kept kinds")
			assert.Equal(t, tt.expectedRejected, rejected, "rejected kinds")
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
			Name: "gw", Namespace: "default", Generation: 1,
		},
	}
	l := &Listener{
		K8sGatewayListener: &gatewayv1.Listener{
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
		Conditions: l.conditionsFor(conditions.VerdictUnsupportedProtocol, "unsupported"),
	}

	l.checkPort(caps.Static(1024, false))

	assert.Empty(t, l.CheckPort.Conditions, "checkPort should be a no-op when an earlier check already rejected the listener")
}

// The accessors are the only place that knows where a listener's attributes come
// from. They must read the ListenerSet entry when the listener was contributed by
// one, the Gateway listener otherwise, and never panic on a Listener with no
// source attached — the field they replaced was a value, so reading it from a
// zero-value Listener could not crash.
func TestListenerAccessors(t *testing.T) {
	hostname := gatewayv1.Hostname("gw.example.com")
	entryHostname := gatewayv1.Hostname("ls.example.com")
	terminate := gatewayv1.TLSModeTerminate

	t.Run("reads the Gateway listener", func(t *testing.T) {
		l := &Listener{K8sGatewayListener: &gatewayv1.Listener{
			Name:          "gw-http",
			Port:          80,
			Protocol:      gatewayv1.HTTPProtocolType,
			Hostname:      &hostname,
			AllowedRoutes: &gatewayv1.AllowedRoutes{},
		}}

		assert.False(t, l.FromListenerSet())
		assert.Equal(t, gatewayv1.SectionName("gw-http"), l.Name())
		assert.Equal(t, gatewayv1.PortNumber(80), l.Port())
		assert.Equal(t, gatewayv1.HTTPProtocolType, l.Protocol())
		assert.Equal(t, &hostname, l.Hostname())
		assert.Nil(t, l.TLS())
		assert.NotNil(t, l.AllowedRoutes())
	})

	t.Run("reads the ListenerSet entry", func(t *testing.T) {
		l := &Listener{K8sListenerSetListenerEntry: &gatewayv1.ListenerEntry{
			Name:     "ls-https",
			Port:     443,
			Protocol: gatewayv1.HTTPSProtocolType,
			Hostname: &entryHostname,
			TLS:      &gatewayv1.ListenerTLSConfig{Mode: &terminate},
		}}

		assert.True(t, l.FromListenerSet())
		assert.Equal(t, gatewayv1.SectionName("ls-https"), l.Name())
		assert.Equal(t, gatewayv1.PortNumber(443), l.Port())
		assert.Equal(t, gatewayv1.HTTPSProtocolType, l.Protocol())
		assert.Equal(t, &entryHostname, l.Hostname())
		assert.NotNil(t, l.TLS())
		assert.Nil(t, l.AllowedRoutes())
	})

	t.Run("no source attached returns zero values without panicking", func(t *testing.T) {
		l := &Listener{}

		assert.False(t, l.FromListenerSet())
		assert.Empty(t, l.Name())
		assert.Zero(t, l.Port())
		assert.Empty(t, l.Protocol())
		assert.Nil(t, l.Hostname())
		assert.Nil(t, l.TLS())
		assert.Nil(t, l.AllowedRoutes())
	})

	t.Run("the ListenerSet entry wins when both are set", func(t *testing.T) {
		// Not a state the builders may produce, but the accessors must still be
		// total rather than depend on an unenforced invariant.
		l := &Listener{
			K8sGatewayListener:          &gatewayv1.Listener{Name: "from-gateway", Port: 80},
			K8sListenerSetListenerEntry: &gatewayv1.ListenerEntry{Name: "from-listenerset", Port: 443},
		}

		assert.Equal(t, gatewayv1.SectionName("from-listenerset"), l.Name())
		assert.Equal(t, gatewayv1.PortNumber(443), l.Port())
	})
}

// An object reference in a listener is relative to the resource that declares it.
// GEP-1713: "ReferenceGrants applied to a Gateway are not inherited by child
// ListenerSets", and the converse. So the "from" side of the grant question, and
// the namespace an unqualified reference resolves in, both follow the owner --
// never the Gateway the listener happens to be served on.
func TestListenerReferencesFollowTheOwner(t *testing.T) {
	gw := client.ObjectKey{Namespace: "infra", Name: "shared"}
	ls := client.ObjectKey{Namespace: "tenant-a", Name: "app"}

	gatewayListener := &Listener{
		ListenerKey: gatewayListenerKey(gw, "https"),
		OwnerKind:   kindGateway,
	}
	listenerSetListener := &Listener{
		ListenerKey: listenerSetListenerKey(gw, ls, "https"),
		OwnerKind:   kindListenerSet,
	}

	t.Run("grant from", func(t *testing.T) {
		assert.Equal(t,
			GrantFrom{Group: gatewayv1.GroupName, Kind: "Gateway", Namespace: "infra"},
			gatewayListener.referenceGrantFrom(),
		)

		// The whole point: a grant written "from: Gateway in infra" must NOT answer
		// for this listener, and one written "from: ListenerSet in tenant-a" must.
		assert.Equal(t,
			GrantFrom{Group: gatewayv1.GroupName, Kind: "ListenerSet", Namespace: "tenant-a"},
			listenerSetListener.referenceGrantFrom(),
		)
	})

	t.Run("owner description", func(t *testing.T) {
		// Reproduces the wording the condition message has always had for a Gateway
		// listener, so an existing message is unchanged: "... for listener in
		// gateway infra/shared".
		assert.Equal(t, "gateway infra/shared", gatewayListener.ownerDescription())
		assert.Equal(t, "listenerset tenant-a/app", listenerSetListener.ownerDescription())
	})

	t.Run("unqualified certificateRef resolves in the owner namespace", func(t *testing.T) {
		certRef := gatewayv1.SecretObjectReference{Name: "tls"}

		assert.Equal(t,
			types.NamespacedName{Namespace: "infra", Name: "tls"},
			gatewayListener.certificateRefKey(certRef),
		)
		assert.Equal(t,
			types.NamespacedName{Namespace: "tenant-a", Name: "tls"},
			listenerSetListener.certificateRefKey(certRef),
			"resolving in the Gateway's namespace would read a Secret the ListenerSet never named",
		)
	})

	t.Run("a qualified certificateRef keeps its own namespace", func(t *testing.T) {
		elsewhere := gatewayv1.Namespace("certs")
		certRef := gatewayv1.SecretObjectReference{Name: "tls", Namespace: &elsewhere}

		assert.Equal(t,
			types.NamespacedName{Namespace: "certs", Name: "tls"},
			listenerSetListener.certificateRefKey(certRef),
		)
	})
}

// A verdict the vocabulary cannot spell must not come back as an empty condition
// set. That value already means something else here -- a check with nothing to
// report -- and taking it that way has a consequence well past a missing line in a
// status: with no Conflicted condition, BuildConditions leaves Accepted true and
// Valid set, so UpdateListenerProgrammedCondition stops skipping the listener and
// marks it Programmed, while the virtual-listener builder never put it in a
// frontend. A listener reported as programmed that serves nothing.
//
// The reachable case is the one below: checkConflict is the only check whose
// verdict is read from data rather than written as a constant.
func TestUnspellableVerdictRejectsTheListenerInstead(t *testing.T) {
	l, _ := newListenerForPortTest(80)

	rendered := l.conditionsFor(conditions.ListenerConditionVerdict("NotAVerdict"), "ignored")

	accepted, exists := rendered.GetCondition(generic.ConditionType(gatewayv1.ListenerConditionAccepted))
	assert.True(t, exists, "the fallback must still say something, or the listener reads as accepted")
	assert.Equal(t, metav1.ConditionFalse, accepted.Status)
	assert.Equal(t, string(gatewayv1.ListenerReasonInvalid), accepted.Reason)
	assert.Contains(t, accepted.Message, "NotAVerdict",
		"the gap belongs in the message, where someone reading the resource can see it")
	assert.Contains(t, accepted.Message, l.Key().String(),
		"and it has to name which listener could not be reported")
}

// The whole chain, from the one check that reads its verdict from the conflict
// table: a table entry claiming a conflict with a verdict nobody can spell must
