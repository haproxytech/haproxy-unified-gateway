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
package status

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func testExtractGVK(client.Object) schema.GroupVersionKind {
	return schema.GroupVersionKind{}
}

// listenerSetStatusTestPatcher builds a patcher directly, so a case can vary one part
// of what it publishes without going through the tree.
func listenerSetStatusTestPatcher(conditions generic.Conditions, entries ...gatewayv1.ListenerEntryStatus) *listenerSetStatusPatcher {
	return &listenerSetStatusPatcher{conditions: conditions, entries: entries, logger: slog.Default()}
}

func listenerSetAcceptedCondition() generic.Conditions {
	return generic.Conditions{
		generic.ConditionType(gatewayv1.ListenerSetConditionAccepted): {
			Type:    generic.ConditionType(gatewayv1.ListenerSetConditionAccepted),
			Status:  metav1.ConditionTrue,
			Reason:  string(gatewayv1.ListenerSetReasonAccepted),
			Message: "ListenerSet is accepted",
		},
	}
}

func listenerEntryStatus(name gatewayv1.SectionName, attachedRoutes int32) gatewayv1.ListenerEntryStatus {
	return gatewayv1.ListenerEntryStatus{
		Name: name,
		Conditions: []metav1.Condition{{
			Type:               string(gatewayv1.ListenerSetConditionAccepted),
			Status:             metav1.ConditionTrue,
			Reason:             string(gatewayv1.ListenerSetReasonAccepted),
			Message:            "Listener is accepted",
			ObservedGeneration: 7,
		}},
		AttachedRoutes: attachedRoutes,
	}
}

// StatusEqual decides whether the write is skipped, so every case here is a claim that
// some difference between what we computed and what the cluster holds is noticed -- or
// that a difference which is not one is not.
func TestListenerSetStatusPatcher_StatusEqual(t *testing.T) {
	group := gatewayv1.Group(gatewayv1.GroupName)
	tests := []struct {
		name       string
		conditions generic.Conditions
		entries    []gatewayv1.ListenerEntryStatus
		published  gatewayv1.ListenerSetStatus
		want       bool
	}{
		{
			name:       "identical conditions and entries",
			conditions: listenerSetAcceptedCondition(),
			entries:    []gatewayv1.ListenerEntryStatus{listenerEntryStatus("http", 2)},
			published: gatewayv1.ListenerSetStatus{
				Conditions: listenerSetAcceptedCondition().ToMetav1Conditions(),
				Listeners:  []gatewayv1.ListenerEntryStatus{listenerEntryStatus("http", 2)},
			},
			want: true,
		},
		{
			name:       "entries published in another order",
			conditions: listenerSetAcceptedCondition(),
			entries: []gatewayv1.ListenerEntryStatus{
				listenerEntryStatus("http", 2),
				listenerEntryStatus("https", 0),
			},
			published: gatewayv1.ListenerSetStatus{
				Conditions: listenerSetAcceptedCondition().ToMetav1Conditions(),
				Listeners: []gatewayv1.ListenerEntryStatus{
					listenerEntryStatus("https", 0),
					listenerEntryStatus("http", 2),
				},
			},
			want: true, // status.listeners is a set by name, so the order is not a difference
		},
		{
			name:       "own condition message differs",
			conditions: listenerSetAcceptedCondition(),
			published: gatewayv1.ListenerSetStatus{
				Conditions: generic.Conditions{
					generic.ConditionType(gatewayv1.ListenerSetConditionAccepted): {
						Type:    generic.ConditionType(gatewayv1.ListenerSetConditionAccepted),
						Status:  metav1.ConditionTrue,
						Reason:  string(gatewayv1.ListenerSetReasonAccepted),
						Message: "something else",
					},
				}.ToMetav1Conditions(),
			},
			want: false,
		},
		{
			name:       "cluster holds a condition we no longer compute",
			conditions: listenerSetAcceptedCondition(),
			published: gatewayv1.ListenerSetStatus{
				Conditions: append(
					listenerSetAcceptedCondition().ToMetav1Conditions(),
					metav1.Condition{Type: string(gatewayv1.ListenerSetConditionProgrammed), Status: metav1.ConditionTrue},
				),
			},
			want: false,
		},
		{
			name:       "an entry is not published yet",
			conditions: listenerSetAcceptedCondition(),
			entries:    []gatewayv1.ListenerEntryStatus{listenerEntryStatus("http", 2)},
			published: gatewayv1.ListenerSetStatus{
				Conditions: listenerSetAcceptedCondition().ToMetav1Conditions(),
			},
			want: false,
		},
		{
			name:       "cluster holds an entry we no longer compute",
			conditions: listenerSetAcceptedCondition(),
			published: gatewayv1.ListenerSetStatus{
				Conditions: listenerSetAcceptedCondition().ToMetav1Conditions(),
				Listeners:  []gatewayv1.ListenerEntryStatus{listenerEntryStatus("http", 2)},
			},
			want: false,
		},
		{
			name:       "an entry's attachedRoutes differs",
			conditions: listenerSetAcceptedCondition(),
			entries:    []gatewayv1.ListenerEntryStatus{listenerEntryStatus("http", 3)},
			published: gatewayv1.ListenerSetStatus{
				Conditions: listenerSetAcceptedCondition().ToMetav1Conditions(),
				Listeners:  []gatewayv1.ListenerEntryStatus{listenerEntryStatus("http", 2)},
			},
			want: false,
		},
		{
			name:       "an entry's conditions differ",
			conditions: listenerSetAcceptedCondition(),
			entries:    []gatewayv1.ListenerEntryStatus{listenerEntryStatus("http", 2)},
			published: gatewayv1.ListenerSetStatus{
				Conditions: listenerSetAcceptedCondition().ToMetav1Conditions(),
				Listeners: []gatewayv1.ListenerEntryStatus{{
					Name:           "http",
					AttachedRoutes: 2,
					Conditions: []metav1.Condition{{
						Type:               string(gatewayv1.ListenerSetConditionAccepted),
						Status:             metav1.ConditionFalse,
						Reason:             string(gatewayv1.ListenerSetReasonListenersNotValid),
						ObservedGeneration: 7,
					}},
				}},
			},
			want: false,
		},
		{
			name:       "an entry's supportedKinds differ",
			conditions: listenerSetAcceptedCondition(),
			entries: []gatewayv1.ListenerEntryStatus{{
				Name:           "http",
				AttachedRoutes: 2,
				SupportedKinds: []gatewayv1.RouteGroupKind{{Group: &group, Kind: gatewayv1.Kind("HTTPRoute")}},
			}},
			published: gatewayv1.ListenerSetStatus{
				Conditions: listenerSetAcceptedCondition().ToMetav1Conditions(),
				Listeners: []gatewayv1.ListenerEntryStatus{{
					Name:           "http",
					AttachedRoutes: 2,
				}},
			},
			want: false,
		},
		{
			name: "nothing published and nothing computed",
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sp := listenerSetStatusTestPatcher(tt.conditions, tt.entries...)
			ls := &gatewayv1.ListenerSet{Status: tt.published}

			got, err := sp.StatusEqual(ls)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// The constructor is where the tree's own types become API types, and where the
// generation the verdict builder stamped survives: it is in the conditions already, so
// nothing re-stamps them on the way out.
func TestListenerSetStatusPatcher_PublishesTheTreeVerdict(t *testing.T) {
	group := gatewayv1.Group(gatewayv1.GroupName)
	treeLs := &tree.ListenerSet{
		Conditions: listenerSetAcceptedCondition(),
		Entries: []tree.ListenerEntryStatus{{
			Name: "http",
			Conditions: generic.Conditions{
				generic.ConditionType(gatewayv1.ListenerSetConditionAccepted): {
					Type:               generic.ConditionType(gatewayv1.ListenerSetConditionAccepted),
					Status:             metav1.ConditionTrue,
					Reason:             string(gatewayv1.ListenerSetReasonAccepted),
					Message:            "Listener is accepted",
					ObservedGeneration: 7,
				},
			},
			SupportedKinds: []gatewayv1.RouteGroupKind{{Group: &group, Kind: gatewayv1.Kind("HTTPRoute")}},
			AttachedRoutes: 3,
		}},
	}
	treeLs.Conditions.SetGeneration(7)

	sp := newListenerSetStatusPatcher(treeLs, testExtractGVK, slog.Default())
	ls := &gatewayv1.ListenerSet{ObjectMeta: metav1.ObjectMeta{Namespace: "tenant", Name: "ls"}}

	require.NoError(t, sp.SetStatus(ls))

	require.Len(t, ls.Status.Listeners, 1)
	entry := ls.Status.Listeners[0]
	require.Equal(t, gatewayv1.SectionName("http"), entry.Name)
	require.Equal(t, int32(3), entry.AttachedRoutes)
	require.Equal(t, []gatewayv1.RouteGroupKind{{Group: &group, Kind: gatewayv1.Kind("HTTPRoute")}}, entry.SupportedKinds)

	require.Len(t, entry.Conditions, 1)
	require.Equal(t, int64(7), entry.Conditions[0].ObservedGeneration,
		"an entry's conditions must carry the ListenerSet's generation, not the parent Gateway's")

	require.Len(t, ls.Status.Conditions, 1)
	require.Equal(t, int64(7), ls.Status.Conditions[0].ObservedGeneration)
}

// The ListenerSets a status test works with: the Gateway that allows them, one that does not,
// and the ListenerSet naming either.
const (
	statusTestGatewayNamespace = "infra"
	statusTestGatewayName      = "gw"
	statusTestForeignGateway   = "not-ours"
	statusTestListenerSetName  = "ls"
	statusTestListenerSetEntry = "http"
)

func newStatusTestControllerStore(t *testing.T) *tree.ControllerStore {
	t.Helper()
	gwKey := types.NamespacedName{Namespace: statusTestGatewayNamespace, Name: statusTestGatewayName}
	from := gatewayv1.NamespacesFromAll
	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: gwKey.Namespace, Name: gwKey.Name},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "hug",
			AllowedListeners: &gatewayv1.AllowedListeners{
				Namespaces: &gatewayv1.ListenerNamespaces{From: &from},
			},
		},
	}
	return &tree.ControllerStore{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		ClusterStore: &store.ClusterStore{
			Gateways:     map[types.NamespacedName]*gatewayv1.Gateway{gwKey: gw},
			ListenerSets: map[types.NamespacedName]*gatewayv1.ListenerSet{},
			Namespaces:   map[types.NamespacedName]*corev1.Namespace{},
			Updates:      store.NewClusterUpdates(),
		},
		GateTree: &tree.GateTree{
			Gateways:       map[types.NamespacedName]*tree.Gateway{},
			GatewayClasses: map[types.NamespacedName]*tree.GatewayClass{{Name: "hug"}: {}},
			ListenerSets:   map[types.NamespacedName]*tree.ListenerSet{},
		},
	}
}

// attachedStatusTestListenerSet drives the contribution pass for one ListenerSet naming
// parentName, and returns its tree node. The attachment decision is the pass's and not the
// fixture's: a test that wrote the field itself would still pass if the guard stopped reading
// it.
func attachedStatusTestListenerSet(t *testing.T, parentName string) *tree.ListenerSet {
	t.Helper()
	controllerStore := newStatusTestControllerStore(t)
	lsKey := types.NamespacedName{Namespace: "tenant", Name: statusTestListenerSetName}
	k8sLs := &gatewayv1.ListenerSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: lsKey.Namespace, Name: lsKey.Name},
		Spec: gatewayv1.ListenerSetSpec{
			ParentRef: gatewayv1.ParentGatewayReference{
				Name:      gatewayv1.ObjectName(parentName),
				Namespace: pointerTo(gatewayv1.Namespace(statusTestGatewayNamespace)),
			},
			Listeners: []gatewayv1.ListenerEntry{{
				Name: statusTestListenerSetEntry, Port: 8080, Protocol: gatewayv1.HTTPProtocolType,
			}},
		},
	}
	controllerStore.ClusterStore.Updates.ListenerSets[lsKey] = store.Update[*gatewayv1.ListenerSet]{
		Status:    store.StatusUpserted,
		NewObject: k8sLs,
	}
	builder := &tree.ListenerSetContributionBuilder{ControllerStore: controllerStore}
	builder.ComputeTreeUpdates()

	require.Contains(t, controllerStore.GateTree.ListenerSets, lsKey)
	return controllerStore.GateTree.ListenerSets[lsKey]
}

func newStatusTestUpdater(t *testing.T) *StatusUpdaterImpl {
	t.Helper()
	return &StatusUpdaterImpl{
		config: StatusUpdaterConf{
			logger:     slog.Default(),
			client:     fake.NewClientBuilder().Build(),
			extractGVK: testExtractGVK,
		},
	}
}

// staleCountTestUpdater is a status updater over a client that carries a status
// subresource for both objects the Gateway pass writes, so a test can read back what was
// published. It is built here rather than reusing the pass's own fixture helper: this test
// pins the pass, and a helper that grew with it should not be able to hide a regression.
func staleCountTestUpdater(t *testing.T, gw *tree.Gateway) (*StatusUpdaterImpl, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, gatewayv1.Install(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&gatewayv1.Gateway{}, &gatewayv1.ListenerSet{}).
		WithObjects(gw.K8sResource).Build()
	return &StatusUpdaterImpl{config: StatusUpdaterConf{
		logger:     slog.Default(),
		client:     c,
		extractGVK: testExtractGVK,
	}}, c
}

// The count is a claim about the ListenerSets attached to the Gateway, not about the
// Gateway's own spec: it moves when one of them stops contributing, in a cycle where the
// Gateway may receive no event at all. An unchanged Gateway is otherwise skipped, so the
// publication has to be driven by the count itself -- otherwise the resource keeps a
// count that describes a past cycle, with no generation of its own to reveal it.
func TestPrepareGatewayUpdatesRepublishesAStaleListenerSetCount(t *testing.T) {
	published := int32(1)
	gwKey := types.NamespacedName{Namespace: "infra", Name: "gw"}
	treeGateway := func(count int32) *tree.Gateway {
		return &tree.Gateway{
			K8sResource: &gatewayv1.Gateway{
				Namespace: gwKey.Namespace, Name: gwKey.Name,
				Status: gatewayv1.GatewayStatus{AttachedListenerSets: &published},
			},
			AttachedListenerSets: count,
		}
	}

	t.Run("a count the resource does not carry is republished", func(t *testing.T) {
		gw := treeGateway(0)
		updater, c := staleCountTestUpdater(t, gw)

		writes := updater.prepareGatewayUpdates(context.Background(), map[types.NamespacedName]*tree.Gateway{gwKey: gw})
		require.Len(t, writes, 1, "an unchanged Gateway owes this write")
		for _, write := range writes {
			write(context.Background())
		}

		live := &gatewayv1.Gateway{}
		require.NoError(t, c.Get(context.Background(), gwKey, live))
		require.NotNil(t, live.Status.AttachedListenerSets)
		require.Equal(t, int32(0), *live.Status.AttachedListenerSets)
	})

	t.Run("a count the resource already carries leaves the Gateway alone", func(t *testing.T) {
		gw := treeGateway(1)
		updater, _ := staleCountTestUpdater(t, gw)

		writes := updater.prepareGatewayUpdates(context.Background(), map[types.NamespacedName]*tree.Gateway{gwKey: gw})
		require.Empty(t, writes, "a status that did not move is not worth a read")
	})

	t.Run("an absent published count is not read as zero", func(t *testing.T) {
		gw := treeGateway(0)
		gw.K8sResource.Status.AttachedListenerSets = nil
		updater, _ := staleCountTestUpdater(t, gw)

		writes := updater.prepareGatewayUpdates(context.Background(), map[types.NamespacedName]*tree.Gateway{gwKey: gw})
		require.Len(t, writes, 1, "a Gateway that was never given a count has to receive it")
	})
}

// A ListenerSet whose parentRef names another controller's resource must not have its
// status touched -- not even cleared -- and that guard is the reason the patcher reads
// the attachment decision rather than assuming a ListenerSet in the tree is ours.
//
// The attachment is taken from the contribution pass rather than set by hand: the pass
// is what decides, and a test that wrote the field itself would still pass if the guard
// stopped reading it. The positive case is asserted first, so a fixture that silently
// stopped producing an attached ListenerSet fails here instead of being read as the
// skip it is meant to exercise.
func TestPrepareListenerSetUpdatesPublishesOnlyWhatItOwns(t *testing.T) {
	t.Run("an attached ListenerSet is published", func(t *testing.T) {
		ls := attachedStatusTestListenerSet(t, statusTestGatewayName)
		require.True(t, ls.IsAttachmentReported(), "fixture must produce an attached ListenerSet")

		writes := newStatusTestUpdater(t).prepareListenerSetUpdates(context.Background(), map[types.NamespacedName]*tree.ListenerSet{
			{Namespace: "tenant", Name: statusTestListenerSetName}: ls,
		})
		require.Len(t, writes, 1)
	})

	t.Run("a ListenerSet naming another controller's Gateway is left alone", func(t *testing.T) {
		ls := attachedStatusTestListenerSet(t, statusTestForeignGateway)
		require.False(t, ls.IsAttachmentReported())

		writes := newStatusTestUpdater(t).prepareListenerSetUpdates(context.Background(), map[types.NamespacedName]*tree.ListenerSet{
			{Namespace: "tenant", Name: statusTestListenerSetName}: ls,
		})
		require.Empty(t, writes, "writing here would clear a status that is not ours to comment on")
	})

	t.Run("a deleted ListenerSet has no resource left to write to", func(t *testing.T) {
		ls := attachedStatusTestListenerSet(t, statusTestGatewayName)
		ls.SetAsDeleted(slog.Default())

		writes := newStatusTestUpdater(t).prepareListenerSetUpdates(context.Background(), map[types.NamespacedName]*tree.ListenerSet{
			{Namespace: "tenant", Name: statusTestListenerSetName}: ls,
		})
		require.Empty(t, writes)
	})
}
