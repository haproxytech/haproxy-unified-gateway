//
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

package base

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// Conditions of a ListenerSet are asserted on Type, Status and Reason only.
//
// Message is free text: the unit tests of k8s/gate/conditions already pin it, and
// asserting it here would make the expectation a transcript of the implementation
// rather than a statement about the object's state. ObservedGeneration is left out
// for the same reason -- it counts updates, so an expectation about it asserts an
// implementation detail.
//
// sameConditionSet compares the sets exactly, which is what lets a case assert an
// absence: an entry in no conflict must carry no Conflicted condition at all.
func sameConditionSet(expected, got []metav1.Condition) (bool, string) {
	spell := func(c metav1.Condition) string {
		return fmt.Sprintf("%s=%s/%s", c.Type, c.Status, c.Reason)
	}
	expectedSpellings := make([]string, 0, len(expected))
	for _, c := range expected {
		expectedSpellings = append(expectedSpellings, spell(c))
	}
	gotSpellings := make([]string, 0, len(got))
	for _, c := range got {
		gotSpellings = append(gotSpellings, spell(c))
	}
	slices.Sort(expectedSpellings)
	slices.Sort(gotSpellings)
	if !slices.Equal(expectedSpellings, gotSpellings) {
		return false, fmt.Sprintf("got [%s], expected [%s]",
			strings.Join(gotSpellings, ", "), strings.Join(expectedSpellings, ", "))
	}
	return true, ""
}

// conditionPresents reports whether a condition with the same Type, Status and
// Reason is present, ignoring the others. Use it where a case asserts one condition
// of a set the API does not fix -- a reason the spec leaves open, or an entry whose
// other conditions an implementation is free to render differently.
func conditionPresents(expected metav1.Condition, got []metav1.Condition) bool {
	for _, c := range got {
		if c.Type == expected.Type && c.Status == expected.Status && c.Reason == expected.Reason {
			return true
		}
	}
	return false
}

// ExpectListenerSetConditions waits for the ListenerSet's own status.conditions.
func (b *BaseSuite) ExpectListenerSetConditions(ctx context.Context, namespace, name string, expected []metav1.Condition) {
	var got []metav1.Condition
	var why string
	if !utils.WaitFor(ctx, interval, timeout, func() bool {
		lset := &gatewayv1.ListenerSet{}
		if err := b.IntTest().Client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, lset); err != nil {
			// Recorded rather than left blank: a read that keeps failing is a case
			// naming something that is not there, and an empty explanation reads as a
			// comparison that found nothing to say.
			why = fmt.Sprintf("the ListenerSet could not be read: %v", err)
			return false
		}
		got = lset.Status.Conditions
		var ok bool
		ok, why = sameConditionSet(expected, got)
		return ok
	}) {
		b.T().Fatalf("ListenerSet %s/%s conditions not correct: %s", namespace, name, why)
	}
}

// ExpectListenerSetEntryConditions waits for the conditions of one entry of a
// ListenerSet, that is status.listeners[].conditions, addressed by entry name. The
// set is compared exactly.
func (b *BaseSuite) ExpectListenerSetEntryConditions(ctx context.Context, namespace, lsetName, entryName string, expected []metav1.Condition) {
	var why string
	if !utils.WaitFor(ctx, interval, timeout, func() bool {
		lset := &gatewayv1.ListenerSet{}
		if err := b.IntTest().Client.Get(ctx, types.NamespacedName{Name: lsetName, Namespace: namespace}, lset); err != nil {
			why = fmt.Sprintf("the ListenerSet could not be read: %v", err)
			return false
		}
		for _, entry := range lset.Status.Listeners {
			if string(entry.Name) != entryName {
				continue
			}
			ok, reason := sameConditionSet(expected, entry.Conditions)
			why = reason
			return ok
		}
		why = fmt.Sprintf("entry %s is absent from status.listeners", entryName)
		return false
	}) {
		b.T().Fatalf("ListenerSet %s/%s entry %s conditions not correct: %s", namespace, lsetName, entryName, why)
	}
}

// ExpectListenerSetEntryHasCondition waits for one condition of an entry to be
// present, whatever the entry's other conditions are.
func (b *BaseSuite) ExpectListenerSetEntryHasCondition(ctx context.Context, namespace, lsetName, entryName string, expected metav1.Condition) {
	var got []metav1.Condition
	if !utils.WaitFor(ctx, interval, timeout, func() bool {
		lset := &gatewayv1.ListenerSet{}
		if err := b.IntTest().Client.Get(ctx, types.NamespacedName{Name: lsetName, Namespace: namespace}, lset); err != nil {
			return false
		}
		for _, entry := range lset.Status.Listeners {
			if string(entry.Name) != entryName {
				continue
			}
			got = entry.Conditions
			return conditionPresents(expected, got)
		}
		return false
	}) {
		b.T().Fatalf("ListenerSet %s/%s entry %s has no %s=%s/%s: got %+v",
			namespace, lsetName, entryName, expected.Type, expected.Status, expected.Reason, got)
	}
}

// ExpectListenerSetEntryAttachedRoutes waits for the attachedRoutes an entry reports.
func (b *BaseSuite) ExpectListenerSetEntryAttachedRoutes(ctx context.Context, namespace, lsetName, entryName string, expected int32) {
	if !utils.WaitFor(ctx, interval, timeout, func() bool {
		lset := &gatewayv1.ListenerSet{}
		if err := b.IntTest().Client.Get(ctx, types.NamespacedName{Name: lsetName, Namespace: namespace}, lset); err != nil {
			return false
		}
		for _, entry := range lset.Status.Listeners {
			if string(entry.Name) == entryName {
				return entry.AttachedRoutes == expected
			}
		}
		return false
	}) {
		b.T().Fatalf("ListenerSet %s/%s entry %s attachedRoutes is not %d", namespace, lsetName, entryName, expected)
	}
}

// ExpectListenerSetEntries waits for the exact set of entry names a ListenerSet
// reports, which is how a case asserts that an entry reported as invalid is still
// reported -- the API asks for an entry per listener, not per valid listener.
func (b *BaseSuite) ExpectListenerSetEntries(ctx context.Context, namespace, lsetName string, expected []string) {
	var got []string
	if !utils.WaitFor(ctx, interval, timeout, func() bool {
		lset := &gatewayv1.ListenerSet{}
		if err := b.IntTest().Client.Get(ctx, types.NamespacedName{Name: lsetName, Namespace: namespace}, lset); err != nil {
			return false
		}
		got = make([]string, 0, len(lset.Status.Listeners))
		for _, entry := range lset.Status.Listeners {
			got = append(got, string(entry.Name))
		}
		slices.Sort(got)
		expectedSorted := slices.Clone(expected)
		slices.Sort(expectedSorted)
		return slices.Equal(got, expectedSorted)
	}) {
		b.T().Fatalf("ListenerSet %s/%s entries are %v, expected %v", namespace, lsetName, got, expected)
	}
}

// ExpectAttachedListenerSets waits for the number of ListenerSets a Gateway reports
// as attached, that is Gateway.status.attachedListenerSets. The field is optional and
// a pointer, so an absent one is read as zero: a Gateway that attaches none is not
// required to publish it at all.
func (b *BaseSuite) ExpectAttachedListenerSets(ctx context.Context, namespace, gwName string, expected int) {
	var got int
	if !utils.WaitFor(ctx, interval, timeout, func() bool {
		gw := &gatewayv1.Gateway{}
		if err := b.IntTest().Client.Get(ctx, types.NamespacedName{Name: gwName, Namespace: namespace}, gw); err != nil {
			return false
		}
		got = 0
		if gw.Status.AttachedListenerSets != nil {
			got = int(*gw.Status.AttachedListenerSets)
		}
		return got == expected
	}) {
		b.T().Fatalf("Gateway %s/%s reports %d attachedListenerSets, expected %d", namespace, gwName, got, expected)
	}
}

// ExpectNoMapEntry waits for no line of a map file to mention the given value.
//
// This is what a purge is asserted with, on a frontend that survives: reading the
// file tells the entries a deleted object had filled apart from a frontend that was
// simply destroyed with its maps.
func (b *BaseSuite) ExpectNoMapEntry(mapFileRelativePath, value string) {
	var mentioned []string
	if !utils.WaitFor(context.Background(), interval, timeout, func() bool {
		lines, err := b.GetMapFileFrom(mapFileRelativePath)
		if err != nil {
			// A map file the purge emptied can also be gone with its frontend.
			return apierrors.IsNotFound(err) || os.IsNotExist(err)
		}
		mentioned = nil
		for _, line := range lines {
			if strings.Contains(line, value) {
				mentioned = append(mentioned, strings.TrimSpace(line))
			}
		}
		return len(mentioned) == 0
	}) {
		b.T().Fatalf("%s still mentions %q: %v", mapFileRelativePath, value, mentioned)
	}
}

// ExpectMapEntryMentions waits for the line of a map file whose key is `key` to
// mention every value of mustMention and none of mustNotMention.
//
// What it asserts is attribution, which is what a value carries when several
// resources share a port: the frontends are keyed on the port, so a listener that
// moves from one Gateway to another keeps its line in the same map -- under the new
// Gateway's key. Asserting the entry's absence there would be asserting the frontend
// had gone, which is a different question.
func (b *BaseSuite) ExpectMapEntryMentions(mapFileRelativePath, key string, mustMention, mustNotMention []string) {
	var offending []string
	if !utils.WaitFor(context.Background(), interval, timeout, func() bool {
		lines, err := b.GetMapFileFrom(mapFileRelativePath)
		if err != nil {
			return false
		}
		offending = nil
		for _, line := range lines {
			fields := strings.Fields(line)
			if len(fields) == 0 || fields[0] != key {
				continue
			}
			for _, wanted := range mustMention {
				if !strings.Contains(line, wanted) {
					offending = append(offending, fmt.Sprintf("%s should mention %s: %s", key, wanted, line))
				}
			}
			for _, unwanted := range mustNotMention {
				if strings.Contains(line, unwanted) {
					offending = append(offending, fmt.Sprintf("%s should not mention %s: %s", key, unwanted, line))
				}
			}
			return len(offending) == 0
		}
		offending = append(offending, fmt.Sprintf("no line of %s has %s as its key", mapFileRelativePath, key))
		return false
	}) {
		b.T().Fatalf("%s: %v", mapFileRelativePath, offending)
	}
}

// ExpectListenerSetReportsNothing waits for a ListenerSet to carry nothing but the
// conditions the API itself seeded, and no entry at all.
//
// "Nothing published" cannot be asserted as an absence of conditions: the ListenerSet
// CRD declares a default status of Accepted and Programmed, both Unknown with the reason
// Pending and the message "Waiting for controller", so a freshly created ListenerSet
// already carries them and a controller that stays silent leaves them exactly as they
// are. Silence is therefore observable as those placeholders surviving untouched, which
// is also a sharper assertion than an absence: any write of ours moves
// observedGeneration to the object's generation and replaces the reason.
//
// As with any assertion of silence, the case has to establish that the object was
// processed: pair it with a second ListenerSet whose status is published, and wait for
// that one first.
func (b *BaseSuite) ExpectListenerSetReportsNothing(ctx context.Context, namespace, name string) {
	var got []metav1.Condition
	var entries int
	if !utils.WaitFor(ctx, interval, timeout, func() bool {
		lset := &gatewayv1.ListenerSet{}
		if err := b.IntTest().Client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, lset); err != nil {
			return false
		}
		got = lset.Status.Conditions
		entries = len(lset.Status.Listeners)
		return len(got) == 2 && entries == 0 &&
			conditionPresents(seededCondition(gatewayv1.ListenerSetConditionAccepted), got) &&
			conditionPresents(seededCondition(gatewayv1.ListenerSetConditionProgrammed), got)
	}) {
		b.T().Fatalf("ListenerSet %s/%s reports more than the API's seeded conditions: %d entries, %+v",
			namespace, name, entries, got)
	}
}

// seededCondition is one of the placeholders the ListenerSet CRD puts in status.conditions
// at creation: Unknown, Pending, "Waiting for controller", and an observedGeneration of
// zero -- the generation of nobody having spoken.
func seededCondition(conditionType gatewayv1.ListenerSetConditionType) metav1.Condition {
	return metav1.Condition{
		Type:               string(conditionType),
		Status:             metav1.ConditionUnknown,
		Reason:             string(gatewayv1.ListenerSetReasonPending),
		Message:            "Waiting for controller",
		ObservedGeneration: 0,
	}
}

// ExpectListenerSetDeleted waits for the ListenerSet to be gone. What the deletion
// leaves behind -- the frontend that survives, the map entries that do not -- is
// asserted separately, by the map and frontend expectations.
func (b *BaseSuite) ExpectListenerSetDeleted(ctx context.Context, namespace, name string) {
	if !utils.WaitFor(ctx, interval, timeout, func() bool {
		lset := &gatewayv1.ListenerSet{}
		err := b.IntTest().Client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, lset)
		return apierrors.IsNotFound(err)
	}) {
		b.T().Fatalf("ListenerSet %s/%s still exists", namespace, name)
	}
}

// ExpectRouteParentHasCondition waits for one route to carry a condition on a given
// parent, and asserts it by presence rather than by set.
//
// The exact set a route publishes for a parent is assembled by two passes -- the
// parentRef check, then the resolution of its rules' references -- so which conditions
// accompany the one asserted here is not something a caller should have to know. The
// parent is identified by kind, name and sectionName, which is also what says *which*
// parent the route blamed: a refusal that names a ListenerSet is a different claim from
// one that names its Gateway.
func (b *BaseSuite) ExpectRouteParentHasCondition(ctx context.Context, namespace, routeName string, parent gatewayv1.ParentReference, expected metav1.Condition) {
	var got []metav1.Condition
	var seen []string
	if !utils.WaitFor(ctx, interval, timeout, func() bool {
		route := &gatewayv1.HTTPRoute{}
		if err := b.IntTest().Client.Get(ctx, types.NamespacedName{Name: routeName, Namespace: namespace}, route); err != nil {
			return false
		}
		seen = seen[:0]
		for _, parentStatus := range route.Status.Parents {
			seen = append(seen, fmt.Sprintf("%s/%s/%s", derefKind(parentStatus.ParentRef.Kind), parentStatus.ParentRef.Name, derefSectionName(parentStatus.ParentRef.SectionName)))
			if !sameParentRef(parentStatus.ParentRef, parent) {
				continue
			}
			got = parentStatus.Conditions
			return conditionPresents(expected, got)
		}
		return false
	}) {
		b.T().Fatalf("route %s/%s has no %s=%s/%s on parent %s/%s/%s; conditions found: %+v; parents seen: %v",
			namespace, routeName, expected.Type, expected.Status, expected.Reason,
			derefKind(parent.Kind), parent.Name, derefSectionName(parent.SectionName), got, seen)
	}
}

// sameParentRef compares the fields a route's parentRef is identified by. Kind and
// sectionName are optional in the spec, so an absent one matches an absent one.
func sameParentRef(got, expected gatewayv1.ParentReference) bool {
	return derefKind(got.Kind) == derefKind(expected.Kind) &&
		got.Name == expected.Name &&
		derefSectionName(got.SectionName) == derefSectionName(expected.SectionName)
}

func derefKind(kind *gatewayv1.Kind) string {
	if kind == nil {
		return "Gateway"
	}
	return string(*kind)
}

func derefSectionName(name *gatewayv1.SectionName) string {
	if name == nil {
		return ""
	}
	return string(*name)
}

// ExpectRouteHasNoParent waits for a route to publish no status for a given parent.
//
// It is the mirror of ExpectRouteParentHasCondition and has the same blind spot in
// reverse: "no entry" is true before the controller has looked at the route, so a case
// that asserts it must first wait for another parent of the same route to be published.
// The pair is what makes the absence a statement about the controller rather than about
// the test's timing.
func (b *BaseSuite) ExpectRouteHasNoParent(ctx context.Context, namespace, routeName string, parent gatewayv1.ParentReference) {
	var seen []string
	if !utils.WaitFor(ctx, interval, timeout, func() bool {
		route := &gatewayv1.HTTPRoute{}
		if err := b.IntTest().Client.Get(ctx, types.NamespacedName{Name: routeName, Namespace: namespace}, route); err != nil {
			return false
		}
		seen = seen[:0]
		for _, parentStatus := range route.Status.Parents {
			if sameParentRef(parentStatus.ParentRef, parent) {
				seen = append(seen, fmt.Sprintf("%s/%s/%s", derefKind(parentStatus.ParentRef.Kind), parentStatus.ParentRef.Name, derefSectionName(parentStatus.ParentRef.SectionName)))
				return false
			}
		}
		return true
	}) {
		b.T().Fatalf("route %s/%s publishes a status for a parent it should say nothing about: %v",
			namespace, routeName, seen)
	}
}
