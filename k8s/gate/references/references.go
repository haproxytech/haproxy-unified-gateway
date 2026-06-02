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
	"context"
	"log/slog"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	utilsk8s "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils-k8s"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ReferencedBy struct {
	exctractGVK utilsk8s.ExtractGVK
	// Owner: Gate Key -> GVK of Owner -> Owner Key
	Owner map[client.ObjectKey]map[schema.GroupVersionKind]map[client.ObjectKey]struct{}
	Name  string
}

func NewReferencedBy(name string, exctractGVK utilsk8s.ExtractGVK) ReferencedBy {
	return ReferencedBy{
		exctractGVK: exctractGVK,
		Owner:       make(map[client.ObjectKey]map[schema.GroupVersionKind]map[client.ObjectKey]struct{}),
		Name:        name,
	}
}

func (r *ReferencedBy) AddReferencedBy(logger *slog.Logger, ownedKey client.ObjectKey, owner client.Object) {
	ownerGVK := r.exctractGVK(owner)
	ownerKey := client.ObjectKeyFromObject(owner)
	r.AddReferencedByUsingKeys(logger, ownedKey, ownerKey, ownerGVK)
}

func (r *ReferencedBy) AddReferencedByUsingKeys(logger *slog.Logger, ownedKey, ownerKey client.ObjectKey, ownerGVK schema.GroupVersionKind) {
	logger.LogAttrs(
		context.WithValue(context.Background(), logging.CallerAdditionalSkipKey, 2), slog.LevelDebug,
		"AddReferencedBy",
		slog.String("name", r.Name),
		slog.String("ownedKey", ownedKey.String()),
		logging.LogAttrKeyGVK(ownerKey, ownerGVK),
	)
	if _, ok := r.Owner[ownedKey]; !ok {
		r.Owner[ownedKey] = make(map[schema.GroupVersionKind]map[client.ObjectKey]struct{})
	}
	if _, ok := r.Owner[ownedKey][ownerGVK]; !ok {
		r.Owner[ownedKey][ownerGVK] = make(map[client.ObjectKey]struct{})
	}
	r.Owner[ownedKey][ownerGVK][ownerKey] = struct{}{}
}

func (r *ReferencedBy) RemoveReferencedBy(logger *slog.Logger, ownedKey client.ObjectKey, owner client.Object) {
	ownerGVK := r.exctractGVK(owner)
	ownerKey := client.ObjectKeyFromObject(owner)
	r.RemoveReferencedByUsingKeys(logger, ownedKey, ownerKey, ownerGVK)
}

func (r *ReferencedBy) RemoveReferencedByUsingKeys(logger *slog.Logger, ownedKey, ownerKey client.ObjectKey, ownerGVK schema.GroupVersionKind) {
	logger.LogAttrs(
		context.WithValue(context.Background(), logging.CallerAdditionalSkipKey, 2), slog.LevelDebug,
		"RemoveReferencedBy",
		slog.String("name", r.Name),
		slog.String("ownedKey", ownedKey.String()),
		logging.LogAttrKeyGVK(ownerKey, ownerGVK),
	)
	if _, ok := r.Owner[ownedKey]; !ok {
		return
	}
	if _, ok := r.Owner[ownedKey][ownerGVK]; !ok {
		return
	}
	delete(r.Owner[ownedKey][ownerGVK], ownerKey)
	if len(r.Owner[ownedKey][ownerGVK]) == 0 {
		delete(r.Owner[ownedKey], ownerGVK)
	}
	if len(r.Owner[ownedKey]) == 0 {
		delete(r.Owner, ownedKey)
	}
}

func (r *ReferencedBy) ReferencedBy(owned client.Object, ownerGVK schema.GroupVersionKind) map[client.ObjectKey]struct{} {
	ownedKey := client.ObjectKeyFromObject(owned)
	return r.ReferencedByUsingKeys(ownedKey, ownerGVK)
}

func (r *ReferencedBy) ReferencedByUsingKeys(ownedKey client.ObjectKey, ownerGVK schema.GroupVersionKind) map[client.ObjectKey]struct{} {
	if r.Owner[ownedKey] == nil {
		return map[client.ObjectKey]struct{}{}
	}
	return r.Owner[ownedKey][ownerGVK]
}

// AllReferenced returns all owned keys that are referenced by a given ownerGVK, along with the list of owner keys
// For examples, return all secrets that are referenced by a Gateway, along with the owner Gateway keys
// map[ownedKey] => map [ownerKey] => struct{}
func (r *ReferencedBy) AllReferenced(ownerGVK schema.GroupVersionKind) map[client.ObjectKey]map[client.ObjectKey]struct{} {
	referenced := make(map[client.ObjectKey]map[client.ObjectKey]struct{})
	for ownedKey, ownerByGVK := range r.Owner {
		gwOwners := ownerByGVK[ownerGVK]
		if len(gwOwners) != 0 {
			referenced[ownedKey] = gwOwners
		}
	}
	return referenced
}

func (r ReferencedBy) DeepCopy() ReferencedBy {
	// Deep copy of owner map
	ownerCopy := make(map[client.ObjectKey]map[schema.GroupVersionKind]map[client.ObjectKey]struct{}, len(r.Owner))
	for k1, v1 := range r.Owner {
		innerCopy := make(map[schema.GroupVersionKind]map[client.ObjectKey]struct{}, len(v1))
		for k2, v2 := range v1 {
			deepestCopy := make(map[client.ObjectKey]struct{}, len(v2))
			for k3 := range v2 {
				deepestCopy[k3] = struct{}{}
			}
			innerCopy[k2] = deepestCopy
		}
		ownerCopy[k1] = innerCopy
	}

	return ReferencedBy{
		exctractGVK: r.exctractGVK,
		Owner:       ownerCopy,
		Name:        r.Name,
	}
}

func (r *ReferencedBy) CleanOwners() {
	for k := range r.Owner {
		delete(r.Owner, k)
	}
}
