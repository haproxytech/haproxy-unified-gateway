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

	v3 "github.com/haproxytech/haproxy-unified-gateway/api/gate/v3"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

func TestHugConfTrustedBackendCRNamespaces(t *testing.T) {
	httpKey := types.NamespacedName{Namespace: "team-a", Name: "http"}
	tlsKey := types.NamespacedName{Namespace: "team-a", Name: "tls"}
	tcpKey := types.NamespacedName{Namespace: "team-a", Name: "tcp"}

	newBuilder := func() *HugConfBuilderImpl {
		gateTree := NewGateTree()
		gateTree.HTTPRoutes[httpKey] = &HTTPRoute{K8sResource: &gatewayv1.HTTPRoute{}}
		gateTree.TLSRoutes[tlsKey] = &TLSRoute{K8sResource: &gatewayv1alpha2.TLSRoute{}}
		gateTree.TCPRoutes[tcpKey] = &TCPRoute{K8sResource: &gatewayv1.TCPRoute{}}
		return &HugConfBuilderImpl{ControllerStore: &ControllerStore{
			ClusterStore: &store.ClusterStore{Updates: store.NewClusterUpdates()},
			GateTree:     gateTree,
			Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		}}
	}
	upsert := func(b *HugConfBuilderImpl, namespaces ...string) {
		conf := &v3.HugConf{Spec: v3.ControllerConfSpec{TrustedBackendCRNamespaces: namespaces}}
		b.onUpsertedSubsystemBackendCR(store.Update[*v3.HugConf]{NewObject: conf, Status: store.StatusUpserted})
	}
	routesScheduled := func(b *HugConfBuilderImpl) int {
		u := b.ClusterStore.Updates
		return len(u.HTTPRoutes) + len(u.TLSRoutes) + len(u.TCPRoutes)
	}

	t.Run("no namespace is trusted by default", func(t *testing.T) {
		b := newBuilder()
		upsert(b)
		assert.Empty(t, b.TrustedBackendCRNamespaces)
		assert.Zero(t, routesScheduled(b))
	})

	t.Run("a new trusted namespace rebuilds every route", func(t *testing.T) {
		b := newBuilder()
		upsert(b, "team-a")
		assert.Contains(t, b.TrustedBackendCRNamespaces, "team-a")
		assert.Equal(t, store.StatusUpserted, b.ClusterStore.Updates.HTTPRoutes[httpKey].Status)
		assert.Equal(t, store.StatusUpserted, b.ClusterStore.Updates.TLSRoutes[tlsKey].Status)
		assert.Equal(t, store.StatusUpserted, b.ClusterStore.Updates.TCPRoutes[tcpKey].Status)
	})

	t.Run("an unchanged list rebuilds nothing", func(t *testing.T) {
		b := newBuilder()
		upsert(b, "team-a")
		b.ClusterStore.Updates = store.NewClusterUpdates()
		upsert(b, "team-a")
		assert.Zero(t, routesScheduled(b))
	})

	t.Run("a pending route update is kept", func(t *testing.T) {
		b := newBuilder()
		b.ClusterStore.Updates.HTTPRoutes[httpKey] = store.Update[*gatewayv1.HTTPRoute]{Status: store.StatusDeleted}
		upsert(b, "team-a")
		assert.Equal(t, store.StatusDeleted, b.ClusterStore.Updates.HTTPRoutes[httpKey].Status)
	})

	t.Run("deleting the HugConf removes the trust", func(t *testing.T) {
		b := newBuilder()
		upsert(b, "team-a")
		b.ClusterStore.Updates = store.NewClusterUpdates()
		b.onDeletedSubsystemBackendCR()
		assert.Empty(t, b.TrustedBackendCRNamespaces)
		assert.Equal(t, 3, routesScheduled(b))
	})
}

func TestIsBackendCRNamespaceTrusted(t *testing.T) {
	tests := []struct {
		name      string
		patterns  []string
		namespace string
		want      bool
	}{
		{"empty list trusts nothing", nil, "team-a", false},
		{"exact name", []string{"infra"}, "infra", true},
		{"exact name does not match a longer name", []string{"infra"}, "infra-evil", false},
		{"wildcard suffix", []string{"company-xyz-*"}, "company-xyz-payments", true},
		{"wildcard does not match another prefix", []string{"company-xyz-*"}, "company-abc-payments", false},
		{"one of several patterns", []string{"infra", "team-*"}, "team-a", true},
		{"malformed pattern matches nothing", []string{"team-["}, "team-[", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := &ControllerStore{TrustedBackendCRNamespaces: tt.patterns}
			assert.Equal(t, tt.want, cs.IsBackendCRNamespaceTrusted(tt.namespace))
		})
	}
}
