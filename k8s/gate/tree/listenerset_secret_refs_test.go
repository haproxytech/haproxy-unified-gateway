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

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/references"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// A Secret can be referenced by a Gateway listener and by a ListenerSet entry, and
// the certificate builder has to write its PEM either way. Asking per owner kind and
// merging in every consumer is what left a contributed HTTPS entry bound to a port
// with no certificate in its crt-list.
func TestAllReferencedByAnyOf(t *testing.T) {
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	gatewayGVK := schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "Gateway"}
	listenerSetGVK := schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "ListenerSet"}

	secretKey := client.ObjectKey{Namespace: "infra", Name: "tls"}
	gwListenerKey := gatewayListenerKey(client.ObjectKey{Namespace: "infra", Name: "gw"}, "https")
	entryKey := listenerSetListenerKey(
		client.ObjectKey{Namespace: "infra", Name: "gw"},
		client.ObjectKey{Namespace: "tenant", Name: "ls"},
		"https",
	)

	index := references.NewReferencedBy[listenerKey]("secret")
	index.AddReferencedByUsingKeys(discard, secretKey, gwListenerKey, gatewayGVK)
	index.AddReferencedByUsingKeys(discard, secretKey, entryKey, listenerSetGVK)

	t.Run("one kind sees only its own owners", func(t *testing.T) {
		assert.Len(t, index.AllReferenced(gatewayGVK)[secretKey], 1)
		assert.Len(t, index.AllReferenced(listenerSetGVK)[secretKey], 1)
	})

	t.Run("both kinds merge into one owner set per secret", func(t *testing.T) {
		owners := index.AllReferencedByAnyOf(gatewayGVK, listenerSetGVK)[secretKey]

		require.Len(t, owners, 2, "the union, not the last kind asked for")
		assert.Contains(t, owners, gwListenerKey)
		assert.Contains(t, owners, entryKey)
	})

	t.Run("a kind nothing references adds nothing", func(t *testing.T) {
		unrelated := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Service"}

		owners := index.AllReferencedByAnyOf(gatewayGVK, unrelated)[secretKey]

		assert.Len(t, owners, 1)
	})

	t.Run("no kinds at all yields nothing rather than everything", func(t *testing.T) {
		assert.Empty(t, index.AllReferencedByAnyOf())
	})
}

// The Secret index is what makes a certificate reach the data plane and what a
// Secret event is resolved through, so a contributed entry missing from it serves no
// TLS -- and a refused ListenerSet present in it would have its Secret written to
// the controller's disk for nothing.
func TestAddListenerSetSecretRefs(t *testing.T) {
	from := gatewayv1.NamespacesFromAll

	newManager := func(t *testing.T, allowedFrom *gatewayv1.FromNamespaces) (*ReferenceManager, *contributionFixture) {
		t.Helper()
		f := newContributionFixture(t, allowedFrom)
		f.store.ReferencedObjects = &ReferencedObjects{
			ReferencedSecrets:         references.NewReferencedBy[listenerKey]("secret"),
			PreviousReferencedSecrets: references.NewReferencedBy[listenerKey]("secret"),
		}
		f.store.ExtractGVK = func(obj client.Object) schema.GroupVersionKind {
			switch obj.(type) {
			case *gatewayv1.ListenerSet:
				return schema.GroupVersionKind{Group: gatewayv1.GroupName, Version: "v1", Kind: "ListenerSet"}
			default:
				return schema.GroupVersionKind{Group: gatewayv1.GroupName, Version: "v1", Kind: "Gateway"}
			}
		}
		return &ReferenceManager{ControllerStore: f.store}, f
	}

	// withTLSEntry replaces the ListenerSet's entries with a single HTTPS one
	// referencing certSecretName, optionally in another namespace.
	withTLSEntry := func(f *contributionFixture, lsNamespace, lsName, certSecretName string, certNamespace *string) {
		lsKey := types.NamespacedName{Namespace: lsNamespace, Name: lsName}
		k8sLs := f.store.ClusterStore.ListenerSets[lsKey]
		mode := gatewayv1.TLSModeTerminate
		certRef := gatewayv1.SecretObjectReference{Name: gatewayv1.ObjectName(certSecretName)}
		if certNamespace != nil {
			certRef.Namespace = (*gatewayv1.Namespace)(certNamespace)
		}
		k8sLs.Spec.Listeners = []gatewayv1.ListenerEntry{{
			Name:     "https",
			Port:     443,
			Protocol: gatewayv1.HTTPSProtocolType,
			TLS: &gatewayv1.ListenerTLSConfig{
				Mode:            &mode,
				CertificateRefs: []gatewayv1.SecretObjectReference{certRef},
			},
		}}
	}

	t.Run("an allowed entry's certificateRef is indexed under the ListenerSet kind", func(t *testing.T) {
		rm, f := newManager(t, &from)
		f.addListenerSet("tenant", "ls", metav1.Now().Time, "placeholder")
		withTLSEntry(f, "tenant", "ls", "tls", nil)

		rm.addListenerSetSecretRefs()

		// The certRef has no namespace, so it resolves in the ListenerSet's own --
		// not the parent Gateway's, which GEP-1713 keeps separate.
		secretKey := client.ObjectKey{Namespace: "tenant", Name: "tls"}
		owners := rm.ReferencedObjects.ReferencedSecrets.AllReferenced(
			schema.GroupVersionKind{Group: gatewayv1.GroupName, Version: "v1", Kind: "ListenerSet"},
		)[secretKey]

		require.Len(t, owners, 1)
		expected := listenerSetListenerKey(f.gwKey, client.ObjectKey{Namespace: "tenant", Name: "ls"}, "https")
		assert.Contains(t, owners, expected,
			"the key carries both the serving Gateway and the declaring ListenerSet")
	})

	t.Run("it is not indexed under the Gateway kind", func(t *testing.T) {
		rm, f := newManager(t, &from)
		f.addListenerSet("tenant", "ls", metav1.Now().Time, "placeholder")
		withTLSEntry(f, "tenant", "ls", "tls", nil)

		rm.addListenerSetSecretRefs()

		asGateway := rm.ReferencedObjects.ReferencedSecrets.AllReferenced(
			schema.GroupVersionKind{Group: gatewayv1.GroupName, Version: "v1", Kind: "Gateway"},
		)
		assert.Empty(t, asGateway,
			"a ReferenceGrant naming a Gateway must not cover its ListenerSets, and the index is what the grant is resolved against")
	})

	t.Run("a refused ListenerSet's secret is not indexed", func(t *testing.T) {
		rm, f := newManager(t, nil) // no allowedListeners: the default is None
		f.addListenerSet("tenant", "ls", metav1.Now().Time, "placeholder")
		withTLSEntry(f, "tenant", "ls", "tls", nil)

		rm.addListenerSetSecretRefs()

		assert.Empty(t, rm.ReferencedObjects.ReferencedSecrets.Owner,
			"otherwise anyone able to create a ListenerSet gets a Secret written to the controller's disk")
	})

	t.Run("an entry without TLS indexes nothing", func(t *testing.T) {
		rm, f := newManager(t, &from)
		f.addListenerSet("tenant", "ls", metav1.Now().Time, "http")

		rm.addListenerSetSecretRefs()

		assert.Empty(t, rm.ReferencedObjects.ReferencedSecrets.Owner)
	})

	t.Run("an explicit certificateRef namespace is honoured", func(t *testing.T) {
		rm, f := newManager(t, &from)
		f.addListenerSet("tenant", "ls", metav1.Now().Time, "placeholder")
		certNamespace := "certs"
		withTLSEntry(f, "tenant", "ls", "tls", &certNamespace)

		rm.addListenerSetSecretRefs()

		assert.Contains(t, rm.ReferencedObjects.ReferencedSecrets.Owner,
			client.ObjectKey{Namespace: "certs", Name: "tls"})
	})
}

// The rebuild is gated on something having changed, and a ListenerSet counts: its
// entries carry certificateRefs, so a cycle triggered by a ListenerSet alone has an
// index to rebuild.
func TestUpdateRefencesGateCoversListenerSets(t *testing.T) {
	f := newContributionFixture(t, nil)
	f.store.ReferencedObjects = &ReferencedObjects{
		ReferencedSecrets:         references.NewReferencedBy[listenerKey]("secret"),
		PreviousReferencedSecrets: references.NewReferencedBy[listenerKey]("secret"),
	}
	f.store.ExtractGVK = func(client.Object) schema.GroupVersionKind {
		return schema.GroupVersionKind{Group: gatewayv1.GroupName, Version: "v1", Kind: "Gateway"}
	}
	rm := &ReferenceManager{ControllerStore: f.store}

	// A Gateway listener whose certificateRef is in the index from an earlier cycle.
	gwListener := gatewayListenerKey(f.gwKey, "https")
	secretKey := client.ObjectKey{Namespace: "infra", Name: "tls"}
	rm.ReferencedObjects.ReferencedSecrets.AddReferencedByUsingKeys(
		f.store.Logger, secretKey, gwListener,
		schema.GroupVersionKind{Group: gatewayv1.GroupName, Version: "v1", Kind: "Gateway"},
	)

	t.Run("no updates at all leaves the index untouched", func(t *testing.T) {
		rm.UpdateRefences()

		assert.Contains(t, rm.ReferencedObjects.ReferencedSecrets.Owner, secretKey,
			"an early return must not be mistaken for a rebuild that found nothing")
	})

	t.Run("a ListenerSet update alone triggers the rebuild", func(t *testing.T) {
		f.store.ClusterStore.Updates.ListenerSets[types.NamespacedName{Namespace: "tenant", Name: "ls"}] = store.Update[*gatewayv1.ListenerSet]{
			Status:    store.StatusUpserted,
			NewObject: mkListenerSet("tenant", "ls", mkParentRef("gw")),
		}

		rm.UpdateRefences()

		assert.NotContains(t, rm.ReferencedObjects.ReferencedSecrets.Owner, secretKey,
			"the rebuild ran: the Gateway is in the store but carries no certificateRef, so the stale entry is gone")
	})
}
