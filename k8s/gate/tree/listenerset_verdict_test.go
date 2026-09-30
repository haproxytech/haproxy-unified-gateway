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

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// verdictFixture is one Gateway and whatever ListenerSets and contributed entries a
// test puts on its merged listener list.
type verdictFixture struct {
	builder *ListenerSetVerdictBuilder
	store   *ControllerStore
	t       *testing.T
	gwKey   client.ObjectKey
	gw      *Gateway
}

// newVerdictFixture builds a Gateway whose Accepted condition is the given one, and
// a verdict builder over it. The Gateway's own conditions are set directly rather
// than computed: this pass reads the Gateway builder's output, so a fixture that
// recomputed it would be testing the wrong thing.
func newVerdictFixture(t *testing.T, gatewayAccepted bool) *verdictFixture {
	t.Helper()

	gwKey := client.ObjectKey{Namespace: "infra", Name: "gw"}
	gw := &Gateway{
		K8sResource: &gatewayv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Namespace: gwKey.Namespace, Name: gwKey.Name, Generation: 7},
			Spec:       gatewayv1.GatewaySpec{GatewayClassName: "hug"},
		},
		TreeStatus: TreeUpdate[Gateway]{Status: store.StatusUpserted},
		Conditions: make(generic.Conditions),
	}
	if gatewayAccepted {
		gw.Conditions.MergeOverrideConditions(conditions.NewGatewayAcceptedOK())
	} else {
		gw.Conditions.MergeOverrideConditions(conditions.NewGatewayAcceptedInvalidConditions("gatewayclass"))
	}

	controllerStore := &ControllerStore{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		GateTree: &GateTree{
			Gateways:     map[types.NamespacedName]*Gateway{gwKey: gw},
			ListenerSets: map[types.NamespacedName]*ListenerSet{},
		},
	}

	return &verdictFixture{
		builder: &ListenerSetVerdictBuilder{ControllerStore: controllerStore},
		store:   controllerStore,
		t:       t,
		gwKey:   gwKey,
		gw:      gw,
	}
}

// addListenerSet puts a ListenerSet in the tree with an already-settled attachment
// decision, the way the contribution pass would have left it.
func (f *verdictFixture) addListenerSet(namespace, name string, attachment listenerSetAttachment, generation int64, entryNames ...string) *ListenerSet {
	entries := make([]gatewayv1.ListenerEntry, 0, len(entryNames))
	for i, entryName := range entryNames {
		entries = append(entries, gatewayv1.ListenerEntry{
			Name:     gatewayv1.SectionName(entryName),
			Port:     gatewayv1.PortNumber(8000 + i),
			Protocol: gatewayv1.HTTPProtocolType,
		})
	}

	k8sLs := &gatewayv1.ListenerSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, Generation: generation},
		Spec: gatewayv1.ListenerSetSpec{
			ParentRef: gatewayv1.ParentGatewayReference{
				Name:      gatewayv1.ObjectName(f.gwKey.Name),
				Namespace: (*gatewayv1.Namespace)(&f.gwKey.Namespace),
			},
			Listeners: entries,
		},
	}

	treeLs := NewListenerSet(k8sLs)
	treeLs.Attachment = attachment
	f.store.GateTree.ListenerSets[types.NamespacedName{Namespace: namespace, Name: name}] = treeLs
	return treeLs
}

// addContributedEntry appends an entry to the Gateway's merged listener list, as the
// Gateway builder would have left it once judged.
func (f *verdictFixture) addContributedEntry(namespace, name, entryName string, accepted bool, programmed metav1.ConditionStatus) *Listener {
	lsKey := client.ObjectKey{Namespace: namespace, Name: name}
	listener := &Listener{
		ListenerKey: listenerSetListenerKey(f.gwKey, lsKey, gatewayv1.SectionName(entryName)),
		K8sListenerSetListenerEntry: &gatewayv1.ListenerEntry{
			Name:     gatewayv1.SectionName(entryName),
			Port:     8080,
			Protocol: gatewayv1.HTTPProtocolType,
		},
		OwnerKind:      kindListenerSet,
		Accepted:       accepted,
		Conditions:     make(generic.Conditions),
		AttachedRoutes: make(map[client.ObjectKey]struct{}),
	}
	f.setEntryConditions(listener, accepted, programmed)
	f.gw.Listeners = append(f.gw.Listeners, listener)
	return listener
}

func (f *verdictFixture) setEntryConditions(listener *Listener, accepted bool, programmed metav1.ConditionStatus) {
	if accepted {
		listener.Conditions.MergeOverrideConditions(entryConditions(f.t, conditions.VerdictAccepted, ""))
	} else {
		listener.Conditions.MergeOverrideConditions(entryConditions(f.t, conditions.VerdictUnsupportedProtocol, "protocol not served"))
	}

	switch programmed {
	case metav1.ConditionTrue:
		listener.Conditions.MergeOverrideConditions(entryConditions(f.t, conditions.VerdictProgrammed, ""))
	case metav1.ConditionUnknown:
		listener.Conditions.MergeOverrideConditions(entryConditions(f.t, conditions.VerdictProgrammingPending, ""))
	default:
		listener.Conditions.MergeOverrideConditions(entryConditions(f.t, conditions.VerdictNotProgrammable, ""))
	}
}

func entryConditions(t *testing.T, verdict conditions.ListenerConditionVerdict, msg string) generic.Conditions {
	t.Helper()
	rendered, err := conditions.NewListenerEntryConditions(verdict, msg)
	require.NoError(t, err)
	return rendered
}

// conditionOf asserts a condition is present and returns it.
func conditionOf(t *testing.T, conds generic.Conditions, conditionType string) generic.Condition {
	t.Helper()
	condition, ok := conds.GetCondition(generic.ConditionType(conditionType))
	require.Truef(t, ok, "condition %s is missing", conditionType)
	return condition
}

// The attachment decision is the contribution pass's, and this pass reports it
// without re-deriving it: the three outcomes do not have the same consequence, and
// one of them owes silence.
func TestListenerSetVerdictAttachment(t *testing.T) {
	t.Run("not allowed is reported on the ListenerSet", func(t *testing.T) {
		f := newVerdictFixture(t, true)
		treeLs := f.addListenerSet("tenant", "ls", attachmentNotAllowed, 3, "http")

		f.builder.ComputeTreeUpdates()

		accepted := conditionOf(t, treeLs.Conditions, string(gatewayv1.ListenerSetConditionAccepted))
		assert.Equal(t, metav1.ConditionFalse, accepted.Status)
		assert.Equal(t, string(gatewayv1.ListenerSetReasonNotAllowed), accepted.Reason)

		programmed := conditionOf(t, treeLs.Conditions, string(gatewayv1.ListenerSetConditionProgrammed))
		assert.Equal(t, metav1.ConditionFalse, programmed.Status)

		assert.Empty(t, treeLs.Entries)
		assert.Zero(t, f.gw.AttachedListenerSets)
	})

	t.Run("a foreign parent writes nothing at all", func(t *testing.T) {
		f := newVerdictFixture(t, true)
		treeLs := f.addListenerSet("tenant", "ls", attachmentUnreported, 3, "http")

		f.builder.ComputeTreeUpdates()

		assert.Empty(t, treeLs.Conditions, "commenting on another controller's resource is the failure mode")
		assert.Empty(t, treeLs.Entries)
	})

	t.Run("a parent not accepted is echoed on the ListenerSet", func(t *testing.T) {
		f := newVerdictFixture(t, false)
		treeLs := f.addListenerSet("tenant", "ls", attachmentAllowed, 3, "http")
		f.addContributedEntry("tenant", "ls", "http", true, metav1.ConditionTrue)

		f.builder.ComputeTreeUpdates()

		accepted := conditionOf(t, treeLs.Conditions, string(gatewayv1.ListenerSetConditionAccepted))
		assert.Equal(t, metav1.ConditionFalse, accepted.Status)
		assert.Equal(t, string(gatewayv1.ListenerSetReasonParentNotAccepted), accepted.Reason)

		programmed := conditionOf(t, treeLs.Conditions, string(gatewayv1.ListenerSetConditionProgrammed))
		assert.Equal(t, metav1.ConditionFalse, programmed.Status)
		assert.Equal(t, "ParentNotProgrammed", programmed.Reason)

		assert.Zero(t, f.gw.AttachedListenerSets, "an unaccepted parent attaches nothing")
	})

	t.Run("a deleted ListenerSet is silent", func(t *testing.T) {
		f := newVerdictFixture(t, true)
		treeLs := f.addListenerSet("tenant", "ls", attachmentAllowed, 3, "http")
		treeLs.SetAsDeleted(slog.New(slog.NewTextHandler(io.Discard, nil)))

		f.builder.ComputeTreeUpdates()

		assert.Empty(t, treeLs.Conditions)
		assert.Empty(t, treeLs.Entries)
		assert.Zero(t, f.gw.AttachedListenerSets)
	})
}

// entryExpectation is one judged entry as the roll-up reads it.
type entryExpectation struct {
	accepted   bool
	programmed metav1.ConditionStatus
}

// The roll-up reads acceptance and programmation from two different places on
// purpose: ResolvedRefs=False leaves an entry Accepted, which is what keeps a
// ReferenceGrant mistake from refusing the whole ListenerSet.
func TestListenerSetVerdictRollUp(t *testing.T) {
	tests := []struct {
		name               string
		gatewayAccepted    bool
		entries            []entryExpectation
		wantAcceptedStatus metav1.ConditionStatus
		wantAcceptedReason gatewayv1.ListenerSetConditionReason
		wantProgrammedStat metav1.ConditionStatus
		wantProgrammedReas string
		wantAttachedCount  int32
	}{
		{
			name:               "every entry programmed",
			gatewayAccepted:    true,
			entries:            []entryExpectation{{true, metav1.ConditionTrue}, {true, metav1.ConditionTrue}},
			wantAcceptedStatus: metav1.ConditionTrue,
			wantAcceptedReason: gatewayv1.ListenerSetReasonAccepted,
			wantProgrammedStat: metav1.ConditionTrue,
			wantProgrammedReas: string(gatewayv1.ListenerSetReasonProgrammed),
			wantAttachedCount:  1,
		},
		{
			name:               "some entries refused keep the ListenerSet accepted",
			gatewayAccepted:    true,
			entries:            []entryExpectation{{true, metav1.ConditionTrue}, {false, metav1.ConditionFalse}},
			wantAcceptedStatus: metav1.ConditionTrue,
			wantAcceptedReason: gatewayv1.ListenerSetReasonListenersNotValid,
			wantProgrammedStat: metav1.ConditionFalse,
			wantProgrammedReas: string(gatewayv1.ListenerSetReasonListenersNotValid),
			wantAttachedCount:  1,
		},
		{
			name:               "no entry left refuses the ListenerSet",
			gatewayAccepted:    true,
			entries:            []entryExpectation{{false, metav1.ConditionFalse}},
			wantAcceptedStatus: metav1.ConditionFalse,
			wantAcceptedReason: gatewayv1.ListenerSetReasonListenersNotValid,
			wantProgrammedStat: metav1.ConditionFalse,
			wantProgrammedReas: string(gatewayv1.ListenerSetReasonListenersNotValid),
			wantAttachedCount:  0,
		},
		{
			name:               "accepted but not programmable is invalid, not pending",
			gatewayAccepted:    true,
			entries:            []entryExpectation{{true, metav1.ConditionTrue}, {true, metav1.ConditionFalse}},
			wantAcceptedStatus: metav1.ConditionTrue,
			wantAcceptedReason: gatewayv1.ListenerSetReasonAccepted,
			wantProgrammedStat: metav1.ConditionFalse,
			wantProgrammedReas: string(gatewayv1.ListenerSetReasonListenersNotValid),
			wantAttachedCount:  1,
		},
		{
			name:               "accepted with one still pending",
			gatewayAccepted:    true,
			entries:            []entryExpectation{{true, metav1.ConditionTrue}, {true, metav1.ConditionUnknown}},
			wantAcceptedStatus: metav1.ConditionTrue,
			wantAcceptedReason: gatewayv1.ListenerSetReasonAccepted,
			wantProgrammedStat: metav1.ConditionUnknown,
			wantProgrammedReas: string(gatewayv1.ListenerSetReasonPending),
			wantAttachedCount:  1,
		},
	}

	entryNames := []string{"one", "two"}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newVerdictFixture(t, tt.gatewayAccepted)
			treeLs := f.addListenerSet("tenant", "ls", attachmentAllowed, 3, entryNames...)

			for i, entry := range tt.entries {
				f.addContributedEntry("tenant", "ls", entryNames[i], entry.accepted, entry.programmed)
			}

			f.builder.ComputeTreeUpdates()

			accepted := conditionOf(t, treeLs.Conditions, string(gatewayv1.ListenerSetConditionAccepted))
			assert.Equal(t, tt.wantAcceptedStatus, accepted.Status)
			assert.Equal(t, string(tt.wantAcceptedReason), accepted.Reason)

			programmed := conditionOf(t, treeLs.Conditions, string(gatewayv1.ListenerSetConditionProgrammed))
			assert.Equal(t, tt.wantProgrammedStat, programmed.Status)
			assert.Equal(t, tt.wantProgrammedReas, programmed.Reason)

			assert.Equal(t, tt.wantAttachedCount, f.gw.AttachedListenerSets)
		})
	}
}

// An entry's conditions are published in its own ListenerSet's status, so they must
// observe that object's generation. Stamping the Gateway's generation would produce
// an observedGeneration no reader can verify.
func TestListenerSetVerdictEntryStatuses(t *testing.T) {
	f := newVerdictFixture(t, true)
	treeLs := f.addListenerSet("tenant", "ls", attachmentAllowed, 42, "alpha", "beta")

	alpha := f.addContributedEntry("tenant", "ls", "alpha", true, metav1.ConditionTrue)
	alpha.AllowedRouteKinds = []gatewayv1.RouteGroupKind{{Kind: gatewayv1.Kind("HTTPRoute")}}
	alpha.AttachedRoutes[client.ObjectKey{Namespace: "tenant", Name: "route-a"}] = struct{}{}
	f.addContributedEntry("tenant", "ls", "beta", true, metav1.ConditionUnknown)

	f.builder.ComputeTreeUpdates()

	require.Len(t, treeLs.Entries, 2)
	assert.Equal(t, gatewayv1.SectionName("alpha"), treeLs.Entries[0].Name)
	assert.Equal(t, gatewayv1.SectionName("beta"), treeLs.Entries[1].Name, "spec order, not a map's")

	assert.Equal(t, int32(1), treeLs.Entries[0].AttachedRoutes)
	assert.Equal(t, []gatewayv1.RouteGroupKind{{Kind: gatewayv1.Kind("HTTPRoute")}}, treeLs.Entries[0].SupportedKinds)

	for _, entry := range treeLs.Entries {
		for conditionType, condition := range entry.Conditions {
			assert.Equalf(t, int64(42), condition.ObservedGeneration,
				"condition %s must observe the ListenerSet's generation, not the Gateway's", conditionType)
		}
	}

	// The listeners themselves were stamped with the Gateway's generation, and the
	// status must not simply have inherited that.
	assert.Equal(t, int64(7), f.gw.K8sResource.Generation)
}

// Homonymous entries from sibling ListenerSets stay apart: a bare name is unique
// only within the resource declaring it, which is why each ListenerSet publishes
// only its own.
func TestListenerSetVerdictKeepsSiblingEntriesApart(t *testing.T) {
	f := newVerdictFixture(t, true)
	older := f.addListenerSet("tenant-a", "ls", attachmentAllowed, 3, "http")
	younger := f.addListenerSet("tenant-b", "ls", attachmentAllowed, 3, "http")

	f.addContributedEntry("tenant-a", "ls", "http", true, metav1.ConditionTrue)
	f.addContributedEntry("tenant-b", "ls", "http", true, metav1.ConditionTrue)

	f.builder.ComputeTreeUpdates()

	require.Len(t, older.Entries, 1)
	require.Len(t, younger.Entries, 1)
	assert.Equal(t, 2, int(f.gw.AttachedListenerSets))
}

// The count is a roll-up over the whole tree, so it must not carry a previous
// cycle's contributions once a ListenerSet stops contributing.
func TestListenerSetVerdictCountIsRecomputed(t *testing.T) {
	f := newVerdictFixture(t, true)
	f.addListenerSet("tenant", "ls", attachmentAllowed, 3, "http")
	f.addContributedEntry("tenant", "ls", "http", true, metav1.ConditionTrue)

	f.builder.ComputeTreeUpdates()
	require.Equal(t, int32(1), f.gw.AttachedListenerSets)

	f.gw.AttachedListenerSets = 5 // simulate a stale value from an earlier cycle

	treeLs := f.store.GateTree.ListenerSets[types.NamespacedName{Namespace: "tenant", Name: "ls"}]
	treeLs.Attachment = attachmentNotAllowed

	f.builder.ComputeTreeUpdates()

	assert.Zero(t, f.gw.AttachedListenerSets)
}

// Every check goes through Listener.conditionsFor, so that is where the two
// vocabularies must be told apart. An entry in two conflicts at once is the case
// that makes it observable: the Gateway's vocabulary has no word for it, and the
// entry's has ListenerConflict.
func TestContributedListenerIsReportedInEntryVocabulary(t *testing.T) {
	f := newVerdictFixture(t, true)
	f.addListenerSet("tenant", "ls", attachmentAllowed, 3, "http")
	entry := f.addContributedEntry("tenant", "ls", "http", true, metav1.ConditionTrue)

	rendered, err := entry.conditionsInOwnVocabulary(conditions.VerdictSeveralConflicts, "both")
	require.NoError(t, err)

	conflicted := conditionOf(t, rendered, string(gatewayv1.ListenerEntryConditionConflicted))
	assert.Equal(t, metav1.ConditionTrue, conflicted.Status)
	assert.Equal(t, string(gatewayv1.ListenerEntryReasonListenerConflict), conflicted.Reason)

	// The same verdict cannot be spelled for a listener the Gateway declares.
	gwListener := &Listener{
		ListenerKey:        gatewayListenerKey(f.gwKey, "http"),
		K8sGatewayListener: &gatewayv1.Listener{Name: "http", Port: 8080, Protocol: gatewayv1.HTTPProtocolType},
		OwnerKind:          kindGateway,
	}
	_, err = gwListener.conditionsInOwnVocabulary(conditions.VerdictSeveralConflicts, "both")
	assert.Error(t, err, "the Gateway vocabulary has no reason for a listener in two conflicts")

	// And the fallback for that programming error must not silently accept it.
	fallback := gwListener.conditionsFor(conditions.VerdictSeveralConflicts, "both")
	accepted := conditionOf(t, fallback, string(gatewayv1.ListenerConditionAccepted))
	assert.Equal(t, metav1.ConditionFalse, accepted.Status)
	assert.Equal(t, string(gatewayv1.ListenerReasonInvalid), accepted.Reason)
}
