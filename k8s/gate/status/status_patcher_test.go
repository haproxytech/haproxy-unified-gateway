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
