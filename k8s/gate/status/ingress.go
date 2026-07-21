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
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// IngressStatus pairs an Ingress with whether this controller handles it.
// Eligibility is computed by the caller (which owns the ControllerStore) so the
// status package stays free of IngressClass resolution logic.
type IngressStatus struct {
	Ingress  *networkingv1.Ingress
	Eligible bool
}

// PrepareIngressStatusUpdate snapshots status writes that advertise the
// controller addresses on eligible Ingresses (status.loadBalancer.ingress) and
// clear that status on ineligible ones (they may have been eligible before).
// It is a no-op when Ingress status updates are disabled. The controller
// addresses are the same ones advertised on Gateway status.
func (s *StatusUpdaterImpl) PrepareIngressStatusUpdate(ctx context.Context, ingresses []IngressStatus) PreparedStatusUpdates {
	if s.config.disableIngressStatusUpdate || len(ingresses) == 0 {
		return nil
	}
	addresses := controllerAddressValues(s.fetchControllerAddresses(ctx))

	var writes PreparedStatusUpdates
	for _, item := range ingresses {
		if item.Ingress == nil {
			continue
		}
		var addrs []string
		if item.Eligible {
			addrs = addresses
		}
		params := StatusUpdateParams[*networkingv1.Ingress]{
			Object:        objtypes.ObjectTypeIngress,
			NsName:        types.NamespacedName{Namespace: item.Ingress.Namespace, Name: item.Ingress.Name},
			StatusPatcher: newIngressStatusPatcher(addrs),
			Getter:        s.config.client,
			StatusUpdater: s.config.client.Status(),
			Logger:        s.config.logger,
			extractGVK:    s.config.extractGVK,
		}
		writes = append(writes, func(ctx context.Context) {
			s.writeIngressStatus(ctx, params)
		})
	}
	return writes
}

func (s *StatusUpdaterImpl) writeIngressStatus(ctx context.Context, params StatusUpdateParams[*networkingv1.Ingress]) {
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
			context.Background(), slog.LevelDebug, // Debug is ok as we retry with the latest k8s resource
			"Failed to update Ingress status",
			logging.LogAttrKeyGVK(params.NsName, params.extractGVK(params.Object)),
			logging.LogAttrError(err),
		)
	}
}

// controllerAddressValues extracts the raw address values (IPs or hostnames)
// from the controller's GatewayStatusAddresses, dropping empties.
func controllerAddressValues(addrs []gatewayv1.GatewayStatusAddress) []string {
	var values []string
	for _, a := range addrs {
		if a.Value != "" {
			values = append(values, a.Value)
		}
	}
	return values
}
