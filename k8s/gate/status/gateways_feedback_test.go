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
package status

import (
	"context"
	"testing"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/diffs"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
)

// The apply feedback runs after the periodic status pass, so the ListenerSets it programmed
// have to be republished in that batch: an entry's Programmed condition lives in its
// ListenerSet's status.listeners, and the feedback is the only thing that writes it after the
// apply.
func TestPrepareFeedbackStatusUpdatePublishesOnlyTheProgrammedListenerSets(t *testing.T) {
	lsKey := types.NamespacedName{Namespace: "tenant", Name: statusTestListenerSetName}
	listenerSets := map[types.NamespacedName]*tree.ListenerSet{
		lsKey: attachedStatusTestListenerSet(t, statusTestGatewayName),
	}
	gateways := map[types.NamespacedName]*tree.Gateway{}
	updater := newStatusTestUpdater(t)

	t.Run("a ListenerSet the apply programmed is republished", func(t *testing.T) {
		result := diffs.HaproxyConfResult{
			ObservedGenerations: tree.ObservedGenerations{
				{Kind: tree.OwnerKindListenerSet, Key: lsKey}: 7,
			},
		}

		writes := updater.PrepareFeedbackStatusUpdate(context.Background(), result, gateways, listenerSets)

		require.Len(t, writes, 1, "the entry's programmation is published on its ListenerSet, and nowhere else")
	})

	t.Run("a ListenerSet the apply did not program is left to the periodic pass", func(t *testing.T) {
		result := diffs.HaproxyConfResult{
			ObservedGenerations: tree.ObservedGenerations{
				{Kind: tree.OwnerKindListenerSet, Key: types.NamespacedName{Namespace: "tenant", Name: "other"}}: 1,
			},
		}

		writes := updater.PrepareFeedbackStatusUpdate(context.Background(), result, gateways, listenerSets)

		require.Empty(t, writes, "the periodic pass published this one before the apply, and nothing moved since")
	})
}
