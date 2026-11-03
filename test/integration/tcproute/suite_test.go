package tcproute

import (
	"context"
	"testing"
	"time"

	rc "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/routes"
	"github.com/haproxytech/haproxy-unified-gateway/test/integration/base"
	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"
	"github.com/stretchr/testify/suite"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const (
	timeout  = time.Second * 30
	interval = time.Second * 1
)

type TCPRouteSuite struct {
	base.BaseSuite
}

func TestTCPRouteSuite(t *testing.T) {
	suite.Run(t, new(TCPRouteSuite))
}

func (s *TCPRouteSuite) SetupSuite() {
	s.BaseSuite.SetupSuite("", 0)
}

func (s *TCPRouteSuite) TearDownSuite() {
	s.BaseSuite.TearDownSuite()
}

func (s *TCPRouteSuite) expectConditionsRouteUpdated(ctx context.Context, namespace, name string, expectedConditions rc.RouteConditions) {
	route := &gatewayv1.TCPRoute{}
	var gotConditions rc.RouteConditions
	if !utils.WaitFor(ctx, interval, timeout, func() bool {
		if err := s.IntTest().Client.Get(
			s.IntTest().Ctx,
			types.NamespacedName{Name: name, Namespace: namespace}, route,
		); err != nil {
			return false
		}

		gotConditions = rc.NewRouteConditionsFromV1RouteConditions(route.Status.Parents, base.TestControllerName)

		return gotConditions.Equal(expectedConditions)
	}) {
		s.T().Fatalf("conditions not correct,\nGot %+v\nExpected %+v\n", gotConditions, expectedConditions)
	}
}
