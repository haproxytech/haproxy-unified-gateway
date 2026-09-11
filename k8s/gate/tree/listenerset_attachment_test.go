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

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func mkListenerSet(namespace, name string, parentRef gatewayv1.ParentGatewayReference) *gatewayv1.ListenerSet {
	return &gatewayv1.ListenerSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec:       gatewayv1.ListenerSetSpec{ParentRef: parentRef},
	}
}

func mkParentRef(name string) gatewayv1.ParentGatewayReference {
	return gatewayv1.ParentGatewayReference{Name: gatewayv1.ObjectName(name)}
}

// The parentRef is the only thing that says which Gateway a ListenerSet aims at, and
// its group, kind and namespace are all defaulted. Misreading any of them attaches
// listeners to the wrong Gateway, or to none.
func TestParentGatewayKey(t *testing.T) {
	t.Run("an unset namespace means the ListenerSet's own", func(t *testing.T) {
		key, namesGateway := parentGatewayKey(mkListenerSet("tenant", "ls", mkParentRef("gw")))

		assert.True(t, namesGateway)
		assert.Equal(t, client.ObjectKey{Namespace: "tenant", Name: "gw"}, key)
	})

	t.Run("an explicit namespace is honoured", func(t *testing.T) {
		ns := gatewayv1.Namespace("infra")
		parentRef := mkParentRef("gw")
		parentRef.Namespace = &ns

		key, namesGateway := parentGatewayKey(mkListenerSet("tenant", "ls", parentRef))

		assert.True(t, namesGateway)
		assert.Equal(t, client.ObjectKey{Namespace: "infra", Name: "gw"}, key)
	})

	t.Run("unset group and kind are the API's defaults, so still a Gateway", func(t *testing.T) {
		_, namesGateway := parentGatewayKey(mkListenerSet("tenant", "ls", mkParentRef("gw")))

		assert.True(t, namesGateway,
			"the API defaults group and kind, so nil must not be read as foreign")
	})

	t.Run("another group or kind names no Gateway at all", func(t *testing.T) {
		foreignGroup := gatewayv1.Group("networking.example.com")
		parentRef := mkParentRef("gw")
		parentRef.Group = &foreignGroup
		_, namesGateway := parentGatewayKey(mkListenerSet("tenant", "ls", parentRef))
		assert.False(t, namesGateway)

		foreignKind := gatewayv1.Kind("Service")
		parentRef = mkParentRef("gw")
		parentRef.Kind = &foreignKind
		_, namesGateway = parentGatewayKey(mkListenerSet("tenant", "ls", parentRef))
		assert.False(t, namesGateway,
			"no lookup in the store would make a Service into a Gateway")
	})
}

// This is the gate that decides who may add listeners to a Gateway, so the default
// matters more than any of the branches: a Gateway that says nothing must not be
// extensible by whoever can create a ListenerSet next to it.
func TestIsListenerSetNamespaceAllowed(t *testing.T) {
	namespaces := map[types.NamespacedName]*v1.Namespace{
		{Name: "tenant"}: {
			ObjectMeta: metav1.ObjectMeta{Name: "tenant", Labels: map[string]string{"tier": "shared"}},
		},
	}
	from := func(value gatewayv1.FromNamespaces) *gatewayv1.AllowedListeners {
		return &gatewayv1.AllowedListeners{Namespaces: &gatewayv1.ListenerNamespaces{From: &value}}
	}

	t.Run("the default at every level is to allow nothing", func(t *testing.T) {
		assert.False(t, isListenerSetNamespaceAllowed("infra", nil, "infra", namespaces),
			"no allowedListeners at all")
		assert.False(t, isListenerSetNamespaceAllowed("infra",
			&gatewayv1.AllowedListeners{}, "infra", namespaces),
			"allowedListeners without namespaces")
		assert.False(t, isListenerSetNamespaceAllowed("infra",
			&gatewayv1.AllowedListeners{Namespaces: &gatewayv1.ListenerNamespaces{}}, "infra", namespaces),
			"namespaces without from")
		assert.False(t, isListenerSetNamespaceAllowed("infra",
			from(gatewayv1.NamespacesFromNone), "infra", namespaces),
			"an explicit None")
	})

	t.Run("Same compares namespaces", func(t *testing.T) {
		assert.True(t, isListenerSetNamespaceAllowed("infra", from(gatewayv1.NamespacesFromSame), "infra", namespaces))
		assert.False(t, isListenerSetNamespaceAllowed("tenant", from(gatewayv1.NamespacesFromSame), "infra", namespaces))
	})

	t.Run("All allows any namespace", func(t *testing.T) {
		assert.True(t, isListenerSetNamespaceAllowed("tenant", from(gatewayv1.NamespacesFromAll), "infra", namespaces))
	})

	t.Run("Selector matches the namespace labels", func(t *testing.T) {
		allowed := from(gatewayv1.NamespacesFromSelector)
		allowed.Namespaces.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"tier": "shared"}}

		assert.True(t, isListenerSetNamespaceAllowed("tenant", allowed, "infra", namespaces))
		assert.False(t, isListenerSetNamespaceAllowed("unknown-ns", allowed, "infra", namespaces),
			"a namespace absent from the store cannot be shown to match")
	})

	t.Run("Selector without a selector denies", func(t *testing.T) {
		assert.False(t, isListenerSetNamespaceAllowed("tenant",
			from(gatewayv1.NamespacesFromSelector), "infra", namespaces),
			"an unusable selector must not be read as permission")
	})
}

// The two ways of failing to attach do not have the same consequence: one is ours to
// report, the other is not ours to comment on at all. Collapsing them is what a
// boolean would have done.
func TestAttachmentOfListenerSet(t *testing.T) {
	gwKey := client.ObjectKey{Namespace: "infra", Name: "gw"}

	newStore := func(gw *gatewayv1.Gateway, classes ...string) *ControllerStore {
		gatewayClasses := map[types.NamespacedName]*GatewayClass{}
		for _, name := range classes {
			gatewayClasses[types.NamespacedName{Name: name}] = &GatewayClass{}
		}
		gateways := map[types.NamespacedName]*gatewayv1.Gateway{}
		if gw != nil {
			gateways[gwKey] = gw
		}
		return &ControllerStore{
			ClusterStore: &store.ClusterStore{
				Gateways:   gateways,
				Namespaces: map[types.NamespacedName]*v1.Namespace{},
			},
			GateTree: &GateTree{GatewayClasses: gatewayClasses},
		}
	}

	mkGateway := func(class string, from *gatewayv1.FromNamespaces) *gatewayv1.Gateway {
		gw := &gatewayv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Namespace: gwKey.Namespace, Name: gwKey.Name},
			Spec:       gatewayv1.GatewaySpec{GatewayClassName: gatewayv1.ObjectName(class)},
		}
		if from != nil {
			gw.Spec.AllowedListeners = &gatewayv1.AllowedListeners{
				Namespaces: &gatewayv1.ListenerNamespaces{From: from},
			}
		}
		return gw
	}

	same := gatewayv1.NamespacesFromSame

	t.Run("a parent absent from the store is not ours to report on", func(t *testing.T) {
		cs := newStore(nil, "hug")
		ls := mkListenerSet("infra", "ls", mkParentRef("gw"))

		assert.Equal(t, attachmentUnreported, cs.attachmentOfListenerSet(ls))
	})

	t.Run("a parent of another GatewayClass is not ours to report on", func(t *testing.T) {
		cs := newStore(mkGateway("someone-else", &same))
		ls := mkListenerSet("infra", "ls", mkParentRef("gw"))

		assert.Equal(t, attachmentUnreported, cs.attachmentOfListenerSet(ls),
			"writing a status would claim a resource aimed at another controller")
	})

	t.Run("a parentRef naming something other than a Gateway is not ours", func(t *testing.T) {
		cs := newStore(mkGateway("hug", &same), "hug")
		foreignKind := gatewayv1.Kind("Service")
		parentRef := mkParentRef("gw")
		parentRef.Kind = &foreignKind

		assert.Equal(t, attachmentUnreported,
			cs.attachmentOfListenerSet(mkListenerSet("infra", "ls", parentRef)))
	})

	t.Run("our parent refusing the attachment is ours to report", func(t *testing.T) {
		cs := newStore(mkGateway("hug", nil), "hug")
		ls := mkListenerSet("infra", "ls", mkParentRef("gw"))

		assert.Equal(t, attachmentNotAllowed, cs.attachmentOfListenerSet(ls),
			"the default is None, and silence here would breach the spec")
	})

	t.Run("our parent allowing it attaches", func(t *testing.T) {
		cs := newStore(mkGateway("hug", &same), "hug")
		ls := mkListenerSet("infra", "ls", mkParentRef("gw"))

		assert.Equal(t, attachmentAllowed, cs.attachmentOfListenerSet(ls))
	})

	t.Run("the parent's validity is deliberately not part of the decision", func(t *testing.T) {
		// The Gateway builder has not run, so nothing here can know whether the
		// parent is accepted. An entry on an invalid Gateway is inert for the same
		// reason its own listeners are; only the status is owed, and elsewhere.
		cs := newStore(mkGateway("hug", &same), "hug")
		ls := mkListenerSet("infra", "ls", mkParentRef("gw"))

		assert.Equal(t, attachmentAllowed, cs.attachmentOfListenerSet(ls))
	})

	t.Run("a nil ListenerSet is unreported rather than a panic", func(t *testing.T) {
		cs := newStore(mkGateway("hug", &same), "hug")

		assert.Equal(t, attachmentUnreported, cs.attachmentOfListenerSet(nil))
	})
}
