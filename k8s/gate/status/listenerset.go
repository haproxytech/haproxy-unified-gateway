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

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	"k8s.io/apimachinery/pkg/util/wait"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// writeListenerSetStatus publishes a ListenerSet's status with the same retry
// policy as a Gateway's: a full status update, retried on conflict so that the
// retry reads the resource again rather than replaying a stale copy.
func (s *StatusUpdaterImpl) writeListenerSetStatus(ctx context.Context, params StatusUpdateParams[*gatewayv1.ListenerSet]) {
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
			"Failed to update ListenerSet status",
			logging.LogAttrKeyGVK(params.NsName, params.extractGVK(params.Object)),
			logging.LogAttrError(err),
		)
	}
}
