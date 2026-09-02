// Copyright 2025 HAProxy Technologies LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package utils // revive:disable:var-naming

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// Helper to create a Gateway with a given name and timestamp offset
func newGateway(name string, now metav1.Time, offset time.Duration) *gatewayv1.Gateway {
	return &gatewayv1.Gateway{
		Name:              name,
		CreationTimestamp: metav1.Time{Time: now.Add(offset)},
	}
}

func TestSortByCreationTimestamp_Gateways(t *testing.T) {
	now := metav1.Now()
	gateways := []client.Object{
		newGateway("gateway1", now, 1*time.Minute),
		nil, // nil value should sort first
		newGateway("gateway2", now, 0),
		newGateway("gateway3", now, 2*time.Minute),
		nil, // another nil value
	}

	SortByCreationTimestamp(gateways)

	// Verify nil values come first
	if gateways[0] != nil {
		t.Errorf("Expected nil at index 0, but got %v", gateways[0])
	}
	if gateways[1] != nil {
		t.Errorf("Expected nil at index 1, but got %v", gateways[1])
	}

	// Verify non-nil gateways are sorted by creation timestamp
	expectedOrder := []string{"gateway2", "gateway1", "gateway3"}
	for i, expectedName := range expectedOrder {
		gateway := gateways[i+2] // offset by 2 because of the two nil values
		if gateway == nil {
			t.Errorf("Expected gateway %s at index %d, but got nil", expectedName, i+2)
			continue
		}
		if gateway.GetName() != expectedName {
			t.Errorf("Expected gateway %s at index %d, but got %s", expectedName, i+2, gateway.GetName())
		}
	}
}

func TestSortByCreationTimestamp_Gateways_SameTimestamp(t *testing.T) {
	now := metav1.Now()
	gateways := []client.Object{
		newGateway("gateway1b", now, 1*time.Minute),
		newGateway("gateway2", now, 0),
		newGateway("gateway1a", now, 1*time.Minute),
	}

	SortByCreationTimestamp(gateways)

	expectedOrder := []string{"gateway2", "gateway1a", "gateway1b"}
	for i, gateway := range gateways {
		if gateway.GetName() != expectedOrder[i] {
			t.Errorf("Expected gateway %s at index %d, but got %s", expectedOrder[i], i, gateway.GetName())
		}
	}
}

// Helper to create a Gateway in a given namespace, all with the same timestamp.
func newGatewayInNamespace(namespace, name string, now metav1.Time) *gatewayv1.Gateway {
	return &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:         namespace,
			Name:              name,
			CreationTimestamp: now,
		},
	}
}

// Homonymous objects in different namespaces, created within the same second,
// must have a specified order. metav1.Time has a one-second granularity, so this
// is the common case when a manifest is applied in one go, and the name alone
// cannot separate them.
func TestSortByCreationTimestamp_SameTimestampAndName_DifferentNamespaces(t *testing.T) {
	now := metav1.Now()

	gateways := []client.Object{
		newGatewayInNamespace("ns-c", "tenant", now),
		newGatewayInNamespace("ns-a", "tenant", now),
		newGatewayInNamespace("ns-b", "tenant", now),
	}

	SortByCreationTimestamp(gateways)

	expectedOrder := []string{"ns-a", "ns-b", "ns-c"}
	for i, gateway := range gateways {
		if gateway.GetNamespace() != expectedOrder[i] {
			t.Errorf("Expected namespace %s at index %d, but got %s",
				expectedOrder[i], i, gateway.GetNamespace())
		}
	}
}

// The namespace is compared before the name, so that the order follows the
// "{namespace}/{name}" precedence the Gateway API prescribes for ListenerSets.
func TestSortByCreationTimestamp_NamespaceBeforeName(t *testing.T) {
	now := metav1.Now()

	gateways := []client.Object{
		newGatewayInNamespace("ns-b", "aaa", now),
		newGatewayInNamespace("ns-a", "zzz", now),
	}

	SortByCreationTimestamp(gateways)

	if gateways[0].GetNamespace() != "ns-a" {
		t.Errorf("Expected ns-a/zzz first (namespace wins over name), got %s/%s",
			gateways[0].GetNamespace(), gateways[0].GetName())
	}
}

// Objects equal on timestamp, namespace and name keep their input order: the
// sort must be stable, so that an unrelated element being added or removed
// cannot permute the others.
func TestSortByCreationTimestamp_IsStable(t *testing.T) {
	now := metav1.Now()

	first := newGatewayInNamespace("ns", "same", now)
	second := newGatewayInNamespace("ns", "same", now)
	third := newGatewayInNamespace("ns", "same", now)

	gateways := []client.Object{first, second, third}

	SortByCreationTimestamp(gateways)

	if gateways[0] != client.Object(first) || gateways[1] != client.Object(second) || gateways[2] != client.Object(third) {
		t.Error("Expected fully equal objects to keep their input order")
	}
}

func TestGetHostnamesForRouteWithListener(t *testing.T) {
	hostname := "test.example.com"
	hostnameWildCard := "*.example.com"
	otherHostname := "other.example.com"
	fooHostname := "foo.example.com"
	fooExtendedHostname := "foo.test.example.com"
	otherOrgHostname := "other.test.example.org"

	for i, test := range []struct {
		listenerHostname *string
		routeHostnames   []string
		expected         []string
	}{
		{&hostname, []string{}, []string{hostname}},
		{&hostname, []string{hostname}, []string{hostname}},
		{&hostname, []string{hostname, otherHostname}, []string{hostname}},
		{nil, []string{hostname, fooHostname}, []string{hostname, fooHostname}},
		{nil, []string{}, []string{""}},
		{&hostnameWildCard, []string{}, []string{hostnameWildCard}},
		{&hostnameWildCard, []string{hostname}, []string{hostname}},
		{&hostnameWildCard, []string{hostname, fooExtendedHostname, otherOrgHostname}, []string{fooExtendedHostname, hostname}},
		{&hostname, []string{hostnameWildCard}, []string{hostname}},
		{&hostname, []string{hostnameWildCard, hostname}, []string{hostname}},
		{&hostname, []string{hostnameWildCard, otherHostname, otherOrgHostname}, []string{hostname}},
		{&hostnameWildCard, []string{hostnameWildCard}, []string{hostnameWildCard}},
		{&hostnameWildCard, []string{"*.foo.com"}, []string{}},
	} {
		if result := GetHostnamesForRouteWithListener(test.listenerHostname, test.routeHostnames); !slices.Equal(result, test.expected) {
			t.Errorf("test #%d, Expected %s, got %s", i, test.expected, result)
		}
	}
}

func TestParentRefKeyRoundTrip(t *testing.T) {
	sectionName := gatewayv1.SectionName("http")
	port := gatewayv1.PortNumber(8080)
	refs := []gatewayv1.ParentReference{
		{Name: "gw"},
		{Name: "gw", SectionName: &sectionName},
		{Name: "gw", Port: &port},
		{Name: "gw", SectionName: &sectionName, Port: &port},
	}

	keys := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		key := ParentRefToKey(ref)
		assert.NotContains(t, keys, key, "parent refs must not share a key")
		keys[key] = struct{}{}

		got, err := KeyToParentRef(key)
		require.NoError(t, err)
		assert.Equal(t, ref, got)
	}
}
