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
	"github.com/stretchr/testify/require"
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
			Name:              name,
			Namespace:         "default",
			CreationTimestamp: metav1.NewTime(creationTime),
			Spec: gatewayv1.GatewaySpec{
				Listeners: listeners,
			},
		}
		treeGw := &Gateway{
			K8sResource: gw,
		}
		// Detection compares the merged listener lists, not the specs, so the list
		// has to be built first -- as the builder does before the detection pass.
		treeGw.constructOwnListeners()
		return treeGw
	}

	// Helper to create a synthetic ingress Gateway (GatewayForIngress set).
	mkIngressGateway := func(name string, creationTime time.Time, listeners ...gatewayv1.Listener) *Gateway {
		g := mkGateway(name, creationTime, listeners...)
		g.GatewayForIngress = true
		return g
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
		expected map[string]conflictKinds
	}{
		{
			name: "No conflicts - single gateway, different ports",
			gateways: []*Gateway{
				mkGateway(
					"gw1", t1,
					mkListener("l1", 80, gatewayv1.HTTPProtocolType, nil),
					mkListener("l2", 443, gatewayv1.HTTPSProtocolType, nil),
				),
			},
			expected: map[string]conflictKinds{
				"default/gw1_l1": noConflict,
				"default/gw1_l2": noConflict,
			},
		},
		{
			name: "Conflict - same port, different protocol categories",
			gateways: []*Gateway{
				mkGateway(
					"gw1", t1,
					mkListener("http", 80, gatewayv1.HTTPProtocolType, nil),
					mkListener("https", 80, gatewayv1.HTTPSProtocolType, nil), // 80 is already HTTP/Insecure
				),
			},
			expected: map[string]conflictKinds{
				"default/gw1_http":  noConflict,
				"default/gw1_https": conflictOnProtocol,
			},
		},
		{
			name: "No Conflict - same port, same protocol category (Insecure), non-overlapping hostnames",
			gateways: []*Gateway{
				mkGateway(
					"gw1", t1,
					mkListener("h1", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
					mkListener("h2", 80, gatewayv1.HTTPProtocolType, ptr("bar.com")),
				),
			},
			expected: map[string]conflictKinds{
				"default/gw1_h1": noConflict,
				"default/gw1_h2": noConflict,
			},
		},
		{
			name: "Conflict - same port, same protocol category, overlapping hostnames (Exact)",
			gateways: []*Gateway{
				mkGateway(
					"gw1", t1,
					mkListener("h1", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
					mkListener("h2", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
				),
			},
			expected: map[string]conflictKinds{
				"default/gw1_h1": noConflict,
				"default/gw1_h2": conflictOnHostname,
			},
		},
		{
			name: "No Conflict - wildcard and matching specific hostname coexist (specific beats wildcard)",
			gateways: []*Gateway{
				mkGateway(
					"gw1", t1,
					mkListener("wild", 80, gatewayv1.HTTPProtocolType, ptr("*.example.com")),
					mkListener("exact", 80, gatewayv1.HTTPProtocolType, ptr("foo.example.com")),
				),
			},
			expected: map[string]conflictKinds{
				"default/gw1_wild":  noConflict,
				"default/gw1_exact": noConflict,
			},
		},
		{
			name: "Conflict - same port, identical wildcard hostnames",
			gateways: []*Gateway{
				mkGateway(
					"gw1", t1,
					mkListener("wild1", 80, gatewayv1.HTTPProtocolType, ptr("*.example.com")),
					mkListener("wild2", 80, gatewayv1.HTTPProtocolType, ptr("*.example.com")),
				),
			},
			expected: map[string]conflictKinds{
				"default/gw1_wild1": noConflict,
				"default/gw1_wild2": conflictOnHostname,
			},
		},
		{
			name: "Conflict - same port, two empty hostnames conflict with each other",
			gateways: []*Gateway{
				mkGateway(
					"gw1", t1,
					mkListener("empty", 80, gatewayv1.HTTPProtocolType, nil),
					mkListener("same", 80, gatewayv1.HTTPProtocolType, nil),
				),
			},
			expected: map[string]conflictKinds{
				"default/gw1_empty": noConflict,
				"default/gw1_same":  conflictOnHostname,
			},
		},
		{
			name: "No Conflict - empty hostname coexists with specific hostname (catch-all + specific)",
			gateways: []*Gateway{
				mkGateway(
					"gw1", t1,
					mkListener("catch-all", 80, gatewayv1.HTTPProtocolType, nil),
					mkListener("specific", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
				),
			},
			expected: map[string]conflictKinds{
				"default/gw1_catch-all": noConflict,
				"default/gw1_specific":  noConflict,
			},
		},
		{
			// Mirrors the Gateway API conformance manifest same-namespace-with-https-listener:
			// all four listeners must be accepted with no hostname conflict.
			name: "No Conflict - empty, wildcard, specific, and specific-under-wildcard all coexist",
			gateways: []*Gateway{
				mkGateway(
					"gw1", t1,
					mkListener("catch-all", 443, gatewayv1.HTTPSProtocolType, nil),
					mkListener("wildcard", 443, gatewayv1.HTTPSProtocolType, ptr("*.wildcard.org")),
					mkListener("specific", 443, gatewayv1.HTTPSProtocolType, ptr("second-example.org")),
					mkListener("specific-under-wildcard", 443, gatewayv1.HTTPSProtocolType, ptr("fourth-example.wildcard.org")),
				),
			},
			expected: map[string]conflictKinds{
				"default/gw1_catch-all":               noConflict,
				"default/gw1_wildcard":                noConflict,
				"default/gw1_specific":                noConflict,
				"default/gw1_specific-under-wildcard": noConflict,
			},
		},
		{
			name: "Multiple Gateways - identical listeners on different gateways are merged (no conflict)",
			gateways: []*Gateway{
				mkGateway(
					"old", t1,
					mkListener("l1", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
				),
				mkGateway(
					"new", t2,
					mkListener("l1", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
				),
			},
			expected: map[string]conflictKinds{
				"default/old_l1": noConflict,
				"default/new_l1": noConflict,
			},
		},
		{
			name: "Multiple Gateways - identical listeners on different gateways are merged (no conflict, reverse order in input)",
			gateways: []*Gateway{
				mkGateway(
					"new", t2,
					mkListener("l1", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
				),
				mkGateway(
					"old", t1,
					mkListener("l1", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
				),
			},
			expected: map[string]conflictKinds{
				"default/old_l1": noConflict,
				"default/new_l1": noConflict,
			},
		},
		{
			name: "Protocol conflict across gateways",
			gateways: []*Gateway{
				mkGateway(
					"old", t1,
					mkListener("http", 80, gatewayv1.HTTPProtocolType, nil),
				),
				mkGateway(
					"new", t2,
					mkListener("https", 80, gatewayv1.HTTPSProtocolType, nil),
				),
			},
			expected: map[string]conflictKinds{
				"default/old_http":  noConflict,
				"default/new_https": conflictOnProtocol,
			},
		},
		{
			// GatewayForIngress must win the port conflict even when the synthetic
			// gateway is newer than the real one. It is processed first regardless
			// of creation timestamp, so it claims the port's protocol category and
			// the real listener conflicts. Without that rule the oldest-wins default
			// would let the real (older) Gateway displace ingress traffic.
			name: "Ingress gateway wins the port conflict despite being newer",
			gateways: []*Gateway{
				mkGateway(
					"real", t1,
					mkListener("http", 80, gatewayv1.HTTPProtocolType, nil),
				),
				mkIngressGateway(
					"ing", t2,
					mkListener("https", 80, gatewayv1.HTTPSProtocolType, nil),
				),
			},
			expected: map[string]conflictKinds{
				"default/ing_https": noConflict,
				"default/real_http": conflictOnProtocol,
			},
		},
		{
			name: "Complex scenario",
			gateways: []*Gateway{
				mkGateway(
					"gw1", t1,
					mkListener("http", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
					mkListener("http2", 80, gatewayv1.HTTPProtocolType, ptr("bar.com")),
				),
				mkGateway(
					"gw2", t2,
					mkListener("ok-foo", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
					mkListener("catch-all", 80, gatewayv1.HTTPProtocolType, nil), // nil = empty hostname, does NOT conflict with ok-foo (catch-all coexists with specific hostnames)
				),
			},
			expected: map[string]conflictKinds{
				"default/gw1_http":      noConflict,
				"default/gw1_http2":     noConflict,
				"default/gw2_ok-foo":    noConflict,
				"default/gw2_catch-all": noConflict,
			},
		},
		{
			name: "Mix of ProtocolTypes and Hostnames on same port",
			gateways: []*Gateway{
				mkGateway(
					"gw1", t1,
					mkListener("http-foo", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),   // Winner (Insecure)
					mkListener("https-bar", 80, gatewayv1.HTTPSProtocolType, ptr("bar.com")), // Conflict (Secure != Insecure), distinct host irrelevant
					mkListener("tls-foo", 80, gatewayv1.TLSProtocolType, ptr("foo.com")),     // Conflict (TLS != Insecure), host irrelevant
					mkListener("http-wild", 80, gatewayv1.HTTPProtocolType, ptr("*.com")),    // Same category. Different hostname string → no hostname conflict.
					mkListener("http-bar", 80, gatewayv1.HTTPProtocolType, ptr("bar.com")),   // Same category. Different hostname string → no conflict.
					mkListener("tcp", 80, gatewayv1.TCPProtocolType, nil),                    // Conflict (Unknown != Insecure)
				),
				mkGateway(
					"gw2", t1,
					mkListener("tls-foo", 443, gatewayv1.TLSProtocolType, ptr("foo.com")),     // Winner (TLS)
					mkListener("https-bar", 443, gatewayv1.HTTPSProtocolType, ptr("bar.com")), // Conflict (Secure != TLS)
					mkListener("tls-wild", 443, gatewayv1.TLSProtocolType, ptr("*.com")),      // Same category. Different hostname string → no conflict.
					mkListener("tls-bar", 443, gatewayv1.TLSProtocolType, ptr("bar.com")),     // Same category. Different hostname string → no conflict.
				),
			},
			expected: map[string]conflictKinds{
				"default/gw1_http-foo":  noConflict,
				"default/gw1_https-bar": conflictOnProtocol,
				"default/gw1_tls-foo":   conflictOnProtocol,
				"default/gw1_http-wild": noConflict,
				"default/gw1_http-bar":  noConflict,
				"default/gw1_tcp":       conflictOnProtocol,
				"default/gw2_tls-foo":   noConflict,
				"default/gw2_https-bar": conflictOnProtocol,
				"default/gw2_tls-wild":  noConflict,
				"default/gw2_tls-bar":   noConflict,
			},
		},
		{
			name: "Same Gateway - overlapping hostname is still a conflict",
			gateways: []*Gateway{
				mkGateway(
					"gw1", t1,
					mkListener("l1", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
					mkListener("l2", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
				),
				mkGateway(
					"gw2", t2,
					mkListener("l1", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
				),
			},
			expected: map[string]conflictKinds{
				// l1 and l2 on same gw1 conflict; gw2/l1 (different gateway) is merged
				"default/gw1_l1": noConflict,
				"default/gw1_l2": conflictOnHostname,
				"default/gw2_l1": noConflict,
			},
		},
		{
			name: "Nil gateways (DELETED status) should be ignored",
			gateways: []*Gateway{
				mkGateway(
					"gw1", t1,
					mkListener("l1", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
				),
				nil, // Deleted gateway should be ignored
				mkGateway(
					"gw2", t2,
					mkListener("l1", 80, gatewayv1.HTTPProtocolType, ptr("bar.com")),
				),
				nil, // Another deleted gateway
			},
			expected: map[string]conflictKinds{
				"default/gw1_l1": noConflict,
				"default/gw2_l1": noConflict,
			},
		},
		{
			name: "Nil gateway with conflict - only non-nil gateways participate",
			gateways: []*Gateway{
				nil, // Deleted gateway should not claim the port
				mkGateway(
					"gw1", t1,
					mkListener("l1", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
				),
				mkGateway(
					"gw2", t2,
					mkListener("l1", 80, gatewayv1.HTTPProtocolType, ptr("foo.com")),
				),
			},
			expected: map[string]conflictKinds{
				// Different gateways with identical listeners → merged, no conflict
				"default/gw1_l1": noConflict,
				"default/gw2_l1": noConflict,
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
			actual := make(map[string]conflictKinds)

			for _, res := range b.ControllerStore.conflictsByPort {
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
			Name:              name,
			Namespace:         "default",
			CreationTimestamp: metav1.NewTime(t1),
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

// Grouping is pure and total: every listener of every non-deleted Gateway lands
// under its port, in the precedence order it was given, and nothing is decided or
// written. The pass it replaced returned only the listeners that survived its own
// protocol verdict, which is what made the composite conflict unreachable.
func TestGroupListenersByPort(t *testing.T) {
	t1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	mkGateway := func(name string, creationTime time.Time, listeners ...gatewayv1.Listener) *Gateway {
		treeGw := &Gateway{K8sResource: &gatewayv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:              name,
				Namespace:         "default",
				CreationTimestamp: metav1.NewTime(creationTime),
			},
			Spec: gatewayv1.GatewaySpec{Listeners: listeners},
		}}
		treeGw.constructOwnListeners()
		return treeGw
	}

	mkListener := func(name string, port int, protocol gatewayv1.ProtocolType) gatewayv1.Listener {
		return gatewayv1.Listener{
			Name:     gatewayv1.SectionName(name),
			Port:     gatewayv1.PortNumber(port),
			Protocol: protocol,
		}
	}

	t.Run("listeners of one Gateway are grouped by port", func(t *testing.T) {
		gw := mkGateway("gw1", t1,
			mkListener("http", 80, gatewayv1.HTTPProtocolType),
			mkListener("https", 443, gatewayv1.HTTPSProtocolType),
		)

		listenersByPort := groupListenersByPort([]*Gateway{gw})

		assert.Len(t, listenersByPort, 2)
		assert.Equal(t, "http", string(listenersByPort[80][0].Name()))
		assert.Equal(t, "https", string(listenersByPort[443][0].Name()))
	})

	t.Run("a mismatched protocol is grouped like any other, not withheld", func(t *testing.T) {
		gw := mkGateway("gw1", t1,
			mkListener("http", 80, gatewayv1.HTTPProtocolType),
			mkListener("https", 80, gatewayv1.HTTPSProtocolType),
		)

		listenersByPort := groupListenersByPort([]*Gateway{gw})

		assert.Len(t, listenersByPort[80], 2,
			"withholding it is what kept it from ever reaching the hostname pass")
	})

	t.Run("the order the Gateways were given is the precedence order kept", func(t *testing.T) {
		older := mkGateway("gw1", t1, mkListener("first", 80, gatewayv1.HTTPProtocolType))
		younger := mkGateway("gw2", t2, mkListener("second", 80, gatewayv1.HTTPSProtocolType))

		listenersByPort := groupListenersByPort([]*Gateway{older, younger})

		assert.Equal(t, "first", string(listenersByPort[80][0].Name()),
			"the first entry is what decides the port's category, so the order is a contract")
		assert.Equal(t, "second", string(listenersByPort[80][1].Name()))
	})

	t.Run("nil and deleted gateways are skipped", func(t *testing.T) {
		alive := mkGateway("alive", t1, mkListener("l1", 80, gatewayv1.HTTPProtocolType))
		deleted := mkGateway("deleted", t2, mkListener("l2", 80, gatewayv1.HTTPProtocolType))
		deleted.TreeStatus.Status = store.StatusDeleted

		listenersByPort := groupListenersByPort([]*Gateway{nil, deleted, alive})

		assert.Len(t, listenersByPort[80], 1)
		assert.Equal(t, "l1", string(listenersByPort[80][0].Name()))
	})
}

// The protocol pass is the single writer that establishes the table's totality: an
// entry for every listener, the empty set included. The verdict diff reads an
// absent entry as a real difference, so a listener the pass forgot would be
// reported as changed on every cycle -- and one it wrote twice would hide a
// verdict.
func TestDetectProtocolConflicts(t *testing.T) {
	t1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	mkGateway := func(name string, listeners ...gatewayv1.Listener) *Gateway {
		treeGw := &Gateway{K8sResource: &gatewayv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:              name,
				Namespace:         "default",
				CreationTimestamp: metav1.NewTime(t1),
			},
			Spec: gatewayv1.GatewaySpec{Listeners: listeners},
		}}
		treeGw.constructOwnListeners()
		return treeGw
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

	keyOf := func(gw *Gateway, listenerName string) listenerKey {
		return gatewayListenerKey(client.ObjectKeyFromObject(gw.K8sResource), gatewayv1.SectionName(listenerName))
	}

	t.Run("every listener gets an entry, the ones in no conflict included", func(t *testing.T) {
		gw := mkGateway("gw1",
			mkListener("http", 80, gatewayv1.HTTPProtocolType),
			mkListener("http2", 80, gatewayv1.HTTPProtocolType),
			mkListener("https", 80, gatewayv1.HTTPSProtocolType),
		)
		b := newBuilder()

		b.detectProtocolConflicts(groupListenersByPort([]*Gateway{gw}))

		assert.Len(t, b.ControllerStore.conflictsByPort[80], 3,
			"totality: three listeners on the port, three entries")
		assert.Equal(t, noConflict, b.ControllerStore.conflictsByPort[80][keyOf(gw, "http")])
		assert.Equal(t, noConflict, b.ControllerStore.conflictsByPort[80][keyOf(gw, "http2")])
		assert.Equal(t, protocolConflict, b.ControllerStore.conflictsByPort[80][keyOf(gw, "https")])
	})

	t.Run("the first listener decides the port's category", func(t *testing.T) {
		gw := mkGateway("gw1",
			mkListener("https", 80, gatewayv1.HTTPSProtocolType),
			mkListener("http", 80, gatewayv1.HTTPProtocolType),
		)
		b := newBuilder()

		b.detectProtocolConflicts(groupListenersByPort([]*Gateway{gw}))

		assert.Equal(t, protocols.ProtocolCategorySecure, b.ControllerStore.protocolCategoryByPort[80],
			"the secure listener came first, so the port is secure")
		assert.Equal(t, protocolConflict, b.ControllerStore.conflictsByPort[80][keyOf(gw, "http")])
	})

	t.Run("ports are independent", func(t *testing.T) {
		gw := mkGateway("gw1",
			mkListener("http", 80, gatewayv1.HTTPProtocolType),
			mkListener("https", 443, gatewayv1.HTTPSProtocolType),
		)
		b := newBuilder()

		b.detectProtocolConflicts(groupListenersByPort([]*Gateway{gw}))

		assert.Equal(t, protocols.ProtocolCategoryInsecure, b.ControllerStore.protocolCategoryByPort[80])
		assert.Equal(t, protocols.ProtocolCategorySecure, b.ControllerStore.protocolCategoryByPort[443])
		assert.Equal(t, noConflict, b.ControllerStore.conflictsByPort[443][keyOf(gw, "https")])
	})
}

// The composite case is what the exclusion made unreachable, and it is now the
// point of the whole restructuring: a listener can hold both kinds at once, which
// is what a ListenerSet entry will report as ListenerConflict.
//
// The scenario is the smallest one that produces it. On port 80 the first listener
// fixes the insecure category; two HTTPS listeners of the same Gateway follow, both
// conflicting on protocol, and the second also claims a hostname the first has
// taken. Under the old shape it never met the hostname pass at all.
func TestBothConflictKindsAccumulateOnOneListener(t *testing.T) {
	hostname := gatewayv1.Hostname("foo.example.com")
	mkListener := func(name string, protocol gatewayv1.ProtocolType, host *gatewayv1.Hostname) gatewayv1.Listener {
		return gatewayv1.Listener{
			Name:     gatewayv1.SectionName(name),
			Port:     80,
			Protocol: protocol,
			Hostname: host,
		}
	}
	gw := &Gateway{K8sResource: &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "gw1", Namespace: "default"},
		Spec: gatewayv1.GatewaySpec{Listeners: []gatewayv1.Listener{
			mkListener("http", gatewayv1.HTTPProtocolType, nil),
			mkListener("https-first", gatewayv1.HTTPSProtocolType, &hostname),
			mkListener("https-second", gatewayv1.HTTPSProtocolType, &hostname),
		}},
	}}
	gw.constructOwnListeners()
	gwKey := client.ObjectKeyFromObject(gw.K8sResource)

	b := &GatewayBuilderImpl{ControllerStore: &ControllerStore{
		GateTree: &GateTree{Gateways: map[client.ObjectKey]*Gateway{gwKey: gw}},
	}}
	b.resetListenerConflicts()
	b.computeListenerConflicts()

	conflictsOnPort80 := b.ControllerStore.conflictsByPort[80]
	require.Len(t, conflictsOnPort80, 3, "totality: an entry for every listener of the port")

	assert.Equal(t, noConflict, conflictsOnPort80[gatewayListenerKey(gwKey, "http")],
		"the listener that fixed the category is in no conflict")

	second := conflictsOnPort80[gatewayListenerKey(gwKey, "https-second")]
	assert.True(t, second.has(conflictOnProtocol))
	assert.True(t, second.has(conflictOnHostname),
		"reaching the hostname pass is exactly what the exclusion prevented")

	// The claim index is keyed on the category, so a listener conflicting on protocol
	// cannot take the claim of one that does not. The catch-all HTTP listener keeps
	// its own, and the first HTTPS listener keeps the secure claim on that hostname.
	assert.Equal(t, protocolConflict, conflictsOnPort80[gatewayListenerKey(gwKey, "https-first")],
		"the first claimant of a hostname in its own category conflicts on protocol only")
}

// A ListenerSet entry is compared in the scope of the Gateway that serves it, and
// the precedence order of the merged list decides which claimant keeps a hostname or
// fixes a port's category. Neither property is observable through a Gateway's own
// listeners alone: for those, the serving Gateway and the declaring resource are the
// same object, and their order is spec order. Both failure modes are silent -- no
// conflict at all in one case, the wrong listener marked in the other -- so each
// assertion below names the listener that must win rather than only that a conflict
// exists.
func TestListenerSetEntriesAreComparedInTheirServingGatewayScope(t *testing.T) {
	t1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	gw1Key := client.ObjectKey{Namespace: "default", Name: "gw1"}
	gw2Key := client.ObjectKey{Namespace: "default", Name: "gw2"}

	ptr := func(s string) *string { return &s }

	hostnameOf := func(s *string) *gatewayv1.Hostname {
		if s == nil {
			return nil
		}
		h := gatewayv1.Hostname(*s)
		return &h
	}

	mkGateway := func(key client.ObjectKey, created time.Time, listeners ...gatewayv1.Listener) *Gateway {
		treeGw := &Gateway{K8sResource: &gatewayv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:              key.Name,
				Namespace:         key.Namespace,
				CreationTimestamp: metav1.NewTime(created),
			},
			Spec: gatewayv1.GatewaySpec{Listeners: listeners},
		}}
		treeGw.constructOwnListeners()
		return treeGw
	}

	ownListener := func(name string, port int, protocol gatewayv1.ProtocolType, hostname *string) gatewayv1.Listener {
		return gatewayv1.Listener{
			Name:     gatewayv1.SectionName(name),
			Port:     gatewayv1.PortNumber(port),
			Protocol: protocol,
			Hostname: hostnameOf(hostname),
		}
	}

	// mkEntry is one entry a ListenerSet contributes to a Gateway's merged list.
	mkEntry := func(gatewayKey, listenerSetKey client.ObjectKey, name string, port int, protocol gatewayv1.ProtocolType, hostname *string) *Listener {
		return &Listener{
			ListenerKey: listenerSetListenerKey(gatewayKey, listenerSetKey, gatewayv1.SectionName(name)),
			K8sListenerSetListenerEntry: &gatewayv1.ListenerEntry{
				Name:     gatewayv1.SectionName(name),
				Port:     gatewayv1.PortNumber(port),
				Protocol: protocol,
				Hostname: hostnameOf(hostname),
			},
			OwnerKind: kindListenerSet,
		}
	}

	// merge hands the contributions to the Gateway the way the builder does, so their
	// position -- the Gateway's own listeners first, then the contributions in the
	// order given -- is the one production produces.
	merge := func(gw *Gateway, contributions ...*Listener) *Gateway {
		gw.constructListeners(contributions)
		return gw
	}

	type conflictResults struct {
		conflicts              map[gatewayv1.PortNumber]conflictsByListener
		protocolCategoryByPort map[gatewayv1.PortNumber]protocols.ProtocolCategory
	}

	run := func(t *testing.T, gateways ...*Gateway) conflictResults {
		t.Helper()
		b := &GatewayBuilderImpl{ControllerStore: &ControllerStore{
			GateTree: &GateTree{Gateways: make(map[client.ObjectKey]*Gateway, len(gateways))},
		}}
		for _, gw := range gateways {
			b.GateTree.Gateways[client.ObjectKeyFromObject(gw.K8sResource)] = gw
		}
		b.resetListenerConflicts()
		b.computeListenerConflicts()
		return conflictResults{
			conflicts:              b.ControllerStore.conflictsByPort,
			protocolCategoryByPort: b.ControllerStore.protocolCategoryByPort,
		}
	}

	conflictsOf := func(t *testing.T, results conflictResults, port int, key listenerKey) conflictKinds {
		t.Helper()
		onPort, ok := results.conflicts[gatewayv1.PortNumber(port)]
		require.Truef(t, ok, "no conflict table for port %d", port)
		kinds, ok := onPort[key]
		require.Truef(t, ok, "no entry for %s on port %d -- the table is total over the listeners", key, port)
		return kinds
	}

	t.Run("sibling ListenerSets of one Gateway conflict, the older keeping the hostname", func(t *testing.T) {
		older := mkEntry(gw1Key, client.ObjectKey{Namespace: "tenant", Name: "ls-old"}, "http", 80, gatewayv1.HTTPProtocolType, ptr("foo.com"))
		younger := mkEntry(gw1Key, client.ObjectKey{Namespace: "tenant", Name: "ls-new"}, "http", 80, gatewayv1.HTTPProtocolType, ptr("foo.com"))
		gw1 := merge(mkGateway(gw1Key, t1), older, younger)

		results := run(t, gw1)

		assert.Equal(t, noConflict, conflictsOf(t, results, 80, older.Key()))
		assert.Equal(t, hostnameConflict, conflictsOf(t, results, 80, younger.Key()),
			"the claim is keyed on the serving Gateway: were it the declaring ListenerSet, the two would be different owners, neither would conflict, and the map file would settle it by last writer")
	})

	t.Run("two Gateways claiming one hostname do not conflict", func(t *testing.T) {
		gw1 := merge(mkGateway(gw1Key, t1, ownListener("http", 80, gatewayv1.HTTPProtocolType, ptr("foo.com"))))
		gw2 := merge(mkGateway(gw2Key, t2, ownListener("http", 80, gatewayv1.HTTPProtocolType, ptr("foo.com"))))

		results := run(t, gw1, gw2)

		assert.Equal(t, noConflict, conflictsOf(t, results, 80, gatewayListenerKey(gw1Key, "http")))
		assert.Equal(t, noConflict, conflictsOf(t, results, 80, gatewayListenerKey(gw2Key, "http")),
			"the claim carries the serving Gateway: without it, two Gateways would be made to conflict, which the API's per-Gateway addresses model does not")
	})

	t.Run("the Gateway's own listener keeps the hostname against an entry", func(t *testing.T) {
		// The Gateway is the newer object, so a precedence read from creation
		// timestamps would hand the hostname to the entry. The API gives the parent's
		// own listeners the highest priority regardless, and constructListeners says
		// so by putting them first unconditionally.
		entry := mkEntry(gw1Key, client.ObjectKey{Namespace: "tenant", Name: "ls-old"}, "http", 80, gatewayv1.HTTPProtocolType, ptr("foo.com"))
		gw1 := merge(mkGateway(gw1Key, t2, ownListener("http", 80, gatewayv1.HTTPProtocolType, ptr("foo.com"))), entry)

		results := run(t, gw1)

		assert.Equal(t, noConflict, conflictsOf(t, results, 80, gatewayListenerKey(gw1Key, "http")))
		assert.Equal(t, hostnameConflict, conflictsOf(t, results, 80, entry.Key()),
			"the parent's own listeners win whatever the timestamps say")
	})

	t.Run("the Gateway's own listener fixes the port's category", func(t *testing.T) {
		entry := mkEntry(gw1Key, client.ObjectKey{Namespace: "tenant", Name: "ls-old"}, "http", 443, gatewayv1.HTTPProtocolType, nil)
		gw1 := merge(mkGateway(gw1Key, t1, ownListener("https", 443, gatewayv1.HTTPSProtocolType, nil)), entry)

		results := run(t, gw1)

		assert.Equal(t, protocols.ProtocolCategorySecure, results.protocolCategoryByPort[443],
			"the first listener in precedence order decides, and it is the Gateway's own")
		assert.Equal(t, noConflict, conflictsOf(t, results, 443, gatewayListenerKey(gw1Key, "https")))
		assert.Equal(t, protocolConflict, conflictsOf(t, results, 443, entry.Key()))
	})
}
