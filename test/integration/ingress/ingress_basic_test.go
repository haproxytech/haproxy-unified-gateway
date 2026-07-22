// Copyright 2025 HAProxy Technologies LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ingress

import (
	"path"
	"testing"

	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"
	"github.com/stretchr/testify/suite"
)

type IngressTestSuite struct {
	IngressSuite
}

func TestIngressTestSuite(t *testing.T) {
	suite.Run(t, new(IngressTestSuite))
}

// An Ingress whose IngressClass is bound to the controller (bare
// haproxy.org/ingress-controller name, matching the integration controller
// which runs with no --ingress-class) is translated into a synthetic HTTPRoute
// attached to the synthetic ingress gateway, so the referenced Service backend
// is created.
func (s *IngressTestSuite) Test_Ingress_Accepted() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "basic", "ok")
	manifests := []string{"ingressclass.yaml", "http-echo.yaml", "ingress.yaml"}
	s.CreateFixtures(fixturePath, manifests)
	defer s.CleanupFixtures(fixturePath, manifests)

	s.expectBackendExists("hug_e2e-tests-ingress_http-echo_80__")
}

// When two Ingresses are created in the same batch, one bound to the controller
// and one bound to a foreign controller, only the eligible one is translated.
// The eligible backend acts as a synchronisation barrier: once it exists the
// batch has been processed, so the foreign Ingress backend must be absent.
func (s *IngressTestSuite) Test_Ingress_NotAccepted_ForeignController() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "basic", "foreign_controller")
	manifests := []string{
		"ingressclass-hug.yaml", "ingressclass-foreign.yaml",
		"http-echo.yaml", "http-echo-foreign.yaml",
		"ingress-hug.yaml", "ingress-foreign.yaml",
	}
	s.CreateFixtures(fixturePath, manifests)
	defer s.CleanupFixtures(fixturePath, manifests)

	// Barrier: the eligible Ingress backend is created.
	s.expectBackendExists("hug_e2e-tests-ingress_http-echo_80__")
	// The foreign-controller Ingress must not have produced a backend.
	s.ExpectBackendsDoNotExist(s.Test().Ctx, "hug_e2e-tests-ingress_http-echo-foreign_80__")
}

// An Ingress backend that references its Service port by name is resolved to the
// Service's numeric port, so the backend is created just like a numeric-port one.
func (s *IngressTestSuite) Test_Ingress_NamedServicePort() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "basic", "named_port")
	manifests := []string{"ingressclass.yaml", "http-echo.yaml", "ingress.yaml"}
	s.CreateFixtures(fixturePath, manifests)
	defer s.CleanupFixtures(fixturePath, manifests)

	// Service port "web" resolves to 80, so the backend name uses 80.
	s.expectBackendExists("hug_e2e-tests-ingress_http-echo_80__")
}

// Deleting the IngressClass an Ingress depends on must re-evaluate the Ingress
// and remove its backend, without a controller restart. Re-evaluation on an
// IngressClass change is driven by the Ingress builder consuming
// Updates.IngressClasses plus the IngressClass enqueue edge.
func (s *IngressTestSuite) Test_Ingress_BackendRemovedWhenIngressClassDeleted() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "basic", "ok")
	manifests := []string{"ingressclass.yaml", "http-echo.yaml", "ingress.yaml"}
	s.CreateFixtures(fixturePath, manifests)
	// The IngressClass is deleted mid-test; only the rest is cleaned up at the end.
	defer s.CleanupFixtures(fixturePath, []string{"http-echo.yaml", "ingress.yaml"})

	const backendName = "hug_e2e-tests-ingress_http-echo_80__"
	// Barrier: the Ingress is accepted and its backend is built.
	s.expectBackendExists(backendName)

	// Delete only the IngressClass: the Ingress becomes ineligible and its
	// backend must be removed.
	s.CleanupFixtures(fixturePath, []string{"ingressclass.yaml"})
	s.ExpectBackendsDoNotExist(s.Test().Ctx, backendName)
}

// Adding the gate.v3.haproxy.org/backend-cr annotation to an Ingress's target
// Service after the Ingress backend is already built must re-reconcile the
// Ingress and merge the referenced Backend CR into its (ingress-origin) backend.
// This exercises the Ingress controller's Service watch, which must stay
// registered alongside its other secondary watches.
func (s *IngressTestSuite) Test_Ingress_ServiceBackendCRAppliedWhenAnnotationAddedLater() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "basic", "service_backend_cr")
	manifests := []string{"ingressclass.yaml", "be-svc.yaml", "deploy-service.yaml", "ingress.yaml"}
	s.CreateFixtures(fixturePath, manifests)
	defer s.CleanupFixtures(fixturePath, manifests)

	const backendName = "hug_e2e-tests-ingress_http-echo_80__"
	// Barrier: the backend exists with the default server timeout (the Service
	// has no backend-cr annotation yet, so be-svc is not merged).
	s.expectBackendExists(backendName)
	s.expectBackendServerTimeout(backendName, 50000)

	// Add the Service-level annotation: the Ingress must be re-reconciled and its
	// backend merged with be-svc (server_timeout 71000).
	s.CreateFixtures(fixturePath, []string{"service-annotated.yaml"})
	s.expectBackendServerTimeout(backendName, 71000)
}

// An Ingress whose cr-backend annotation points to a HUG (gate) Backend CR uses
// the wrong CR type: an Ingress may only reference a foreign
// ingress.v3.haproxy.org Backend. The reference must be ignored (not merged),
// but the backend must still be built with defaults — matching a Gateway API
// route with a wrong-type cr-backend, rather than being set aside.
func (s *IngressTestSuite) Test_Ingress_MistypedCRBackend_StillBuildsBackend() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "basic", "mistyped_crbackend")
	manifests := []string{"ingressclass.yaml", "be-gate.yaml", "deploy-service.yaml", "ingress.yaml"}
	s.CreateFixtures(fixturePath, manifests)
	defer s.CleanupFixtures(fixturePath, manifests)

	// The backend name carries the ExtensionRef filter hash, so match by prefix.
	be := s.expectBackendByPrefix("hug_e2e-tests-ingress_http-echo_80_")
	// The gate CR was ignored (foreign-only for Ingress), so the server timeout
	// is the default, not the CR's 71000.
	s.Require().NotNil(be.ServerTimeout)
	s.Require().Equal(int64(50000), *be.ServerTimeout,
		"the mistyped gate Backend CR must be ignored, not merged")
}

// An Ingress created before its IngressClass is ineligible; adding the matching
// IngressClass afterwards must re-evaluate the Ingress and create its backend
// without a controller restart. A second, already-eligible Ingress is used as a
// synchronisation barrier so the target Ingress is provably seen as ineligible
// before its class is added.
func (s *IngressTestSuite) Test_Ingress_BackendAppearsWhenIngressClassAddedLater() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "basic", "ingressclass_added")
	initial := []string{
		"ic-barrier.yaml", "svc-barrier.yaml", "ing-barrier.yaml",
		"svc-target.yaml", "ing-target.yaml",
	}
	s.CreateFixtures(fixturePath, initial)
	defer s.CleanupFixtures(fixturePath, append(initial, "ic-target.yaml"))

	const barrierBackend = "hug_e2e-tests-ingress_http-echo-barrier_80__"
	const targetBackend = "hug_e2e-tests-ingress_http-echo-target_80__"

	// Barrier: the eligible Ingress backend exists, so the batch (including the
	// ineligible target Ingress) has been processed.
	s.expectBackendExists(barrierBackend)
	s.ExpectBackendsDoNotExist(s.Test().Ctx, targetBackend)

	// Add the target IngressClass: the target Ingress must be re-evaluated and
	// its backend created.
	s.CreateFixtures(fixturePath, []string{"ic-target.yaml"})
	s.expectBackendExists(targetBackend)
}

// An Ingress with spec.tls must bring up the synthetic https listener: its
// TLS secret is loaded as the listener certificate, so the https frontend is
// programmed and its route maps exist. Without a certificate the https listener
// would be invalid and dropped.
func (s *IngressTestSuite) Test_Ingress_TLSBringsUpHTTPSListener() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "basic", "tls")
	manifests := []string{"ingressclass.yaml", "secret.yaml", "http-echo.yaml", "ingress.yaml"}
	s.CreateFixtures(fixturePath, manifests)
	defer s.CleanupFixtures(fixturePath, manifests)

	// Barrier: the Ingress is accepted and its backend built.
	s.expectBackendExists("hug_e2e-tests-ingress_http-echo_80__")
	// The https frontend is programmed (its maps exist) because the TLS secret
	// provides a certificate for the synthetic https listener.
	s.expectMapFileExists("hug_https_8443/path_prefix.map")
}

// An Ingress with only a default backend (no rules) must still produce a
// backend for the referenced Service, mirroring kubernetes-ingress. Before
// default-backend support this Ingress translated to nothing.
func (s *IngressTestSuite) Test_Ingress_DefaultBackendBuildsBackend() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "basic", "default_backend")
	manifests := []string{"ingressclass.yaml", "http-echo.yaml", "ingress.yaml"}
	s.CreateFixtures(fixturePath, manifests)
	defer s.CleanupFixtures(fixturePath, manifests)

	s.expectBackendExists("hug_e2e-tests-ingress_http-echo_80__")
}

// Ingress pathType must route to the matching HAProxy map: an Exact path lands
// in the exact-match map and a Prefix path in the prefix-match map, never
// swapped. Both paths target the same Service, so they share one backend and
// differ only by their routing map entry.
func (s *IngressTestSuite) Test_Ingress_PathTypeRoutesToCorrectMap() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "basic", "pathtype")
	manifests := []string{"ingressclass.yaml", "http-echo.yaml", "ingress.yaml"}
	s.CreateFixtures(fixturePath, manifests)
	defer s.CleanupFixtures(fixturePath, manifests)

	// Barrier: the Ingress is accepted and its backend built.
	s.expectBackendExists("hug_e2e-tests-ingress_http-echo_80__")

	const exactMap = "hug_http_8080/path_exact.map"
	const prefixMap = "hug_http_8080/path_prefix.map"

	s.expectMapFileContainsPath(exactMap, "/exact")
	s.expectMapFileContainsPath(prefixMap, "/prefix")

	// The pathTypes must not be swapped.
	s.Require().False(s.mapFileContainsPath(prefixMap, "/exact"),
		"the Exact path must not appear in the prefix-match map")
	s.Require().False(s.mapFileContainsPath(exactMap, "/prefix"),
		"the Prefix path must not appear in the exact-match map")
}

// A managed Ingress must have its LoadBalancer status populated with the
// controller address. A HUG Service (identified by its label) of type
// ExternalName provides a deterministic hostname, which must appear in the
// Ingress status.loadBalancer.ingress.
func (s *IngressTestSuite) Test_Ingress_LoadBalancerStatusAdvertisesControllerAddress() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "basic", "status_address")
	manifests := []string{"hug-service.yaml", "ingressclass.yaml", "http-echo.yaml", "ingress.yaml"}
	s.CreateFixtures(fixturePath, manifests)
	defer s.CleanupFixtures(fixturePath, manifests)

	// Barrier: the Ingress is accepted and wired through.
	s.expectBackendExists("hug_e2e-tests-ingress_http-echo_80__")
	// Its LoadBalancer status advertises the controller address.
	s.expectIngressLBHostname("ingress-echo", "hug.example.com")
}

// An Ingress with no TLS must not produce an empty-named frontend: the synthetic
// https listener is dropped when it has no certificate, so no "hug_" maps
// directory (LinkID + "_" + empty virtual listener name) is created.
func (s *IngressTestSuite) Test_Ingress_NoEmptyNameFrontendWithoutTLS() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "basic", "ok")
	manifests := []string{"ingressclass.yaml", "http-echo.yaml", "ingress.yaml"}
	s.CreateFixtures(fixturePath, manifests)
	defer s.CleanupFixtures(fixturePath, manifests)

	// Barrier: the Ingress is wired through, so the maps have been written.
	s.expectBackendExists("hug_e2e-tests-ingress_http-echo_80__")
	s.expectMapDirAbsent("hug_")
}
