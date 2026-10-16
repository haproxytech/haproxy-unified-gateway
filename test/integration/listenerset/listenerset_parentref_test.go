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

// Package listenerset — parentRef resolution and allowance.
//
// These cases are about which Gateway a ListenerSet's parentRef resolves to, and what
// it may do there. HUG answers the resolution in one place
// (tree/attachmentOfListenerSet and parentGatewayKey) and its three possible answers
// are what the cases pin:
//
//   - the parentRef names a Gateway this controller serves and that allows the
//     ListenerSet: the entries enter the merged list, and a status is published;
//   - the parentRef names a Gateway this controller serves and that refuses it:
//     nothing is contributed, and the refusal is published -- Accepted and Programmed
//     both False with NotAllowed, the reason the API declares for it;
//   - the parentRef names nothing of ours, or nothing at all: **nothing is published**.
//     That last one is a choice, not a requirement: writing a status there would be
//     commenting on a resource aimed at another controller. The cases pin it as a
//     choice, which is why each of them pairs the silent ListenerSet with one whose
//     status is published: waiting for the published one is what proves the batch was
//     processed, and so what makes the absence meaningful.
//
// One behaviour is pinned although it is unsatisfying, and the cases say so where it
// appears: when a ListenerSet's parent stops being served by this controller -- deleted,
// or its GatewayClass repointed -- the last published status **stays**. The patcher
// skips a ListenerSet whose attachment is no longer reported rather than clearing a
// status that would then not be ours to comment on, and the conditions carry the
// ListenerSet's own generation, which has not moved, so nothing about the stale status
// looks stale to a reader.
package listenerset

import (
	"path"

	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// listenerSetNotAllowed is the pair of conditions a Gateway's refusal publishes. Both
// carry NotAllowed: the API declares that reason for the Accepted condition, and the
// conformance suite requires it on Programmed as well.
func listenerSetNotAllowed() []metav1.Condition {
	return []metav1.Condition{
		{
			Type:   string(gatewayv1.ListenerSetConditionAccepted),
			Status: metav1.ConditionFalse,
			Reason: string(gatewayv1.ListenerSetReasonNotAllowed),
		},
		{
			Type:   string(gatewayv1.ListenerSetConditionProgrammed),
			Status: metav1.ConditionFalse,
			Reason: string(gatewayv1.ListenerSetReasonNotAllowed),
		},
	}
}

// Test_ParentRef_1_Does_Not_Exist is a parentRef naming a Gateway that is not in the
// cluster at all. Nothing is published, while the ListenerSet next to it, aimed at the
// Gateway that does exist, is served.
func (s *ListenerSetSuite) Test_ParentRef_1_Does_Not_Exist() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "parentref-1-does-not-exist")
	s.CreateFixtures(fixturePath, nil)
	defer s.CleanupFixtures(fixturePath, nil)

	ctx := s.Test().Ctx
	ns := s.Test().Namespace

	s.ExpectListenerSetConditions(ctx, ns, "ls-control", listenerSetServable())
	s.ExpectListenerSetHasNoStatus(ctx, ns, "ls-silent")
}

// Test_ParentRef_2_Parent_Deleted attaches first, then deletes the Gateway it points
// at, and pins what is left: the entries leave HAProxy with their Gateway, but the
// status the ListenerSet published before stays exactly where it was.
//
// That the status is not cleared is deliberate -- the patcher will not write to a
// ListenerSet whose parent is no longer ours, since clearing a status is commenting on
// someone else's resource -- but the result is a ListenerSet that reads as accepted and
// programmed while nothing serves it. Pinned as it is, not as it should be.
func (s *ListenerSetSuite) Test_ParentRef_2_Parent_Deleted() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "parentref-2-parent-deleted")
	manifests := []string{"gatewayclass.yaml", "gateway.yaml", "listenerset.yaml"}
	s.CreateFixtures(fixturePath, manifests)
	defer s.CleanupFixtures(fixturePath, manifests)

	ctx := s.Test().Ctx
	ns := s.Test().Namespace

	s.ExpectListenerSetConditions(ctx, ns, "listenerset", listenerSetServable())
	s.ExpectListenerSetEntryConditions(ctx, ns, "listenerset", "ls-listener", acceptedEntry())

	// The parent goes away, taking its frontend and its maps with it.
	s.CleanupFixtures(fixturePath, []string{"gateway.yaml"})

	s.ExpectListenerSetConditions(ctx, ns, "listenerset", listenerSetServable())
}

// Test_ParentRef_3_Not_Managed is a parentRef naming a Gateway that exists but whose
// GatewayClass is another controller's. Nothing is published, and the ListenerSet aimed
// at our own Gateway, in the same fixture, is served -- which is what tells an
// unmanaged parent apart from a controller that simply has not reconciled yet.
func (s *ListenerSetSuite) Test_ParentRef_3_Not_Managed() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "parentref-3-not-managed")
	s.CreateFixtures(fixturePath, nil)
	defer s.CleanupFixtures(fixturePath, nil)

	ctx := s.Test().Ctx
	ns := s.Test().Namespace

	s.ExpectListenerSetConditions(ctx, ns, "ls-control", listenerSetServable())
	s.ExpectListenerSetHasNoStatus(ctx, ns, "ls-silent")
}

// Test_ParentRef_4_Becomes_Managed is the same shape the other way round: the parent is
// another controller's Gateway at first, and its GatewayClass is repointed at this
// controller. The ListenerSet, which did not change, then attaches and its listener is
// served -- the transition that the contributor-set diff was written for.
func (s *ListenerSetSuite) Test_ParentRef_4_Becomes_Managed() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "parentref-4-becomes-managed")
	manifests := []string{"gatewayclass-foreign.yaml", "gateway-foreign.yaml", "listenerset.yaml", "route.yaml", "http-echo.yaml"}
	s.CreateFixtures(fixturePath, manifests)
	// The cleanup names the class once: two manifests describe it, and the deletion is
	// per file.
	defer s.CleanupFixtures(fixturePath, manifests)

	ctx := s.Test().Ctx
	ns := s.Test().Namespace

	s.ExpectListenerSetHasNoStatus(ctx, ns, "listenerset")

	// The class becomes ours. Nothing about the ListenerSet changed.
	s.CreateFixtures(fixturePath, []string{"gatewayclass-foreign-ours.yaml"})

	s.ExpectListenerSetConditions(ctx, ns, "listenerset", listenerSetServable())
	s.ExpectListenerSetEntryConditions(ctx, ns, "listenerset", "ls-listener", acceptedEntry())
	s.ExpectListenerSetEntryAttachedRoutes(ctx, ns, "listenerset", "ls-listener", 1)
}

// Test_ParentRef_5_Stops_Being_Managed repoints a GatewayClass away from this
// controller: the Gateway stops being ours, and the ListenerSet's entries leave HAProxy
// with it.
//
// The status is not touched, and that is deliberate: once nothing is contributed, the
// attachment is no longer ours to report, and writing there would risk colliding with
// the controller that has taken the Gateway over. Whichever controller speaks about the
// ListenerSet next replaces the whole status anyway -- every patcher writes the status
// outright rather than merging -- so the last status we published stays until someone
// with a claim to it overwrites it.
//
// The assertion that matters here is the data plane one: the listener is gone from the
// port's map. Reading the map is what tells a detached Gateway from one that is still
// served, which is the difference this transition is about.
func (s *ListenerSetSuite) Test_ParentRef_5_Stops_Being_Managed() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "parentref-5-stops-being-managed")
	manifests := []string{"gatewayclass-foreign.yaml", "gateway-foreign.yaml", "listenerset.yaml", "route.yaml", "http-echo.yaml"}
	s.CreateFixtures(fixturePath, manifests)
	defer s.CleanupFixtures(fixturePath, manifests)

	ctx := s.Test().Ctx
	ns := s.Test().Namespace

	// Start attached: applying the "ours" version of the class the Gateway names.
	s.CreateFixtures(fixturePath, []string{"gatewayclass-foreign-ours.yaml"})
	s.ExpectListenerSetConditions(ctx, ns, "listenerset", listenerSetServable())
	s.ExpectListenerSetEntryConditions(ctx, ns, "listenerset", "ls-listener", acceptedEntry())
	s.ExpectMapEntryMentions("hug_http_31081/listener_exact_match.map", "ls.haproxy",
		[]string{"foreign-gateway"}, nil)

	// And away from us again.
	s.CreateFixtures(fixturePath, []string{"gatewayclass-foreign.yaml"})

	s.ExpectNoMapEntry("hug_http_31081/listener_exact_match.map", "ls.haproxy")
}

// Test_ParentRef_6_Wrong_Shape is the three ways a parentRef fails to name a Gateway at
// all: the wrong group, the wrong kind, and a namespace in which no such Gateway exists.
// None of them is ours to comment on, so none is published, while the control next to
// them is served. The namespace the API defaults an absent one to is a separate case.
func (s *ListenerSetSuite) Test_ParentRef_6_Wrong_Shape() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "parentref-6-wrong-shape")
	s.CreateFixtures(fixturePath, nil)
	defer s.CleanupFixtures(fixturePath, nil)

	ctx := s.Test().Ctx
	ns := s.Test().Namespace

	s.ExpectListenerSetConditions(ctx, ns, "ls-control", listenerSetServable())
	s.ExpectListenerSetHasNoStatus(ctx, ns, "ls-wrong-group")
	s.ExpectListenerSetHasNoStatus(ctx, ns, "ls-wrong-kind")
	s.ExpectListenerSetHasNoStatus(ctx, ns, "ls-other-namespace")
}

// Test_ParentRef_7_Cross_Namespace is a parentRef naming a Gateway in another
// namespace. That is what the reference allows -- its namespace field exists for it --
// and the answer is the parent's policy: with Same, a ListenerSet from the test's
// namespace is refused with NotAllowed on both conditions; with All, the same
// ListenerSet is served.
func (s *ListenerSetSuite) Test_ParentRef_7_Cross_Namespace() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "parentref-7-cross-namespace")
	inTestNamespace := []string{"gatewayclass.yaml", "listenerset-to-same-policy.yaml", "listenerset-to-all-policy.yaml"}
	s.CreateFixtures(fixturePath, inTestNamespace)
	defer s.CleanupFixtures(fixturePath, inTestNamespace)

	ctx := s.Test().Ctx
	ns := s.Test().Namespace

	// The two parents live in the second namespace the controller watches, and it
	// never creates it.
	inOtherNamespace := []string{"gateway-other-same.yaml", "gateway-other-all.yaml"}
	s.CreateFixturesInNamespace(fixturePath, secretNamespace, inOtherNamespace)
	defer s.CleanupFixturesInNamespace(fixturePath, secretNamespace, inOtherNamespace)

	s.ExpectListenerSetConditions(ctx, ns, "listenerset", listenerSetNotAllowed())
	s.ExpectListenerSetConditions(ctx, ns, "listenerset-allowed", listenerSetServable())
}

// Test_ParentRef_8_Same_Namespace is the namespace field being absent, which the API
// documents as meaning the ListenerSet's own namespace. The other spelling -- naming
// that same namespace explicitly -- is asserted on the ListenerSet built in Go below,
// because the namespace is derived per run and fixtures cannot carry it.
func (s *ListenerSetSuite) Test_ParentRef_8_Same_Namespace() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "parentref-8-same-namespace")
	s.CreateFixtures(fixturePath, nil)
	defer s.CleanupFixtures(fixturePath, nil)

	ctx := s.Test().Ctx
	ns := s.Test().Namespace

	// The absent namespace: attached, since it means the ListenerSet's own.
	s.ExpectListenerSetConditions(ctx, ns, "listenerset-empty-namespace", listenerSetServable())
	s.ExpectListenerSetEntryConditions(ctx, ns, "listenerset-empty-namespace", "ls-listener", acceptedEntry())

	// The same namespace, named: same behaviour, built here for the reason above.
	named := &gatewayv1.ListenerSet{
		ObjectMeta: metav1.ObjectMeta{Name: "listenerset-named-namespace"},
		Spec: gatewayv1.ListenerSetSpec{
			ParentRef: gatewayv1.ParentGatewayReference{
				Name:      "hug-gateway",
				Namespace: ptrTo(gatewayv1.Namespace(ns)),
			},
			Listeners: []gatewayv1.ListenerEntry{{
				Name:     "ls-listener",
				Port:     31081,
				Protocol: gatewayv1.HTTPProtocolType,
				// A hostname of its own: the empty one would mean any hostname on
				// that port, which conflicts with the Gateway's own listener.
				Hostname: ptrTo[gatewayv1.Hostname]("ls-named.haproxy"),
			}},
		},
	}
	err := utils.CreateRuntimeObject(ctx, s.Test().Client, named, true)
	s.Require().NoError(err)
	defer func() {
		_ = utils.DeleteRuntimeObject(ctx, s.Test().Client, named, false)
	}()

	s.ExpectListenerSetConditions(ctx, ns, "listenerset-named-namespace", listenerSetServable())
}

// Test_AllowedListeners_9_Not_Allowed is a Gateway that says nothing about
// allowedListeners. The API defaults that to None, so a Gateway that has never heard of
// ListenerSets is not extensible by anyone who can create one in its namespace, and the
// refusal is published rather than silent.
func (s *ListenerSetSuite) Test_AllowedListeners_9_Not_Allowed() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "allowed-listeners-9-not-allowed")
	s.CreateFixtures(fixturePath, nil)
	defer s.CleanupFixtures(fixturePath, nil)

	ctx := s.Test().Ctx
	ns := s.Test().Namespace

	s.ExpectListenerSetConditions(ctx, ns, "listenerset", listenerSetNotAllowed())
}

// Test_AllowedListeners_10_Same is From: Same on both sides of the boundary: the
// ListenerSet in the Gateway's own namespace attaches, and the one declaring another
// namespace -- pointing at a Gateway that does allow ListenerSets, just not from there --
// is refused.
func (s *ListenerSetSuite) Test_AllowedListeners_10_Same() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "allowed-listeners-10-same")
	inTestNamespace := []string{"gatewayclass.yaml", "gateway-same.yaml", "listenerset-same-namespace.yaml", "listenerset-other-namespace.yaml"}
	s.CreateFixtures(fixturePath, inTestNamespace)
	defer s.CleanupFixtures(fixturePath, inTestNamespace)

	// The Gateway the refused ListenerSet names lives in the second namespace, the one
	// the controller watches and never creates.
	inOtherNamespace := []string{"gateway-other-same.yaml"}
	s.CreateFixturesInNamespace(fixturePath, secretNamespace, inOtherNamespace)
	defer s.CleanupFixturesInNamespace(fixturePath, secretNamespace, inOtherNamespace)

	ctx := s.Test().Ctx
	ns := s.Test().Namespace

	s.ExpectListenerSetConditions(ctx, ns, "listenerset-same-namespace", listenerSetServable())
	s.ExpectListenerSetEntryConditions(ctx, ns, "listenerset-same-namespace", "ls-listener", acceptedEntry())

	s.ExpectListenerSetConditions(ctx, ns, "listenerset-other-namespace", listenerSetNotAllowed())
}

func ptrTo[T any](v T) *T { return &v }
