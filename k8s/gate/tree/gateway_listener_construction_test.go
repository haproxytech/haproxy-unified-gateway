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
	"io"
	"log/slog"
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

func mkConstructionGateway(name string, listeners ...gatewayv1.Listener) *Gateway {
	return &Gateway{
		K8sResource: &gatewayv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec:       gatewayv1.GatewaySpec{Listeners: listeners},
		},
		TreeStatus: TreeUpdate[Gateway]{Status: store.StatusUpserted},
	}
}

// Construction is what detection now reads, so what it stamps on each listener is
// load-bearing twice over: the key identifies the listener in the conflict table,
// and the origin decides which vocabulary its conditions are spelled in and which
// ReferenceGrants cover its certificateRefs.
func TestConstructOwnListeners(t *testing.T) {
	mkListener := func(name string, port int, protocol gatewayv1.ProtocolType) gatewayv1.Listener {
		return gatewayv1.Listener{
			Name:     gatewayv1.SectionName(name),
			Port:     gatewayv1.PortNumber(port),
			Protocol: protocol,
		}
	}

	t.Run("listeners are built in spec order", func(t *testing.T) {
		gw := mkConstructionGateway("gw1",
			mkListener("third", 8080, gatewayv1.HTTPProtocolType),
			mkListener("first", 80, gatewayv1.HTTPProtocolType),
			mkListener("second", 443, gatewayv1.HTTPSProtocolType),
		)

		gw.constructOwnListeners()

		require.Len(t, gw.Listeners, 3)
		assert.Equal(t, "third", string(gw.Listeners[0].Name()),
			"spec order, not name order: the first listener on a port fixes its category")
		assert.Equal(t, "first", string(gw.Listeners[1].Name()))
		assert.Equal(t, "second", string(gw.Listeners[2].Name()))
	})

	t.Run("each listener carries the key naming its Gateway and itself", func(t *testing.T) {
		gw := mkConstructionGateway("gw1", mkListener("http", 80, gatewayv1.HTTPProtocolType))

		gw.constructOwnListeners()

		require.Len(t, gw.Listeners, 1)
		key := gw.Listeners[0].Key()
		assert.Equal(t, client.ObjectKey{Namespace: "default", Name: "gw1"}, key.GatewayKey)
		assert.Equal(t, gatewayv1.SectionName("http"), key.ListenerName)
		assert.Zero(t, key.ListenerSetKey,
			"a listener the Gateway declares itself has no declaring ListenerSet")
	})

	t.Run("the origin is the Gateway, and it is not exempt from ReferenceGrants", func(t *testing.T) {
		gw := mkConstructionGateway("gw1", mkListener("http", 80, gatewayv1.HTTPProtocolType))

		gw.constructOwnListeners()

		require.Len(t, gw.Listeners, 1)
		assert.Equal(t, kindGateway, gw.Listeners[0].OwnerKind)
		assert.False(t, gw.Listeners[0].FromListenerSet())
		assert.False(t, gw.Listeners[0].ExemptFromReferenceGrant)
	})

	t.Run("the synthetic ingress gateway's listeners are exempt", func(t *testing.T) {
		gw := mkConstructionGateway("ingress", mkListener("http", 80, gatewayv1.HTTPProtocolType))
		gw.GatewayForIngress = true

		gw.constructOwnListeners()

		require.Len(t, gw.Listeners, 1)
		assert.True(t, gw.Listeners[0].ExemptFromReferenceGrant,
			"controller-owned, so its certificateRefs answer to no grant")
	})

	t.Run("each listener holds its own spec entry, not the last one of the loop", func(t *testing.T) {
		gw := mkConstructionGateway("gw1",
			mkListener("http", 80, gatewayv1.HTTPProtocolType),
			mkListener("https", 443, gatewayv1.HTTPSProtocolType),
		)

		gw.constructOwnListeners()

		require.Len(t, gw.Listeners, 2)
		assert.Equal(t, gatewayv1.PortNumber(80), gw.Listeners[0].Port())
		assert.Equal(t, gatewayv1.PortNumber(443), gw.Listeners[1].Port())
		assert.Equal(t, protocols.ProtocolCategoryInsecure, gw.Listeners[0].protocolCategory())
		assert.Equal(t, protocols.ProtocolCategorySecure, gw.Listeners[1].protocolCategory())
	})

	t.Run("rebuilding replaces the list rather than appending to it", func(t *testing.T) {
		gw := mkConstructionGateway("gw1", mkListener("http", 80, gatewayv1.HTTPProtocolType))

		gw.constructOwnListeners()
		gw.constructOwnListeners()

		assert.Len(t, gw.Listeners, 1,
			"the re-upsert path calls it a second time in the same cycle")
	})
}

// The listener list is built before detection and a Gateway re-upserted by the
// verdict diff is reset in between, which drops it. The checks the re-upsert exists
// to trigger iterate that list, so a Gateway that lost it would end the cycle with
// no listener at all -- accepted, programmed, and serving nothing. This pins the
// rebuild that closes the window.
func TestReUpsertedGatewayKeepsItsListeners(t *testing.T) {
	const ns = "default"
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))

	older := metav1.NewTime(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	younger := metav1.NewTime(time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC))

	mkGateway := func(name string, created metav1.Time, protocol gatewayv1.ProtocolType, status store.Status) *Gateway {
		treeGw := &Gateway{
			K8sResource: &gatewayv1.Gateway{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, CreationTimestamp: created},
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: "not-in-tree",
					Listeners:        []gatewayv1.Listener{{Name: "l1", Port: 80, Protocol: protocol}},
				},
			},
			TreeStatus: TreeUpdate[Gateway]{Status: status},
		}
		treeGw.constructOwnListeners()
		return treeGw
	}

	// gw1 carries this cycle's event and wins port 80; gw2 is pushed into a protocol
	// conflict without changing at all, which is what re-upserts it.
	gw1 := mkGateway("gw1", older, gatewayv1.HTTPProtocolType, store.StatusUpserted)
	gw2 := mkGateway("gw2", younger, gatewayv1.HTTPSProtocolType, "")

	previous := map[gatewayv1.PortNumber]conflictsByListener{
		80: {gatewayListenerKey(client.ObjectKey{Namespace: ns, Name: "gw2"}, "l1"): noConflict},
	}

	b := &GatewayBuilderImpl{ControllerStore: &ControllerStore{
		Logger: discard,
		GateTree: &GateTree{
			Gateways:       map[client.ObjectKey]*Gateway{},
			GatewayClasses: map[client.ObjectKey]*GatewayClass{},
		},
		UnmanagedGateTree:       &GateTree{Gateways: map[client.ObjectKey]*Gateway{}},
		previousConflictsByPort: previous,
		conflictsByPort:         map[gatewayv1.PortNumber]conflictsByListener{},
		protocolCategoryByPort:  map[gatewayv1.PortNumber]protocols.ProtocolCategory{},
	}}
	for _, gw := range []*Gateway{gw1, gw2} {
		b.ControllerStore.GateTree.Gateways[client.ObjectKeyFromObject(gw.K8sResource)] = gw
	}

	b.checkListenerConflicts()

	require.Equal(t, store.StatusUpserted, gw2.TreeStatus.Status,
		"the changed verdict has to re-upsert gw2, otherwise there is nothing to pin")
	require.Len(t, gw2.Listeners, 1,
		"the re-upsert reset gw2, so the list built before detection had to be rebuilt")
	assert.Equal(t, gatewayv1.SectionName("l1"), gw2.Listeners[0].Name())

	// The verdict recorded before the reset still resolves, which is what makes the
	// rebuild sound: the spec did not move, so the listener came back under its key.
	conflicts, found := b.ControllerStore.conflictsByPort[80][gw2.Listeners[0].Key()]
	require.True(t, found, "the rebuilt listener must be the one the table holds a verdict for")
	assert.True(t, conflicts.has(conflictOnProtocol))
}
