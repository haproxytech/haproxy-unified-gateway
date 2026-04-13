package tlsroute

// func (s *TLSRouteSuite) Test_TLSRoute_SSL_Passthrough() {
// 	fixtureDirPath := utils.GetCRDFixturePath()
// 	fixtureDir := "sslpassthrough"

// 	fixturePath := path.Join(fixtureDirPath, fixtureDir)
// 	s.CreateFixtures(fixturePath, nil)
// 	defer s.CleanupFixtures(fixturePath, nil)

// 	// Expected Conditions
// 	expectationsPath := path.Join(fixturePath, "expectations")
// 	expectedCondPath := path.Join(expectationsPath, "conditions-route.yaml")
// 	expectedConditions := s.YamlToRouteConditions(expectedCondPath)

// 	tlsRouteName := "tlsroute"
// 	s.expectConditionsRouteUpdated(s.Test().Ctx, s.Test().Namespace, tlsRouteName, expectedConditions)

// 	// Check AttachedRoutes on Gateway status
// 	s.expectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "tls-gateway", "tls", 1)

// 	// Check Maps
// 	mapFilePath := "hug_tls_31445"

// 	expectedMapsPath := path.Join(expectationsPath, "maps")
// 	s.ExpectMapContents(mapFilePath, expectedMapsPath)
// }
