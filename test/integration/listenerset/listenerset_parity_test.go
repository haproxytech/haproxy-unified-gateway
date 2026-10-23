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

package listenerset

import (
	"path"

	"github.com/haproxytech/haproxy-unified-gateway/test/integration/base"
	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// The parity cases: a Gateway listener and a ListenerSet entry declaring the same
// listener -- same name, same hostname, same protocol, same route kinds, same routes --
// on a port of their own. What they assert is that the two origins yield the same
// configuration, which is the property a contribution owes the listener it is merged
// into; what differs is only naming, and the assertions say exactly which naming they
// tolerate.
//
// Each protocol is covered twice. The first case has one route naming both listeners: a
// single object feeding two frontends asks the two code paths to agree on the same
// input. The second has two routes, one per side: same request written twice, which asks
// them to agree on distinct objects -- the case where attribution, and the maps keyed by
// the route, could diverge while the first case would not notice.
//
// The generated configuration is compared as the data plane sees it (the frontends and
// the per-frontend maps), not against a hand-written expectation: an expectation of one
// side would be written from the same understanding as the other, so it would assert our
// reading of the code rather than the controller's behaviour.

// Test_10_TLSRoute_Parity_From_One_Route is the TLS passthrough pair fed by a single
// TLSRoute naming both listeners.
func (s *ListenerSetSuite) Test_10_TLSRoute_Parity_From_One_Route() {
	fixtureDir := "10-tls-route-parity-one-route"
	fixturePath := path.Join(utils.GetCRDFixturePath(), fixtureDir)
	manifests := []string{"gatewayclass.yaml", "gateway.yaml", "listenerset.yaml", "tlsroute.yaml", "http-echo.yaml"}
	s.CreateFixtures(fixturePath, manifests)
	defer s.CleanupFixtures(fixturePath, manifests)

	ctx := s.Test().Ctx
	ns := s.Test().Namespace

	s.ExpectListenerSetConditions(ctx, ns, "parity-listenerset", listenerSetServable())
	s.ExpectListenerSetEntryConditions(ctx, ns, "parity-listenerset", "tls", acceptedEntry())

	// One route, two parents: each side counts it against the listener it names, so both
	// counts prove the route reached that side rather than the other.
	s.ExpectAttachedRoute(ctx, ns, "parity-gateway", "tls", 1)
	s.ExpectListenerSetEntryAttachedRoutes(ctx, ns, "parity-listenerset", "tls", 1)

	s.expectTLSRouteParity(ns)
}

// Test_11_TLSRoute_Parity_From_Two_Routes is the same pair with one TLSRoute per side:
// distinct objects, so the routes' own identities are part of what must not leak into
// the result.
func (s *ListenerSetSuite) Test_11_TLSRoute_Parity_From_Two_Routes() {
	fixtureDir := "11-tls-route-parity-two-routes"
	fixturePath := path.Join(utils.GetCRDFixturePath(), fixtureDir)
	manifests := []string{
		"gatewayclass.yaml", "gateway.yaml", "listenerset.yaml",
		"route-gateway.yaml", "route-listener-set.yaml", "http-echo.yaml",
	}
	s.CreateFixtures(fixturePath, manifests)
	defer s.CleanupFixtures(fixturePath, manifests)

	ctx := s.Test().Ctx
	ns := s.Test().Namespace

	s.ExpectListenerSetConditions(ctx, ns, "parity-listenerset", listenerSetServable())
	s.ExpectListenerSetEntryConditions(ctx, ns, "parity-listenerset", "tls", acceptedEntry())

	s.ExpectAttachedRoute(ctx, ns, "parity-gateway", "tls", 1)
	s.ExpectListenerSetEntryAttachedRoutes(ctx, ns, "parity-listenerset", "tls", 1)

	s.expectTLSRouteParity(ns, base.NamingDifference{"parity-route-gateway", "parity-route-listener-set"})
}

// Test_12_HTTPRoute_Parity_From_One_Route is the HTTP pair fed by a single HTTPRoute
// naming both listeners. It also asserts the route's own status on both parents, which is
// where the two origins would show a disagreement first.
func (s *ListenerSetSuite) Test_12_HTTPRoute_Parity_From_One_Route() {
	fixtureDir := "12-http-route-parity-one-route"
	fixturePath := path.Join(utils.GetCRDFixturePath(), fixtureDir)
	manifests := []string{"gatewayclass.yaml", "gateway.yaml", "listenerset.yaml", "httproute.yaml", "http-echo.yaml"}
	s.CreateFixtures(fixturePath, manifests)
	defer s.CleanupFixtures(fixturePath, manifests)

	ctx := s.Test().Ctx
	ns := s.Test().Namespace

	s.ExpectListenerSetConditions(ctx, ns, "parity-listenerset", listenerSetServable())
	s.ExpectListenerSetEntryConditions(ctx, ns, "parity-listenerset", "http", acceptedEntry())
	s.ExpectAttachedRoute(ctx, ns, "parity-gateway", "http", 1)
	s.ExpectListenerSetEntryAttachedRoutes(ctx, ns, "parity-listenerset", "http", 1)

	// The route is accepted on both of its parents, and named by each of them: on a
	// Gateway listener the parent is the Gateway, and on a contributed one it is the
	// ListenerSet.
	accepted := metav1.Condition{
		Type:   string(gatewayv1.RouteConditionAccepted),
		Status: metav1.ConditionTrue,
		Reason: string(gatewayv1.RouteReasonAccepted),
	}
	s.ExpectRouteParentHasCondition(ctx, ns, "parity-route", parityGatewayParentRef("http"), accepted)
	s.ExpectRouteParentHasCondition(ctx, ns, "parity-route", listenerSetParentRef("parity-listenerset", "http"), accepted)

	s.expectHTTPRouteParity(ns)
}

// Test_13_HTTPRoute_Parity_From_Two_Routes is the same pair with one HTTPRoute per side.
func (s *ListenerSetSuite) Test_13_HTTPRoute_Parity_From_Two_Routes() {
	fixtureDir := "13-http-route-parity-two-routes"
	fixturePath := path.Join(utils.GetCRDFixturePath(), fixtureDir)
	manifests := []string{
		"gatewayclass.yaml", "gateway.yaml", "listenerset.yaml",
		"route-gateway.yaml", "route-listener-set.yaml", "http-echo.yaml",
	}
	s.CreateFixtures(fixturePath, manifests)
	defer s.CleanupFixtures(fixturePath, manifests)

	ctx := s.Test().Ctx
	ns := s.Test().Namespace

	s.ExpectListenerSetConditions(ctx, ns, "parity-listenerset", listenerSetServable())
	s.ExpectListenerSetEntryConditions(ctx, ns, "parity-listenerset", "http", acceptedEntry())
	s.ExpectAttachedRoute(ctx, ns, "parity-gateway", "http", 1)
	s.ExpectListenerSetEntryAttachedRoutes(ctx, ns, "parity-listenerset", "http", 1)

	s.expectHTTPRouteParity(ns, base.NamingDifference{"parity-route-gateway", "parity-route-listener-set"})
}

// Test_14_HTTPSRoute_Parity_From_One_Route is the terminating pair -- HTTPS, one
// certificate per origin -- fed by a single HTTPRoute naming both listeners.
//
// It is the case the other three cannot be: with termination, what the two sides serve is
// not only a routing table but a certificate list, and the comparison has to hold there
// too. Each side is asserted to have loaded its own PEM before the two lists are compared,
// so a parity that held by both sides serving nothing is not a pass.
func (s *ListenerSetSuite) Test_14_HTTPSRoute_Parity_From_One_Route() {
	fixtureDir := "14-https-route-parity-one-route"
	fixturePath := path.Join(utils.GetCRDFixturePath(), fixtureDir)
	manifests := []string{
		"gatewayclass.yaml", "gateway.yaml", "listenerset.yaml", "httproute.yaml", "http-echo.yaml",
		"parity-gateway-cert-secret.yaml", "parity-listenerset-cert-secret.yaml",
	}
	s.CreateFixtures(fixturePath, manifests)
	defer s.CleanupFixtures(fixturePath, manifests)

	ctx := s.Test().Ctx
	ns := s.Test().Namespace

	s.ExpectListenerSetConditions(ctx, ns, "parity-listenerset", listenerSetServable())
	s.ExpectListenerSetEntryConditions(ctx, ns, "parity-listenerset", "https", acceptedEntry())
	s.ExpectAttachedRoute(ctx, ns, "parity-gateway", "https", 1)
	s.ExpectListenerSetEntryAttachedRoutes(ctx, ns, "parity-listenerset", "https", 1)

	accepted := metav1.Condition{
		Type:   string(gatewayv1.RouteConditionAccepted),
		Status: metav1.ConditionTrue,
		Reason: string(gatewayv1.RouteReasonAccepted),
	}
	s.ExpectRouteParentHasCondition(ctx, ns, "parity-route", parityGatewayParentRef("https"), accepted)
	s.ExpectRouteParentHasCondition(ctx, ns, "parity-route", listenerSetParentRef("parity-listenerset", "https"), accepted)

	// Both PEMs are the same material, so the subject does not tell the two sides apart: what
	// this asserts is that each side loaded the file its own reference names.
	s.ExpectCertificateLoaded(s.certificatePath(ns, "parity-gateway-cert"), "/CN=offload.haproxy")
	s.ExpectCertificateLoaded(s.certificatePath(ns, "parity-listenerset-cert"), "/CN=offload.haproxy")

	s.expectHTTPSRouteParity(ns)
}

// Test_15_HTTPSRoute_Parity_From_Two_Routes is the same terminating pair with one
// HTTPRoute per side.
func (s *ListenerSetSuite) Test_15_HTTPSRoute_Parity_From_Two_Routes() {
	fixtureDir := "15-https-route-parity-two-routes"
	fixturePath := path.Join(utils.GetCRDFixturePath(), fixtureDir)
	manifests := []string{
		"gatewayclass.yaml", "gateway.yaml", "listenerset.yaml",
		"route-gateway.yaml", "route-listener-set.yaml", "http-echo.yaml",
		"parity-gateway-cert-secret.yaml", "parity-listenerset-cert-secret.yaml",
	}
	s.CreateFixtures(fixturePath, manifests)
	defer s.CleanupFixtures(fixturePath, manifests)

	ctx := s.Test().Ctx
	ns := s.Test().Namespace

	s.ExpectListenerSetConditions(ctx, ns, "parity-listenerset", listenerSetServable())
	s.ExpectListenerSetEntryConditions(ctx, ns, "parity-listenerset", "https", acceptedEntry())
	s.ExpectAttachedRoute(ctx, ns, "parity-gateway", "https", 1)
	s.ExpectListenerSetEntryAttachedRoutes(ctx, ns, "parity-listenerset", "https", 1)

	s.ExpectCertificateLoaded(s.certificatePath(ns, "parity-gateway-cert"), "/CN=offload.haproxy")
	s.ExpectCertificateLoaded(s.certificatePath(ns, "parity-listenerset-cert"), "/CN=offload.haproxy")

	s.expectHTTPSRouteParity(ns, base.NamingDifference{"parity-route-gateway", "parity-route-listener-set"})
}

// expectTLSRouteParity compares the two passthrough frontends of a parity case and their
// maps.
func (s *ListenerSetSuite) expectTLSRouteParity(ns string, extra ...base.NamingDifference) {
	s.expectConfigurationParity(ns, "hug_tls_8088", "hug_tls_8081", extra...)
}

// expectHTTPRouteParity compares the two HTTP frontends of a parity case and their maps.
func (s *ListenerSetSuite) expectHTTPRouteParity(ns string, extra ...base.NamingDifference) {
	s.expectConfigurationParity(ns, "hug_http_8088", "hug_http_8081", extra...)
}

// expectHTTPSRouteParity compares the two terminating frontends of a parity case: their
// configuration, their maps, and the crt-list each one serves. Termination is what the
// crt-list carries, so comparing the two lists is comparing what a TLS handshake on either
// port will be offered -- which the configuration alone does not say.
//
// The two sides terminate with a Secret each, and two Secrets cannot share a name in one
// namespace: the PEM paths, which carry the secret's namespace and name, are part of the
// naming the case declares. The Secrets hold the same material, so what the comparison
// tolerates is the path and nothing else.
func (s *ListenerSetSuite) expectHTTPSRouteParity(ns string, extra ...base.NamingDifference) {
	extra = append([]base.NamingDifference{{
		s.certificatePath(ns, "parity-gateway-cert"),
		s.certificatePath(ns, "parity-listenerset-cert"),
	}}, extra...)

	s.expectConfigurationParity(ns, "hug_https_8088", "hug_https_8081", extra...)
	s.ExpectCrtListsIdenticalModuloNaming("hug_https_8088", "hug_https_8081", parityNaming(ns, extra...)...)
}

// expectConfigurationParity compares two frontends and their maps.
//
// The template ports 8088 and 8081 are the Gateway listener's and the entry's: the cases
// rely on the harness substituting them, so the frontend names and map paths are addressed
// by their template form.
func (s *ListenerSetSuite) expectConfigurationParity(ns, gatewayFrontend, entryFrontend string, extra ...base.NamingDifference) {
	naming := parityNaming(ns, extra...)
	s.ExpectFrontendsIdenticalModuloNaming(gatewayFrontend, entryFrontend, naming...)
	s.ExpectMapsIdenticalModuloNaming(gatewayFrontend, entryFrontend, naming...)
}

// parityNaming is the naming every comparison of a parity case tolerates, whatever it
// compares: the two resources declaring the listeners -- the case's whole point, so it is
// the pair the assertions require to be present on its own side -- plus whatever the caller
// adds for what legitimately differs besides them.
func parityNaming(ns string, extra ...base.NamingDifference) []base.NamingDifference {
	return append([]base.NamingDifference{
		// The resource each frontend was programmed for. It is the pair the assertions
		// require to be present on its own side: a frontend that lost its origin is a defect
		// of its own, and it must not pass by having nothing left to compare.
		{holder(ns, "parity-gateway"), holder(ns, "parity-listenerset")},
		// The identity a listener takes in the maps. A contributed listener is identified by
		// both resources, so that homonymous entries from sibling ListenerSets stay apart --
		// listenerKey.String, whose two forms are:
		//   Gateway listener:     <gwNs>/<gwName>_<listener>
		//   ListenerSet listener: <gwNs>/<gwName>_<lsNs>_<lsName>_<listener>
		// The two chains differ by naming only. The assertions apply the longest
		// replacement first, so the short form does not leave the long one half rewritten.
		{
			holder(ns, "parity-gateway"),
			holder(ns, "parity-gateway") + "_" + ns + "_parity-listenerset",
		},
	}, extra...)
}

// holder is how a frontend's origin names the resource it was programmed for: the
// namespace and name of the declaring object.
func holder(ns, name string) string {
	return ns + "/" + name
}

// parityGatewayParentRef names one of the reference Gateway's own listeners. The suite's
// gatewayParentRef names its usual fixture gateway, and these cases declare their own.
func parityGatewayParentRef(sectionName string) gatewayv1.ParentReference {
	return gatewayv1.ParentReference{
		Kind:        new(gatewayv1.Kind("Gateway")),
		Name:        "parity-gateway",
		SectionName: new(gatewayv1.SectionName(sectionName)),
	}
}
