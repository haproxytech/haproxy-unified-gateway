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
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// ObjectStoreUpdater updates the cluster state.
type ObjectStoreUpdater interface {
	upsert(obj client.Object)
	delete(obj client.Object, nsname types.NamespacedName)
	resetUpdates()
}

// to ensure that objectStoreImpl implements ObjectStoreUpdater interface
var _ ObjectStoreUpdater = &objectStoreImpl[*gatewayv1.GatewayClass]{}

// objectStoreImpl wraps maps of types.NamespacedName to Kubernetes resources
// (e.g. map[types.NamespacedName]*v1.Gateway) so that they can be used through Updater interface.
type objectStoreImpl[T client.Object] struct {
	objects map[types.NamespacedName]T
	updates map[types.NamespacedName]Update[T]
	logger  *slog.Logger
	mu      sync.Mutex
}

func newObjectStoreImpl[T client.Object](objects map[types.NamespacedName]T, updates map[types.NamespacedName]Update[T], slogger *slog.Logger) *objectStoreImpl[T] {
	return &objectStoreImpl[T]{
		objects: objects,
		updates: updates,
		logger:  slogger,
	}
}

func (m *objectStoreImpl[T]) upsert(obj client.Object) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := obj.(T)
	if !ok {
		m.logger.LogAttrs(
			context.Background(), slog.LevelError,
			fmt.Sprintf("obj type mismatch. got %T, expected %T", obj, t),
		)
		return
	}
	key := client.ObjectKeyFromObject(obj)
	previousObj := m.objects[key]
	m.objects[key] = t

	update, firstUpdateOk := m.updates[key]
	// if not ok, this means that it's the first time we receive an event on this object
	// keep the initial version of the object
	if !firstUpdateOk {
		update.OldObject = previousObj
	}
	update.Status = StatusUpserted
	update.NewObject = t
	m.updates[key] = update
}

func (m *objectStoreImpl[T]) delete(_ client.Object, nsname types.NamespacedName) {
	m.mu.Lock()
	defer m.mu.Unlock()
	previousObj := m.objects[nsname]
	delete(m.objects, nsname)
	update, firstUpdateOk := m.updates[nsname]
	if !firstUpdateOk {
		update.OldObject = previousObj
	}
	update.Status = StatusDeleted
	var zeroValue T
	update.NewObject = zeroValue // Reset the new object to zero value
	m.updates[nsname] = update
}

func (m *objectStoreImpl[T]) resetUpdates() {
	m.mu.Lock()
	defer m.mu.Unlock()
	utils.ClearMap(m.updates)
}

type storeAdapter struct {
	stores map[schema.GroupVersionKind]ObjectStoreUpdater
}
