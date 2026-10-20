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

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// DeepCopy round-trips the ListenerSet through JSON, which silently drops unexported
// fields -- the mechanism that once lost a Listener's key and surfaced three layers away,
// as map entries nothing could purge. Comparing the whole struct catches that for every
// field, present or future, rather than only for the ones this test happens to name.
//
// TreeStatus is the deliberate exception: it is borrowed and restored around the
// serialization, and the copy is left with it empty.
func TestListenerSetDeepCopyPreservesEverythingButTreeStatus(t *testing.T) {
	group := gatewayv1.Group(gatewayv1.GroupName)
	entryConditions, err := conditions.NewListenerEntryConditions(conditions.VerdictAccepted, "")
	require.NoError(t, err)

	ls := &ListenerSet{
		K8sResource: &gatewayv1.ListenerSet{
			Namespace:         "tenant",
			Name:              "ls",
			Generation:        4,
			CreationTimestamp: metav1.NewTime(time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC)),
			Spec: gatewayv1.ListenerSetSpec{
				ParentRef: gatewayv1.ParentGatewayReference{Name: "gw"},
				Listeners: []gatewayv1.ListenerEntry{{
					Name: "http", Port: 8080, Protocol: gatewayv1.HTTPProtocolType,
				}},
			},
			Status: gatewayv1.ListenerSetStatus{
				Conditions: []metav1.Condition{{
					Type: string(gatewayv1.ListenerSetConditionAccepted), Status: metav1.ConditionTrue, Reason: "Accepted",
				}},
				Listeners: []gatewayv1.ListenerEntryStatus{{Name: "http", AttachedRoutes: 2}},
			},
		},
		Attachment: attachmentAllowed,
		Conditions: generic.Conditions{
			generic.ConditionType(gatewayv1.ListenerSetConditionAccepted): {
				Type:    generic.ConditionType(gatewayv1.ListenerSetConditionAccepted),
				Status:  metav1.ConditionTrue,
				Reason:  string(gatewayv1.ListenerSetReasonAccepted),
				Message: "ListenerSet is accepted",
			},
		},
		Entries: []ListenerEntryStatus{{
			Name:           "http",
			Conditions:     entryConditions,
			SupportedKinds: []gatewayv1.RouteGroupKind{{Group: &group, Kind: gatewayv1.Kind("HTTPRoute")}},
			AttachedRoutes: 2,
		}},
		TreeStatus: TreeUpdate[ListenerSet]{
			Status:          store.StatusUpserted,
			OldTreeResource: &ListenerSet{K8sResource: &gatewayv1.ListenerSet{Name: "previous"}},
		},
	}

	copied := ls.DeepCopy()
	require.NotNil(t, copied)

	// The K8s resource is compared apart. It is an API object, and its timestamps do not
	// survive a JSON round-trip byte for byte -- time.Time carries a location the parser
	// rebuilds -- so comparing it in the same breath would fail on a difference that is
	// not this test's subject. Everything the tree itself holds is compared together,
	// with the resource neutralised on both sides so that the comparison stays total.
	expected := *ls
	expected.TreeStatus = TreeUpdate[ListenerSet]{}
	expected.K8sResource = nil
	actual := *copied
	actual.K8sResource = nil

	assert.Equal(t, expected, actual,
		"every field the tree holds must survive the serialization, whether or not it is exported")

	// The resource is copied, not shared, and its content comes through.
	require.NotNil(t, copied.K8sResource)
	assert.NotSame(t, ls.K8sResource, copied.K8sResource)
	assert.Equal(t, ls.K8sResource.Name, copied.K8sResource.Name)
	assert.Equal(t, ls.K8sResource.Generation, copied.K8sResource.Generation)
	assert.Equal(t, ls.K8sResource.Spec, copied.K8sResource.Spec)
	assert.Equal(t, ls.K8sResource.Status.Listeners, copied.K8sResource.Status.Listeners)

	// The original is the one DeepCopy borrows TreeStatus from, so it must still have it.
	assert.Equal(t, store.StatusUpserted, ls.TreeStatus.Status)
	require.NotNil(t, ls.TreeStatus.OldTreeResource)
	assert.Equal(t, "previous", ls.TreeStatus.OldTreeResource.K8sResource.Name)
}

// A nil receiver is a legitimate input on the same lines as everywhere else in this
// package, where pointers are dereferenced behind nil guards.
func TestListenerSetDeepCopyOfNilIsNil(t *testing.T) {
	var ls *ListenerSet

	assert.Nil(t, ls.DeepCopy())
}

// The patcher skips a ListenerSet whose attachment is unreported rather than publishing
// the empty conditions the silent case leaves behind, and this predicate is the only
// thing that tells it so -- the attachment vocabulary stays private to this package.
//
// Both reported outcomes answer true, a refusal included: a Gateway that does not allow
// the attachment still owes the ListenerSet a status.
func TestListenerSetIsAttachmentReported(t *testing.T) {
	tests := []struct {
		name       string
		attachment listenerSetAttachment
		want       bool
	}{
		{name: "unreported stays silent", attachment: attachmentUnreported, want: false},
		{name: "a refusal is reported", attachment: attachmentNotAllowed, want: true},
		{name: "an attachment is reported", attachment: attachmentAllowed, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ls := &ListenerSet{Attachment: tt.attachment}
			assert.Equal(t, tt.want, ls.IsAttachmentReported())
		})
	}
}
