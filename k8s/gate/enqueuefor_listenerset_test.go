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
package controller

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/index"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

func listenerSetWithCertRef(namespace, name, secretNamespace, secretName string) *gatewayv1.ListenerSet {
	secretNs := gatewayv1.Namespace(secretNamespace)
	return &gatewayv1.ListenerSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: gatewayv1.ListenerSetSpec{Listeners: []gatewayv1.ListenerEntry{{
			Name: "https", Port: 8443, Protocol: gatewayv1.HTTPSProtocolType,
			TLS: &gatewayv1.ListenerTLSConfig{CertificateRefs: []gatewayv1.SecretObjectReference{{
				Name: gatewayv1.ObjectName(secretName), Namespace: &secretNs,
			}}},
		}}},
	}
}

func listenerSetGrant(namespace, name string, from []gatewayv1beta1.ReferenceGrantFrom, to []gatewayv1beta1.ReferenceGrantTo) *gatewayv1beta1.ReferenceGrant {
	return &gatewayv1beta1.ReferenceGrant{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec:       gatewayv1beta1.ReferenceGrantSpec{From: from, To: to},
	}
}

func listenerSetFrom(namespace string) gatewayv1beta1.ReferenceGrantFrom {
	return gatewayv1beta1.ReferenceGrantFrom{
		Group: gatewayv1.GroupName, Kind: "ListenerSet", Namespace: gatewayv1.Namespace(namespace),
	}
}

func secretTo(name *gatewayv1.ObjectName) gatewayv1beta1.ReferenceGrantTo {
	return gatewayv1beta1.ReferenceGrantTo{Group: "", Kind: "Secret", Name: name}
}

// The index is what routes a Secret to the ListenerSets that name it, and the lookup has to
// find a cross-namespace ref: that is why the key carries the Secret's namespace rather
// than its bare name.
func TestEnqueueListenerSetsForSecret(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	scheme := runtime.NewScheme()
	require.NoError(t, gatewayv1.AddToScheme(scheme))

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			listenerSetWithCertRef("tenant", "ls", "certs", "shared"),
			listenerSetWithCertRef("tenant", "other", "certs", "unrelated"),
		).
		WithIndex(&gatewayv1.ListenerSet{}, index.ListenerSetCertificateRefIndexField,
			index.ListenerSetCertificateRefIndexFunc(logger)).
		Build()

	mapFunc := enqueueListenerSetsForSecret(utils.NewDedicatedNamespaces(nil))(fakeClient, nil)

	requests := mapFunc(context.Background(), &apiv1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "certs", Name: "shared"},
	})
	require.Len(t, requests, 1)
	assert.Equal(t, types.NamespacedName{Namespace: "tenant", Name: "ls"}, requests[0].NamespacedName,
		"only the ListenerSet naming the changed Secret is enqueued")

	assert.Empty(t, mapFunc(context.Background(), &apiv1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "certs", Name: "nothing-references-me"},
	}))
}

// The ReferenceGrant edge finds its candidates through the target-namespace index and then
// checks the grant's shape, so a grant that cannot permit a cross-namespace reference is
// never enqueued -- and, with the predicate, never even listed.
func TestEnqueueListenerSetsForReferenceGrant(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	scheme := runtime.NewScheme()
	require.NoError(t, gatewayv1.AddToScheme(scheme))

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			listenerSetWithCertRef("tenant", "ls", "certs", "shared"),
			listenerSetWithCertRef("tenant", "same", "tenant", "own"),
		).
		WithIndex(&gatewayv1.ListenerSet{}, index.ListenerSetCertificateRefNamespaceIndexField,
			index.ListenerSetCertificateRefNamespaceIndexFunc(logger)).
		Build()

	mapFunc := enqueueListenerSetsForReferenceGrant(utils.NewDedicatedNamespaces(nil))(fakeClient, nil)

	shared := gatewayv1.ObjectName("shared")
	grant := listenerSetGrant("certs", "grant", []gatewayv1beta1.ReferenceGrantFrom{listenerSetFrom("tenant")}, []gatewayv1beta1.ReferenceGrantTo{secretTo(&shared)})

	requests := mapFunc(context.Background(), grant)
	require.Len(t, requests, 1)
	assert.Equal(t, types.NamespacedName{Namespace: "tenant", Name: "ls"}, requests[0].NamespacedName,
		"a same-namespace reference needs no grant, so it is not enqueued")

	// A grant naming another Secret cannot permit the reference that exists.
	other := gatewayv1.ObjectName("other")
	irrelevant := listenerSetGrant("certs", "grant", []gatewayv1beta1.ReferenceGrantFrom{listenerSetFrom("tenant")}, []gatewayv1beta1.ReferenceGrantTo{secretTo(&other)})
	assert.Empty(t, mapFunc(context.Background(), irrelevant))
}

func TestReferenceGrantCouldCoverListenerSet(t *testing.T) {
	tests := []struct {
		name string
		obj  client.Object
		want bool
	}{
		{
			name: "a Secret granted to a ListenerSet can matter",
			obj:  listenerSetGrant("certs", "g", []gatewayv1beta1.ReferenceGrantFrom{listenerSetFrom("tenant")}, []gatewayv1beta1.ReferenceGrantTo{secretTo(nil)}),
			want: true,
		},
		{
			name: "a non-Secret target cannot",
			obj: listenerSetGrant("certs", "g", []gatewayv1beta1.ReferenceGrantFrom{listenerSetFrom("tenant")},
				[]gatewayv1beta1.ReferenceGrantTo{{Group: "", Kind: "Service"}}),
			want: false,
		},
		{
			name: "a From of another kind cannot",
			obj: listenerSetGrant("certs", "g",
				[]gatewayv1beta1.ReferenceGrantFrom{{Group: gatewayv1.GroupName, Kind: "Gateway", Namespace: "tenant"}},
				[]gatewayv1beta1.ReferenceGrantTo{secretTo(nil)}),
			want: false,
		},
		{name: "another object is not a grant", obj: &gatewayv1.Gateway{}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, referenceGrantCouldCoverListenerSet(tt.obj))
		})
	}
}

func TestListenerSetCoveredByReferenceGrant(t *testing.T) {
	shared := gatewayv1.ObjectName("shared")
	grant := listenerSetGrant("certs", "g", []gatewayv1beta1.ReferenceGrantFrom{listenerSetFrom("tenant")}, []gatewayv1beta1.ReferenceGrantTo{secretTo(&shared)})

	covered := listenerSetWithCertRef("tenant", "ls", "certs", "shared")
	assert.True(t, listenerSetCoveredByReferenceGrant(*covered, grant))

	anotherSecret := listenerSetWithCertRef("tenant", "ls", "certs", "other")
	assert.False(t, listenerSetCoveredByReferenceGrant(*anotherSecret, grant),
		"the grant names a Secret this ListenerSet does not reference")

	otherOwner := listenerSetWithCertRef("elsewhere", "ls", "certs", "shared")
	assert.False(t, listenerSetCoveredByReferenceGrant(*otherOwner, grant),
		"the owner of an entry is its ListenerSet, so a grant for another namespace cannot cover it")

	sameNamespace := listenerSetWithCertRef("tenant", "ls", "tenant", "shared")
	assert.False(t, listenerSetCoveredByReferenceGrant(*sameNamespace, grant),
		"a same-namespace reference needs no grant, so a grant event cannot be its cause")
}
