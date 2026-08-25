package tree

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/protocols"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestNbListenersWithAndWithoutConflict(t *testing.T) {
	gwName := "gw1"
	gwNamespace := "default"

	// Helper to create a Gateway
	mkGateway := func(name, namespace string) *Gateway {
		gw := &gatewayv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
		}
		return &Gateway{
			K8sResource: gw,
		}
	}

	treeGw := mkGateway(gwName, gwNamespace)

	// Helper to create ObjectKey
	mkKey := func(gwName, listenerName string) client.ObjectKey {
		return client.ObjectKey{
			Namespace: gwNamespace,
			Name:      gwName + "_" + listenerName,
		}
	}

	tests := []struct {
		name                     string
		mapPort2ListenerConflict map[gatewayv1.PortNumber]listenerConflict
		expectedWithConflict     int32
		expectedWithoutConflict  int32
	}{
		{
			name:                     "No listeners",
			mapPort2ListenerConflict: map[gatewayv1.PortNumber]listenerConflict{},
			expectedWithConflict:     0,
			expectedWithoutConflict:  0,
		},
		{
			name: "Single listener with conflict",
			mapPort2ListenerConflict: map[gatewayv1.PortNumber]listenerConflict{
				80: {
					mkKey(gwName, "l1"): {hasConflict: true},
				},
			},
			expectedWithConflict:    1,
			expectedWithoutConflict: 0,
		},
		{
			name: "Single listener without conflict",
			mapPort2ListenerConflict: map[gatewayv1.PortNumber]listenerConflict{
				80: {
					mkKey(gwName, "l1"): {hasConflict: false},
				},
			},
			expectedWithConflict:    0,
			expectedWithoutConflict: 1,
		},
		{
			name: "Mixed listeners",
			mapPort2ListenerConflict: map[gatewayv1.PortNumber]listenerConflict{
				80: {
					mkKey(gwName, "l1"): {hasConflict: true},
					mkKey(gwName, "l2"): {hasConflict: false},
				},
				443: {
					mkKey(gwName, "l3"): {hasConflict: true},
				},
			},
			expectedWithConflict:    2,
			expectedWithoutConflict: 1,
		},
		{
			name: "Listeners from other gateways ignored",
			mapPort2ListenerConflict: map[gatewayv1.PortNumber]listenerConflict{
				80: {
					mkKey(gwName, "l1"):  {hasConflict: true},
					mkKey("other", "l1"): {hasConflict: true},
				},
			},
			expectedWithConflict:    1,
			expectedWithoutConflict: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nb := nbListenersWithAndWithoutConflict(tt.mapPort2ListenerConflict, treeGw)
			assert.Equal(t, tt.expectedWithConflict, nb.withConflict)
			assert.Equal(t, tt.expectedWithoutConflict, nb.withoutConflict)
		})
	}
}

func TestGatewaysWithChangedConflictVerdict(t *testing.T) {
	gwNamespace := "default"

	// Helper to create ObjectKey
	mkKey := func(gwName, listenerName string) client.ObjectKey {
		return client.ObjectKey{
			Namespace: gwNamespace,
			Name:      gwName + "_" + listenerName,
		}
	}

	mkGwKey := func(gwName string) client.ObjectKey {
		return client.ObjectKey{
			Namespace: gwNamespace,
			Name:      gwName,
		}
	}

	protocolConflict := listenerConflictCondition{
		hasConflict: true,
		reason:      string(gatewayv1.ListenerReasonProtocolConflict),
		protocol:    protocols.ProtocolCategorySecure,
	}
	hostnameConflict := listenerConflictCondition{
		hasConflict: true,
		reason:      string(gatewayv1.ListenerReasonHostnameConflict),
		protocol:    protocols.ProtocolCategorySecure,
	}
	noConflict := listenerConflictCondition{
		hasConflict: false,
		protocol:    protocols.ProtocolCategorySecure,
	}

	tests := []struct {
		previous       map[gatewayv1.PortNumber]listenerConflict
		current        map[gatewayv1.PortNumber]listenerConflict
		expectedGwKeys map[client.ObjectKey]struct{}
		name           string
	}{
		{
			name:           "Both tables empty",
			previous:       map[gatewayv1.PortNumber]listenerConflict{},
			current:        map[gatewayv1.PortNumber]listenerConflict{},
			expectedGwKeys: map[client.ObjectKey]struct{}{},
		},
		{
			// The listener is fine and stays fine: nothing to recompute.
			name: "Unchanged listener without conflict",
			previous: map[gatewayv1.PortNumber]listenerConflict{
				80: {mkKey("gw1", "l1"): noConflict},
			},
			current: map[gatewayv1.PortNumber]listenerConflict{
				80: {mkKey("gw1", "l1"): noConflict},
			},
			expectedGwKeys: map[client.ObjectKey]struct{}{},
		},
		{
			// A lasting misconfiguration must not be re-checked on every cycle:
			// this is the case the previous old-union-new selection re-upserted
			// for nothing.
			name: "Unchanged listener with the same conflict",
			previous: map[gatewayv1.PortNumber]listenerConflict{
				80: {mkKey("gw1", "l1"): protocolConflict},
			},
			current: map[gatewayv1.PortNumber]listenerConflict{
				80: {mkKey("gw1", "l1"): protocolConflict},
			},
			expectedGwKeys: map[client.ObjectKey]struct{}{},
		},
		{
			name: "Listener enters conflict",
			previous: map[gatewayv1.PortNumber]listenerConflict{
				80: {mkKey("gw1", "l1"): noConflict},
			},
			current: map[gatewayv1.PortNumber]listenerConflict{
				80: {mkKey("gw1", "l1"): protocolConflict},
			},
			expectedGwKeys: map[client.ObjectKey]struct{}{
				mkGwKey("gw1"): {},
			},
		},
		{
			name: "Listener leaves conflict",
			previous: map[gatewayv1.PortNumber]listenerConflict{
				80: {mkKey("gw1", "l1"): protocolConflict},
			},
			current: map[gatewayv1.PortNumber]listenerConflict{
				80: {mkKey("gw1", "l1"): noConflict},
			},
			expectedGwKeys: map[client.ObjectKey]struct{}{
				mkGwKey("gw1"): {},
			},
		},
		{
			// Still conflicting, but the reason changed: the condition message
			// must be refreshed, which a membership-only comparison would miss.
			name: "Conflict reason changes",
			previous: map[gatewayv1.PortNumber]listenerConflict{
				80: {mkKey("gw1", "l1"): protocolConflict},
			},
			current: map[gatewayv1.PortNumber]listenerConflict{
				80: {mkKey("gw1", "l1"): hostnameConflict},
			},
			expectedGwKeys: map[client.ObjectKey]struct{}{
				mkGwKey("gw1"): {},
			},
		},
		{
			name: "Listener disappeared from the table",
			previous: map[gatewayv1.PortNumber]listenerConflict{
				80: {mkKey("gw1", "l1"): protocolConflict},
			},
			current:        map[gatewayv1.PortNumber]listenerConflict{},
			expectedGwKeys: map[client.ObjectKey]struct{}{mkGwKey("gw1"): {}},
		},
		{
			name:     "Listener appeared in the table",
			previous: map[gatewayv1.PortNumber]listenerConflict{},
			current: map[gatewayv1.PortNumber]listenerConflict{
				80: {mkKey("gw1", "l1"): noConflict},
			},
			expectedGwKeys: map[client.ObjectKey]struct{}{mkGwKey("gw1"): {}},
		},
		{
			// First cycle: previousMapPort2Listeners is still nil.
			name:     "Nil previous table",
			previous: nil,
			current: map[gatewayv1.PortNumber]listenerConflict{
				80:  {mkKey("gw1", "l1"): noConflict},
				443: {mkKey("gw2", "l1"): protocolConflict},
			},
			expectedGwKeys: map[client.ObjectKey]struct{}{
				mkGwKey("gw1"): {},
				mkGwKey("gw2"): {},
			},
		},
		{
			name: "Only the Gateway whose verdict changed is selected",
			previous: map[gatewayv1.PortNumber]listenerConflict{
				80: {
					mkKey("gw1", "l1"): noConflict,
					mkKey("gw2", "l1"): protocolConflict,
				},
				443: {mkKey("gw3", "l1"): noConflict},
			},
			current: map[gatewayv1.PortNumber]listenerConflict{
				80: {
					mkKey("gw1", "l1"): noConflict,
					mkKey("gw2", "l1"): protocolConflict,
				},
				443: {mkKey("gw3", "l1"): hostnameConflict},
			},
			expectedGwKeys: map[client.ObjectKey]struct{}{
				mkGwKey("gw3"): {},
			},
		},
		{
			name: "Several listeners of the same Gateway yield a single key",
			previous: map[gatewayv1.PortNumber]listenerConflict{
				80:  {mkKey("gw1", "l1"): noConflict},
				443: {mkKey("gw1", "l2"): noConflict},
			},
			current: map[gatewayv1.PortNumber]listenerConflict{
				80:  {mkKey("gw1", "l1"): protocolConflict},
				443: {mkKey("gw1", "l2"): hostnameConflict},
			},
			expectedGwKeys: map[client.ObjectKey]struct{}{
				mkGwKey("gw1"): {},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gwKeys := gatewaysWithChangedConflictVerdict(tt.previous, tt.current)
			assert.Equal(t, tt.expectedGwKeys, gwKeys)
		})
	}
}

// TestCheckListenerConflictsReUpsert covers the two situations the verdict diff is
// meant to separate: a Gateway whose verdict changed because of another Gateway
// must be re-upserted so the new verdict reaches its listeners, while a Gateway
// whose verdict is unchanged must be left alone even though it is conflicting.
func TestCheckListenerConflictsReUpsert(t *testing.T) {
	const ns = "default"
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))

	older := metav1.NewTime(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	younger := metav1.NewTime(time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC))

	// mkGateway builds a Gateway with a single listener on port 80. The
	// GatewayClass is deliberately absent from the tree so processManagementChecks
	// returns early: this test is about the selection, not about management.
	mkGateway := func(name string, created metav1.Time, protocol gatewayv1.ProtocolType, status store.Status) *Gateway {
		return &Gateway{
			K8sResource: &gatewayv1.Gateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:              name,
					Namespace:         ns,
					CreationTimestamp: created,
				},
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: "not-in-tree",
					Listeners: []gatewayv1.Listener{
						{Name: "l1", Port: 80, Protocol: protocol},
					},
				},
			},
			TreeStatus: TreeUpdate[Gateway]{Status: status},
		}
	}

	listenerKey := func(gwName string) client.ObjectKey {
		return client.ObjectKey{Namespace: ns, Name: gwName + "_l1"}
	}

	newBuilder := func(previous map[gatewayv1.PortNumber]listenerConflict, gws ...*Gateway) *GatewayBuilderImpl {
		gateways := map[client.ObjectKey]*Gateway{}
		for _, gw := range gws {
			gateways[client.ObjectKeyFromObject(gw.K8sResource)] = gw
		}
		return &GatewayBuilderImpl{
			ControllerStore: &ControllerStore{
				Logger:                    discard,
				GateTree:                  &GateTree{Gateways: gateways, GatewayClasses: map[client.ObjectKey]*GatewayClass{}},
				UnmanagedGateTree:         &GateTree{Gateways: map[client.ObjectKey]*Gateway{}},
				previousMapPort2Listeners: previous,
				mapPort2Listeners:         map[gatewayv1.PortNumber]listenerConflict{},
			},
		}
	}

	t.Run("gateway entering conflict because of another gateway is re-upserted", func(t *testing.T) {
		// gw1 is older and is the one carrying an event this cycle; it wins port 80
		// and pushes gw2 into a protocol conflict without gw2 changing at all.
		gw1 := mkGateway("gw1", older, gatewayv1.HTTPProtocolType, store.StatusUpserted)
		gw2 := mkGateway("gw2", younger, gatewayv1.HTTPSProtocolType, "")

		// Previous cycle: gw2 was alone on port 80, hence no conflict.
		previous := map[gatewayv1.PortNumber]listenerConflict{
			80: {
				listenerKey("gw2"): {protocol: protocols.ProtocolCategorySecure},
			},
		}

		b := newBuilder(previous, gw1, gw2)
		b.checkListenerConflicts()

		assert.True(t, b.ControllerStore.mapPort2Listeners[80][listenerKey("gw2")].hasConflict,
			"gw2 listener must be detected as conflicting")
		assert.Equal(t, store.StatusUpserted, gw2.TreeStatus.Status,
			"gw2 must be re-upserted so the new verdict reaches its listeners")
	})

	t.Run("gateway with an unchanged conflict is not re-upserted", func(t *testing.T) {
		// Same two Gateways, but the conflict already existed and nothing changed:
		// this cycle was triggered by an unrelated resource, so neither Gateway is
		// in ClusterStore.Updates.
		gw1 := mkGateway("gw1", older, gatewayv1.HTTPProtocolType, "")
		gw2 := mkGateway("gw2", younger, gatewayv1.HTTPSProtocolType, "")

		previous := map[gatewayv1.PortNumber]listenerConflict{
			80: {
				listenerKey("gw1"): {protocol: protocols.ProtocolCategoryInsecure},
				listenerKey("gw2"): {
					hasConflict: true,
					reason:      string(gatewayv1.ListenerReasonProtocolConflict),
					protocol:    protocols.ProtocolCategorySecure,
				},
			},
		}

		b := newBuilder(previous, gw1, gw2)
		b.checkListenerConflicts()

		assert.Equal(t, previous, b.ControllerStore.mapPort2Listeners,
			"the recomputed table must be identical to the previous one")
		assert.Empty(t, gw1.TreeStatus.Status, "gw1 must not be re-upserted")
		assert.Empty(t, gw2.TreeStatus.Status, "gw2 must not be re-upserted for an unchanged conflict")
	})
}
