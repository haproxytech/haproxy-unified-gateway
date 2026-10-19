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

	rc "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/routes"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// routeAttachFixture is the shape the tree gets into before routes are checked: a
// Gateway with one own listener, one contributed entry, and a *Gateway node whose
// merged Listeners list holds both.
type routeAttachFixture struct {
	store  *ControllerStore
	gwKey  client.ObjectKey
	treeGw *Gateway
	treeLs *ListenerSet
}

func newRouteAttachFixture(t *testing.T) *routeAttachFixture {
	t.Helper()

	from := gatewayv1.NamespacesFromAll
	f := newContributionFixture(t, &from)
	f.store.ExtractGVK = func(obj client.Object) schema.GroupVersionKind {
		switch obj.(type) {
		case *gatewayv1.ListenerSet:
			return schema.GroupVersionKind{Group: gatewayv1.GroupName, Version: "v1", Kind: "ListenerSet"}
		case *gatewayv1.HTTPRoute:
			return schema.GroupVersionKind{Group: gatewayv1.GroupName, Version: "v1", Kind: "HTTPRoute"}
		default:
			return schema.GroupVersionKind{Group: gatewayv1.GroupName, Version: "v1", Kind: "Gateway"}
		}
	}

	// One listener the Gateway declares itself, so the tests can show it is not
	// reachable by naming the ListenerSet -- and that the entry is not reachable
	// by naming the Gateway.
	gwK8s := f.store.ClusterStore.Gateways[f.gwKey]
	gwK8s.Spec.Listeners = []gatewayv1.Listener{
		{Name: "own", Port: 80, Protocol: gatewayv1.HTTPProtocolType},
	}

	treeLs := f.addListenerSet("tenant", "ls", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), "contributed")
	f.builder.contributeListeners()
	require.Equal(t, attachmentAllowed, treeLs.Attachment,
		"fixture: the ListenerSet must be one we report on, not an unreported one")

	treeGw := &Gateway{
		K8sResource: gwK8s,
		TreeStatus:  TreeUpdate[Gateway]{Status: store.StatusUpserted},
	}
	treeGw.constructListeners(listenersFromEntries(f.gwKey, treeLs.K8sResource))
	f.store.GateTree.Gateways[f.gwKey] = treeGw

	// The Gateway builder normally fills these in; the route checks need them.
	for _, l := range treeGw.Listeners {
		l.Accepted = true
		l.AllowedRouteKinds = []gatewayv1.RouteGroupKind{{Kind: "HTTPRoute"}}
	}

	return &routeAttachFixture{store: f.store, gwKey: f.gwKey, treeGw: treeGw, treeLs: treeLs}
}

func (*routeAttachFixture) route(parentRef gatewayv1.ParentReference) *HTTPRoute {
	return NewRoute(&gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "tenant", Name: "route"},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: []gatewayv1.ParentReference{parentRef}},
		},
	}, "controller")
}

func (*routeAttachFixture) listenerSetParentRef(sectionName *gatewayv1.SectionName) gatewayv1.ParentReference {
	group := gatewayv1.Group(gatewayv1.GroupName)
	kind := gatewayv1.Kind("ListenerSet")
	namespace := gatewayv1.Namespace("tenant")
	return gatewayv1.ParentReference{
		Group:       &group,
		Kind:        &kind,
		Namespace:   &namespace,
		Name:        "ls",
		SectionName: sectionName,
	}
}

func (f *routeAttachFixture) gatewayParentRef(sectionName *gatewayv1.SectionName) gatewayv1.ParentReference {
	namespace := gatewayv1.Namespace(f.gwKey.Namespace)
	return gatewayv1.ParentReference{
		Namespace:   &namespace,
		Name:        gatewayv1.ObjectName(f.gwKey.Name),
		SectionName: sectionName,
	}
}

// A parentRef naming the ListenerSet with a sectionName attaches to that entry, and
// only to it -- the entry is the direct Parent, not the Gateway serving it.
func TestHTTPRouteListenerSetParentRefWithSectionName(t *testing.T) {
	f := newRouteAttachFixture(t)

	section := gatewayv1.SectionName("contributed")
	parentRef := f.listenerSetParentRef(&section)
	r := f.route(parentRef)

	result := r.checkParentRef(parentRef, *f.store)

	require.True(t, result.Managed, "the ListenerSet is ours, the parentRef must be reported")
	assert.True(t, result.Valid)
	assert.Equal(t, rc.ConditionAccepted(), result.Conditions)

	entry := f.treeGw.ContributedListenerByName(
		client.ObjectKey{Namespace: "tenant", Name: "ls"}, section)
	require.NotNil(t, entry)
	_, attached := entry.AttachedRoutes[client.ObjectKey{Namespace: "tenant", Name: "route"}]
	assert.True(t, attached, "the route must be counted on the entry it named")

	assert.Empty(t, f.treeGw.OwnListenerByName("own").AttachedRoutes,
		"the Gateway's own listener is not the direct Parent here")
}

// No sectionName: every entry of the ListenerSet is relevant, and the Gateway's own
// listeners are not -- they belong to the other direct Parent.
func TestHTTPRouteListenerSetParentRefWithoutSectionName(t *testing.T) {
	f := newRouteAttachFixture(t)

	parentRef := f.listenerSetParentRef(nil)
	r := f.route(parentRef)

	result := r.checkParentRef(parentRef, *f.store)

	require.True(t, result.Managed)
	assert.True(t, result.Valid)

	entry := f.treeGw.ContributedListenerByName(
		client.ObjectKey{Namespace: "tenant", Name: "ls"}, gatewayv1.SectionName("contributed"))
	require.NotNil(t, entry)
	assert.Len(t, entry.AttachedRoutes, 1, "the entry is a relevant listener")
	assert.Empty(t, f.treeGw.OwnListenerByName("own").AttachedRoutes,
		"a Gateway listener is not part of a ListenerSet parentRef")
}

// The counter-example the API gives: a Gateway parentRef whose sectionName exists
// only on one of its ListenerSets must not attach -- reaching it requires naming the
// ListenerSet as parent.
func TestHTTPRouteGatewayParentRefDoesNotReachListenerSetEntry(t *testing.T) {
	f := newRouteAttachFixture(t)

	section := gatewayv1.SectionName("contributed")
	parentRef := f.gatewayParentRef(&section)
	r := f.route(parentRef)

	result := r.checkParentRef(parentRef, *f.store)

	require.True(t, result.Managed)
	assert.False(t, result.Valid)
	assert.Equal(t, rc.ConditionNotAcceptedNoMatchingParent(), result.Conditions,
		"the name exists, but not on the Gateway's own listeners")

	entry := f.treeGw.ContributedListenerByName(
		client.ObjectKey{Namespace: "tenant", Name: "ls"}, section)
	require.NotNil(t, entry)
	assert.Empty(t, entry.AttachedRoutes, "no attachment through a Gateway parentRef")
}

// An unreported ListenerSet is one whose parentRef names another controller's
// Gateway: nothing is ours to attach to, and nothing is ours to report on either.
func TestHTTPRouteUnreportedListenerSetStaysSilent(t *testing.T) {
	f := newRouteAttachFixture(t)
	f.treeLs.Attachment = attachmentUnreported

	section := gatewayv1.SectionName("contributed")
	parentRef := f.listenerSetParentRef(&section)
	r := f.route(parentRef)

	result := r.checkParentRef(parentRef, *f.store)

	assert.False(t, result.Managed, "writing a status would comment on someone else's resource")
	assert.False(t, result.Valid)
	assert.Empty(t, result.Conditions)
}

// A ListenerSet the Gateway refuses contributes no listener, so a route naming it has
// nothing to attach to: it gets the same answer a deleted parent gets, NoMatchingParent,
// rather than the empty entry the walk over "no attachable listener" used to leave
// behind -- a parent announced and silent, which is neither silence nor a refusal.
func TestHTTPRouteRefusedListenerSetIsRefusedWithNoMatchingParent(t *testing.T) {
	f := newRouteAttachFixture(t)

	// The refusal is what the contribution pass would have decided, and a refused
	// ListenerSet puts nothing in the Gateway's merged list.
	f.treeLs.Attachment = attachmentNotAllowed
	f.treeGw.constructListeners(nil)

	// No sectionName on purpose: with one, a missing listener is already refused,
	// and it is the whole-resource form that used to produce the empty entry.
	parentRef := f.listenerSetParentRef(nil)
	r := f.route(parentRef)

	result := r.checkParentRef(parentRef, *f.store)

	assert.True(t, result.Managed, "the parent is ours, so the refusal is ours to report")
	assert.False(t, result.Valid)
	assert.Len(t, result.Conditions, 1,
		"an entry with no conditions at all announces a parent and says nothing about it")
	assert.True(t, result.Conditions.Equal(rc.ConditionNotAcceptedNoMatchingParent()))
}
