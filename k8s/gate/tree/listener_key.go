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
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// listenerKey identifies one listener of the merged list of a Gateway.
//
// A listener name is unique only within the resource that declares it: the
// Gateway API explicitly allows the same name on a Gateway and on its attached
// ListenerSets, and on sibling ListenerSets. Identifying a listener by
// (Gateway, name) alone therefore collapses homonyms onto one key, which would
// silently merge their conflict verdicts and, worse, make the map cleanup of one
// ListenerSet purge the routing entries of its homonyms.
//
// The key is comparable, so it is usable as a map key directly and needs no
// parsing anywhere: both derivations the rest of the tree needs are field reads.
//
// ListenerSetKey being set *is* the origin of the listener, rather than a separate
// flag that could drift. Note that a ListenerSet may legitimately carry the same
// namespace and name as its parent Gateway -- names are only unique per kind --
// so the two fields can hold equal values while denoting different objects.
type listenerKey struct {
	// GatewayKey identifies the Gateway whose merged list contains this listener. It is
	// always set, including for a listener contributed by a ListenerSet.
	//
	// It is denormalised on purpose. Deriving it from the ListenerSet's parentRef
	// would be a lookup, and that lookup fails exactly when it is needed: on
	// deletion the ListenerSet is gone from the store, while its listener's
	// frontend still has to be reprogrammed and its map entries purged.
	GatewayKey client.ObjectKey
	// ListenerSetKey identifies the resource that contributed this listener, set
	// only when it did not come from the Gateway's own spec.
	ListenerSetKey client.ObjectKey
	// ListenerName is the listener name, as declared in its own resource.
	ListenerName gatewayv1.SectionName
}

// gatewayListenerKey builds the key of a listener declared in a Gateway's spec.
func gatewayListenerKey(gateway client.ObjectKey, name gatewayv1.SectionName) listenerKey {
	return listenerKey{GatewayKey: gateway, ListenerName: name}
}

// listenerSetListenerKey builds the key of a listener contributed to a Gateway by
// a ListenerSet.
func listenerSetListenerKey(gateway, listenerSet client.ObjectKey, name gatewayv1.SectionName) listenerKey {
	return listenerKey{GatewayKey: gateway, ListenerSetKey: listenerSet, ListenerName: name}
}

// FromListenerSet reports whether the listener was contributed by a ListenerSet.
//
// The test is on the name rather than on the whole struct: an empty namespace is
// legitimate in this codebase -- the synthetic Ingress Gateway has one -- while an
// empty name never is.
func (k listenerKey) FromListenerSet() bool {
	return k.ListenerSetKey.Name != ""
}

// OwnerKey returns the resource that declares this listener, and therefore the one
// whose status carries its conditions.
//
// Do not use it to decide *how* to write that status: the two kinds have distinct
// condition vocabularies and distinct status structures, so that branch is
// legitimate and must stay visible at the point of rendering.
func (k listenerKey) OwnerKey() client.ObjectKey {
	if k.FromListenerSet() {
		return k.ListenerSetKey
	}
	return k.GatewayKey
}

// String encodes the key as a flat name, which is what identifies a listener in
// the origins of the HAProxy map and crt-list files.
//
// The encoding is one-way and never parsed back. Both derivations the tree needs
// are fields of the struct, so nothing has to split this string, which is what
// used to make the format load-bearing.
//
// A Gateway listener encodes exactly as before, so the origins already written to
// disk are unchanged. Neither an object name (RFC 1123) nor a listener name
// (SectionName) may contain '_', so the two forms cannot be confused: a Gateway
// listener has one separator, a ListenerSet listener three.
//
//	Gateway listener:     <gwNamespace>/<gwName>_<listenerName>
//	ListenerSet listener: <gwNamespace>/<gwName>_<lsNamespace>_<lsName>_<listenerName>
func (k listenerKey) String() string {
	name := k.GatewayKey.Name + "_"
	if k.FromListenerSet() {
		name += k.ListenerSetKey.Namespace + "_" + k.ListenerSetKey.Name + "_"
	}
	return client.ObjectKey{
		Namespace: k.GatewayKey.Namespace,
		Name:      name + string(k.ListenerName),
	}.String()
}
