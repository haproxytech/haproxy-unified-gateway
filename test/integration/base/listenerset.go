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
		if err := b.Test().Client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, lset); err != nil {
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
		if err := b.Test().Client.Get(ctx, types.NamespacedName{Name: lsetName, Namespace: namespace}, lset); err != nil {
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
		if err := b.Test().Client.Get(ctx, types.NamespacedName{Name: lsetName, Namespace: namespace}, lset); err != nil {
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
		if err := b.Test().Client.Get(ctx, types.NamespacedName{Name: lsetName, Namespace: namespace}, lset); err != nil {
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
		if err := b.Test().Client.Get(ctx, types.NamespacedName{Name: lsetName, Namespace: namespace}, lset); err != nil {
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
		if err := b.Test().Client.Get(ctx, types.NamespacedName{Name: gwName, Namespace: namespace}, gw); err != nil {
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

// ExpectListenerSetDeleted waits for the ListenerSet to be gone. What the deletion
// leaves behind -- the frontend that survives, the map entries that do not -- is
// asserted separately, by the map and frontend expectations.
func (b *BaseSuite) ExpectListenerSetDeleted(ctx context.Context, namespace, name string) {
	if !utils.WaitFor(ctx, interval, timeout, func() bool {
		lset := &gatewayv1.ListenerSet{}
		err := b.Test().Client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, lset)
		return apierrors.IsNotFound(err)
	}) {
		b.T().Fatalf("ListenerSet %s/%s still exists", namespace, name)
	}
}
