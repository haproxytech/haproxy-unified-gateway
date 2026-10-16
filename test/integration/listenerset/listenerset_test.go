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

// Keep the aligned coverage table in the package comment readable.
//revive:disable:line-length-limit

// Package listenerset exercises a ListenerSet end to end: a Gateway declaring it
// accepts ListenerSets, the entries it contributes being merged into the Gateway's
// listeners, the conditions both objects publish for them, and what the data plane
// keeps when the ListenerSet goes away.
//
// The conditions each case asserts, by object, are these. `exact` means the case
// pins the whole set the object publishes for that listener, so an extra condition
// fails the case as much as a missing one; `present` means it pins one condition
// and leaves the rest to the implementation. A dash means the case asserts nothing
// of that condition -- so the table also says what is *not* covered yet.
//
// | case                                                | Gateway                 | ListenerSet                       | entry                                                             | route                    |
// |-----------------------------------------------------|-------------------------|-----------------------------------|-------------------------------------------------------------------|--------------------------|
// | 1 contribution                                      | attachedListenerSets=1  | Accepted=True, Programmed=True    | ResolvedRefs/Accepted/Programmed=True, attachedRoutes=1, exact     | attaches, no condition   |
// | 2 sibling conflicts (A older, B refused)            | attachedListenerSets=1  | A Accepted/Programmed=True        | A: accepted, exact -- B: Accepted/Programmed=False + Conflicted, exact | -                        |
// | **3** ReferenceGrant naming the ListenerSet         | attachedListenerSets=1  | refused, then Accepted/Programmed=True | refused: ResolvedRefs=False/RefNotPermitted, present -- permitted: accepted, exact | -                        |
// | **4** deletion on a shared frontend                 | attachedListenerSets=0  | gone                              | gone, and the entry is gone from the surviving frontend's map     | -                        |
// | **5** re-parenting to another Gateway               | A: 1 then 0, B: 0 then 1 | Accepted/Programmed=True          | accepted, exact throughout, attachedRoutes=1                       | follows the ListenerSet  |
//
// The conditions each case asserts come from the conformance suite, whose ListenerSet
// cases pass. An accepted entry carries exactly ResolvedRefs=True, Accepted=True and
// Programmed=True; a rejected one carries four -- the three that name the conflict,
// with the conflict's reason on Accepted and Programmed as well
// (Accepted=False/<reason>, Programmed=False/<reason>, Conflicted=True/<reason>),
// plus ResolvedRefs=True, since losing a port is not a reference problem. Where the API leaves a set open -- the conditions of an entry whose
// reference is not permitted -- the case pins one condition instead of the set.
package listenerset

//revive:enable:line-length-limit

import (
	"path"

	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// acceptedEntry is what a listener contributes when nothing stands in its way.
func acceptedEntry() []metav1.Condition {
	return []metav1.Condition{
		{
			Type:   string(gatewayv1.ListenerEntryConditionResolvedRefs),
			Status: metav1.ConditionTrue,
			Reason: string(gatewayv1.ListenerEntryReasonResolvedRefs),
		},
		{
			Type:   string(gatewayv1.ListenerEntryConditionAccepted),
			Status: metav1.ConditionTrue,
			Reason: string(gatewayv1.ListenerEntryReasonAccepted),
		},
		{
			Type:   string(gatewayv1.ListenerEntryConditionProgrammed),
			Status: metav1.ConditionTrue,
			Reason: string(gatewayv1.ListenerEntryReasonProgrammed),
		},
	}
}

// rejectedByConflict is what an entry carries when it loses a listener conflict:
// four conditions. Three name the conflict, and carry its reason on all three --
// Accepted and Programmed say why they are false, which is what the conformance
// suite requires of a conflicted listener. The fourth is ResolvedRefs, and it is
// true: a conflict is not a reference problem, and a listener that resolves its
// references and loses a port says both.
//
// The conformance helper this shape was read from is a "must have these" check, not
// an equality of sets, so it is the exact-set assertion here that pins the fourth.
func rejectedByConflict(reason gatewayv1.ListenerEntryConditionReason) []metav1.Condition {
	return []metav1.Condition{
		{
			Type:   string(gatewayv1.ListenerEntryConditionConflicted),
			Status: metav1.ConditionTrue,
			Reason: string(reason),
		},
		{
			Type:   string(gatewayv1.ListenerEntryConditionAccepted),
			Status: metav1.ConditionFalse,
			Reason: string(reason),
		},
		{
			Type:   string(gatewayv1.ListenerEntryConditionProgrammed),
			Status: metav1.ConditionFalse,
			Reason: string(reason),
		},
		{
			Type:   string(gatewayv1.ListenerEntryConditionResolvedRefs),
			Status: metav1.ConditionTrue,
			Reason: string(gatewayv1.ListenerEntryReasonResolvedRefs),
		},
	}
}

// listenerSetServable is a ListenerSet whose entries are all valid.
func listenerSetServable() []metav1.Condition {
	return []metav1.Condition{
		{
			Type:   string(gatewayv1.ListenerSetConditionAccepted),
			Status: metav1.ConditionTrue,
			Reason: string(gatewayv1.ListenerSetReasonAccepted),
		},
		{
			Type:   string(gatewayv1.ListenerSetConditionProgrammed),
			Status: metav1.ConditionTrue,
			Reason: string(gatewayv1.ListenerSetReasonProgrammed),
		},
	}
}

// listenerSetNotServable is a ListenerSet with no valid entry left. Accepted is
// False and not just flagged: with nothing valid to serve, the ListenerSet is not
// accepted at all. An entry reported invalid is still reported, which is why the
// cases below assert the entry set separately from the roll-up.
func listenerSetNotServable() []metav1.Condition {
	return []metav1.Condition{
		{
			Type:   string(gatewayv1.ListenerSetConditionAccepted),
			Status: metav1.ConditionFalse,
			Reason: string(gatewayv1.ListenerSetReasonListenersNotValid),
		},
		{
			Type:   string(gatewayv1.ListenerSetConditionProgrammed),
			Status: metav1.ConditionFalse,
			Reason: string(gatewayv1.ListenerSetReasonListenersNotValid),
		},
	}
}

// Test_1_Contribution is a ListenerSet contributing a listener the Gateway serves:
// the entry is accepted, the route attached through the ListenerSet's own parentRef
// is counted on that entry, and the Gateway reports the ListenerSet as attached
// without ceasing to serve the listener it declares itself.
func (s *ListenerSetSuite) Test_1_Contribution() {
	fixtureDir := "1-contribution"
	fixturePath := path.Join(utils.GetCRDFixturePath(), fixtureDir)
	s.CreateFixtures(fixturePath, nil)
	defer s.CleanupFixtures(fixturePath, nil)

	ctx := s.Test().Ctx
	ns := s.Test().Namespace

	s.ExpectListenerSetConditions(ctx, ns, "listenerset-contributing", listenerSetServable())
	// The entry is reported whatever its state, so the entry set is asserted too.
	s.ExpectListenerSetEntries(ctx, ns, "listenerset-contributing", []string{"ls-listener"})
	s.ExpectListenerSetEntryConditions(ctx, ns, "listenerset-contributing", "ls-listener", acceptedEntry())
	// The route names the ListenerSet as its parent, so it lands on the entry
	// rather than on the Gateway's own listener.
	s.ExpectListenerSetEntryAttachedRoutes(ctx, ns, "listenerset-contributing", "ls-listener", 1)
	s.ExpectAttachedListenerSets(ctx, ns, "hug-gateway", 1)
}

// Test_2_SiblingConflicts is two ListenerSets claiming the same hostname on the
// same port of one Gateway. The ListenerSet created first keeps its listener and the
// second is refused, which is the precedence the API prescribes; the case asserts
// both, and that the older one stays untouched by the arrival of the younger.
//
// The two are applied in two steps on purpose: applied together they would share a
// creation timestamp, and precedence between listeners created at the same instant
// is not something to assert.
func (s *ListenerSetSuite) Test_2_SiblingConflicts() {
	fixtureDir := "2-sibling-conflicts"
	fixturePath := path.Join(utils.GetCRDFixturePath(), fixtureDir)
	older := []string{"gatewayclass.yaml", "http-echo.yaml", "gateway.yaml", "listenerset-a.yaml"}
	s.CreateFixtures(fixturePath, older)
	defer s.CleanupFixtures(fixturePath, nil)

	ctx := s.Test().Ctx
	ns := s.Test().Namespace

	s.ExpectListenerSetConditions(ctx, ns, "listenerset-a", listenerSetServable())
	s.ExpectListenerSetEntryConditions(ctx, ns, "listenerset-a", "ls-a-listener", acceptedEntry())

	s.CreateFixtures(fixturePath, []string{"listenerset-b.yaml"})

	s.ExpectListenerSetConditions(ctx, ns, "listenerset-b", listenerSetNotServable())
	s.ExpectListenerSetEntryConditions(ctx, ns, "listenerset-b", "ls-b-listener",
		rejectedByConflict(gatewayv1.ListenerEntryReasonHostnameConflict))
	// One, not two: attachedListenerSets counts the ListenerSets whose own Accepted
	// condition is true, and this one is refused. It stays in its own status, which
	// is where the refusal is reported.
	s.ExpectAttachedListenerSets(ctx, ns, "hug-gateway", 1)
	// Losing a conflict did not disturb the winner.
	s.ExpectListenerSetEntryConditions(ctx, ns, "listenerset-a", "ls-a-listener", acceptedEntry())
}

// Test_4_Deletion_On_A_Shared_Frontend deletes a ListenerSet from a Gateway whose
// frontend outlives it. The point is the data plane: the Gateway keeps serving, so
// nothing tears the frontend down, and only a diff of the previous listeners purges
// the maps the ListenerSet's listener had filled.
func (s *ListenerSetSuite) Test_4_Deletion_On_A_Shared_Frontend() {
	fixtureDir := "4-deletion-on-a-shared-frontend"
	fixturePath := path.Join(utils.GetCRDFixturePath(), fixtureDir)
	s.CreateFixtures(fixturePath, nil)
	// The ListenerSet is left out of the cleanup: this case deletes it itself, and a
	// second deletion fails on an object that is already gone.
	defer s.CleanupFixtures(fixturePath, []string{"gatewayclass.yaml", "http-echo.yaml", "gateway.yaml"})

	ctx := s.Test().Ctx
	ns := s.Test().Namespace
	lsetName := "listenerset-contributing"

	s.ExpectListenerSetEntryConditions(ctx, ns, lsetName, "ls-listener", acceptedEntry())
	s.ExpectAttachedListenerSets(ctx, ns, "hug-gateway", 1)

	s.CleanupFixtures(fixturePath, []string{"listenerset.yaml"})

	s.ExpectListenerSetDeleted(ctx, ns, lsetName)
	s.ExpectAttachedListenerSets(ctx, ns, "hug-gateway", 0)
	// The frontend is shared: the Gateway keeps a listener of its own, so nothing
	// tears it down and its maps stay where they are. What must be gone is what the
	// ListenerSet's listener had filled -- reading the file is what tells a purge
	// apart from a frontend destroyed with its maps.
	s.ExpectNoMapEntry("hug_http_31081/listener_exact_match.map", "ls.haproxy")
}

// Test_5_Reparenting moves a ListenerSet from one Gateway to another: parentRef is
// not immutable, so this is a legal edit.
//
// It is the transition that used to leave the former parent behind. Its merged list
// is only rebuilt when the Gateway itself is updated, and a ListenerSet that changed
// parent does not put the former one there -- it names the new one. So the first
// Gateway kept the entries in its frontend and in its maps, and kept counting them in
// its port conflicts, while its own status no longer mentioned them at all.
func (s *ListenerSetSuite) Test_5_Reparenting() {
	fixtureDir := "5-reparenting"
	fixturePath := path.Join(utils.GetCRDFixturePath(), fixtureDir)
	first := []string{
		"gatewayclass.yaml", "http-echo.yaml", "gateway-a.yaml", "gateway-b.yaml",
		"route.yaml", "listenerset.yaml",
	}
	s.CreateFixtures(fixturePath, first)
	// The cleanup names the ListenerSet once, through the manifest that describes it
	// last: the two manifests are one object, and the deletion is per file, so
	// listing both would delete it twice and fail on the second attempt.
	defer s.CleanupFixtures(fixturePath, []string{
		"gatewayclass.yaml", "http-echo.yaml", "gateway-a.yaml", "gateway-b.yaml",
		"route.yaml", "listenerset-moved.yaml",
	})

	ctx := s.Test().Ctx
	ns := s.Test().Namespace
	lsetName := "listenerset-moving"

	// Attached to the first Gateway, and served there.
	s.ExpectListenerSetConditions(ctx, ns, lsetName, listenerSetServable())
	s.ExpectListenerSetEntryConditions(ctx, ns, lsetName, "ls-listener", acceptedEntry())
	s.ExpectAttachedListenerSets(ctx, ns, "hug-gateway-a", 1)
	s.ExpectAttachedListenerSets(ctx, ns, "hug-gateway-b", 0)

	// The move. Only the ListenerSet changes; the first Gateway is not touched by
	// anything, and it is the one whose state is now wrong.
	s.CreateFixtures(fixturePath, []string{"listenerset-moved.yaml"})

	s.ExpectAttachedListenerSets(ctx, ns, "hug-gateway-b", 1)
	s.ExpectAttachedListenerSets(ctx, ns, "hug-gateway-a", 0)
	// The route is parented to the ListenerSet, so it followed it.
	s.ExpectListenerSetEntryAttachedRoutes(ctx, ns, lsetName, "ls-listener", 1)
	s.ExpectListenerSetEntryConditions(ctx, ns, lsetName, "ls-listener", acceptedEntry())
	// And the first Gateway's frontend no longer carries the entry: the shared
	// frontend survives, since the Gateway declares a listener of its own, so what
	// proves the purge is the entry being gone from the map, not the frontend being
	// gone with it.
	s.ExpectNoMapEntry("hug_http_31081/listener_exact_match.map", "ls.haproxy")
}

// Test_3_ReferenceGrant_From_The_ListenerSet is a listener whose certificateRef
// crosses a namespace: it is permitted only by a grant whose `from` names the
// ListenerSet that holds the reference. A grant naming the parent Gateway does not
// cover it, and GEP-1713 says so in both directions.
//
// Written as two steps -- refused, then permitted -- because that is also the only
// way to exercise the ReferenceGrant edge of the ListenerSet controller: a grant
// event has to re-enqueue a ListenerSet that did not change, or the listener stays
// refused until something else touches it.
func (s *ListenerSetSuite) Test_3_ReferenceGrant_From_The_ListenerSet() {
	fixtureDir := "3-reference-grant-from-the-listener-set"
	fixturePath := path.Join(utils.GetCRDFixturePath(), fixtureDir)
	inTestNamespace := []string{"gatewayclass.yaml", "gateway.yaml", "listenerset.yaml"}
	s.CreateFixtures(fixturePath, inTestNamespace)
	// The manifest list is given explicitly: a bare cleanup walks every file of the
	// directory in the test namespace, the Secret among them, and fails on a Secret
	// that lives in another one.
	defer s.CleanupFixtures(fixturePath, inTestNamespace)

	ctx := s.Test().Ctx
	ns := s.Test().Namespace
	lsetName := "listenerset-with-grant"

	// The Secret is in the other namespace from the start: what the first step
	// lacks is the grant, not the certificate.
	s.CreateFixturesInNamespace(fixturePath, secretNamespace, []string{"secret.yaml"})
	defer s.CleanupFixturesInNamespace(fixturePath, secretNamespace, []string{"secret.yaml"})

	// Nothing permits the reference yet, so the entry cannot resolve it. Its other
	// conditions are the implementation's to render, hence the presence assertion;
	// the roll-up, which the conformance suite pins, is asserted as an exact set.
	s.ExpectListenerSetEntryHasCondition(ctx, ns, lsetName, "ls-listener", metav1.Condition{
		Type:   string(gatewayv1.ListenerEntryConditionResolvedRefs),
		Status: metav1.ConditionFalse,
		Reason: string(gatewayv1.ListenerEntryReasonRefNotPermitted),
	})
	s.ExpectListenerSetConditions(ctx, ns, lsetName, listenerSetNotServable())

	// The grant, in the namespace of the Secret, naming the ListenerSet as the
	// resource the reference comes from. Built here rather than kept as a manifest
	// because it has to name the test's own namespace, which is derived per run.
	grant := &gatewayv1.ReferenceGrant{
		ObjectMeta: metav1.ObjectMeta{Name: "allow-listener-set-cert", Namespace: secretNamespace},
		Spec: gatewayv1.ReferenceGrantSpec{
			From: []gatewayv1.ReferenceGrantFrom{{
				Group:     gatewayv1.Group(gatewayv1.GroupName),
				Kind:      "ListenerSet",
				Namespace: gatewayv1.Namespace(ns),
			}},
			To: []gatewayv1.ReferenceGrantTo{{
				Group: "",
				Kind:  "Secret",
			}},
		},
	}
	err := utils.CreateRuntimeObject(ctx, s.Test().Client, grant, true)
	s.Require().NoError(err)
	defer func() {
		_ = utils.DeleteRuntimeObject(ctx, s.Test().Client, grant, false)
	}()

	s.ExpectListenerSetEntryConditions(ctx, ns, lsetName, "ls-listener", acceptedEntry())
	s.ExpectListenerSetConditions(ctx, ns, lsetName, listenerSetServable())
}
