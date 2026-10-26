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
	"path/filepath"
	"slices"
	"time"

	futils "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/fileutils"
	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// The three cases in this file assert what the TLS *configuration* owes, not only what the
// verdicts conclude: which certificate HAProxy loaded, and what the frontend's crt-list
// holds. An entry contributed by a ListenerSet is a second origin for a certificate --
// GEP-1713 gives it its own reference scope -- and the crt-list of a port both origins
// share is where those two origins have to meet.
//
// The framework's ExpectCertificates and ExpectCrtLists compare *global* counts, which a
// suite where several cases bring certificates cannot use: a frontend another case is
// still tearing down makes the count wrong for a reason that has nothing to do with the
// case being asserted. The helpers below are therefore scoped to one frontend and one
// certificate.
const (
	tlsWaitInterval = 500 * time.Millisecond
	tlsWaitTimeout  = 30 * time.Second
)

// certificatePath is where the controller writes the PEM of a referenced Secret:
// <certs>/<secret namespace>/<secret name's first two characters>/<link id>_<namespace>_<name>.pem.
func (s *ListenerSetSuite) certificatePath(secretNamespace, secretName string) string {
	return path.Join(s.IntTest().HaproxyCfgDir, "certs", secretNamespace, secretName[:2],
		"hug_"+secretNamespace+"_"+secretName+".pem")
}

// crtListOf reads the crt-list HAProxy was given for one frontend. The port is written as
// the manifest's template port and substituted, because HAProxy names the file after the
// port it actually bound.
func (s *ListenerSetSuite) crtListOf(frontend string) ([]string, error) {
	expected := futils.FilePath{
		Dir:      path.Join(s.IntTest().HaproxyCfgDir, "certlists"),
		FileName: "/hug_" + frontend + ".list",
	}
	actualFullPath := utils.SubstitutePortsInString(expected.FullPath(), s.IntTest().PortMap)
	actual := futils.FilePath{
		Dir:      filepath.Dir(actualFullPath),
		FileName: "/" + filepath.Base(actualFullPath),
	}
	return actual.ReadLines()
}

// ExpectCrtList asserts, as an exact set, the certificates the frontend serves. A
// certificate the entry references but is not allowed to use must be absent from it.
func (s *ListenerSetSuite) ExpectCrtList(frontend string, expectedCerts []string) {
	s.T().Helper()
	want := slices.Clone(expectedCerts)
	slices.Sort(want)

	var got []string
	matches := utils.WaitFor(s.IntTest().Ctx, tlsWaitInterval, tlsWaitTimeout, func() bool {
		read, err := s.crtListOf(frontend)
		if err != nil {
			return false
		}
		got = slices.Clone(read)
		slices.Sort(got)
		return slices.Equal(got, want)
	})
	s.Require().Truef(matches, "crt-list of frontend %s: want %v, got %v", frontend, want, got)
}

// ExpectCertificateLoaded asserts that HAProxy has a certificate at certPath and that it
// carries expectedSubject. Reading it through the runtime is what tells a PEM written on
// disk from one a crt-list actually references.
func (s *ListenerSetSuite) ExpectCertificateLoaded(certPath, expectedSubject string) {
	s.T().Helper()
	var subject string
	loaded := utils.WaitFor(s.IntTest().Ctx, tlsWaitInterval, tlsWaitTimeout, func() bool {
		cert, err := s.IntTest().RuntimeClient.ShowCertificate(certPath)
		if err != nil || cert == nil {
			return false
		}
		subject = cert.Subject
		return subject == expectedSubject
	})
	s.Require().Truef(loaded, "certificate %s: want subject %q, got %q", certPath, expectedSubject, subject)
}

// listenerSetCertificateGrant builds, in the Secret's namespace, a grant for a
// cross-namespace certificateRef. fromKind is the resource the grant names as the one
// holding the reference, which is what decides whether it covers a ListenerSet entry.
func (s *ListenerSetSuite) listenerSetCertificateGrant(name, fromKind, toSecretName string) *gatewayv1.ReferenceGrant {
	return &gatewayv1.ReferenceGrant{
		Name: name, Namespace: secretNamespace,
		Spec: gatewayv1.ReferenceGrantSpec{
			From: []gatewayv1.ReferenceGrantFrom{{
				Group:     gatewayv1.Group(gatewayv1.GroupName),
				Kind:      gatewayv1.Kind(fromKind),
				Namespace: gatewayv1.Namespace(s.IntTest().Namespace),
			}},
			To: []gatewayv1.ReferenceGrantTo{{
				Group: "", Kind: "Secret", Name: new(gatewayv1.ObjectName(toSecretName)),
			}},
		},
	}
}

// Test_6_TLS_Two_Origins is one port served by the Gateway's own HTTPS listener and by an
// entry a ListenerSet contributes, each with a certificate of its own and its own
// hostname. The frontend is one, so its crt-list is one, and it has to hold both
// certificates: a contributed entry's TLS configuration comes down to exactly that.
//
// The entry's certificateRef names a Secret of the same namespace, which needs no grant --
// that is the point of the case. The two cross-namespace ones follow.
func (s *ListenerSetSuite) Test_6_TLS_Two_Origins() {
	fixtureDir := "6-tls-two-origins"
	fixturePath := path.Join(utils.GetCRDFixturePath(), fixtureDir)
	s.CreateFixtures(fixturePath, nil)
	defer s.CleanupFixtures(fixturePath, nil)

	ctx := s.IntTest().Ctx
	ns := s.IntTest().Namespace
	frontend := "https_31445"

	s.ExpectListenerSetEntryConditions(ctx, ns, "listenerset-tls", "ls-tls-listener", acceptedEntry())
	s.ExpectListenerSetConditions(ctx, ns, "listenerset-tls", listenerSetServable())
	s.ExpectAttachedListenerSets(ctx, ns, "hug-gateway", 1)

	gatewayCert := s.certificatePath(ns, "offload")
	entryCert := s.certificatePath(ns, "offload2")
	s.ExpectCrtList(frontend, []string{gatewayCert, entryCert})
	s.ExpectCertificateLoaded(gatewayCert, "/CN=offload.haproxy")
	s.ExpectCertificateLoaded(entryCert, "/CN=offload2.haproxy")

	// The entry's hostname is what picks it out of that shared frontend, and the map line
	// is attributed to the ListenerSet that declares it, not to the Gateway.
	s.ExpectMapEntryMentions("hug_"+frontend+"/listener_exact_match.map", "ls-tls.haproxy",
		[]string{"listenerset-tls"}, nil)
}

// Test_7_TLS_ReferenceGrant_From_The_ListenerSet is a certificateRef crossing a namespace:
// it resolves only through a grant naming the ListenerSet that holds the reference. This
// case asserts the certificate as well as the condition, which is what tells the
// resolution from the configuration: the frontend's crt-list gains the entry's
// certificate exactly when the grant appears.
//
// Written as two steps because that is also the only way to exercise the ListenerSet
// controller's ReferenceGrant edge: a grant event has to re-enqueue a ListenerSet that did
// not change, or the entry stays refused until something else touches it.
func (s *ListenerSetSuite) Test_7_TLS_ReferenceGrant_From_The_ListenerSet() {
	fixtureDir := "7-tls-reference-grant"
	fixturePath := path.Join(utils.GetCRDFixturePath(), fixtureDir)
	inTestNamespace := []string{"gatewayclass.yaml", "gateway.yaml", "listenerset.yaml", "route.yaml", "http-echo.yaml", "offload-secret.yaml"}
	s.CreateFixtures(fixturePath, inTestNamespace)
	// The manifest list is given explicitly, the way the ReferenceGrant case does it: a
	// bare cleanup walks every file of the directory in the test namespace and fails on a
	// Secret that lives in another one.
	defer s.CleanupFixtures(fixturePath, inTestNamespace)

	ctx := s.IntTest().Ctx
	ns := s.IntTest().Namespace
	frontend := "https_31444"
	lsetName := "listenerset-grant"
	entryName := "ls-grant-listener"
	gatewayCert := s.certificatePath(ns, "offload")
	entryCert := s.certificatePath(secretNamespace, "ls-grant-cert")

	s.CreateFixturesInNamespace(fixturePath, secretNamespace, []string{"ls-grant-cert.yaml"})
	defer s.CleanupFixturesInNamespace(fixturePath, secretNamespace, []string{"ls-grant-cert.yaml"})

	// Nothing permits the reference yet, so the entry keeps ResolvedRefs=False and its
	// certificate is not configured: the shared frontend serves the Gateway's own
	// certificate only. Its other conditions are the implementation's to render, hence the
	// presence assertion rather than the exact set the permitted state is asserted with.
	s.ExpectListenerSetEntryHasCondition(ctx, ns, lsetName, entryName, metav1.Condition{
		Type:   string(gatewayv1.ListenerEntryConditionResolvedRefs),
		Status: metav1.ConditionFalse,
		Reason: string(gatewayv1.ListenerEntryReasonRefNotPermitted),
	})
	s.ExpectCrtList(frontend, []string{gatewayCert})

	grant := s.listenerSetCertificateGrant("allow-listener-set-cert", "ListenerSet", "ls-grant-cert")
	s.Require().NoError(utils.CreateRuntimeObject(ctx, s.IntTest().Client, grant, true))
	defer func() {
		_ = utils.DeleteRuntimeObject(ctx, s.IntTest().Client, grant, false)
	}()

	s.ExpectListenerSetEntryConditions(ctx, ns, lsetName, entryName, acceptedEntry())
	s.ExpectListenerSetConditions(ctx, ns, lsetName, listenerSetServable())
	s.ExpectCrtList(frontend, []string{gatewayCert, entryCert})
	s.ExpectCertificateLoaded(entryCert, "/CN=offload2.haproxy")
}

// Test_8_TLS_Grant_Naming_The_Gateway_Does_Not_Cover is the same reference as case 7, with
// the grant naming the parent Gateway instead of the ListenerSet. GEP-1713 keeps the two
// scopes apart, and the consequence is a data-plane one: the entry stays refused and its
// certificate stays out of the crt-list.
//
// The case adds the grant that does cover it afterwards, so the refusal is shown to be the
// missing permission rather than a setup that could not have worked either way.
func (s *ListenerSetSuite) Test_8_TLS_Grant_Naming_The_Gateway_Does_Not_Cover() {
	fixtureDir := "8-tls-grant-names-the-gateway"
	fixturePath := path.Join(utils.GetCRDFixturePath(), fixtureDir)
	inTestNamespace := []string{"gatewayclass.yaml", "gateway.yaml", "listenerset.yaml", "route.yaml", "http-echo.yaml", "offload-secret.yaml"}
	s.CreateFixtures(fixturePath, inTestNamespace)
	defer s.CleanupFixtures(fixturePath, inTestNamespace)

	ctx := s.IntTest().Ctx
	ns := s.IntTest().Namespace
	frontend := "https_8443"
	lsetName := "listenerset-grant-wrong-origin"
	entryName := "ls-wrong-origin-listener"
	gatewayCert := s.certificatePath(ns, "offload")
	entryCert := s.certificatePath(secretNamespace, "ls-grant-cert")

	s.CreateFixturesInNamespace(fixturePath, secretNamespace, []string{"ls-grant-cert.yaml"})
	defer s.CleanupFixturesInNamespace(fixturePath, secretNamespace, []string{"ls-grant-cert.yaml"})

	wrongOrigin := s.listenerSetCertificateGrant("allow-gateway-cert", "Gateway", "ls-grant-cert")
	s.Require().NoError(utils.CreateRuntimeObject(ctx, s.IntTest().Client, wrongOrigin, true))
	defer func() {
		_ = utils.DeleteRuntimeObject(ctx, s.IntTest().Client, wrongOrigin, false)
	}()

	s.ExpectListenerSetEntryHasCondition(ctx, ns, lsetName, entryName, metav1.Condition{
		Type:   string(gatewayv1.ListenerEntryConditionResolvedRefs),
		Status: metav1.ConditionFalse,
		Reason: string(gatewayv1.ListenerEntryReasonRefNotPermitted),
	})
	s.ExpectCrtList(frontend, []string{gatewayCert})

	rightOrigin := s.listenerSetCertificateGrant("allow-listener-set-cert", "ListenerSet", "ls-grant-cert")
	s.Require().NoError(utils.CreateRuntimeObject(ctx, s.IntTest().Client, rightOrigin, true))
	defer func() {
		_ = utils.DeleteRuntimeObject(ctx, s.IntTest().Client, rightOrigin, false)
	}()

	s.ExpectListenerSetEntryConditions(ctx, ns, lsetName, entryName, acceptedEntry())
	s.ExpectCrtList(frontend, []string{gatewayCert, entryCert})
	s.ExpectCertificateLoaded(entryCert, "/CN=offload2.haproxy")
}

// Test_9_TLS_Grant_Revoked is case 7 backwards: the grant is there from the start, and
// removing it has to take the entry's certificate back out of the shared frontend's
// crt-list. That removal is a different path from never having been permitted -- an
// existing crt-list is rewritten, rather than one never written -- and it is what a revoked
// grant owes the data plane.
//
// The Gateway's own listener must keep serving the port throughout: losing a contribution
// is not losing the frontend.
func (s *ListenerSetSuite) Test_9_TLS_Grant_Revoked() {
	fixtureDir := "9-tls-grant-revoked"
	fixturePath := path.Join(utils.GetCRDFixturePath(), fixtureDir)
	inTestNamespace := []string{"gatewayclass.yaml", "gateway.yaml", "listenerset.yaml", "route.yaml", "http-echo.yaml", "offload-secret.yaml"}
	s.CreateFixtures(fixturePath, inTestNamespace)
	defer s.CleanupFixtures(fixturePath, inTestNamespace)

	ctx := s.IntTest().Ctx
	ns := s.IntTest().Namespace
	frontend := "https_9090"
	lsetName := "listenerset-grant-revoked"
	entryName := "ls-revoked-listener"
	gatewayCert := s.certificatePath(ns, "offload")
	entryCert := s.certificatePath(secretNamespace, "ls-grant-cert")

	s.CreateFixturesInNamespace(fixturePath, secretNamespace, []string{"ls-grant-cert.yaml"})
	defer s.CleanupFixturesInNamespace(fixturePath, secretNamespace, []string{"ls-grant-cert.yaml"})

	// Permitted from the start: the entry is programmed, and the one crt-list of the shared
	// port holds both certificates.
	grant := s.listenerSetCertificateGrant("allow-listener-set-cert", "ListenerSet", "ls-grant-cert")
	s.Require().NoError(utils.CreateRuntimeObject(ctx, s.IntTest().Client, grant, true))

	s.ExpectListenerSetEntryConditions(ctx, ns, lsetName, entryName, acceptedEntry())
	s.ExpectListenerSetConditions(ctx, ns, lsetName, listenerSetServable())
	s.ExpectCrtList(frontend, []string{gatewayCert, entryCert})
	s.ExpectCertificateLoaded(entryCert, "/CN=offload2.haproxy")

	// Revoked: the reference stops resolving, the entry is no longer servable, and the
	// certificate it brought leaves the crt-list while the Gateway's own stays. A failure
	// before this point leaves the grant behind; the suite's own teardown deletes the
	// namespace it lives in.
	s.Require().NoError(utils.DeleteRuntimeObject(ctx, s.IntTest().Client, grant, false))

	s.ExpectListenerSetEntryHasCondition(ctx, ns, lsetName, entryName, metav1.Condition{
		Type:   string(gatewayv1.ListenerEntryConditionResolvedRefs),
		Status: metav1.ConditionFalse,
		Reason: string(gatewayv1.ListenerEntryReasonRefNotPermitted),
	})
	s.ExpectListenerSetConditions(ctx, ns, lsetName, listenerSetNotServable())
	s.ExpectCrtList(frontend, []string{gatewayCert})
}
