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
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// loggableKey is what ReferencedBy accepts as an owner identity: comparable,
// since it keys a map, and renderable, since the debug logs name the owner.
//
// Owners are not always Kubernetes objects. A Gateway listener, for instance, is
// identified by a composite key: the flat client.ObjectKey it used to be encoded
// into forced every reader to decode it back, which is exactly what this type
// parameter removes.
type loggableKey interface {
	comparable
	String() string
}

type ReferencedBy[OwnerKey loggableKey] struct {
	// Owner: Gate Key -> GVK of Owner -> Owner Key
	Owner map[client.ObjectKey]map[schema.GroupVersionKind]map[OwnerKey]struct{}
	Name  string
}

func NewReferencedBy[OwnerKey loggableKey](name string) ReferencedBy[OwnerKey] {
	return ReferencedBy[OwnerKey]{
		Owner: make(map[client.ObjectKey]map[schema.GroupVersionKind]map[OwnerKey]struct{}),
		Name:  name,
	}
}

// logAttrOwner renders the owner the way logging.LogAttrKeyGVK does, which cannot
// be reused here: it takes a concrete client.ObjectKey.
func logAttrOwner[OwnerKey loggableKey](ownerKey OwnerKey, ownerGVK schema.GroupVersionKind) slog.Attr {
	return slog.Group(
		"resource",
		logging.LogAttrGVK(ownerGVK),
		slog.String("objectKey", ownerKey.String()),
	)
}

func (r *ReferencedBy[OwnerKey]) AddReferencedByUsingKeys(logger *slog.Logger, ownedKey client.ObjectKey, ownerKey OwnerKey, ownerGVK schema.GroupVersionKind) {
	logger.LogAttrs(
		context.WithValue(context.Background(), logging.CallerAdditionalSkipKey, 2), slog.LevelDebug,
		"AddReferencedBy",
		slog.String("name", r.Name),
		slog.String("ownedKey", ownedKey.String()),
		logAttrOwner(ownerKey, ownerGVK),
	)
	if _, ok := r.Owner[ownedKey]; !ok {
		r.Owner[ownedKey] = make(map[schema.GroupVersionKind]map[OwnerKey]struct{})
	}
	if _, ok := r.Owner[ownedKey][ownerGVK]; !ok {
		r.Owner[ownedKey][ownerGVK] = make(map[OwnerKey]struct{})
	}
	r.Owner[ownedKey][ownerGVK][ownerKey] = struct{}{}
}

func (r *ReferencedBy[OwnerKey]) RemoveReferencedByUsingKeys(logger *slog.Logger, ownedKey client.ObjectKey, ownerKey OwnerKey, ownerGVK schema.GroupVersionKind) {
	logger.LogAttrs(
		context.WithValue(context.Background(), logging.CallerAdditionalSkipKey, 2), slog.LevelDebug,
		"RemoveReferencedBy",
		slog.String("name", r.Name),
		slog.String("ownedKey", ownedKey.String()),
		logAttrOwner(ownerKey, ownerGVK),
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

func (r *ReferencedBy[OwnerKey]) ReferencedBy(owned client.Object, ownerGVK schema.GroupVersionKind) map[OwnerKey]struct{} {
	ownedKey := client.ObjectKeyFromObject(owned)
	return r.ReferencedByUsingKeys(ownedKey, ownerGVK)
}

func (r *ReferencedBy[OwnerKey]) ReferencedByUsingKeys(ownedKey client.ObjectKey, ownerGVK schema.GroupVersionKind) map[OwnerKey]struct{} {
	if r.Owner[ownedKey] == nil {
		return map[OwnerKey]struct{}{}
	}
	return r.Owner[ownedKey][ownerGVK]
}

// AllReferenced returns all owned keys that are referenced by a given ownerGVK, along with the list of owner keys
// For examples, return all secrets that are referenced by a Gateway, along with the owner Gateway keys
// map[ownedKey] => map [ownerKey] => struct{}
func (r *ReferencedBy[OwnerKey]) AllReferenced(ownerGVK schema.GroupVersionKind) map[client.ObjectKey]map[OwnerKey]struct{} {
	referenced := make(map[client.ObjectKey]map[OwnerKey]struct{})
	for ownedKey, ownerByGVK := range r.Owner {
		gwOwners := ownerByGVK[ownerGVK]
		if len(gwOwners) != 0 {
			referenced[ownedKey] = gwOwners
		}
	}
	return referenced
}

// AllReferencedByAnyOf returns every owned key referenced by an owner of any of the
// given kinds, with the owner keys merged into one set per owned key.
//
// It exists because one owned object can be referenced by owners of different kinds
// and a consumer may legitimately want all of them: a Secret is referenced by a
// Gateway listener's certificateRefs and by a ListenerSet entry's, and the
// certificate builder has to write a PEM for it either way. Asking per kind and
// merging in the caller would put the union in every consumer, where forgetting one
// kind produces a listener bound to a port with no certificate -- a failure visible
// only in a TLS handshake.
//
// The kinds stay a parameter rather than becoming "all kinds": the owner GVK is also
// what a ReferenceGrant is resolved against, so a consumer that must distinguish
// them keeps AllReferenced.
func (r *ReferencedBy[OwnerKey]) AllReferencedByAnyOf(ownerGVKs ...schema.GroupVersionKind) map[client.ObjectKey]map[OwnerKey]struct{} {
	referenced := make(map[client.ObjectKey]map[OwnerKey]struct{})
	for ownedKey, ownerByGVK := range r.Owner {
		for _, ownerGVK := range ownerGVKs {
			owners := ownerByGVK[ownerGVK]
			if len(owners) == 0 {
				continue
			}
			if referenced[ownedKey] == nil {
				referenced[ownedKey] = make(map[OwnerKey]struct{}, len(owners))
			}
			for ownerKey := range owners {
				referenced[ownedKey][ownerKey] = struct{}{}
			}
		}
	}
	return referenced
}

func (r ReferencedBy[OwnerKey]) DeepCopy() ReferencedBy[OwnerKey] {
	// Deep copy of owner map
	ownerCopy := make(map[client.ObjectKey]map[schema.GroupVersionKind]map[OwnerKey]struct{}, len(r.Owner))
	for k1, v1 := range r.Owner {
		innerCopy := make(map[schema.GroupVersionKind]map[OwnerKey]struct{}, len(v1))
		for k2, v2 := range v1 {
			deepestCopy := make(map[OwnerKey]struct{}, len(v2))
			for k3 := range v2 {
				deepestCopy[k3] = struct{}{}
			}
			innerCopy[k2] = deepestCopy
		}
		ownerCopy[k1] = innerCopy
	}

	return ReferencedBy[OwnerKey]{
		Owner: ownerCopy,
		Name:  r.Name,
	}
}

func (r *ReferencedBy[OwnerKey]) CleanOwners() {
	for k := range r.Owner {
		delete(r.Owner, k)
	}
}
