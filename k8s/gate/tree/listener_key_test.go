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

	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestListenerKeyOrigin(t *testing.T) {
	gw := client.ObjectKey{Namespace: "ns-gw", Name: "hug-gateway"}
	ls := client.ObjectKey{Namespace: "ns-ls", Name: "ls-app"}

	t.Run("gateway listener", func(t *testing.T) {
		key := gatewayListenerKey(gw, "https")

		assert.False(t, key.FromListenerSet())
		assert.Equal(t, gw, key.OwnerKey(), "the Gateway declares it, so it carries its status")
	})

	t.Run("listenerset listener", func(t *testing.T) {
		key := listenerSetListenerKey(gw, ls, "https")

		assert.True(t, key.FromListenerSet())
		assert.Equal(t, ls, key.OwnerKey(), "the ListenerSet declares it, so it carries its status")
		assert.Equal(t, gw, key.GatewayKey, "the parent Gateway stays reachable without a lookup")
	})

	// The synthetic Ingress Gateway has an empty namespace, so emptiness must be
	// tested on the name. A key whose Gateway has no namespace is still a Gateway
	// listener.
	t.Run("an empty gateway namespace is not a ListenerSet", func(t *testing.T) {
		key := gatewayListenerKey(client.ObjectKey{Name: "ing:gateway"}, "http")

		assert.False(t, key.FromListenerSet())
	})
}

// The encoded form is what identifies a listener in the HAProxy map and crt-list
// origins, so it is a format rather than an implementation detail: a change to it
// silently orphans the entries already written to disk.
func TestListenerKeyEncoding(t *testing.T) {
	gw := client.ObjectKey{Namespace: "ns-gw", Name: "hug-gateway"}
	ls := client.ObjectKey{Namespace: "ns-ls", Name: "ls-app"}

	// A Gateway listener must encode exactly as it did before the key became a
	// struct, so that existing origins keep resolving.
	assert.Equal(t,
		"ns-gw/hug-gateway_https",
		gatewayListenerKey(gw, "https").String(),
	)

	assert.Equal(t,
		"ns-gw/hug-gateway_ns-ls_ls-app_https",
		listenerSetListenerKey(gw, ls, "https").String(),
	)

	// The two forms are told apart by the number of '_' separators, which holds
	// only because neither an object name (RFC 1123) nor a listener name
	// (SectionName pattern) may contain one. That assumption is load-bearing: a
	// section name allowed to carry '_' would let a Gateway listener encode exactly
	// like a ListenerSet one.
	assert.Equal(t,
		listenerSetListenerKey(gw, client.ObjectKey{Namespace: "a", Name: "b"}, "c").String(),
		gatewayListenerKey(gw, "a_b_c").String(),
		"documents the assumption rather than a desirable property: this Gateway form is "+
			"rejected by the API server, and the encoding relies on that",
	)

	// What the encoding must guarantee for names the API server does accept: a
	// listener declared by the Gateway and one contributed by a ListenerSet never
	// collide, whatever they are called.
	assert.NotEqual(t,
		gatewayListenerKey(gw, "https").String(),
		listenerSetListenerKey(gw, ls, "https").String(),
	)
}

// The point of the structured key: homonymous listeners contributed by different
// resources are distinct, where the flat (Gateway, name) encoding collapsed them
// onto one entry -- merging their conflict verdicts and making the map cleanup of
// one purge the routing entries of the others.
func TestListenerKeyDistinguishesHomonyms(t *testing.T) {
	gw := client.ObjectKey{Namespace: "ns", Name: "gw"}
	lsA := client.ObjectKey{Namespace: "ns", Name: "ls-a"}
	lsB := client.ObjectKey{Namespace: "other", Name: "ls-b"}

	keys := map[listenerKey]string{
		gatewayListenerKey(gw, "http"):          "from the Gateway",
		listenerSetListenerKey(gw, lsA, "http"): "from ls-a",
		listenerSetListenerKey(gw, lsB, "http"): "from ls-b",
	}

	assert.Len(t, keys, 3, "three listeners named http on one Gateway must be three keys")

	// And an identical key is the same entry, since the struct is comparable.
	keys[gatewayListenerKey(gw, "http")] = "again"
	assert.Len(t, keys, 3)
	assert.Equal(t, "again", keys[gatewayListenerKey(gw, "http")])

	// A ListenerSet may legitimately share its parent Gateway's namespace and name,
	// names being unique only per kind. The two fields still denote different
	// objects.
	sameName := listenerSetListenerKey(gw, gw, "http")
	assert.True(t, sameName.FromListenerSet())
	assert.NotEqual(t, gatewayListenerKey(gw, "http"), sameName)
}

// DeepCopy round-trips the Listener through JSON, which silently drops unexported
// fields. A copy carrying a zero key encodes as an origin prefix that matches
// nothing, so the map cleanup computed from a previous state would purge nothing
// and stale routing entries would survive -- which is not visible in the copy
// itself, only three layers away in a map file.
func TestListenerDeepCopyPreservesKey(t *testing.T) {
	gw := client.ObjectKey{Namespace: "ns", Name: "gw"}
	listener := &Listener{
		ListenerKey: gatewayListenerKey(gw, "https"),
	}

	copied := listener.DeepCopy()

	assert.Equal(t, listener.Key(), copied.Key())
	assert.Equal(t, "ns/gw_https", copied.Key().String(), "the origin prefix must survive the copy")
}
