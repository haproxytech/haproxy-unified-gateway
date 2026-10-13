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
	"errors"
	"log/slog"
	"time"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/diffs"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	objtypes "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/object-types"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// PrepareFeedbackStatusUpdate builds a PreparedStatusUpdates from the tree's
// current listener Programmed conditions. Only the resources present in
// result.ObservedGenerations are included, each published on the object that carries its
// conditions: the Gateway for its own listeners, the ListenerSet for the entries it
// contributed -- whose programmation is reported nowhere else, since the periodic pass
// runs before the apply. The conditions must already have been updated via
// GateTree.UpdateListenerProgrammedConditions before this is called.
func (s *StatusUpdaterImpl) PrepareFeedbackStatusUpdate(
	ctx context.Context,
	result diffs.HaproxyConfResult,
	gateways map[types.NamespacedName]*tree.Gateway,
	listenerSets map[types.NamespacedName]*tree.ListenerSet,
) PreparedStatusUpdates {
	updates := make(PreparedStatusUpdates, 0, len(result.ObservedGenerations))
	for owner := range result.ObservedGenerations {
		if owner.Kind != tree.OwnerKindGateway {
			continue
		}
		gwKey := owner.Key
		gw, ok := gateways[gwKey]
		if !ok {
			continue
		}
		// The synthetic ingress gateway has no API object to patch a status on.
		if gw.GatewayForIngress {
			continue
		}
		params := StatusUpdateParams[*gatewayv1.Gateway]{
			Object:        objtypes.ObjectTypeGateway,
			NsName:        gwKey,
			StatusPatcher: newGatewayListenerFeedbackStatusPatcher(gw, s.config.logger),
			Getter:        s.config.client,
			StatusUpdater: s.config.client.Status(),
			Logger:        s.config.logger,
			extractGVK:    s.config.extractGVK,
		}
		s.config.logger.LogAttrs(
			context.Background(), slog.LevelDebug,
			"Preparing feedback Programmed status update",
			logging.LogAttrKeyGVK(gwKey, s.config.extractGVK(objtypes.ObjectTypeGateway)),
		)
		updates = append(updates, func(ctx context.Context) {
			err := wait.ExponentialBackoffWithContext(
				ctx,
				wait.Backoff{
					Duration: time.Millisecond * 200,
					Factor:   2,
					Jitter:   0.5,
					Steps:    4,
					Cap:      time.Millisecond * 3000,
				},
				TryPatchStatusFunc(params),
			)
			if err != nil && !errors.Is(err, context.Canceled) {
				s.config.logger.LogAttrs(
					context.Background(), slog.LevelError,
					"Failed to patch gateway listener Programmed conditions",
					logging.LogAttrKeyGVK(gwKey, s.config.extractGVK(params.Object)),
					logging.LogAttrError(err),
				)
			}
		})
	}

	// An entry's Programmed condition is published in its ListenerSet's status.listeners, and
	// the periodic pass ran before the apply: republish the ListenerSets this apply programmed,
	// or the fresh verdict would wait for an unrelated event to be written.
	observedListenerSets := make(map[types.NamespacedName]*tree.ListenerSet)
	for owner := range result.ObservedGenerations {
		if owner.Kind != tree.OwnerKindListenerSet {
			continue
		}
		if ls, ok := listenerSets[owner.Key]; ok {
			observedListenerSets[owner.Key] = ls
		}
	}
	updates = append(updates, s.prepareListenerSetUpdates(ctx, observedListenerSets)...)

	return updates
}

func (s *StatusUpdaterImpl) writeGatewayStatus(ctx context.Context, params StatusUpdateParams[*gatewayv1.Gateway]) {
	err := wait.ExponentialBackoffWithContext(
		ctx,
		wait.Backoff{
			Duration: time.Millisecond * 200,
			Factor:   2,
			Jitter:   0.5,
			Steps:    4,
			Cap:      time.Millisecond * 3000,
		},
		TryUpdateStatusFunc(params),
	)
	if err != nil && !errors.Is(err, context.Canceled) {
		s.config.logger.LogAttrs(
			context.Background(), slog.LevelDebug, // Debug is ok as we will retry with the latest k8s resource
			"Failed to update status",
			logging.LogAttrKeyGVK(params.NsName, params.extractGVK(params.Object)),
			logging.LogAttrError(err),
		)
	}
}
