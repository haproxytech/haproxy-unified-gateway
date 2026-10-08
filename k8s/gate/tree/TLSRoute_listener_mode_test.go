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
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

func modePtr(m gatewayv1.TLSModeType) *gatewayv1.TLSModeType {
	return &m
}

// tlsListener is a TLS listener on the test Gateway, in the given TLS mode.
func tlsListener(name string, mode gatewayv1.TLSModeType) gatewayv1.Listener {
	return gatewayv1.Listener{
		Name:     gatewayv1.SectionName(name),
		Port:     443,
		Protocol: gatewayv1.TLSProtocolType,
		TLS:      &gatewayv1.ListenerTLSConfig{Mode: modePtr(mode)},
	}
}

func newTLSRouteFixture(t *testing.T, listeners ...gatewayv1.Listener) (*ControllerStore, client.ObjectKey) {
	t.Helper()

	gwKey := client.ObjectKey{Namespace: "infra", Name: "gw"}
	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: gwKey.Namespace, Name: gwKey.Name},
		Spec:       gatewayv1.GatewaySpec{GatewayClassName: "hug", Listeners: listeners},
	}

	treeGw := &Gateway{
		K8sResource: gw,
		TreeStatus:  TreeUpdate[Gateway]{Status: store.StatusUpserted},
	}
	treeGw.constructOwnListeners()

	// The Gateway builder normally fills these in; the route checks need them.
	//
	// Accepted is deliberately taken from the listener's own checkProtocol verdict,
	// not hand-set: a TLS listener in Terminate mode is rejected there
	// (Accepted=False, UnsupportedProtocol), and the route is supposed to consume
	// that precomputed verdict. allowedRoutes on the other hand allows TLSRoute on
	// *every* listener, so only the listener verdict can keep the route off a
	// Terminate one.
	for _, l := range treeGw.Listeners {
		l.AllowedRouteKinds = []gatewayv1.RouteGroupKind{{Kind: "TLSRoute"}}
		l.checkProtocol(gateManagedRouteGroupKindsByProtocol)
		l.Accepted = l.CheckProtocol.Valid
	}

	cs := &ControllerStore{
		ClusterStore: &store.ClusterStore{
			Gateways:   map[types.NamespacedName]*gatewayv1.Gateway{gwKey: gw},
			Namespaces: map[types.NamespacedName]*v1.Namespace{},
		},
		GateTree: &GateTree{
			Gateways: map[types.NamespacedName]*Gateway{gwKey: treeGw},
		},
		ExtractGVK: func(obj client.Object) schema.GroupVersionKind {
			switch obj.(type) {
			case *gatewayv1alpha2.TLSRoute:
				return schema.GroupVersionKind{Group: gatewayv1.GroupName, Version: "v1alpha2", Kind: "TLSRoute"}
			case *gatewayv1.ListenerSet:
				return schema.GroupVersionKind{Group: gatewayv1.GroupName, Version: "v1", Kind: "ListenerSet"}
			default:
				return schema.GroupVersionKind{Group: gatewayv1.GroupName, Version: "v1", Kind: "Gateway"}
			}
		},
	}
	return cs, gwKey
}

func tlsRouteOn(gwKey client.ObjectKey, sectionName *gatewayv1.SectionName) (*TLSRoute, gatewayv1.ParentReference) {
	namespace := gatewayv1.Namespace(gwKey.Namespace)
	parentRef := gatewayv1.ParentReference{
		Namespace:   &namespace,
		Name:        gatewayv1.ObjectName(gwKey.Name),
		SectionName: sectionName,
	}

	r := NewTLSRoute(&gatewayv1alpha2.TLSRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: gwKey.Namespace, Name: "route"},
		Spec: gatewayv1alpha2.TLSRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{parentRef},
			},
		},
	}, "controller")
	return r, parentRef
}

func acceptedReason(t *testing.T, conds generic.Conditions) string {
	t.Helper()
	cond, ok := conds[generic.ConditionType(gatewayv1.RouteConditionAccepted)]
	require.True(t, ok, "every parentRef must get an Accepted condition")
	return cond.Reason
}

// The mode rule lives in the listener, not in the route: checkProtocol already made a
// Terminate listener not Accepted, and the route must simply not attach to it -- no
// re-reading of the TLS mode per route.
func TestTLSRouteTerminateListenerIsRejectedByItsOwnVerdict(t *testing.T) {
	cs, gwKey := newTLSRouteFixture(t, tlsListener("tls-terminate", gatewayv1.TLSModeTerminate))

	terminate := cs.GateTree.Gateways[types.NamespacedName(gwKey)].OwnListenerByName("tls-terminate")
	require.NotNil(t, terminate)
	require.False(t, terminate.Accepted, "checkProtocol must have rejected the Terminate listener")

	r, parentRef := tlsRouteOn(gwKey, nil)
	result := r.checkParentRef(parentRef, *cs)

	require.True(t, result.Managed)
	assert.False(t, result.Valid)
	assert.Equal(t, string(gatewayv1.RouteReasonNoMatchingParent), acceptedReason(t, result.Conditions))
	assert.Empty(t, terminate.AttachedRoutes, "a non-Accepted listener must not receive the TLSRoute")
}

// The same rejection holds when the listener is named explicitly: the sectionName
// branch must not bypass the listener's Accepted verdict.
func TestTLSRouteSectionNameOnTerminateListenerIsRejected(t *testing.T) {
	cs, gwKey := newTLSRouteFixture(t, tlsListener("tls-terminate", gatewayv1.TLSModeTerminate))
	section := gatewayv1.SectionName("tls-terminate")
	r, parentRef := tlsRouteOn(gwKey, &section)

	result := r.checkParentRef(parentRef, *cs)

	require.True(t, result.Managed)
	assert.False(t, result.Valid)
	assert.Equal(t, string(gatewayv1.RouteReasonNoMatchingParent), acceptedReason(t, result.Conditions))
}

// A Passthrough listener is Accepted: the route attaches to it, and the Terminate
// sibling on the same Gateway is left out by its own verdict.
func TestTLSRouteAttachesOnlyToPassthroughListener(t *testing.T) {
	cs, gwKey := newTLSRouteFixture(t,
		tlsListener("tls-terminate", gatewayv1.TLSModeTerminate),
		tlsListener("tls-passthrough", gatewayv1.TLSModePassthrough),
	)
	r, parentRef := tlsRouteOn(gwKey, nil)

	result := r.checkParentRef(parentRef, *cs)

	require.True(t, result.Managed)
	assert.True(t, result.Valid)

	gw := cs.GateTree.Gateways[types.NamespacedName(gwKey)]
	require.True(t, gw.OwnListenerByName("tls-passthrough").Accepted)
	assert.Len(t, gw.OwnListenerByName("tls-passthrough").AttachedRoutes, 1,
		"the only listener that may carry a TLSRoute")
	assert.Empty(t, gw.OwnListenerByName("tls-terminate").AttachedRoutes)
}
