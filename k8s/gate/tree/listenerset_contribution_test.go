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

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// contributionFixture is one Gateway that allows ListenerSets from anywhere, plus
// whatever ListenerSets a test puts in the tree.
type contributionFixture struct {
	builder *ListenerSetContributionBuilder
	store   *ControllerStore
	gwKey   client.ObjectKey
	// allowed is the policy the fixture's Gateway was built with, so a second
	// Gateway added by a case allows ListenerSets the same way.
	allowed *gatewayv1.AllowedListeners
}

func newContributionFixture(t *testing.T, allowedFrom *gatewayv1.FromNamespaces) *contributionFixture {
	t.Helper()
	var allowed *gatewayv1.AllowedListeners
	if allowedFrom != nil {
		allowed = &gatewayv1.AllowedListeners{Namespaces: &gatewayv1.ListenerNamespaces{From: allowedFrom}}
	}
	return newContributionFixtureWithPolicy(t, allowed)
}

// newContributionFixtureWithPolicy builds the same fixture for the policies an
// AllowedListeners alone cannot express, a namespace Selector in particular.
func newContributionFixtureWithPolicy(t *testing.T, allowed *gatewayv1.AllowedListeners) *contributionFixture {
	t.Helper()
	gwKey := client.ObjectKey{Namespace: "infra", Name: "gw"}

	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: gwKey.Namespace, Name: gwKey.Name},
		Spec:       gatewayv1.GatewaySpec{GatewayClassName: "hug", AllowedListeners: allowed},
	}

	controllerStore := &ControllerStore{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		ClusterStore: &store.ClusterStore{
			Gateways:     map[types.NamespacedName]*gatewayv1.Gateway{gwKey: gw},
			ListenerSets: map[types.NamespacedName]*gatewayv1.ListenerSet{},
			Namespaces:   map[types.NamespacedName]*v1.Namespace{},
			Updates:      store.NewClusterUpdates(),
		},
		GateTree: &GateTree{
			Gateways:       map[types.NamespacedName]*Gateway{},
			GatewayClasses: map[types.NamespacedName]*GatewayClass{{Name: "hug"}: {}},
			ListenerSets:   map[types.NamespacedName]*ListenerSet{},
		},
	}

	return &contributionFixture{
		builder: &ListenerSetContributionBuilder{ControllerStore: controllerStore},
		store:   controllerStore,
		gwKey:   gwKey,
		allowed: allowed,
	}
}

// addGateway puts a second Gateway in the cluster store, so a case can move a
// ListenerSet from one parent to another. Only the cluster store needs it: the
// contribution pass reads the cluster store, not the tree.
func (f *contributionFixture) addGateway(namespace, name string) client.ObjectKey {
	key := client.ObjectKey{Namespace: namespace, Name: name}
	f.store.ClusterStore.Gateways[key] = &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec:       gatewayv1.GatewaySpec{GatewayClassName: "hug", AllowedListeners: f.allowed},
	}
	return key
}

// reparent points a ListenerSet at another Gateway, which is a legal edit: the API
// declares no immutability on parentRef.
func reparent(treeLs *ListenerSet, key client.ObjectKey) {
	treeLs.K8sResource.Spec.ParentRef = gatewayv1.ParentGatewayReference{
		Name:      gatewayv1.ObjectName(key.Name),
		Namespace: (*gatewayv1.Namespace)(&key.Namespace),
	}
}

// addListenerSet puts a ListenerSet in the tree, attached to the fixture's Gateway.
func (f *contributionFixture) addListenerSet(
	namespace, name string, created time.Time, entryNames ...string,
) *ListenerSet {
	entries := make([]gatewayv1.ListenerEntry, 0, len(entryNames))
	for i, entryName := range entryNames {
		entries = append(entries, gatewayv1.ListenerEntry{
			Name:     gatewayv1.SectionName(entryName),
			Port:     gatewayv1.PortNumber(8000 + i),
			Protocol: gatewayv1.HTTPProtocolType,
		})
	}

	k8sLs := &gatewayv1.ListenerSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:         namespace,
			Name:              name,
			CreationTimestamp: metav1.NewTime(created),
		},
		Spec: gatewayv1.ListenerSetSpec{
			ParentRef: gatewayv1.ParentGatewayReference{
				Name:      gatewayv1.ObjectName(f.gwKey.Name),
				Namespace: (*gatewayv1.Namespace)(&f.gwKey.Namespace),
			},
			Listeners: entries,
		},
	}

	lsKey := types.NamespacedName{Namespace: namespace, Name: name}
	f.store.ClusterStore.ListenerSets[lsKey] = k8sLs
	treeLs := NewListenerSet(k8sLs)
	f.store.GateTree.ListenerSets[lsKey] = treeLs
	return treeLs
}

func (f *contributionFixture) contributedNames() []string {
	names := []string{}
	for _, listener := range f.store.contributedListenersByGatewayKey[f.gwKey] {
		names = append(names, string(listener.Name()))
	}
	return names
}

// The precedence the API prescribes is a contract, not a convenience: the first
// listener on a port fixes that port's protocol category and the first claimant of a
// hostname keeps it, so the order decides which listener is accepted and which is
// told it conflicts.
func TestContributeListenersOrder(t *testing.T) {
	older := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	younger := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)

	t.Run("ListenerSets contribute oldest first", func(t *testing.T) {
		from := gatewayv1.NamespacesFromAll
		f := newContributionFixture(t, &from)
		f.addListenerSet("tenant", "b-younger", younger, "from-younger")
		f.addListenerSet("tenant", "a-older", older, "from-older")

		f.builder.contributeListeners()

		assert.Equal(t, []string{"from-older", "from-younger"}, f.contributedNames(),
			"creation time wins over the name, and over map iteration order")
	})

	t.Run("a tie on creation time is broken by namespace then name", func(t *testing.T) {
		from := gatewayv1.NamespacesFromAll
		f := newContributionFixture(t, &from)
		f.addListenerSet("zeta", "ls", older, "from-zeta")
		f.addListenerSet("alpha", "ls", older, "from-alpha")
		f.addListenerSet("alpha", "zz", older, "from-alpha-zz")

		f.builder.contributeListeners()

		assert.Equal(t, []string{"from-alpha", "from-alpha-zz", "from-zeta"}, f.contributedNames(),
			"the API breaks the tie on {namespace}/{name}")
	})

	t.Run("the entries of one ListenerSet keep their spec order", func(t *testing.T) {
		from := gatewayv1.NamespacesFromAll
		f := newContributionFixture(t, &from)
		f.addListenerSet("tenant", "ls", older, "third", "first", "second")

		f.builder.contributeListeners()

		assert.Equal(t, []string{"third", "first", "second"}, f.contributedNames(),
			"spec order, which is the order within a resource the API prescribes")
	})
}

// Anything in the merged list can win a port, so a refused ListenerSet must not get
// there at all -- filtering after the conflict comparison would let an intruder
// arbitrate against a legitimate listener.
func TestContributeListenersFiltersRefused(t *testing.T) {
	created := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("a ListenerSet the Gateway does not allow contributes nothing", func(t *testing.T) {
		f := newContributionFixture(t, nil) // no allowedListeners: the default is None
		treeLs := f.addListenerSet("infra", "ls", created, "http")

		f.builder.contributeListeners()

		assert.Empty(t, f.contributedNames())
		assert.Equal(t, attachmentNotAllowed, treeLs.Attachment,
			"the refusal is recorded so it can be reported on the ListenerSet's status")
	})

	t.Run("Same refuses a ListenerSet from another namespace", func(t *testing.T) {
		from := gatewayv1.NamespacesFromSame
		f := newContributionFixture(t, &from)
		outside := f.addListenerSet("tenant", "outside", created, "from-tenant")
		inside := f.addListenerSet("infra", "inside", created, "from-infra")

		f.builder.contributeListeners()

		assert.Equal(t, []string{"from-infra"}, f.contributedNames())
		assert.Equal(t, attachmentNotAllowed, outside.Attachment)
		assert.Equal(t, attachmentAllowed, inside.Attachment)
	})

	t.Run("a deleted ListenerSet contributes nothing and is reported on no longer", func(t *testing.T) {
		from := gatewayv1.NamespacesFromAll
		f := newContributionFixture(t, &from)
		treeLs := f.addListenerSet("tenant", "ls", created, "http")

		f.builder.contributeListeners()
		require.Equal(t, attachmentAllowed, treeLs.Attachment)
		require.Len(t, f.contributedNames(), 1)

		treeLs.SetAsDeleted(f.store.Logger)
		f.builder.contributeListeners()

		assert.Empty(t, f.contributedNames(),
			"its entries have to leave the merged list, which is what removes the frontend")
		assert.Equal(t, attachmentUnreported, treeLs.Attachment,
			"a deleted resource gets no status, so the decision goes back to silent")
	})

	t.Run("the table is rebuilt, not accumulated", func(t *testing.T) {
		from := gatewayv1.NamespacesFromAll
		f := newContributionFixture(t, &from)
		f.addListenerSet("tenant", "ls", created, "http")

		f.builder.contributeListeners()
		f.builder.contributeListeners()

		assert.Len(t, f.contributedNames(), 1,
			"two cycles must not double the contribution")
	})
}

// A contributed listener is identified by both resources, and the pair is what keeps
// two entries sharing a name apart -- which the API explicitly allows.
func TestContributedListenerIdentityAndOrigin(t *testing.T) {
	from := gatewayv1.NamespacesFromAll
	f := newContributionFixture(t, &from)
	f.addListenerSet("tenant", "ls", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), "http")

	f.builder.contributeListeners()

	contributed := f.store.contributedListenersByGatewayKey[f.gwKey]
	require.Len(t, contributed, 1)
	listener := contributed[0]

	assert.Equal(t, f.gwKey, listener.Key().GatewayKey,
		"the Gateway it is served on, which decides its frontend")
	assert.Equal(t, client.ObjectKey{Namespace: "tenant", Name: "ls"}, listener.Key().ListenerSetKey,
		"the resource declaring it, which decides whose status reports it")
	assert.True(t, listener.FromListenerSet())
	assert.Equal(t, kindListenerSet, listener.OwnerKind)
	assert.False(t, listener.ExemptFromReferenceGrant,
		"a ListenerSet is a tenant resource; the exemption is for the synthetic gateway")
}

// Two homonymous entries from sibling ListenerSets are a case the API calls out, and
// the bare name is exactly what would collapse them.
func TestContributedHomonymousEntriesStayDistinct(t *testing.T) {
	created := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	from := gatewayv1.NamespacesFromAll
	f := newContributionFixture(t, &from)
	f.addListenerSet("tenant", "first", created, "http")
	f.addListenerSet("tenant", "second", created, "http")

	f.builder.contributeListeners()

	contributed := f.store.contributedListenersByGatewayKey[f.gwKey]
	require.Len(t, contributed, 2, "both entries are contributed despite sharing a name")
	assert.NotEqual(t, contributed[0].Key(), contributed[1].Key(),
		"the declaring ListenerSet is what keeps the keys apart")
}

// The merged list is one writer and these two lines are the whole precedence
// contract: the Gateway's own listeners first, then the contributions.
func TestGatewayConstructListenersMergesOwnThenContributed(t *testing.T) {
	gw := &Gateway{
		K8sResource: &gatewayv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Namespace: "infra", Name: "gw"},
			Spec: gatewayv1.GatewaySpec{Listeners: []gatewayv1.Listener{
				{Name: "own-first", Port: 80, Protocol: gatewayv1.HTTPProtocolType},
				{Name: "own-second", Port: 81, Protocol: gatewayv1.HTTPProtocolType},
			}},
		},
	}
	contributed := listenersFromEntries(
		client.ObjectKey{Namespace: "infra", Name: "gw"},
		&gatewayv1.ListenerSet{
			ObjectMeta: metav1.ObjectMeta{Namespace: "tenant", Name: "ls"},
			Spec: gatewayv1.ListenerSetSpec{Listeners: []gatewayv1.ListenerEntry{
				{Name: "contributed", Port: 82, Protocol: gatewayv1.HTTPProtocolType},
			}},
		},
	)

	gw.constructListeners(contributed)

	require.Len(t, gw.Listeners, 3)
	assert.Equal(t, []string{"own-first", "own-second", "contributed"},
		[]string{
			string(gw.Listeners[0].Name()),
			string(gw.Listeners[1].Name()),
			string(gw.Listeners[2].Name()),
		},
		"own listeners come first, which is what makes them win a contested port")

	assert.Len(t, gw.OwnListeners(), 2,
		"OwnListeners is what a parentRef naming the Gateway attaches to")

	gw.constructListeners(nil)
	assert.Len(t, gw.Listeners, 2,
		"rebuilding with no contribution must drop the entries, not keep them")
}

// A ListenerSet alone produces no configuration: everything downstream is keyed on
// the Gateway, so an event that did not reach the parent would change nothing.
func TestReprocessGatewaysOfChangedListenerSets(t *testing.T) {
	created := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("a changed ListenerSet puts its parent back in the Gateway updates", func(t *testing.T) {
		from := gatewayv1.NamespacesFromAll
		f := newContributionFixture(t, &from)
		treeLs := f.addListenerSet("tenant", "ls", created, "http")
		lsKey := types.NamespacedName{Namespace: "tenant", Name: "ls"}
		f.store.ClusterStore.Updates.ListenerSets[lsKey] = store.Update[*gatewayv1.ListenerSet]{
			Status:    store.StatusUpserted,
			NewObject: treeLs.K8sResource,
		}

		f.builder.reprocessGatewaysOfChangedListenerSets()

		update, injected := f.store.ClusterStore.Updates.Gateways[f.gwKey]
		require.True(t, injected, "without this the merged list is never rebuilt")
		assert.Equal(t, store.StatusUpserted, update.Status)
		assert.NotNil(t, update.NewObject)
	})

	t.Run("a deleted ListenerSet still reaches its parent, via the old object", func(t *testing.T) {
		from := gatewayv1.NamespacesFromAll
		f := newContributionFixture(t, &from)
		treeLs := f.addListenerSet("tenant", "ls", created, "http")
		oldObject := treeLs.K8sResource
		f.store.ClusterStore.Updates.ListenerSets[types.NamespacedName{Namespace: "tenant", Name: "ls"}] = store.Update[*gatewayv1.ListenerSet]{Status: store.StatusDeleted, OldObject: oldObject}

		f.builder.reprocessGatewaysOfChangedListenerSets()

		_, injected := f.store.ClusterStore.Updates.Gateways[f.gwKey]
		assert.True(t, injected,
			"the tree keeps a deleted ListenerSet with a nil resource, so the parentRef is only readable here")
	})

	t.Run("an existing Gateway update is not overwritten", func(t *testing.T) {
		from := gatewayv1.NamespacesFromAll
		f := newContributionFixture(t, &from)
		treeLs := f.addListenerSet("tenant", "ls", created, "http")
		lsKey := types.NamespacedName{Namespace: "tenant", Name: "ls"}
		f.store.ClusterStore.Updates.ListenerSets[lsKey] = store.Update[*gatewayv1.ListenerSet]{
			Status:    store.StatusUpserted,
			NewObject: treeLs.K8sResource,
		}
		f.store.ClusterStore.Updates.Gateways[f.gwKey] = store.Update[*gatewayv1.Gateway]{Status: store.StatusDeleted}

		f.builder.reprocessGatewaysOfChangedListenerSets()

		assert.Equal(t, store.StatusDeleted, f.store.ClusterStore.Updates.Gateways[f.gwKey].Status,
			"a real Gateway event, deletion included, must not be downgraded to an upsert")
	})

	t.Run("a ListenerSet aiming elsewhere reaches nothing", func(t *testing.T) {
		from := gatewayv1.NamespacesFromAll
		f := newContributionFixture(t, &from)
		foreign := mkListenerSet("tenant", "ls", mkParentRef("not-in-store"))
		f.store.ClusterStore.Updates.ListenerSets[types.NamespacedName{Namespace: "tenant", Name: "ls"}] = store.Update[*gatewayv1.ListenerSet]{Status: store.StatusUpserted, NewObject: foreign}

		f.builder.reprocessGatewaysOfChangedListenerSets()

		assert.Empty(t, f.store.ClusterStore.Updates.Gateways)
	})
}

// The set of ListenerSets contributing to a Gateway is rebuilt from the whole tree
// every cycle, so its contents say nothing about what moved. Comparing it with the
// previous cycle is what finds the Gateways whose merged list is now wrong, and those
// changes are not all ListenerSet events: a namespace whose labels change, under
// allowedListeners.from: Selector, changes the answer for a ListenerSet that did not
// change at all.
func TestReprocessGatewaysWhoseContributorsChanged(t *testing.T) {
	created := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("a ListenerSet that changes parent reaches both Gateways", func(t *testing.T) {
		from := gatewayv1.NamespacesFromAll
		f := newContributionFixture(t, &from)
		otherGwKey := f.addGateway("infra", "gw-other")
		treeLs := f.addListenerSet("tenant", "ls", created, "http")

		f.builder.contributeListeners()
		require.Equal(t, []string{"http"}, f.contributedNames())

		// A new cycle, with no event at all: the ListenerSet moved to another
		// Gateway, which the API allows -- parentRef is not immutable.
		f.store.ClusterStore.Updates = store.NewClusterUpdates()
		reparent(treeLs, otherGwKey)
		f.builder.contributeListeners()

		assert.Empty(t, f.store.contributedListenersByGatewayKey[f.gwKey],
			"the former parent no longer receives the entries")
		assert.Len(t, f.store.contributedListenersByGatewayKey[otherGwKey], 1,
			"the new parent does")

		_, orphaned := f.store.ClusterStore.Updates.Gateways[f.gwKey]
		assert.True(t, orphaned,
			"the former parent still holds those listeners in its merged list, so it has to rebuild it")
		_, gained := f.store.ClusterStore.Updates.Gateways[otherGwKey]
		assert.True(t, gained, "the new parent has to build a merged list")
	})

	t.Run("a namespace whose labels stop matching reaches its Gateway", func(t *testing.T) {
		selector := &metav1.LabelSelector{MatchLabels: map[string]string{"tenant": "allowed"}}
		f := newContributionFixtureWithPolicy(t, &gatewayv1.AllowedListeners{
			Namespaces: &gatewayv1.ListenerNamespaces{
				From:     ptrTo(gatewayv1.NamespacesFromSelector),
				Selector: selector,
			},
		})
		nsKey := types.NamespacedName{Name: "tenant"}
		f.store.ClusterStore.Namespaces[nsKey] = &v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "tenant", Labels: map[string]string{"tenant": "allowed"}},
		}
		f.addListenerSet("tenant", "ls", created, "http")

		f.builder.contributeListeners()
		require.Equal(t, []string{"http"}, f.contributedNames())

		// The label is removed. Nothing about the ListenerSet changed, so no
		// ListenerSet event is raised and no parent is enqueued from one.
		f.store.ClusterStore.Updates = store.NewClusterUpdates()
		f.store.ClusterStore.Namespaces[nsKey].Labels = nil
		f.builder.contributeListeners()

		assert.Empty(t, f.contributedNames(),
			"a ListenerSet the Gateway no longer allows contributes nothing")
		_, injected := f.store.ClusterStore.Updates.Gateways[f.gwKey]
		assert.True(t, injected,
			"otherwise the merged list keeps serving a listener the Gateway no longer allows")
	})

	t.Run("a namespace whose labels start matching reaches its Gateway", func(t *testing.T) {
		selector := &metav1.LabelSelector{MatchLabels: map[string]string{"tenant": "allowed"}}
		f := newContributionFixtureWithPolicy(t, &gatewayv1.AllowedListeners{
			Namespaces: &gatewayv1.ListenerNamespaces{
				From:     ptrTo(gatewayv1.NamespacesFromSelector),
				Selector: selector,
			},
		})
		nsKey := types.NamespacedName{Name: "tenant"}
		f.store.ClusterStore.Namespaces[nsKey] = &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant"}}
		f.addListenerSet("tenant", "ls", created, "http")

		f.builder.contributeListeners()
		require.Empty(t, f.contributedNames())

		f.store.ClusterStore.Updates = store.NewClusterUpdates()
		f.store.ClusterStore.Namespaces[nsKey].Labels = map[string]string{"tenant": "allowed"}
		f.builder.contributeListeners()

		assert.Equal(t, []string{"http"}, f.contributedNames())
		_, injected := f.store.ClusterStore.Updates.Gateways[f.gwKey]
		assert.True(t, injected, "the newly allowed listener has to reach the merged list")
	})

	t.Run("an unchanged set reprocesses nothing", func(t *testing.T) {
		from := gatewayv1.NamespacesFromAll
		f := newContributionFixture(t, &from)
		f.addListenerSet("tenant", "ls", created, "http")

		f.builder.contributeListeners()
		f.store.ClusterStore.Updates = store.NewClusterUpdates()
		f.builder.contributeListeners()

		assert.Empty(t, f.store.ClusterStore.Updates.Gateways,
			"a cycle that changed nothing must not rebuild every Gateway that has a ListenerSet")
	})
}

func ptrTo[T any](v T) *T { return &v }
