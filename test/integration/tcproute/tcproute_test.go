package tcproute

import (
	"path"
	"testing"

	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"
	"github.com/stretchr/testify/suite"
)

// Adding TCPRouteBasicTestSuite to allow running the test directly
type TCPRouteBasicTestSuite struct {
	TCPRouteSuite
}

func TestTCPRouteBasicTestSuite(t *testing.T) {
	suite.Run(t, new(TCPRouteBasicTestSuite))
}

func (s *TCPRouteBasicTestSuite) Test_TCPRoute_Basic() {
	fixtureDirPath := utils.GetCRDFixturePath()

	fixturePath := path.Join(fixtureDirPath, "tcp")
	s.CreateFixtures(fixturePath, nil)
	mapFilePath := "hug_tcp_31444"
	defer s.CleanupFixturesCheckMapFiles(fixturePath, nil, []string{mapFilePath})

	// Expected Conditions
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "conditions-route.yaml")
	expectedConditions := s.YamlToRouteConditions(expectedCondPath)

	tcpRouteName := "tcproute"
	s.expectConditionsRouteUpdated(s.IntTest().Ctx, s.IntTest().Namespace, tcpRouteName, expectedConditions)

	// Check AttachedRoutes on Gateway status
	s.ExpectAttachedRoute(s.IntTest().Ctx, s.IntTest().Namespace, "tcp-gateway", "tcp", 1)

	// Check Maps
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectMapContents(mapFilePath, expectedMapsPath)
}

// Test_TCPRoute_PortClaimAcrossGateways verifies that a TCP port stays with the
// first Gateway listening on it. A second Gateway on the same port is refused
// for its conflicting listener and its route, although older, does not take the
// port.
func (s *TCPRouteBasicTestSuite) Test_TCPRoute_PortClaimAcrossGateways() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "port-claim")
	mapFile := "hug_tcp_31444/tcp_route.map"
	portKey := utils.SubstitutePortsInString("tcp_31444", s.IntTest().PortMap)
	expectPortKept := func() {
		s.ExpectMapEntryMentions(mapFile, portKey, []string{"_http-echo_443_"}, []string{"_http-echo_80_"})
	}

	// The other tenant's route is created first, so it is the older route.
	s.CreateFixtures(fixturePath, []string{"gatewayclass.yaml", "http-echo.yaml", "early-route.yaml"})
	s.CreateFixtures(fixturePath, []string{"gateway.yaml", "tcproute.yaml"})
	defer s.CleanupFixturesCheckMapFiles(fixturePath, nil, []string{"hug_tcp_31444"})
	expectPortKept()

	s.CreateFixtures(fixturePath, []string{"late-gateway.yaml"})
	s.expectAllListenersConflicting("tcp-gateway-late")
	expectPortKept()
	s.ExpectAttachedRoute(s.IntTest().Ctx, s.IntTest().Namespace, "tcp-gateway", "tcp", 1)
}
