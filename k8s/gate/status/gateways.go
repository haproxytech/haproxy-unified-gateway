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
	objtypes "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/object-types"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func (s *StatusUpdaterImpl) writeGatewayStatus(ctx context.Context, gw *tree.Gateway, addresses []gatewayv1.GatewayStatusAddress) {
	updateOptions := StatusUpdateParams[*gatewayv1.Gateway]{
		Object:        objtypes.ObjectTypeGateway,
		NsName:        types.NamespacedName{Name: gw.K8sResource.Name, Namespace: gw.K8sResource.Namespace},
		StatusPatcher: newGatewayStatusPatcher(gw, addresses),
		Getter:        s.config.client,
		StatusUpdater: s.config.client.Status(),
		Logger:        s.config.logger,
		extractGVK:    s.config.extractGVK,
	}

	err := wait.ExponentialBackoffWithContext(
		ctx,
		wait.Backoff{
			Duration: time.Millisecond * 200,
			Factor:   2,
			Jitter:   0.5,
			Steps:    4,
			Cap:      time.Millisecond * 3000,
		},
		// Function returns true if the condition is satisfied, or an error if the loop should be aborted.
		TryUpdateStatusFunc(updateOptions),
	)
	if err != nil && !errors.Is(err, context.Canceled) {
		s.config.logger.LogAttrs(context.Background(), slog.LevelError,
			"Failed to update status",
			logging.LogAttrResource(gw.K8sResource, s.config.extractGVK(gw.K8sResource)),
			logging.LogAttrError(err),
		)
	}
}
