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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

// upserted builds a tree ReferenceGrant in StatusUpserted state.
func upserted(k8s *gatewayv1beta1.ReferenceGrant) ReferenceGrant {
	return ReferenceGrant{
		K8sResource: k8s,
		TreeStatus:  TreeUpdate[ReferenceGrant]{Status: store.StatusUpserted},
	}
}

// deleted builds a tree ReferenceGrant in StatusDeleted state, with the
// pre-deletion snapshot in OldTreeResource (as SetAsDeleted would produce).
func deleted(k8s *gatewayv1beta1.ReferenceGrant) ReferenceGrant {
	return ReferenceGrant{
		K8sResource: nil,
		TreeStatus: TreeUpdate[ReferenceGrant]{
			Status:          store.StatusDeleted,
			OldTreeResource: &ReferenceGrant{K8sResource: k8s},
		},
	}
}

// TestReferenceGrantManager_WildcardTo verifies that a grant with an empty To.Name
// (wildcard) allows access for any resource name in the target namespace.
func TestReferenceGrantManager_WildcardTo(t *testing.T) {
	mgr := NewReferenceGrantManager()

	k8s := &gatewayv1beta1.ReferenceGrant{
		ObjectMeta: metav1.ObjectMeta{Namespace: "backend-ns", Name: "grant-wildcard"},
		Spec: gatewayv1beta1.ReferenceGrantSpec{
			From: []gatewayv1beta1.ReferenceGrantFrom{{
				Group:     gatewayv1.GroupName,
				Kind:      "HTTPRoute",
				Namespace: "route-ns",
			}},
			To: []gatewayv1beta1.ReferenceGrantTo{{
				Group: "",
				Kind:  "Service",
				Name:  nil, // wildcard — any Service name
			}},
		},
	}

	mgr.UpsertReferenceGrant(upserted(k8s))
	mgr.ComputeToFrom()

	assert.True(t, mgr.IsAccessGranted(gatewayv1.GroupName, "HTTPRoute", "route-ns", "", "Service", "backend-ns", "svc-a"),
		"wildcard grant should permit any named service")
	assert.True(t, mgr.IsAccessGranted(gatewayv1.GroupName, "HTTPRoute", "route-ns", "", "Service", "backend-ns", "svc-b"),
		"wildcard grant should permit a different service name")
	assert.False(t, mgr.IsAccessGranted(gatewayv1.GroupName, "HTTPRoute", "other-ns", "", "Service", "backend-ns", "svc-a"),
		"grant should not cover a route from a different namespace")
}

// TestReferenceGrantManager_NamedTo verifies that a grant with a specific To.Name
// allows only the named resource and not other names.
func TestReferenceGrantManager_NamedTo(t *testing.T) {
	mgr := NewReferenceGrantManager()

	k8s := &gatewayv1beta1.ReferenceGrant{
		ObjectMeta: metav1.ObjectMeta{Namespace: "backend-ns", Name: "grant-named"},
		Spec: gatewayv1beta1.ReferenceGrantSpec{
			From: []gatewayv1beta1.ReferenceGrantFrom{{
				Group:     gatewayv1.GroupName,
				Kind:      "HTTPRoute",
				Namespace: "route-ns",
			}},
			To: []gatewayv1beta1.ReferenceGrantTo{{
				Group: "",
				Kind:  "Service",
				Name:  new((gatewayv1.ObjectName)("my-svc")),
			}},
		},
	}

	mgr.UpsertReferenceGrant(upserted(k8s))
	mgr.ComputeToFrom()

	assert.True(t, mgr.IsAccessGranted(gatewayv1.GroupName, "HTTPRoute", "route-ns", "", "Service", "backend-ns", "my-svc"),
		"named grant should permit the exact service")
	assert.False(t, mgr.IsAccessGranted(gatewayv1.GroupName, "HTTPRoute", "route-ns", "", "Service", "backend-ns", "other-svc"),
		"named grant must not permit a different service name")
}

// TestReferenceGrantManager_UpdateWipesPreviousFootprint verifies that upserting a
// modified grant clears the old From entries before inserting the new ones.
// This exercises the RemoveReferenceGrantWithCheck(_, false) call inside Upsert.
func TestReferenceGrantManager_UpdateWipesPreviousFootprint(t *testing.T) {
	mgr := NewReferenceGrantManager()

	k8sV1 := &gatewayv1beta1.ReferenceGrant{
		ObjectMeta: metav1.ObjectMeta{Namespace: "backend-ns", Name: "grant-update"},
		Spec: gatewayv1beta1.ReferenceGrantSpec{
			From: []gatewayv1beta1.ReferenceGrantFrom{{
				Group:     gatewayv1.GroupName,
				Kind:      "HTTPRoute",
				Namespace: "route-ns-old",
			}},
			To: []gatewayv1beta1.ReferenceGrantTo{{
				Group: "",
				Kind:  "Service",
				Name:  new((gatewayv1.ObjectName)("svc")),
			}},
		},
	}

	mgr.UpsertReferenceGrant(upserted(k8sV1))
	mgr.ComputeToFrom()
	assert.True(t, mgr.IsAccessGranted(gatewayv1.GroupName, "HTTPRoute", "route-ns-old", "", "Service", "backend-ns", "svc"))

	// Update the grant: only route-ns-new is now authorised.
	k8sV2 := &gatewayv1beta1.ReferenceGrant{
		ObjectMeta: metav1.ObjectMeta{Namespace: "backend-ns", Name: "grant-update"},
		Spec: gatewayv1beta1.ReferenceGrantSpec{
			From: []gatewayv1beta1.ReferenceGrantFrom{{
				Group:     gatewayv1.GroupName,
				Kind:      "HTTPRoute",
				Namespace: "route-ns-new",
			}},
			To: []gatewayv1beta1.ReferenceGrantTo{{
				Group: "",
				Kind:  "Service",
				Name:  new((gatewayv1.ObjectName)("svc")),
			}},
		},
	}

	mgr.UpsertReferenceGrant(upserted(k8sV2))
	mgr.ComputeToFrom()

	assert.False(t, mgr.IsAccessGranted(gatewayv1.GroupName, "HTTPRoute", "route-ns-old", "", "Service", "backend-ns", "svc"),
		"old From must be revoked after update")
	assert.True(t, mgr.IsAccessGranted(gatewayv1.GroupName, "HTTPRoute", "route-ns-new", "", "Service", "backend-ns", "svc"),
		"new From must be allowed after update")
}

// TestReferenceGrantManager_DeleteMultiToNoLeftovers verifies that deleting a grant
// that covers multiple To entries leaves no stale entries in either ToReferenceGrantFrom
// or ReferenceGrantsTo.
func TestReferenceGrantManager_DeleteMultiToNoLeftovers(t *testing.T) {
	mgr := NewReferenceGrantManager()

	k8s := &gatewayv1beta1.ReferenceGrant{
		ObjectMeta: metav1.ObjectMeta{Namespace: "backend-ns", Name: "grant-multi"},
		Spec: gatewayv1beta1.ReferenceGrantSpec{
			From: []gatewayv1beta1.ReferenceGrantFrom{{
				Group:     gatewayv1.GroupName,
				Kind:      "HTTPRoute",
				Namespace: "route-ns",
			}},
			To: []gatewayv1beta1.ReferenceGrantTo{
				{Group: "", Kind: "Service", Name: new((gatewayv1.ObjectName)("svc-a"))},
				{Group: "", Kind: "Service", Name: new((gatewayv1.ObjectName)("svc-b"))},
			},
		},
	}

	mgr.UpsertReferenceGrant(upserted(k8s))
	mgr.ComputeToFrom()

	mgr.RemoveReferenceGrant(deleted(k8s))
	mgr.ComputeToFrom()

	assert.Empty(t, mgr.ToReferenceGrantFrom, "ToReferenceGrantFrom must be empty after delete")
	assert.Empty(t, mgr.ReferenceGrantsTo, "ReferenceGrantsTo must be empty after delete")
	assert.False(t, mgr.IsAccessGranted(gatewayv1.GroupName, "HTTPRoute", "route-ns", "", "Service", "backend-ns", "svc-a"))
	assert.False(t, mgr.IsAccessGranted(gatewayv1.GroupName, "HTTPRoute", "route-ns", "", "Service", "backend-ns", "svc-b"))
}

// TestReferenceGrantManager_SameNamespaceAlwaysGranted verifies that same-namespace
// references are always allowed regardless of whether any grant exists.
func TestReferenceGrantManager_SameNamespaceAlwaysGranted(t *testing.T) {
	mgr := NewReferenceGrantManager()

	assert.True(t, mgr.IsAccessGranted(gatewayv1.GroupName, "HTTPRoute", "ns", "", "Service", "ns", "svc"),
		"same-namespace access must be permitted with no grants at all")
}

// TestReferenceGrantManager_TwoGrantsSameTo_DeleteOneKeepsOther verifies that when two
// grants cover the same To with different Froms, deleting one does not revoke the other.
func TestReferenceGrantManager_TwoGrantsSameTo_DeleteOneKeepsOther(t *testing.T) {
	mgr := NewReferenceGrantManager()

	k8sA := &gatewayv1beta1.ReferenceGrant{
		ObjectMeta: metav1.ObjectMeta{Namespace: "backend-ns", Name: "grant-a"},
		Spec: gatewayv1beta1.ReferenceGrantSpec{
			From: []gatewayv1beta1.ReferenceGrantFrom{{
				Group:     gatewayv1.GroupName,
				Kind:      "HTTPRoute",
				Namespace: "route-ns-a",
			}},
			To: []gatewayv1beta1.ReferenceGrantTo{{Group: "", Kind: "Service", Name: new((gatewayv1.ObjectName)("shared-svc"))}},
		},
	}
	k8sB := &gatewayv1beta1.ReferenceGrant{
		ObjectMeta: metav1.ObjectMeta{Namespace: "backend-ns", Name: "grant-b"},
		Spec: gatewayv1beta1.ReferenceGrantSpec{
			From: []gatewayv1beta1.ReferenceGrantFrom{{
				Group:     gatewayv1.GroupName,
				Kind:      "HTTPRoute",
				Namespace: "route-ns-b",
			}},
			To: []gatewayv1beta1.ReferenceGrantTo{{Group: "", Kind: "Service", Name: new((gatewayv1.ObjectName)("shared-svc"))}},
		},
	}

	mgr.UpsertReferenceGrant(upserted(k8sA))
	mgr.UpsertReferenceGrant(upserted(k8sB))
	mgr.ComputeToFrom()

	assert.True(t, mgr.IsAccessGranted(gatewayv1.GroupName, "HTTPRoute", "route-ns-a", "", "Service", "backend-ns", "shared-svc"))
	assert.True(t, mgr.IsAccessGranted(gatewayv1.GroupName, "HTTPRoute", "route-ns-b", "", "Service", "backend-ns", "shared-svc"))

	// Delete grant-a only.
	mgr.RemoveReferenceGrant(deleted(k8sA))
	mgr.ComputeToFrom()

	assert.False(t, mgr.IsAccessGranted(gatewayv1.GroupName, "HTTPRoute", "route-ns-a", "", "Service", "backend-ns", "shared-svc"),
		"route-ns-a must lose access after its grant is deleted")
	assert.True(t, mgr.IsAccessGranted(gatewayv1.GroupName, "HTTPRoute", "route-ns-b", "", "Service", "backend-ns", "shared-svc"),
		"route-ns-b must retain access via its own grant")
}
