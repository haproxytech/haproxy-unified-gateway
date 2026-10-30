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
	"log/slog"
	"testing"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestGatewayListenerFeedbackStatusPatcher_StatusEqual(t *testing.T) {
	logger := slog.Default()

	tests := []struct {
		// name of test
		name string

		// snapshot is a map of listener names to conditions to update.
		snapshot map[gatewayv1.SectionName]generic.Condition

		// live is the gateway to update.
		live *gatewayv1.Gateway

		// want is the expected result of StatusEqual(live)
		want bool
	}{
		{
			name: "equal conditions",
			snapshot: map[gatewayv1.SectionName]generic.Condition{
				"listener1": {
					Status:             metav1.ConditionTrue,
					Reason:             "Programmed",
					Message:            "All good",
					ObservedGeneration: 1,
				},
			},
			live: &gatewayv1.Gateway{
				Status: gatewayv1.GatewayStatus{
					Listeners: []gatewayv1.ListenerStatus{
						{
							Name: "listener1",
							Conditions: []metav1.Condition{
								{
									Type:               string(gatewayv1.ListenerConditionProgrammed),
									Status:             metav1.ConditionTrue,
									Reason:             "Programmed",
									Message:            "All good",
									ObservedGeneration: 1,
								},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "different message",
			snapshot: map[gatewayv1.SectionName]generic.Condition{
				"listener1": {
					Status:             metav1.ConditionTrue,
					Reason:             "Programmed",
					Message:            "New message",
					ObservedGeneration: 1,
				},
			},
			live: &gatewayv1.Gateway{
				Status: gatewayv1.GatewayStatus{
					Listeners: []gatewayv1.ListenerStatus{
						{
							Name: "listener1",
							Conditions: []metav1.Condition{
								{
									Type:               string(gatewayv1.ListenerConditionProgrammed),
									Status:             metav1.ConditionTrue,
									Reason:             "Programmed",
									Message:            "Old message",
									ObservedGeneration: 1,
								},
							},
						},
					},
				},
			},
			want: false, // Should be false because messages differ
		},
		{
			name: "different reason",
			snapshot: map[gatewayv1.SectionName]generic.Condition{
				"listener1": {
					Status:             metav1.ConditionTrue,
					Reason:             "NewReason",
					ObservedGeneration: 1,
				},
			},
			live: &gatewayv1.Gateway{
				Status: gatewayv1.GatewayStatus{
					Listeners: []gatewayv1.ListenerStatus{
						{
							Name: "listener1",
							Conditions: []metav1.Condition{
								{
									Type:               string(gatewayv1.ListenerConditionProgrammed),
									Status:             metav1.ConditionTrue,
									Reason:             "OldReason",
									ObservedGeneration: 1,
								},
							},
						},
					},
				},
			},
			want: false,
		},
		{
			name: "different status",
			snapshot: map[gatewayv1.SectionName]generic.Condition{
				"listener1": {
					Status:             metav1.ConditionTrue,
					Reason:             "Programmed",
					ObservedGeneration: 1,
				},
			},
			live: &gatewayv1.Gateway{
				Status: gatewayv1.GatewayStatus{
					Listeners: []gatewayv1.ListenerStatus{
						{
							Name: "listener1",
							Conditions: []metav1.Condition{
								{
									Type:               string(gatewayv1.ListenerConditionProgrammed),
									Status:             metav1.ConditionFalse,
									Reason:             "Programmed",
									ObservedGeneration: 1,
								},
							},
						},
					},
				},
			},
			want: false,
		},
		{
			name: "stale snapshot (live has higher generation)",
			snapshot: map[gatewayv1.SectionName]generic.Condition{
				"listener1": {
					Status:             metav1.ConditionTrue,
					Reason:             "Programmed",
					ObservedGeneration: 1,
				},
			},
			live: &gatewayv1.Gateway{
				Status: gatewayv1.GatewayStatus{
					Listeners: []gatewayv1.ListenerStatus{
						{
							Name: "listener1",
							Conditions: []metav1.Condition{
								{
									Type:               string(gatewayv1.ListenerConditionProgrammed),
									Status:             metav1.ConditionFalse,
									Reason:             "Error",
									ObservedGeneration: 2,
								},
							},
						},
					},
				},
			},
			want: true, // Should return true to skip update due to stale guard
		},
		{
			name: "condition missing in live",
			snapshot: map[gatewayv1.SectionName]generic.Condition{
				"listener1": {
					Status:             metav1.ConditionTrue,
					Reason:             "Programmed",
					ObservedGeneration: 1,
				},
			},
			live: &gatewayv1.Gateway{
				Status: gatewayv1.GatewayStatus{
					Listeners: []gatewayv1.ListenerStatus{
						{
							Name:       "listener1",
							Conditions: []metav1.Condition{},
						},
					},
				},
			},
			want: false,
		},
		{
			name: "listener missing in live (eg removed => stale update)",
			snapshot: map[gatewayv1.SectionName]generic.Condition{
				"listener1": {
					Status:             metav1.ConditionTrue,
					Reason:             "Programmed",
					ObservedGeneration: 1,
				},
			},
			live: &gatewayv1.Gateway{
				Status: gatewayv1.GatewayStatus{
					Listeners: []gatewayv1.ListenerStatus{
						{
							Name:       "listener2",
							Conditions: []metav1.Condition{},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "multiple listeners all equal",
			snapshot: map[gatewayv1.SectionName]generic.Condition{
				"listener1": {
					Status:             metav1.ConditionTrue,
					Reason:             "Programmed",
					Message:            "All good",
					ObservedGeneration: 1,
				},
				"listener2": {
					Status:             metav1.ConditionTrue,
					Reason:             "Programmed",
					Message:            "All good",
					ObservedGeneration: 1,
				},
			},
			live: &gatewayv1.Gateway{
				Status: gatewayv1.GatewayStatus{
					Listeners: []gatewayv1.ListenerStatus{
						{
							Name: "listener1",
							Conditions: []metav1.Condition{
								{
									Type:               string(gatewayv1.ListenerConditionProgrammed),
									Status:             metav1.ConditionTrue,
									Reason:             "Programmed",
									Message:            "All good",
									ObservedGeneration: 1,
								},
							},
						},
						{
							Name: "listener2",
							Conditions: []metav1.Condition{
								{
									Type:               string(gatewayv1.ListenerConditionProgrammed),
									Status:             metav1.ConditionTrue,
									Reason:             "Programmed",
									Message:            "All good",
									ObservedGeneration: 1,
								},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "multiple listeners one changed (listener2)",
			snapshot: map[gatewayv1.SectionName]generic.Condition{
				"listener1": {
					Status:             metav1.ConditionTrue,
					Reason:             "Programmed",
					Message:            "All good",
					ObservedGeneration: 1,
				},
				"listener2": {
					Status:             metav1.ConditionTrue,
					Reason:             "Programmed",
					Message:            "New message",
					ObservedGeneration: 1,
				},
			},
			live: &gatewayv1.Gateway{
				Status: gatewayv1.GatewayStatus{
					Listeners: []gatewayv1.ListenerStatus{
						{
							Name: "listener1",
							Conditions: []metav1.Condition{
								{
									Type:               string(gatewayv1.ListenerConditionProgrammed),
									Status:             metav1.ConditionTrue,
									Reason:             "Programmed",
									Message:            "All good",
									ObservedGeneration: 1,
								},
							},
						},
						{
							Name: "listener2",
							Conditions: []metav1.Condition{
								{
									Type:               string(gatewayv1.ListenerConditionProgrammed),
									Status:             metav1.ConditionTrue,
									Reason:             "Programmed",
									Message:            "Old message",
									ObservedGeneration: 1,
								},
							},
						},
					},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sp := &gatewayListenerFeedbackStatusPatcher{
				listenerProgrammedConditions: tt.snapshot,
				logger:                       logger,
			}
			got, err := sp.StatusEqual(tt.live)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// An absent field and a zero count are two different claims: the first says no
// controller published a count, the second that none is attached. Reading them as
// equal would make a Gateway that has never been given one skip its first write and
// never receive the field.
func TestAttachedListenerSetsEqual(t *testing.T) {
	tests := []struct {
		name      string
		published *int32
		computed  int32
		want      bool
	}{
		{name: "absent published, none computed", want: false},
		{name: "absent published, some computed", computed: 3, want: false},
		{name: "zero published, none computed", published: new(int32), want: true},
		{name: "zero published, some computed", published: new(int32), computed: 1, want: false},
		{name: "same count", published: new(int32(2)), computed: 2, want: true},
		{name: "count went down", published: new(int32(2)), computed: 1, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, attachedListenerSetsEqual(tt.published, tt.computed))
		})
	}
}

//go:fix inline
func pointerTo[T any](v T) *T { return new(v) }

// Conditions another controller wrote on the Gateway or a listener survive our
// status writes untouched, and their presence alone does not make every cycle
// rewrite the status (v1.7 conformance: GatewayPreserveForeignConditions).
func TestGatewayStatusPatcher_PreservesForeignConditions(t *testing.T) {
	programmedCondition := func(message string, generation int64) generic.Condition {
		return generic.Condition{
			Type:               generic.ConditionType(gatewayv1.ListenerConditionProgrammed),
			Status:             metav1.ConditionTrue,
			Reason:             "Programmed",
			Message:            message,
			ObservedGeneration: generation,
		}
	}
	foreignCondition := func(generation int64) metav1.Condition {
		return metav1.Condition{
			Type:               "conformance.gateway.networking.k8s.io/StaleCondition",
			Status:             metav1.ConditionTrue,
			Reason:             "Seeded",
			Message:            "another controller's claim",
			ObservedGeneration: generation,
			LastTransitionTime: metav1.Now(),
		}
	}
	newTreeGateway := func(message string, generation int64) *tree.Gateway {
		gw := tree.NewGateway(&gatewayv1.Gateway{Namespace: "infra", Name: "gw"})
		gw.Valid = true
		gw.Listeners = []*tree.Listener{{
			K8sGatewayListener: &gatewayv1.Listener{Name: "http", Port: 80, Protocol: gatewayv1.HTTPProtocolType},
			Conditions: generic.Conditions{
				generic.ConditionType(gatewayv1.ListenerConditionProgrammed): programmedCondition(message, generation),
			},
		}}
		return gw
	}

	gw := newTreeGateway("first write", 1)
	sp := newGatewayStatusPatcher(gw, nil, testExtractGVK, slog.Default())

	live := &gatewayv1.Gateway{}
	upToDate, err := sp.StatusEqual(live)
	require.NoError(t, err)
	require.False(t, upToDate, "nothing published yet")
	require.NoError(t, sp.SetStatus(live))

	// Another controller seeds a condition on the Gateway and on the listener,
	// and the CRD seeds a Programmed placeholder for a type we own.
	seededGw := foreignCondition(live.Generation)
	live.Status.Conditions = append(live.Status.Conditions, seededGw, crdPlaceholder(string(gatewayv1.GatewayConditionProgrammed)))
	live.Status.Listeners[0].Conditions = append(live.Status.Listeners[0].Conditions, seededGw)

	upToDate, err = sp.StatusEqual(live)
	require.NoError(t, err)
	require.False(t, upToDate, "a placeholder for an owned type is ours to replace")

	// Our own state moves; the write keeps the foreign condition, drops the
	// placeholder for the owned type we do not compute, and updates ours.
	gw = newTreeGateway("second write", 2)
	sp = newGatewayStatusPatcher(gw, nil, testExtractGVK, slog.Default())
	upToDate, err = sp.StatusEqual(live)
	require.NoError(t, err)
	require.False(t, upToDate, "our listener condition changed")
	require.NoError(t, sp.SetStatus(live))

	require.Equal(t, []metav1.Condition{seededGw},
		foreignConditionsIn(live.Status.Conditions), "gateway-level foreign condition")
	require.Len(t, live.Status.Listeners, 1)
	require.Equal(t, []metav1.Condition{seededGw},
		foreignConditionsIn(live.Status.Listeners[0].Conditions), "listener-level foreign condition")

	programmed := false
	for _, cond := range live.Status.Listeners[0].Conditions {
		if cond.Type == string(gatewayv1.ListenerConditionProgrammed) {
			programmed = cond.Message == "second write" && cond.ObservedGeneration == 2
		}
	}
	require.True(t, programmed, "our own condition must still be updated")

	upToDate, err = sp.StatusEqual(live)
	require.NoError(t, err)
	require.True(t, upToDate, "the write settles with the foreign conditions still present")
}

func foreignConditionsIn(conditions []metav1.Condition) []metav1.Condition {
	foreign := make([]metav1.Condition, 0)
	for _, cond := range conditions {
		if cond.Type == "conformance.gateway.networking.k8s.io/StaleCondition" {
			foreign = append(foreign, cond)
		}
	}
	return foreign
}

// crdPlaceholder mirrors the default condition the Gateway CRD seeds at
// creation, for a type the controller owns but does not compute here.
func crdPlaceholder(conditionType string) metav1.Condition {
	return metav1.Condition{
		Type:               conditionType,
		Status:             metav1.ConditionUnknown,
		Reason:             "Pending",
		Message:            "Waiting for controller",
		ObservedGeneration: 0,
	}
}

// The count is read from the tree and published as a pointer on the API status. The two
// halves are checked together: a patcher that read the field but wrote nothing, or wrote
// a constant, would pass only one of them.
func TestGatewayStatusPatcher_PublishesTheAttachedListenerSetCount(t *testing.T) {
	gw := tree.NewGateway(&gatewayv1.Gateway{Namespace: "infra", Name: "gw"})
	gw.AttachedListenerSets = 2

	sp := newGatewayStatusPatcher(gw, nil, testExtractGVK, slog.Default())

	live := &gatewayv1.Gateway{}
	upToDate, err := sp.StatusEqual(live)
	require.NoError(t, err)
	require.False(t, upToDate, "a Gateway with no published count has to be written once")

	require.NoError(t, sp.SetStatus(live))
	require.NotNil(t, live.Status.AttachedListenerSets)
	require.Equal(t, int32(2), *live.Status.AttachedListenerSets)

	// The published status must stand on its own: the tree Gateway is counted up and
	// reset in place every cycle while this write runs asynchronously. A patcher that
	// held a pointer into the tree would see its published count change under it.
	gw.AttachedListenerSets = 0
	require.Equal(t, int32(2), *live.Status.AttachedListenerSets,
		"the published count must be a copy, not a pointer into the tree")

	// The patcher compares against the count it captured, so it is up to date with
	// what it just wrote even though the tree has moved on.
	upToDate, err = sp.StatusEqual(live)
	require.NoError(t, err)
	require.True(t, upToDate)
}
