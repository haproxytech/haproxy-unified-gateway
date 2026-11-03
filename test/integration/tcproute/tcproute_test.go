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
