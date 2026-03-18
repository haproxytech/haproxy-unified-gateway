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
package references

import (
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type mockObject struct {
	client.Object
	name      string
	namespace string
	gvk       schema.GroupVersionKind
}

func (m *mockObject) GetName() string {
	return m.name
}

func (m *mockObject) GetNamespace() string {
	return m.namespace
}

func (m *mockObject) GroupVersionKind() schema.GroupVersionKind {
	return m.gvk
}

func mockExtractGVK(obj client.Object) schema.GroupVersionKind {
	if mo, ok := obj.(*mockObject); ok {
		return mo.gvk
	}
	// Fallback for actual types if needed, though mocks are preferred for isolation
	switch obj.(type) {
	case *gatewayv1.GatewayClass:
		return schema.GroupVersionKind{Group: gatewayv1.GroupVersion.Group, Version: gatewayv1.GroupVersion.Version, Kind: "GatewayClass"}
	case *gatewayv1.Gateway:
		return schema.GroupVersionKind{Group: gatewayv1.GroupVersion.Group, Version: gatewayv1.GroupVersion.Version, Kind: "Gateway"}
	}
	return schema.GroupVersionKind{}
}

func TestReferencedBy_Add(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	gvkGatewayClass := schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "GatewayClass"}
	gvkGateway := schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "Gateway"}

	owned1 := &mockObject{name: "owned-gate-1", namespace: "default", gvk: gvkGatewayClass} // A HugGate CR
	owned1Key := client.ObjectKeyFromObject(owned1)

	ownerGWC1 := &mockObject{name: "gwc-1", namespace: "", gvk: gvkGatewayClass}
	ownerGWC2 := &mockObject{name: "gwc-2", namespace: "", gvk: gvkGatewayClass}
	ownerGW1 := &mockObject{name: "gw-1", namespace: "ns1", gvk: gvkGateway}

	ownedKey1 := client.ObjectKeyFromObject(owned1)
	ownerGWCKey1 := client.ObjectKeyFromObject(ownerGWC1)
	ownerGWCKey2 := client.ObjectKeyFromObject(ownerGWC2)
	ownerGWKey1 := client.ObjectKeyFromObject(ownerGW1)

	rb := NewReferencedBy("name", mockExtractGVK)

	t.Run("initial state", func(t *testing.T) {
		assert.Empty(t, rb.Owner)
		refs := rb.ReferencedBy(owned1, gvkGatewayClass)
		assert.Empty(t, refs)
	})

	t.Run("add first reference", func(t *testing.T) {
		rb.AddReferencedBy(logger, owned1Key, ownerGWC1)
		assert.Contains(t, rb.Owner, ownedKey1)
		assert.Contains(t, rb.Owner[ownedKey1], gvkGatewayClass)
		assert.Contains(t, rb.Owner[ownedKey1][gvkGatewayClass], ownerGWCKey1)

		refs := rb.ReferencedBy(owned1, gvkGatewayClass)
		assert.Len(t, refs, 1)
		assert.Contains(t, refs, ownerGWCKey1)
	})

	t.Run("add second reference of same GVK", func(t *testing.T) {
		rb.AddReferencedBy(logger, owned1Key, ownerGWC2)
		assert.Contains(t, rb.Owner[ownedKey1][gvkGatewayClass], ownerGWCKey2)

		refs := rb.ReferencedBy(owned1, gvkGatewayClass)
		assert.Len(t, refs, 2)
		assert.Contains(t, refs, ownerGWCKey1)
		assert.Contains(t, refs, ownerGWCKey2)
	})

	t.Run("add reference of different GVK", func(t *testing.T) {
		rb.AddReferencedBy(logger, owned1Key, ownerGW1)
		assert.Contains(t, rb.Owner[ownedKey1], gvkGateway)
		assert.Contains(t, rb.Owner[ownedKey1][gvkGateway], ownerGWKey1)

		refsGWC := rb.ReferencedBy(owned1, gvkGatewayClass)
		assert.Len(t, refsGWC, 2)

		refsGW := rb.ReferencedBy(owned1, gvkGateway)
		assert.Len(t, refsGW, 1)
		assert.Contains(t, refsGW, ownerGWKey1)
	})

	t.Run("add existing reference (idempotency)", func(t *testing.T) {
		rb.AddReferencedBy(logger, owned1Key, ownerGWC1)
		refs := rb.ReferencedBy(owned1, gvkGatewayClass)
		assert.Len(t, refs, 2) // Should not add a duplicate
	})

	t.Run("referenced by non-existent owned object or GVK", func(t *testing.T) {
		nonExistentOwned := &mockObject{name: "owned-does-not-exist", gvk: gvkGatewayClass}
		assert.Empty(t, rb.ReferencedBy(nonExistentOwned, gvkGatewayClass))

		// Add a reference to re-populate
		rb.AddReferencedBy(logger, owned1Key, ownerGWC1)
		nonExistentGVK := schema.GroupVersionKind{Group: "foo", Version: "v1", Kind: "Bar"}
		assert.Empty(t, rb.ReferencedBy(owned1, nonExistentGVK))
	})
}

func TestReferencedBy_Remove(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	gvkGatewayClass := schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "GatewayClass"}
	gvkGateway := schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "Gateway"}

	owned1 := &mockObject{name: "owned-gate-1", namespace: "default", gvk: gvkGatewayClass} // A HugGate CR
	owned1Key := client.ObjectKeyFromObject(owned1)

	ownerGWC1 := &mockObject{name: "gwc-1", namespace: "", gvk: gvkGatewayClass}
	ownerGWC2 := &mockObject{name: "gwc-2", namespace: "", gvk: gvkGatewayClass}
	ownerGW1 := &mockObject{name: "gw-1", namespace: "ns1", gvk: gvkGateway}

	ownedKey1 := client.ObjectKeyFromObject(owned1)
	ownerGWCKey1 := client.ObjectKeyFromObject(ownerGWC1)
	ownerGWCKey2 := client.ObjectKeyFromObject(ownerGWC2)

	rb := NewReferencedBy("name", mockExtractGVK)

	t.Run("initial state", func(t *testing.T) {
		assert.Empty(t, rb.Owner)
		refs := rb.ReferencedBy(owned1, gvkGatewayClass)
		assert.Empty(t, refs)
	})

	rb.AddReferencedBy(logger, owned1Key, ownerGWC1)
	rb.AddReferencedBy(logger, owned1Key, ownerGWC2)
	rb.AddReferencedBy(logger, owned1Key, ownerGW1)

	t.Run("remove reference", func(t *testing.T) {
		rb.RemoveReferencedBy(logger, owned1Key, ownerGWC1)
		refs := rb.ReferencedBy(owned1, gvkGatewayClass)
		assert.Len(t, refs, 1)
		assert.NotContains(t, refs, ownerGWCKey1)
		assert.Contains(t, refs, ownerGWCKey2)
		assert.Contains(t, rb.Owner[ownedKey1], gvkGatewayClass) // GVK map should still exist
	})

	t.Run("remove last reference for a GVK", func(t *testing.T) {
		rb.RemoveReferencedBy(logger, owned1Key, ownerGWC2)
		refs := rb.ReferencedBy(owned1, gvkGatewayClass)
		assert.Empty(t, refs)
		// The map for gvkGatewayClass might or might not be deleted,
		// current implementation keeps it if other GVKs exist for the owned object.
		// Let's check that the specific key is gone.
		assert.NotContains(t, rb.Owner[ownedKey1][gvkGatewayClass], ownerGWCKey2)

		// Check that other GVKs are unaffected
		refsGW := rb.ReferencedBy(owned1, gvkGateway)
		assert.Len(t, refsGW, 1)
	})

	t.Run("remove last reference for an owned object", func(t *testing.T) {
		rb.RemoveReferencedBy(logger, owned1Key, ownerGW1)
		refs := rb.ReferencedBy(owned1, gvkGateway)
		assert.Empty(t, refs)

		// The ownedKey1 entry might or might not be deleted from rb.owner based on implementation.
		// Current implementation keeps the ownedKey entry with empty GVK maps.
		// Let's ensure no references are returned.
		assert.Empty(t, rb.ReferencedBy(owned1, gvkGatewayClass))
		assert.Empty(t, rb.ReferencedBy(owned1, gvkGateway))
	})

	t.Run("remove non-existent reference", func(t *testing.T) {
		nonExistentOwner := &mockObject{name: "does-not-exist", gvk: gvkGatewayClass}
		rb.RemoveReferencedBy(logger, owned1Key, nonExistentOwner) // Should not panic
		// State should be unchanged from previous test
		assert.Empty(t, rb.ReferencedBy(owned1, gvkGatewayClass))
		assert.Empty(t, rb.ReferencedBy(owned1, gvkGateway))
	})

	t.Run("remove from non-existent owned object", func(t *testing.T) {
		nonExistentOwned := &mockObject{name: "owned-does-not-exist", gvk: gvkGatewayClass}
		nonExistentOwnedKey := client.ObjectKeyFromObject(nonExistentOwned)
		rb.RemoveReferencedBy(logger, nonExistentOwnedKey, ownerGWC1) // Should not panic
		assert.NotContains(t, rb.Owner, client.ObjectKeyFromObject(nonExistentOwned))
	})

	t.Run("referenced by non-existent owned object or GVK", func(t *testing.T) {
		nonExistentOwned := &mockObject{name: "owned-does-not-exist", gvk: gvkGatewayClass}
		assert.Empty(t, rb.ReferencedBy(nonExistentOwned, gvkGatewayClass))

		// Add a reference to re-populate
		rb.AddReferencedBy(logger, owned1Key, ownerGWC1)
		nonExistentGVK := schema.GroupVersionKind{Group: "foo", Version: "v1", Kind: "Bar"}
		assert.Empty(t, rb.ReferencedBy(owned1, nonExistentGVK))
	})
}

func TestReferencedBy_ReferencedBy(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	gvkGatewayClass := schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "GatewayClass"}
	gvkGateway := schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "Gateway"}
	gvkOther := schema.GroupVersionKind{Group: "foo", Version: "v1", Kind: "Bar"}

	owned1 := &mockObject{name: "owned-1", namespace: "default", gvk: gvkGatewayClass}
	owned1Key := client.ObjectKeyFromObject(owned1)
	owned2 := &mockObject{name: "owned-2", namespace: "default", gvk: gvkGatewayClass}

	ownerGWC1 := &mockObject{name: "gwc-1", namespace: "", gvk: gvkGatewayClass}
	ownerGW1 := &mockObject{name: "gw-1", namespace: "ns1", gvk: gvkGateway}
	ownerGW2 := &mockObject{name: "gw-2", namespace: "ns1", gvk: gvkGateway}

	ownerGWCKey1 := client.ObjectKeyFromObject(ownerGWC1)
	ownerGWKey1 := client.ObjectKeyFromObject(ownerGW1)
	ownerGWKey2 := client.ObjectKeyFromObject(ownerGW2)

	rb := NewReferencedBy("name", mockExtractGVK)

	// Add references
	rb.AddReferencedBy(logger, owned1Key, ownerGWC1)
	rb.AddReferencedBy(logger, owned1Key, ownerGW1)
	rb.AddReferencedBy(logger, owned1Key, ownerGW2)

	t.Run("get references for existing owned object and GVK", func(t *testing.T) {
		refs := rb.ReferencedBy(owned1, gvkGateway)
		assert.Len(t, refs, 2)
		assert.Contains(t, refs, ownerGWKey1)
		assert.Contains(t, refs, ownerGWKey2)

		refsGWC := rb.ReferencedBy(owned1, gvkGatewayClass)
		assert.Len(t, refsGWC, 1)
		assert.Contains(t, refsGWC, ownerGWCKey1)
	})

	t.Run("get references for existing owned object but non-existent GVK", func(t *testing.T) {
		refs := rb.ReferencedBy(owned1, gvkOther)
		assert.Empty(t, refs)
	})

	t.Run("get references for non-existent owned object", func(t *testing.T) {
		refs := rb.ReferencedBy(owned2, gvkGateway)
		assert.Empty(t, refs)
	})

	t.Run("get references after removal", func(t *testing.T) {
		rb.RemoveReferencedBy(logger, owned1Key, ownerGW1)
		refs := rb.ReferencedBy(owned1, gvkGateway)
		assert.Len(t, refs, 1)
		assert.NotContains(t, refs, ownerGWKey1)
		assert.Contains(t, refs, ownerGWKey2)
	})
}
