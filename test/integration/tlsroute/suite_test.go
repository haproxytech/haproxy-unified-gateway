package tlsroute

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
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

const (
	timeout  = time.Second * 30
	interval = time.Second * 1
)

type TLSRouteSuite struct {
	base.BaseSuite
}

func TestTLSRouteSuite(t *testing.T) {
	suite.Run(t, new(TLSRouteSuite))
}

func (s *TLSRouteSuite) SetupSuite() {
	s.BaseSuite.SetupSuite("", 0)
}

func (s *TLSRouteSuite) TearDownSuite() {
	s.BaseSuite.TearDownSuite()
}

func (s *TLSRouteSuite) expectConditionsRouteUpdated(ctx context.Context, namespace, name string, expectedConditions rc.RouteConditions) {
	route := &gatewayv1alpha2.TLSRoute{}
	var gotConditions rc.RouteConditions
	if !utils.WaitFor(ctx, interval, timeout, func() bool {
		if err := s.Test().Client.Get(
			s.Test().Ctx,
			types.NamespacedName{Name: name, Namespace: namespace}, route,
		); err != nil {
			return false
		}

		// Convert v1alpha2 parents to v1 parents for compatibility with rc.NewRouteConditionsFromV1RouteConditions
		v1Parents := make([]gatewayv1.RouteParentStatus, len(route.Status.Parents))
		for i, p := range route.Status.Parents {
			var group *gatewayv1.Group
			if p.ParentRef.Group != nil {
				g := gatewayv1.Group(*p.ParentRef.Group)
				group = &g
			}
			var kind *gatewayv1.Kind
			if p.ParentRef.Kind != nil {
				k := gatewayv1.Kind(*p.ParentRef.Kind)
				kind = &k
			}
			var ns *gatewayv1.Namespace
			if p.ParentRef.Namespace != nil {
				n := gatewayv1.Namespace(*p.ParentRef.Namespace)
				ns = &n
			}
			var sectionName *gatewayv1.SectionName
			if p.ParentRef.SectionName != nil {
				sec := gatewayv1.SectionName(*p.ParentRef.SectionName)
				sectionName = &sec
			}
			var port *gatewayv1.PortNumber
			if p.ParentRef.Port != nil {
				pt := gatewayv1.PortNumber(*p.ParentRef.Port)
				port = &pt
			}

			v1Parents[i] = gatewayv1.RouteParentStatus{
				ParentRef: gatewayv1.ParentReference{
					Group:       group,
					Kind:        kind,
					Namespace:   ns,
					Name:        gatewayv1.ObjectName(p.ParentRef.Name),
					SectionName: sectionName,
					Port:        port,
				},
				ControllerName: gatewayv1.GatewayController(p.ControllerName),
				Conditions:     p.Conditions,
			}
		}

		gotConditions = rc.NewRouteConditionsFromV1RouteConditions(v1Parents, base.TestControllerName)

		res := gotConditions.Equal(expectedConditions)

		return res
	}) {
		s.T().Fatalf("conditions not correct,\nGot %+v\nExpected %+v\n", gotConditions, expectedConditions)
	}
}

func (s *TLSRouteSuite) expectAttachedRoute(ctx context.Context, namespace, gwName, listenerName string, expectNbAttachedRoutes int32) {
	gw := &gatewayv1.Gateway{}
	if !utils.WaitFor(ctx, interval, timeout, func() bool {
		if err := s.Test().Client.Get(
			s.Test().Ctx,
			types.NamespacedName{Name: gwName, Namespace: namespace}, gw,
		); err != nil {
			return false
		}

		for _, listenerStatus := range gw.Status.Listeners {
			if string(listenerStatus.Name) == listenerName {
				return listenerStatus.AttachedRoutes == expectNbAttachedRoutes
			}
		}

		return false
	}) {
		s.T().Fatal("AttachedRoutes not correct")
	}
}
