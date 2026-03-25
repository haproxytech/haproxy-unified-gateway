package status

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/gateway-api/apis/v1alpha2"
)

func (s *StatusUpdaterImpl) writeTLSRouteStatus(ctx context.Context, params StatusUpdateParams[*v1alpha2.TLSRoute]) {
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
		s.config.logger.LogAttrs(context.Background(), slog.LevelDebug, // Debug is ok as we will retry with the latest k8s resource
			"Failed to update status",
			logging.LogAttrKeyGVK(params.NsName, params.extractGVK(params.Object)),
			logging.LogAttrError(err),
		)
	}
}
