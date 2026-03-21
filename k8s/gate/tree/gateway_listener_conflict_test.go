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
	"time"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/protocols"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestComputeListenerConflicts(t *testing.T) {
	t1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	_ = t2

	// Helper to create a Gateway
	mkGateway := func(name string, creationTime time.Time, listeners ...gatewayv1.Listener) *Gateway {
		gw := &gatewayv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:              name,
				Namespace:         "default",
				CreationTimestamp: metav1.NewTime(creationTime),
			},
			Spec: gatewayv1.GatewaySpec{
				Listeners: listeners,
			},
		}
		return &Gateway{
			K8sResource: gw,
		}
	}

	// Helper to create a Listener
	mkListener := func(name string, port int, protocol gatewayv1.ProtocolType, hostname *string) gatewayv1.Listener {
		var h *gatewayv1.Hostname
		if hostname != nil {
			val := gatewayv1.Hostname(*hostname)
			h = &val
		}
		return gatewayv1.Listener{
			Name:     gatewayv1.SectionName(name),
			Port:     gatewayv1.PortNumber(port),
			Protocol: protocol,
			Hostname: h,
		}
	}

	ptr := func(s string) *string { return &s }
	_ = ptr

	tests := []struct {
		name     string
		gateways []*Gateway
		expected map[string]listenerConflictCondition
	}{
		{
			name: "No conflicts - single gateway, different ports",
			gateways: []*Gateway{
				mkGateway("gw1", t1,
					mkListener("l1", 80, gatewayv1.HTTPProtocolType, nil),
					mkListener("l2", 443, gatewayv1.HTTPSProtocolType, nil),
				),
			},
			expected: map[string]listenerConflictCondition{
				"default/gw1_l1": {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryInsecure},
				"default/gw1_l2": {hasConflict: false, reason: "", protocol: protocols.ProtocolCategorySecure},
			},
		},
		{
			name: "Conflict - same port, different protocol categories",
			gateways: []*Gateway{
				mkGateway("gw1", t1,
					mkListener("http", 80, gatewayv1.HTTPProtocolType, nil),
					mkListener("https", 80, gatewayv1.HTTPSProtocolType, nil), // 80 is already HTTP/Insecure
				),
			},
			expected: map[string]listenerConflictCondition{
				"default/gw1_http":  {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryInsecure},
				"default/gw1_https": {hasConflict: true, reason: string(gatewayv1.ListenerReasonProtocolConflict), protocol: protocols.ProtocolCategorySecure},
			},
		},
		{
			name: "No Conflict - same port, same protocol category (Insecure), non-overlapping hostnames",
			gateways: []*Gateway{
				mkGateway("gw1", t1,
					mkListener("h1", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
					mkListener("h2", 80, gatewayv1.HTTPProtocolType, ptr("bar.com")),
				),
			},
			expected: map[string]listenerConflictCondition{
				"default/gw1_h1": {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryInsecure},
				"default/gw1_h2": {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryInsecure},
			},
		},
		{
			name: "Conflict - same port, same protocol category, overlapping hostnames (Exact)",
			gateways: []*Gateway{
				mkGateway("gw1", t1,
					mkListener("h1", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
					mkListener("h2", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
				),
			},
			expected: map[string]listenerConflictCondition{
				"default/gw1_h1": {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryInsecure},
				"default/gw1_h2": {hasConflict: true, reason: string(gatewayv1.ListenerReasonHostnameConflict), protocol: protocols.ProtocolCategoryInsecure},
			},
		},
		{
			name: "No Conflict - wildcard and matching specific hostname coexist (specific beats wildcard)",
			gateways: []*Gateway{
				mkGateway("gw1", t1,
					mkListener("wild", 80, gatewayv1.HTTPProtocolType, ptr("*.example.com")),
					mkListener("exact", 80, gatewayv1.HTTPProtocolType, ptr("foo.example.com")),
				),
			},
			expected: map[string]listenerConflictCondition{
				"default/gw1_wild":  {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryInsecure},
				"default/gw1_exact": {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryInsecure},
			},
		},
		{
			name: "Conflict - same port, identical wildcard hostnames",
			gateways: []*Gateway{
				mkGateway("gw1", t1,
					mkListener("wild1", 80, gatewayv1.HTTPProtocolType, ptr("*.example.com")),
					mkListener("wild2", 80, gatewayv1.HTTPProtocolType, ptr("*.example.com")),
				),
			},
			expected: map[string]listenerConflictCondition{
				"default/gw1_wild1": {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryInsecure},
				"default/gw1_wild2": {hasConflict: true, reason: string(gatewayv1.ListenerReasonHostnameConflict), protocol: protocols.ProtocolCategoryInsecure},
			},
		},
		{
			name: "Conflict - same port, two empty hostnames conflict with each other",
			gateways: []*Gateway{
				mkGateway("gw1", t1,
					mkListener("empty", 80, gatewayv1.HTTPProtocolType, nil),
					mkListener("same", 80, gatewayv1.HTTPProtocolType, nil),
				),
			},
			expected: map[string]listenerConflictCondition{
				"default/gw1_empty": {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryInsecure},
				"default/gw1_same":  {hasConflict: true, reason: string(gatewayv1.ListenerReasonHostnameConflict), protocol: protocols.ProtocolCategoryInsecure},
			},
		},
		{
			name: "No Conflict - empty hostname coexists with specific hostname (catch-all + specific)",
			gateways: []*Gateway{
				mkGateway("gw1", t1,
					mkListener("catch-all", 80, gatewayv1.HTTPProtocolType, nil),
					mkListener("specific", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
				),
			},
			expected: map[string]listenerConflictCondition{
				"default/gw1_catch-all": {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryInsecure},
				"default/gw1_specific":  {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryInsecure},
			},
		},
		{
			// Mirrors the Gateway API conformance manifest same-namespace-with-https-listener:
			// all four listeners must be accepted with no hostname conflict.
			name: "No Conflict - empty, wildcard, specific, and specific-under-wildcard all coexist",
			gateways: []*Gateway{
				mkGateway("gw1", t1,
					mkListener("catch-all", 443, gatewayv1.HTTPSProtocolType, nil),
					mkListener("wildcard", 443, gatewayv1.HTTPSProtocolType, ptr("*.wildcard.org")),
					mkListener("specific", 443, gatewayv1.HTTPSProtocolType, ptr("second-example.org")),
					mkListener("specific-under-wildcard", 443, gatewayv1.HTTPSProtocolType, ptr("fourth-example.wildcard.org")),
				),
			},
			expected: map[string]listenerConflictCondition{
				"default/gw1_catch-all":               {hasConflict: false, reason: "", protocol: protocols.ProtocolCategorySecure},
				"default/gw1_wildcard":                {hasConflict: false, reason: "", protocol: protocols.ProtocolCategorySecure},
				"default/gw1_specific":                {hasConflict: false, reason: "", protocol: protocols.ProtocolCategorySecure},
				"default/gw1_specific-under-wildcard": {hasConflict: false, reason: "", protocol: protocols.ProtocolCategorySecure},
			},
		},
		{
			name: "Multiple Gateways - identical listeners on different gateways are merged (no conflict)",
			gateways: []*Gateway{
				mkGateway("old", t1,
					mkListener("l1", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
				),
				mkGateway("new", t2,
					mkListener("l1", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
				),
			},
			expected: map[string]listenerConflictCondition{
				"default/old_l1": {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryInsecure},
				"default/new_l1": {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryInsecure},
			},
		},
		{
			name: "Multiple Gateways - identical listeners on different gateways are merged (no conflict, reverse order in input)",
			gateways: []*Gateway{
				mkGateway("new", t2,
					mkListener("l1", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
				),
				mkGateway("old", t1,
					mkListener("l1", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
				),
			},
			expected: map[string]listenerConflictCondition{
				"default/old_l1": {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryInsecure},
				"default/new_l1": {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryInsecure},
			},
		},
		{
			name: "Protocol conflict across gateways",
			gateways: []*Gateway{
				mkGateway("old", t1,
					mkListener("http", 80, gatewayv1.HTTPProtocolType, nil),
				),
				mkGateway("new", t2,
					mkListener("https", 80, gatewayv1.HTTPSProtocolType, nil),
				),
			},
			expected: map[string]listenerConflictCondition{
				"default/old_http":  {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryInsecure},
				"default/new_https": {hasConflict: true, reason: string(gatewayv1.ListenerReasonProtocolConflict), protocol: protocols.ProtocolCategorySecure},
			},
		},
		{
			name: "Complex scenario",
			gateways: []*Gateway{
				mkGateway("gw1", t1,
					mkListener("http", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
					mkListener("http2", 80, gatewayv1.HTTPProtocolType, ptr("bar.com")),
				),
				mkGateway("gw2", t2,
					mkListener("ok-foo", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
					mkListener("catch-all", 80, gatewayv1.HTTPProtocolType, nil), // nil = empty hostname, does NOT conflict with ok-foo (catch-all coexists with specific hostnames)
				),
			},
			expected: map[string]listenerConflictCondition{
				"default/gw1_http":      {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryInsecure},
				"default/gw1_http2":     {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryInsecure},
				"default/gw2_ok-foo":    {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryInsecure},
				"default/gw2_catch-all": {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryInsecure},
			},
		},
		{
			name: "Mix of ProtocolTypes and Hostnames on same port",
			gateways: []*Gateway{
				mkGateway("gw1", t1,
					mkListener("http-foo", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),   // Winner (Insecure)
					mkListener("https-bar", 80, gatewayv1.HTTPSProtocolType, ptr("bar.com")), // Conflict (Secure != Insecure), distinct host irrelevant
					mkListener("tls-foo", 80, gatewayv1.TLSProtocolType, ptr("foo.com")),     // Conflict (TLS != Insecure), host irrelevant
					mkListener("http-wild", 80, gatewayv1.HTTPProtocolType, ptr("*.com")),    // Same category. Different hostname string → no hostname conflict.
					mkListener("http-bar", 80, gatewayv1.HTTPProtocolType, ptr("bar.com")),   // Same category. Different hostname string → no conflict.
					mkListener("tcp", 80, gatewayv1.TCPProtocolType, nil),                    // Conflict (Unknown != Insecure)
				),
				mkGateway("gw2", t1,
					mkListener("tls-foo", 443, gatewayv1.TLSProtocolType, ptr("foo.com")),     // Winner (TLS)
					mkListener("https-bar", 443, gatewayv1.HTTPSProtocolType, ptr("bar.com")), // Conflict (Secure != TLS)
					mkListener("tls-wild", 443, gatewayv1.TLSProtocolType, ptr("*.com")),      // Same category. Different hostname string → no conflict.
					mkListener("tls-bar", 443, gatewayv1.TLSProtocolType, ptr("bar.com")),     // Same category. Different hostname string → no conflict.
				),
			},
			expected: map[string]listenerConflictCondition{
				"default/gw1_http-foo":  {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryInsecure},
				"default/gw1_https-bar": {hasConflict: true, reason: string(gatewayv1.ListenerReasonProtocolConflict), protocol: protocols.ProtocolCategorySecure},
				"default/gw1_tls-foo":   {hasConflict: true, reason: string(gatewayv1.ListenerReasonProtocolConflict), protocol: protocols.ProtocolCategoryTLS},
				"default/gw1_http-wild": {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryInsecure},
				"default/gw1_http-bar":  {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryInsecure},
				"default/gw1_tcp":       {hasConflict: true, reason: string(gatewayv1.ListenerReasonProtocolConflict), protocol: protocols.ProcotolCategoryTCP},
				"default/gw2_tls-foo":   {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryTLS},
				"default/gw2_https-bar": {hasConflict: true, reason: string(gatewayv1.ListenerReasonProtocolConflict), protocol: protocols.ProtocolCategorySecure},
				"default/gw2_tls-wild":  {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryTLS},
				"default/gw2_tls-bar":   {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryTLS},
			},
		},
		{
			name: "Same Gateway - overlapping hostname is still a conflict",
			gateways: []*Gateway{
				mkGateway("gw1", t1,
					mkListener("l1", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
					mkListener("l2", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
				),
				mkGateway("gw2", t2,
					mkListener("l1", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
				),
			},
			expected: map[string]listenerConflictCondition{
				// l1 and l2 on same gw1 conflict; gw2/l1 (different gateway) is merged
				"default/gw1_l1": {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryInsecure},
				"default/gw1_l2": {hasConflict: true, reason: string(gatewayv1.ListenerReasonHostnameConflict), protocol: protocols.ProtocolCategoryInsecure},
				"default/gw2_l1": {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryInsecure},
			},
		},
		{
			name: "Nil gateways (DELETED status) should be ignored",
			gateways: []*Gateway{
				mkGateway("gw1", t1,
					mkListener("l1", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
				),
				nil, // Deleted gateway should be ignored
				mkGateway("gw2", t2,
					mkListener("l1", 80, gatewayv1.HTTPProtocolType, ptr("bar.com")),
				),
				nil, // Another deleted gateway
			},
			expected: map[string]listenerConflictCondition{
				"default/gw1_l1": {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryInsecure},
				"default/gw2_l1": {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryInsecure},
			},
		},
		{
			name: "Nil gateway with conflict - only non-nil gateways participate",
			gateways: []*Gateway{
				nil, // Deleted gateway should not claim the port
				mkGateway("gw1", t1,
					mkListener("l1", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
				),
				mkGateway("gw2", t2,
					mkListener("l1", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
				),
			},
			expected: map[string]listenerConflictCondition{
				// Different gateways with identical listeners → merged, no conflict
				"default/gw1_l1": {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryInsecure},
				"default/gw2_l1": {hasConflict: false, reason: "", protocol: protocols.ProtocolCategoryInsecure},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &GatewayBuilderImpl{
				ControllerStore: &ControllerStore{
					GateTree: &GateTree{
						Gateways: make(map[client.ObjectKey]*Gateway),
					},
				},
			}
			b.resetListenerConflicts()

			// Populate store
			for _, gw := range tt.gateways {
				var key client.ObjectKey
				if gw != nil {
					key = client.ObjectKeyFromObject(gw.K8sResource)
				}
				b.GateTree.Gateways[key] = gw
			}

			b.computeListenerConflicts()

			// Flatten results
			actual := make(map[string]listenerConflictCondition)

			for _, res := range b.ControllerStore.mapPort2Listeners {
				for k, v := range res {
					actual[k.String()] = v
				}
			}

			// Verify results
			assert.Equal(t, len(tt.expected), len(actual), "Unexpected number of results")

			for k, expectedVal := range tt.expected {
				actualVal, found := actual[k]
				assert.True(t, found, "Expected result for %s not found", k)
				assert.Equal(t, expectedVal, actualVal, "Mismatch for %s", k)
			}
		})
	}
}

func TestNonDeletedGateways(t *testing.T) {
	t1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	mkGateway := func(name string, status store.Status) *Gateway {
		gw := &gatewayv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:              name,
				Namespace:         "default",
				CreationTimestamp: metav1.NewTime(t1),
			},
		}
		return &Gateway{
			K8sResource: gw,
			TreeStatus:  TreeUpdate[Gateway]{Status: status},
		}
	}

	newBuilder := func(gateways map[client.ObjectKey]*Gateway) *GatewayBuilderImpl {
		return &GatewayBuilderImpl{
			ControllerStore: &ControllerStore{
				GateTree: &GateTree{Gateways: gateways},
			},
		}
	}

	t.Run("empty map returns empty result", func(t *testing.T) {
		b := newBuilder(make(map[client.ObjectKey]*Gateway))
		assert.Empty(t, b.nonDeletedGateways())
	})

	t.Run("upserted gateway is included", func(t *testing.T) {
		gw := mkGateway("gw1", store.StatusUpserted)
		key := client.ObjectKeyFromObject(gw.K8sResource)
		b := newBuilder(map[client.ObjectKey]*Gateway{key: gw})
		result := b.nonDeletedGateways()
		assert.Len(t, result, 1)
		assert.Equal(t, gw, result[key])
	})

	t.Run("deleted gateway is excluded", func(t *testing.T) {
		gw := mkGateway("gw1", store.StatusDeleted)
		key := client.ObjectKeyFromObject(gw.K8sResource)
		b := newBuilder(map[client.ObjectKey]*Gateway{key: gw})
		assert.Empty(t, b.nonDeletedGateways())
	})

	t.Run("nil gateway value is excluded", func(t *testing.T) {
		key := client.ObjectKey{Namespace: "default", Name: "gw1"}
		b := newBuilder(map[client.ObjectKey]*Gateway{key: nil})
		assert.Empty(t, b.nonDeletedGateways())
	})

	t.Run("gateway with nil K8sResource is excluded", func(t *testing.T) {
		gw := &Gateway{TreeStatus: TreeUpdate[Gateway]{Status: store.StatusUpserted}}
		key := client.ObjectKey{Namespace: "default", Name: "gw1"}
		b := newBuilder(map[client.ObjectKey]*Gateway{key: gw})
		assert.Empty(t, b.nonDeletedGateways())
	})

	t.Run("mix of deleted and non-deleted", func(t *testing.T) {
		gwAlive := mkGateway("alive", store.StatusUpserted)
		gwDead := mkGateway("dead", store.StatusDeleted)
		aliveKey := client.ObjectKeyFromObject(gwAlive.K8sResource)
		deadKey := client.ObjectKeyFromObject(gwDead.K8sResource)
		b := newBuilder(map[client.ObjectKey]*Gateway{aliveKey: gwAlive, deadKey: gwDead})
		result := b.nonDeletedGateways()
		assert.Len(t, result, 1)
		assert.Equal(t, gwAlive, result[aliveKey])
		assert.NotContains(t, result, deadKey)
	})
}

func TestListenersPerPort(t *testing.T) {
	t1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	mkGateway := func(name string, creationTime time.Time, listeners ...gatewayv1.Listener) *Gateway {
		gw := &gatewayv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:              name,
				Namespace:         "default",
				CreationTimestamp: metav1.NewTime(creationTime),
			},
			Spec: gatewayv1.GatewaySpec{Listeners: listeners},
		}
		return &Gateway{K8sResource: gw}
	}

	mkListener := func(name string, port int, protocol gatewayv1.ProtocolType) gatewayv1.Listener {
		return gatewayv1.Listener{
			Name:     gatewayv1.SectionName(name),
			Port:     gatewayv1.PortNumber(port),
			Protocol: protocol,
		}
	}

	newBuilder := func() *GatewayBuilderImpl {
		b := &GatewayBuilderImpl{
			ControllerStore: &ControllerStore{
				GateTree: &GateTree{Gateways: make(map[client.ObjectKey]*Gateway)},
			},
		}
		b.resetListenerConflicts()
		return b
	}

	t.Run("single listener goes to portListeners with no conflict", func(t *testing.T) {
		gw := mkGateway("gw1", t1, mkListener("l1", 80, gatewayv1.HTTPProtocolType))
		b := newBuilder()
		result := b.listenersPerPort([]*Gateway{gw})
		assert.Len(t, result[80], 1)
		assert.Equal(t, "l1", string(result[80][0].listenerRef.Name))
		assert.Empty(t, b.ControllerStore.mapPort2Listeners)
	})

	t.Run("different ports, no conflicts", func(t *testing.T) {
		gw := mkGateway("gw1", t1,
			mkListener("http", 80, gatewayv1.HTTPProtocolType),
			mkListener("https", 443, gatewayv1.HTTPSProtocolType),
		)
		b := newBuilder()
		result := b.listenersPerPort([]*Gateway{gw})
		assert.Len(t, result, 2)
		assert.Len(t, result[80], 1)
		assert.Len(t, result[443], 1)
		assert.Empty(t, b.ControllerStore.mapPort2Listeners)
	})

	t.Run("same port, same protocol category -> both in portListeners, no conflict", func(t *testing.T) {
		gw := mkGateway("gw1", t1,
			mkListener("l1", 80, gatewayv1.HTTPProtocolType),
			mkListener("l2", 80, gatewayv1.HTTPProtocolType),
		)
		b := newBuilder()
		result := b.listenersPerPort([]*Gateway{gw})
		assert.Len(t, result[80], 2)
		assert.Empty(t, b.ControllerStore.mapPort2Listeners)
	})

	t.Run("same port, different protocol categories -> first in portListeners, second is ProtocolConflict", func(t *testing.T) {
		gw := mkGateway("gw1", t1,
			mkListener("http", 80, gatewayv1.HTTPProtocolType),
			mkListener("https", 80, gatewayv1.HTTPSProtocolType),
		)
		b := newBuilder()
		result := b.listenersPerPort([]*Gateway{gw})

		assert.Len(t, result[80], 1)
		assert.Equal(t, "http", string(result[80][0].listenerRef.Name))

		conflictsOnPort80 := b.ControllerStore.mapPort2Listeners[80]
		assert.Len(t, conflictsOnPort80, 1)
		httpsKey := NewListenerKey(gw.K8sResource, gw.K8sResource.Spec.Listeners[1])
		lcc := conflictsOnPort80[httpsKey]
		assert.True(t, lcc.hasConflict)
		assert.Equal(t, string(gatewayv1.ListenerReasonProtocolConflict), lcc.reason)
		assert.Equal(t, protocols.ProtocolCategorySecure, lcc.protocol)
	})

	t.Run("multiple gateways, same port, same protocol -> all in portListeners", func(t *testing.T) {
		gw1 := mkGateway("gw1", t1, mkListener("l1", 80, gatewayv1.HTTPProtocolType))
		gw2 := mkGateway("gw2", t2, mkListener("l2", 80, gatewayv1.HTTPProtocolType))
		b := newBuilder()
		result := b.listenersPerPort([]*Gateway{gw1, gw2})
		assert.Len(t, result[80], 2)
		assert.Empty(t, b.ControllerStore.mapPort2Listeners)
	})

	t.Run("multiple gateways, same port, different protocols -> first wins, second is ProtocolConflict", func(t *testing.T) {
		gw1 := mkGateway("gw1", t1, mkListener("http", 80, gatewayv1.HTTPProtocolType))
		gw2 := mkGateway("gw2", t2, mkListener("https", 80, gatewayv1.HTTPSProtocolType))
		b := newBuilder()
		result := b.listenersPerPort([]*Gateway{gw1, gw2})

		assert.Len(t, result[80], 1)
		assert.Equal(t, "http", string(result[80][0].listenerRef.Name))

		conflictsOnPort80 := b.ControllerStore.mapPort2Listeners[80]
		assert.Len(t, conflictsOnPort80, 1)
		httpsKey := NewListenerKey(gw2.K8sResource, gw2.K8sResource.Spec.Listeners[0])
		lcc := conflictsOnPort80[httpsKey]
		assert.True(t, lcc.hasConflict)
		assert.Equal(t, string(gatewayv1.ListenerReasonProtocolConflict), lcc.reason)
	})

	t.Run("nil and deleted gateways are skipped", func(t *testing.T) {
		alive := mkGateway("alive", t1, mkListener("l1", 80, gatewayv1.HTTPProtocolType))
		deleted := mkGateway("deleted", t2, mkListener("l2", 80, gatewayv1.HTTPProtocolType))
		deleted.TreeStatus.Status = store.StatusDeleted
		b := newBuilder()
		result := b.listenersPerPort([]*Gateway{nil, deleted, alive})
		assert.Len(t, result[80], 1)
		assert.Equal(t, "l1", string(result[80][0].listenerRef.Name))
	})
}
