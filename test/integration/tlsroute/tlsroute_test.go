package tlsroute

import (
	"path"
	"testing"

	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"
	"github.com/stretchr/testify/suite"
)

// Adding TLSRouteSslPassthroughTestSuite to allow running the test directly
type TLSRouteSslPassthroughTestSuite struct {
	TLSRouteSuite
}

func TestTLSRouteSslPassthroughTestSuite(t *testing.T) {
	suite.Run(t, new(TLSRouteSslPassthroughTestSuite))
}

func (s *TLSRouteSslPassthroughTestSuite) Test_TLSRoute_SSL_Passthrough() {
	fixtureDirPath := utils.GetCRDFixturePath()

	fixturePath := path.Join(fixtureDirPath, "sslpassthrough")
	s.CreateFixtures(fixturePath, nil)
	mapFilePath := "hug_tls_31445"
	defer s.CleanupFixturesCheckMapFiles(fixturePath, nil, []string{mapFilePath})

	// Expected Conditions
	expectationsPath := path.Join(fixturePath, "expectations")
	expectedCondPath := path.Join(expectationsPath, "conditions-route.yaml")
	expectedConditions := s.YamlToRouteConditions(expectedCondPath)

	tlsRouteName := "tlsroute"
	s.expectConditionsRouteUpdated(s.Test().Ctx, s.Test().Namespace, tlsRouteName, expectedConditions)

	// Check AttachedRoutes on Gateway status
	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "tls-gateway", "tls", 1)

	// Check Maps
	expectedMapsPath := path.Join(expectationsPath, "maps")
	s.ExpectListenerRouteMapContents(expectedMapsPath)
	s.ExpectMapContents(mapFilePath, expectedMapsPath)
}
