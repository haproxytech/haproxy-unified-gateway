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
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// GoneListeners decides what a surviving frontend must purge, so the identity it compares
// on is the whole question: a listener that merely moved is not gone, and an entry of
// another ListenerSet is not the same listener even when it carries the same name.
func TestVirtualListenerGoneListeners(t *testing.T) {
	gwKey := client.ObjectKey{Namespace: "infra", Name: "gw"}
	lsA := client.ObjectKey{Namespace: "tenant", Name: "ls-a"}
	lsB := client.ObjectKey{Namespace: "tenant", Name: "ls-b"}

	own := func(name gatewayv1.SectionName) *Listener {
		return &Listener{ListenerKey: gatewayListenerKey(gwKey, name)}
	}
	contributed := func(ls client.ObjectKey, name gatewayv1.SectionName) *Listener {
		return &Listener{ListenerKey: listenerSetListenerKey(gwKey, ls, name)}
	}
	keysOf := func(listeners []*Listener) []string {
		keys := make([]string, 0, len(listeners))
		for _, l := range listeners {
			keys = append(keys, l.Key().String())
		}
		return keys
	}
	previous := func() *VirtualListener {
		return &VirtualListener{Listeners: []*Listener{own("http"), own("https"), contributed(lsA, "http")}}
	}

	t.Run("the same listeners in another order are not gone", func(t *testing.T) {
		current := &VirtualListener{Listeners: []*Listener{contributed(lsA, "http"), own("https"), own("http")}}

		assert.Empty(t, current.GoneListeners(previous()))
	})

	t.Run("a listener the frontend no longer serves is gone", func(t *testing.T) {
		current := &VirtualListener{Listeners: []*Listener{own("http")}}

		assert.Equal(t,
			[]string{own("https").Key().String(), contributed(lsA, "http").Key().String()},
			keysOf(current.GoneListeners(previous())))
	})

	t.Run("a homonym of another ListenerSet is a different listener", func(t *testing.T) {
		current := &VirtualListener{Listeners: []*Listener{own("http"), own("https"), contributed(lsB, "http")}}

		assert.Equal(t,
			[]string{contributed(lsA, "http").Key().String()},
			keysOf(current.GoneListeners(previous())),
			"the entry that is gone is the one that was really there, not the new homonym")
	})

	t.Run("without a previous frontend there is nothing to purge", func(t *testing.T) {
		current := &VirtualListener{Listeners: []*Listener{own("http")}}

		assert.Empty(t, current.GoneListeners(nil))
	})
}
