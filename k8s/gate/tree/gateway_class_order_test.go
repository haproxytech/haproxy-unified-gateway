// Copyright 2026 HAProxy Technologies LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
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

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestGatewayClassUpdatePreservesDirectUpdates(t *testing.T) {
	class := &gatewayv1.GatewayClass{Name: "test"}
	gateway := &gatewayv1.Gateway{
		Name: "gateway", Namespace: "default", Generation: 1,
		Spec: gatewayv1.GatewaySpec{GatewayClassName: "test"},
	}
	updated := gateway.DeepCopy()
	updated.Generation = 2
	unrelated := &gatewayv1.Gateway{
		Name: "unrelated", Namespace: "default",
		Spec: gatewayv1.GatewaySpec{GatewayClassName: "other"},
	}
	key := client.ObjectKeyFromObject(gateway)
	unrelatedKey := client.ObjectKeyFromObject(unrelated)
	classKey := client.ObjectKeyFromObject(class)
	cs := &ControllerStore{
		ClusterStore: &store.ClusterStore{
			Gateways: map[client.ObjectKey]*gatewayv1.Gateway{key: updated, unrelatedKey: unrelated},
			Updates:  store.NewClusterUpdates(),
		},
		GateTree:          NewGateTree(),
		UnmanagedGateTree: NewGateTree(),
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	cs.GateTree.GatewayClasses[classKey] = &GatewayClass{K8sResource: class, Valid: true}
	cs.ClusterStore.Updates.GatewayClasses[classKey] = store.Update[*gatewayv1.GatewayClass]{
		NewObject: class, Status: store.StatusUpserted,
	}
	cs.ClusterStore.Updates.Gateways[key] = store.Update[*gatewayv1.Gateway]{
		OldObject: gateway, NewObject: updated, Status: store.StatusUpserted,
	}

	builder := NewGatewayBuilder(GatewayBuilderParams{ControllerStore: cs})
	builder.ComputeTreeUpdates()

	update := cs.ClusterStore.Updates.Gateways[key]
	require.Same(t, gateway, update.OldObject)
	require.Same(t, updated, update.NewObject)
	require.False(t, update.Indirect)
	require.NotContains(t, cs.ClusterStore.Updates.Gateways, unrelatedKey)
}

func TestTLSGatewayStatusWithDelayedGatewayClass(t *testing.T) {
	for _, delayed := range []bool{false, true} {
		name := "class in the initial batch"
		if delayed {
			name = "class in the following batch"
		}
		t.Run(name, func(t *testing.T) {
			gateway := &gatewayv1.Gateway{
				Name:       "invalid-tls",
				Namespace:  "default",
				Generation: 1,
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: "test",
					Listeners: []gatewayv1.Listener{{
						Name: "https", Port: 443, Protocol: gatewayv1.HTTPSProtocolType,
						TLS: &gatewayv1.ListenerTLSConfig{
							CertificateRefs: []gatewayv1.SecretObjectReference{{Name: "missing"}},
						},
					}},
				},
			}
			class := &gatewayv1.GatewayClass{
				Name: "test", Generation: 1,
				Spec: gatewayv1.GatewayClassSpec{ControllerName: "example.com/controller"},
			}
			key := client.ObjectKeyFromObject(gateway)
			classKey := client.ObjectKeyFromObject(class)
			cs := &ControllerStore{
				ClusterStore: &store.ClusterStore{
					Gateways:       map[client.ObjectKey]*gatewayv1.Gateway{key: gateway},
					GatewayClasses: make(map[client.ObjectKey]*gatewayv1.GatewayClass),
					Updates:        store.NewClusterUpdates(),
				},
				GateTree:               NewGateTree(),
				UnmanagedGateTree:      NewGateTree(),
				InstalledGwAPIVersions: &InstalledVersions{Valid: true},
				Logger:                 slog.New(slog.NewTextHandler(io.Discard, nil)),
			}
			classes := NewGatewayClassBuilder(GatewayClassBuilderParams{ControllerStore: cs})
			gateways := NewGatewayBuilder(GatewayBuilderParams{
				ControllerStore: cs, ReferenceGrantManager: NewReferenceGrantManager(),
			})
			cs.ClusterStore.Updates.Gateways[key] = store.Update[*gatewayv1.Gateway]{
				NewObject: gateway, Status: store.StatusUpserted,
			}
			if delayed {
				classes.ComputeTreeUpdates()
				gateways.ComputeTreeUpdates()
				classes.CleanTreeUpdates()
				gateways.CleanTreeUpdates()
				cs.ClusterStore.Updates = store.NewClusterUpdates()
			}
			cs.ClusterStore.GatewayClasses[classKey] = class
			cs.ClusterStore.Updates.GatewayClasses[classKey] = store.Update[*gatewayv1.GatewayClass]{
				NewObject: class, Status: store.StatusUpserted,
			}
			classes.ComputeTreeUpdates()
			gateways.ComputeTreeUpdates()

			result := cs.GateTree.Gateways[key]
			require.NotNil(t, result, "the class event must recover the stored Gateway without another Gateway event")
			for _, conditionType := range []gatewayv1.GatewayConditionType{gatewayv1.GatewayConditionAccepted, gatewayv1.GatewayConditionProgrammed} {
				condition, ok := result.Conditions.GetCondition(generic.ConditionType(conditionType))
				require.True(t, ok)
				require.EqualValues(t, 1, condition.ObservedGeneration)
			}
			listener := result.OwnListenerByName("https")
			require.NotNil(t, listener)
			resolved, ok := listener.Conditions.GetCondition(generic.ConditionType(gatewayv1.ListenerConditionResolvedRefs))
			require.True(t, ok)
			require.Equal(t, string(gatewayv1.ListenerReasonInvalidCertificateRef), resolved.Reason)
			require.EqualValues(t, 1, resolved.ObservedGeneration)
		})
	}
}
