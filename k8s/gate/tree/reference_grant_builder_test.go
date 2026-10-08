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
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	v1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

func newGrantDependentsStore() (*ControllerStore, Builder) {
	cs := &ControllerStore{
		ClusterStore: &store.ClusterStore{Updates: store.NewClusterUpdates()},
		GateTree:     NewGateTree(),
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return cs, NewReferenceGrantBuilder(cs, NewReferenceGrantManager())
}

func nsPtr(namespace string) *gatewayv1.Namespace {
	ns := gatewayv1.Namespace(namespace)
	return &ns
}

func crossRef(namespace string) gatewayv1.BackendObjectReference {
	return gatewayv1.BackendObjectReference{Name: "svc", Namespace: nsPtr(namespace)}
}

func httpRouteWithRefs(name string, refs ...gatewayv1.BackendObjectReference) *HTTPRoute {
	backendRefs := make([]gatewayv1.HTTPBackendRef, 0, len(refs))
	for _, ref := range refs {
		backendRefs = append(backendRefs, gatewayv1.HTTPBackendRef{
			BackendObjectReference: ref,
		})
	}
	return &HTTPRoute{
		K8sResource: &gatewayv1.HTTPRoute{Namespace: "app", Name: name},
		Rules: []*HTTPRouteRule{{
			K8sResource: gatewayv1.HTTPRouteRule{BackendRefs: backendRefs},
		}},
	}
}

func deletedInfraGrant() store.Update[*gatewayv1beta1.ReferenceGrant] {
	return store.Update[*gatewayv1beta1.ReferenceGrant]{
		OldObject: &gatewayv1beta1.ReferenceGrant{
			Namespace: "infra", Name: "grant",
			Spec: gatewayv1beta1.ReferenceGrantSpec{
				From: []gatewayv1beta1.ReferenceGrantFrom{{
					Group: gatewayv1.GroupName, Kind: "HTTPRoute", Namespace: "app",
				}},
				To: []gatewayv1beta1.ReferenceGrantTo{{Group: "", Kind: "Service"}},
			},
		},
		Status: store.StatusDeleted,
	}
}

// A grant change must recheck its dependents in the same cycle: a revocation
// cannot wait for a watch event that may land in a later batch.
func TestGrantChangeRechecksCrossNamespaceDependents(t *testing.T) {
	cs, builder := newGrantDependentsStore()

	crossRouteKey := types.NamespacedName{Namespace: "app", Name: "cross"}
	sameRouteKey := types.NamespacedName{Namespace: "app", Name: "same"}
	otherRouteKey := types.NamespacedName{Namespace: "app", Name: "other-ns"}
	tlsRouteKey := types.NamespacedName{Namespace: "app", Name: "tls-cross"}
	gatewayKey := types.NamespacedName{Namespace: "app", Name: "gw"}

	cs.GateTree.HTTPRoutes[crossRouteKey] = httpRouteWithRefs("cross", crossRef("infra"))
	cs.GateTree.HTTPRoutes[sameRouteKey] = httpRouteWithRefs("same", gatewayv1.BackendObjectReference{Name: "svc"})
	cs.GateTree.HTTPRoutes[otherRouteKey] = httpRouteWithRefs("other-ns", crossRef("elsewhere"))

	cs.GateTree.TLSRoutes[tlsRouteKey] = &TLSRoute{
		K8sResource: &v1alpha2.TLSRoute{Namespace: "app", Name: "tls-cross"},
		Rules: []*TLSRouteRule{{
			K8sResource: v1alpha2.TLSRouteRule{
				BackendRefs: []gatewayv1.BackendRef{{BackendObjectReference: crossRef("infra")}},
			},
		}},
	}

	cs.GateTree.Gateways[gatewayKey] = &Gateway{
		K8sResource: &gatewayv1.Gateway{Namespace: "app", Name: "gw"},
		Listeners: []*Listener{
			{K8sGatewayListener: &gatewayv1.Listener{
				Name: "https",
				TLS: &gatewayv1.ListenerTLSConfig{
					CertificateRefs: []gatewayv1.SecretObjectReference{{Name: "cert", Namespace: nsPtr("infra")}},
				},
			}},
		},
	}

	grantKey := types.NamespacedName{Namespace: "infra", Name: "grant"}
	cs.ClusterStore.Updates.ReferenceGrants[grantKey] = deletedInfraGrant()

	builder.ComputeTreeUpdates()

	httpUpdate, ok := cs.ClusterStore.Updates.HTTPRoutes[crossRouteKey]
	if assert.True(t, ok, "cross-namespace route must be rechecked") {
		assert.True(t, httpUpdate.Indirect)
		assert.Equal(t, store.StatusUpserted, httpUpdate.Status)
		assert.Same(t, cs.GateTree.HTTPRoutes[crossRouteKey].K8sResource, httpUpdate.NewObject)
	}
	assert.NotContains(t, cs.ClusterStore.Updates.HTTPRoutes, sameRouteKey,
		"same-namespace references need no grant")
	assert.NotContains(t, cs.ClusterStore.Updates.HTTPRoutes, otherRouteKey,
		"references into an unrelated namespace are unaffected")

	tlsUpdate, ok := cs.ClusterStore.Updates.TLSRoutes[tlsRouteKey]
	if assert.True(t, ok, "cross-namespace TLSRoute must be rechecked") {
		assert.True(t, tlsUpdate.Indirect)
	}

	gwUpdate, ok := cs.ClusterStore.Updates.Gateways[gatewayKey]
	if assert.True(t, ok, "gateway with a cross-namespace certificate must be rechecked") {
		assert.True(t, gwUpdate.Indirect)
	}
}

// Dependents updated directly in the same cycle keep their own update: the
// indirect recheck only fills the gaps.
func TestGrantChangeKeepsDirectDependentUpdates(t *testing.T) {
	cs, builder := newGrantDependentsStore()

	routeKey := types.NamespacedName{Namespace: "app", Name: "direct"}
	cs.GateTree.HTTPRoutes[routeKey] = httpRouteWithRefs("direct", crossRef("infra"))

	direct := store.Update[*gatewayv1.HTTPRoute]{
		NewObject: cs.GateTree.HTTPRoutes[routeKey].K8sResource,
		Status:    store.StatusUpserted,
	}
	cs.ClusterStore.Updates.HTTPRoutes[routeKey] = direct

	grantKey := types.NamespacedName{Namespace: "infra", Name: "grant"}
	cs.ClusterStore.Updates.ReferenceGrants[grantKey] = deletedInfraGrant()

	builder.ComputeTreeUpdates()

	assert.Equal(t, direct, cs.ClusterStore.Updates.HTTPRoutes[routeKey],
		"a direct update must not be replaced by an indirect recheck")
}
