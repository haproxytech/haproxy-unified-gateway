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

package store

import (
	"bytes"
	"log/slog"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func newTestGatewayClass(nsname types.NamespacedName) *gatewayv1.GatewayClass {
	return &gatewayv1.GatewayClass{
		Name: nsname.Name,
		Spec: gatewayv1.GatewayClassSpec{
			ControllerName: gatewayv1.GatewayController("example.com/controller"),
		},
	}
}

func newTestObjectStore[T client.Object](t *testing.T) (*objectStoreImpl[T], *bytes.Buffer) {
	t.Helper()
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	return &objectStoreImpl[T]{
		objects: make(map[types.NamespacedName]T),
		updates: make(map[types.NamespacedName]Update[T]),
		logger:  logger,
		mu:      sync.Mutex{},
	}, &logBuf
}

func TestObjectStoreImpl_Upsert(t *testing.T) {
	nsName := types.NamespacedName{Name: "test-gc"}
	gc1 := newTestGatewayClass(nsName)
	gc2 := newTestGatewayClass(nsName) // Same name, different object instance
	gc2.Spec.ControllerName = "example.com/controller-v2"
	gc3 := newTestGatewayClass(nsName)
	gc3.Spec.ControllerName = "example.com/controller-v3"

	t.Run("new object", func(t *testing.T) {
		store, _ := newTestObjectStore[*gatewayv1.GatewayClass](t)
		store.upsert(gc1)

		assert.Equal(t, gc1, store.objects[nsName])
		update, ok := store.updates[nsName]
		require.True(t, ok)
		assert.Equal(t, StatusUpserted, update.Status)
		assert.Nil(t, update.OldObject, "PreviousObject should be nil for a new object")
	})

	t.Run("update existing object - first op in batch", func(t *testing.T) {
		store, _ := newTestObjectStore[*gatewayv1.GatewayClass](t)
		store.objects[nsName] = gc1 // Pre-populate

		store.upsert(gc2)

		assert.Equal(t, gc2, store.objects[nsName])
		update, ok := store.updates[nsName]
		require.True(t, ok)
		assert.Equal(t, StatusUpserted, update.Status)
		assert.Equal(t, gc1, update.OldObject, "PreviousObject should be the initial state")
	})

	t.Run("update existing object - subsequent op in batch", func(t *testing.T) {
		store, _ := newTestObjectStore[*gatewayv1.GatewayClass](t)
		store.objects[nsName] = gc1 // Pre-populate

		store.upsert(gc2) // First upsert
		update1, ok1 := store.updates[nsName]
		require.True(t, ok1)
		assert.Equal(t, gc1, update1.OldObject, "PreviousObject after first upsert")

		store.upsert(gc3) // Second upsert
		assert.Equal(t, gc3, store.objects[nsName])
		update2, ok2 := store.updates[nsName]
		require.True(t, ok2)
		assert.Equal(t, StatusUpserted, update2.Status)
		assert.Equal(t, gc1, update2.OldObject, "PreviousObject should remain from the first operation in the batch")
	})

	t.Run("wrong object type", func(t *testing.T) {
		store, logBuf := newTestObjectStore[*gatewayv1.GatewayClass](t)
		wrongTypeObj := &v1.Service{Name: "test-gc"}

		store.upsert(wrongTypeObj)

		assert.Empty(t, store.objects, "Objects map should be empty")
		assert.Empty(t, store.updates, "Updates map should be empty")

		logOutput := logBuf.String()
		assert.Contains(t, logOutput, "obj type mismatch. got *v1.Service, expected *v1.GatewayClass")
		assert.Contains(t, logOutput, "level=ERROR")
	})
}

func TestObjectStoreImpl_Delete(t *testing.T) {
	nsName := types.NamespacedName{Name: "test-gc"}
	gc1 := newTestGatewayClass(nsName)
	gc2 := newTestGatewayClass(nsName)
	gc2.Spec.ControllerName = "example.com/controller-v2"

	t.Run("delete existing object - first op in batch", func(t *testing.T) {
		store, _ := newTestObjectStore[*gatewayv1.GatewayClass](t)
		store.objects[nsName] = gc1 // Pre-populate

		store.delete(&gatewayv1.GatewayClass{}, nsName)

		_, exists := store.objects[nsName]
		assert.False(t, exists, "Object should be deleted from store")

		update, ok := store.updates[nsName]
		require.True(t, ok)
		assert.Equal(t, StatusDeleted, update.Status)
		assert.Equal(t, gc1, update.OldObject, "PreviousObject should be the state before deletion")
	})

	t.Run("delete non-existing object", func(t *testing.T) {
		store, _ := newTestObjectStore[*gatewayv1.GatewayClass](t)
		nonExistentNsName := types.NamespacedName{Name: "non-existent"}

		store.delete(&gatewayv1.GatewayClass{}, nonExistentNsName)

		assert.Empty(t, store.objects, "Objects map should remain empty")

		update, ok := store.updates[nonExistentNsName]
		require.True(t, ok)
		assert.Equal(t, StatusDeleted, update.Status)
		assert.Nil(t, update.OldObject, "PreviousObject should be nil for non-existing object")
	})

	t.Run("upsert then delete in same batch", func(t *testing.T) {
		store, _ := newTestObjectStore[*gatewayv1.GatewayClass](t)
		store.objects[nsName] = gc1 // Pre-populate initial state

		// 1. Upsert (first operation in batch for this object)
		store.upsert(gc2)
		assert.Equal(t, gc2, store.objects[nsName])
		updateUpsert, okUpsert := store.updates[nsName]
		require.True(t, okUpsert)
		assert.Equal(t, StatusUpserted, updateUpsert.Status)
		assert.Equal(t, gc1, updateUpsert.OldObject, "PreviousObject after upsert should be initial state")

		// 2. Delete (second operation in batch for this object)
		store.delete(&gatewayv1.GatewayClass{}, nsName)
		_, exists := store.objects[nsName]
		assert.False(t, exists, "Object should be deleted from store")

		updateDelete, okDelete := store.updates[nsName]
		require.True(t, okDelete)
		assert.Equal(t, StatusDeleted, updateDelete.Status)
		// PreviousObject should still be the state from before the *first* operation in the batch
		assert.Equal(t, gc1, updateDelete.OldObject, "PreviousObject after delete should remain from the first operation in the batch")
	})

	t.Run("delete then upsert in same batch", func(t *testing.T) {
		store, _ := newTestObjectStore[*gatewayv1.GatewayClass](t)
		store.objects[nsName] = gc1 // Pre-populate initial state

		// 1. Delete (first operation in batch for this object)
		store.delete(&gatewayv1.GatewayClass{}, nsName)
		_, exists := store.objects[nsName]
		assert.False(t, exists)
		updateDelete, okDelete := store.updates[nsName]
		require.True(t, okDelete)
		assert.Equal(t, StatusDeleted, updateDelete.Status)
		assert.Equal(t, gc1, updateDelete.OldObject, "PreviousObject after delete should be initial state")

		// 2. Upsert (second operation in batch for this object, re-adding)
		store.upsert(gc2)
		assert.Equal(t, gc2, store.objects[nsName])

		updateUpsert, okUpsert := store.updates[nsName]
		require.True(t, okUpsert)
		assert.Equal(t, StatusUpserted, updateUpsert.Status)
		// PreviousObject should still be the state from before the *first* operation in the batch
		assert.Equal(t, gc1, updateUpsert.OldObject, "PreviousObject after upsert should remain from the first operation in the batch")
	})
}

func TestObjectStoreImpl_ResetUpdates(t *testing.T) {
	nsName := types.NamespacedName{Name: "test-gc"}
	gc1 := newTestGatewayClass(nsName)

	store, _ := newTestObjectStore[*gatewayv1.GatewayClass](t)

	// Perform some operations to populate updates
	store.upsert(gc1)
	require.NotEmpty(t, store.updates, "Updates map should not be empty before reset")

	store.resetUpdates()
	assert.Empty(t, store.updates, "Updates map should be empty after reset")

	// Test resetting an already empty map
	store.resetUpdates()
	assert.Empty(t, store.updates, "Updates map should remain empty after resetting an empty map")
}

func TestObjectStoreImpl_Concurrency(t *testing.T) {
	store, _ := newTestObjectStore[*gatewayv1.GatewayClass](t)
	numGoroutines := 100
	numOpsPerGoroutine := 50
	var wg sync.WaitGroup

	for i := range numGoroutines {
		wg.Go(func() {
			for j := range numOpsPerGoroutine {
				idRoutine := strconv.Itoa(i)
				idOp := strconv.Itoa(j)
				nsName := types.NamespacedName{Name: "test" + "-" + idRoutine + "-" + idOp} // Unique name
				obj := newTestGatewayClass(nsName)

				if (j % 2) == 0 {
					store.upsert(obj)
				} else {
					// To make delete meaningful, upsert first then delete
					store.upsert(obj) // ensure it exists for PreviousObject tracking
					store.delete(&gatewayv1.GatewayClass{}, nsName)
				}
			}
		})
	}
	wg.Wait()

	// Basic check: ensure no panics (race detector would catch issues)
	// Expect half of operations to be upserts (even ops)
	// and half to be deletes (odd ops), so not stored
	assert.Equal(t, (numGoroutines * numOpsPerGoroutine / 2), len(store.objects), "Expected half of operations to be upserts (even ops)")
	assert.Equal(t, (numGoroutines * numOpsPerGoroutine), len(store.updates), "All operations should be recorded in updates")
	t.Logf("Store object count: %d, updates count: %d", len(store.objects), len(store.updates))

	// Reset and check
	store.resetUpdates()
	assert.Empty(t, store.updates, "Updates map should be empty after reset post-concurrency")
}
