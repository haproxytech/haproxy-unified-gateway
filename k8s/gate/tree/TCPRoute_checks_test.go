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
	"io"
	"log/slog"
	"testing"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// Moving a route must drop its previous listener attachments: they are the
// selection input for the port's map.
func TestTCPRouteDetachFromListeners(t *testing.T) {
	listener := &Listener{AttachedRoutes: AttachedRoutes{}}
	route := &TCPRoute{
		K8sResource: &gatewayv1.TCPRoute{Namespace: "ns", Name: "route"},
		Listeners:   utils.NewKeyMap[gatewayv1.ParentReference, []*Listener](utils.ParentRefToKey),
	}
	route.Listeners.Set(gatewayv1.ParentReference{Name: "gw"}, []*Listener{listener})
	listener.AttachedRoutes[client.ObjectKeyFromObject(route.K8sResource)] = struct{}{}

	route.detachFromListeners(ControllerStore{GateTree: NewGateTree()})

	require.Empty(t, listener.AttachedRoutes)
}

// A Service that exists but does not publish the referenced port is an
// unresolvable target, not a healthy one.
func TestTCPRouteRuleChecksServicePort(t *testing.T) {
	cs := ControllerStore{
		GateTree:     NewGateTree(),
		ClusterStore: store.NewClusterStore(),
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		ExtractGVK: func(client.Object) schema.GroupVersionKind {
			return schema.GroupVersionKind{Version: "v1", Kind: "Service"}
		},
	}
	cs.GateTree.Services[types.NamespacedName{Namespace: "ns", Name: "svc"}] = NewService(&corev1.Service{
		Namespace: "ns", Name: "svc",
		Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 8080}}},
	})

	ref := gatewayv1.BackendRef{
		Name: "svc",
		Port: new(gatewayv1.PortNumber(9999)),
	}
	rule := &TCPRouteRule{
		K8sResource:     gatewayv1.TCPRouteRule{BackendRefs: []gatewayv1.BackendRef{ref}},
		CheckBackendRef: utils.NewKeyMap[gatewayv1.BackendObjectReference, CheckResult](utils.BackendObjectReferenceToKey),
	}
	rule.checkBackendRef(&TCPRoute{K8sResource: &gatewayv1.TCPRoute{Namespace: "ns", Name: "route"}}, cs, NewReferenceGrantManager())

	check, ok := rule.CheckBackendRef.Get(ref.BackendObjectReference)
	require.True(t, ok)
	require.False(t, check.Valid)
	require.False(t, rule.Valid)
}
