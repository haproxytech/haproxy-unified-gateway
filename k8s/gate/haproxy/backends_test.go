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
package haproxy

import (
	"io"
	"log/slog"
	"testing"

	v3 "github.com/haproxytech/haproxy-unified-gateway/api/gate/v3"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// testBackendExtractGVK resolves the HUG Backend object type to its GVK, enough
// for IsFilterExtensionRefKindSupported to recognise a cr-backend ExtensionRef.
func testBackendExtractGVK(client.Object) schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: v3.GroupName, Version: "v3", Kind: "Backend"}
}

// newTestBackendMgr builds a minimal HaproxyConfMgrImpl exercising only the
// Backend CR stores and the GVK extractor.
func newTestBackendMgr(backendCRs map[k8stypes.NamespacedName]*v3.Backend, ingressBackendCRs map[k8stypes.NamespacedName]*unstructured.Unstructured) *HaproxyConfMgrImpl {
	if backendCRs == nil {
		backendCRs = map[k8stypes.NamespacedName]*v3.Backend{}
	}
	if ingressBackendCRs == nil {
		ingressBackendCRs = map[k8stypes.NamespacedName]*unstructured.Unstructured{}
	}
	mgr := &HaproxyConfMgrImpl{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		controllerStore: &tree.ControllerStore{
			ClusterStore: &store.ClusterStore{
				BackendCRs:        backendCRs,
				IngressBackendCRs: ingressBackendCRs,
			},
		},
	}
	mgr.params.extractGVK = testBackendExtractGVK
	return mgr
}

// crBackendRef builds an HTTPBackendRef carrying a cr-backend ExtensionRef
// pointing at the named Backend CR (HUG group).
func crBackendRef(name string) gatewayv1.HTTPBackendRef {
	return gatewayv1.HTTPBackendRef{
		Filters: []gatewayv1.HTTPRouteFilter{{
			Type: gatewayv1.HTTPRouteFilterExtensionRef,
			ExtensionRef: &gatewayv1.LocalObjectReference{
				Group: gatewayv1.Group(v3.GroupName),
				Kind:  "Backend",
				Name:  gatewayv1.ObjectName(name),
			},
		}},
	}
}

func TestIngressCRBackendReferencesHugCR(t *testing.T) {
	nsName := k8stypes.NamespacedName{Namespace: "default", Name: "my-backend"}

	t.Run("name resolves only to a HUG Backend CR is a mismatch", func(t *testing.T) {
		mgr := newTestBackendMgr(map[k8stypes.NamespacedName]*v3.Backend{nsName: {}}, nil)
		got, mismatch := mgr.ingressCRBackendReferencesHugCR(crBackendRef(nsName.Name), nsName.Namespace)
		if !mismatch {
			t.Fatal("expected a type mismatch")
		}
		if got != nsName.Name {
			t.Errorf("offending name: want %q, got %q", nsName.Name, got)
		}
	})

	t.Run("name resolves to a foreign Ingress Backend CR is fine", func(t *testing.T) {
		mgr := newTestBackendMgr(nil, map[k8stypes.NamespacedName]*unstructured.Unstructured{nsName: newIngressBackendUnstructured("http", "roundrobin")})
		if _, mismatch := mgr.ingressCRBackendReferencesHugCR(crBackendRef(nsName.Name), nsName.Namespace); mismatch {
			t.Fatal("expected no mismatch when the foreign CR exists")
		}
	})

	t.Run("name present in both stores is fine (foreign wins)", func(t *testing.T) {
		mgr := newTestBackendMgr(
			map[k8stypes.NamespacedName]*v3.Backend{nsName: {}},
			map[k8stypes.NamespacedName]*unstructured.Unstructured{nsName: newIngressBackendUnstructured("http", "roundrobin")},
		)
		if _, mismatch := mgr.ingressCRBackendReferencesHugCR(crBackendRef(nsName.Name), nsName.Namespace); mismatch {
			t.Fatal("expected no mismatch when the foreign CR also exists")
		}
	})

	t.Run("unknown name is not a mismatch (dangling reference)", func(t *testing.T) {
		mgr := newTestBackendMgr(nil, nil)
		if _, mismatch := mgr.ingressCRBackendReferencesHugCR(crBackendRef(nsName.Name), nsName.Namespace); mismatch {
			t.Fatal("expected no mismatch for an unknown reference")
		}
	})

	t.Run("no ExtensionRef filter is not a mismatch", func(t *testing.T) {
		mgr := newTestBackendMgr(map[k8stypes.NamespacedName]*v3.Backend{nsName: {}}, nil)
		if _, mismatch := mgr.ingressCRBackendReferencesHugCR(gatewayv1.HTTPBackendRef{}, nsName.Namespace); mismatch {
			t.Fatal("expected no mismatch without an ExtensionRef filter")
		}
	})
}

// TestWarnMistypedIngressBackendsKeepsBackend verifies that an Ingress-origin
// backend whose cr-backend references a HUG Backend CR (wrong type) is NOT set
// aside: the backend stays in Upserted (it is still built with defaults, the
// reference merely does not resolve in the Ingress store), matching the Gateway
// API route behaviour.
func TestWarnMistypedIngressBackendsKeepsBackend(t *testing.T) {
	const ns = "ingress"
	const backendName = "hug_ingress_nginx-svc_80_abc"
	crNsName := k8stypes.NamespacedName{Namespace: ns, Name: "be-ingress"}
	// A synthetic (ingress-origin) owner route; the "ing:" prefix marks it synthetic.
	syntheticOwner := client.ObjectKey{Namespace: ns, Name: "ing:web:0"}

	mgr := newTestBackendMgr(
		map[k8stypes.NamespacedName]*v3.Backend{crNsName: {}}, // HUG CR present
		nil, // foreign Ingress Backend CR store empty -> type mismatch
	)
	mgr.backendOwners = BackendReferencedBy{
		owners: map[string]map[BackendOwnerType]map[client.ObjectKey]int64{
			backendName: {BackendOwnerTypeHTTPRoute: {syntheticOwner: 1}},
		},
	}
	mgr.backendsImpactedInCycle = BackendsImpactedInCycle{
		Upserted: map[string]map[client.ObjectKey]BackendImpactedInCycle{
			backendName: {syntheticOwner: {BackendRef: crBackendRef("be-ingress")}},
		},
		Deleted:      map[string]struct{}{},
		Unreferenced: map[string]struct{}{},
	}

	mgr.warnMistypedIngressBackends()

	if _, ok := mgr.backendsImpactedInCycle.Upserted[backendName]; !ok {
		t.Fatal("expected the mistyped Ingress backend to remain in Upserted (built with defaults), not set aside")
	}
}

func TestResolveBackendCR(t *testing.T) {
	nsName := k8stypes.NamespacedName{Namespace: "default", Name: "my-backend"}

	const (
		fromGatewayAPI = false
		fromIngress    = true
	)

	t.Run("Gateway API route resolves the HUG Backend CR only", func(t *testing.T) {
		want := &v3.Backend{}
		want.Spec.Mode = "http"
		mgr := newTestBackendMgr(
			map[k8stypes.NamespacedName]*v3.Backend{nsName: want},
			// A foreign CR with the same key must never be consulted for a Gateway
			// API route.
			map[k8stypes.NamespacedName]*unstructured.Unstructured{nsName: newIngressBackendUnstructured("tcp", "leastconn")},
		)

		got, ok := mgr.resolveBackendCR(nsName, fromGatewayAPI)
		if !ok {
			t.Fatal("expected the native Backend CR to be found")
		}
		if got != want {
			t.Fatalf("expected the native Backend CR pointer, got a different one (mode=%q)", got.Spec.Mode)
		}
	})

	t.Run("Ingress resolves the foreign Backend CR only, converted on the fly", func(t *testing.T) {
		mgr := newTestBackendMgr(
			nil,
			map[k8stypes.NamespacedName]*unstructured.Unstructured{nsName: newIngressBackendUnstructured("http", "roundrobin")},
		)

		got, ok := mgr.resolveBackendCR(nsName, fromIngress)
		if !ok {
			t.Fatal("expected the foreign Ingress Backend CR to be resolved")
		}
		if got.Spec.Mode != "http" {
			t.Errorf("Mode: want %q, got %q", "http", got.Spec.Mode)
		}
		// Balance is a nested pointer field of client-native's models.Backend:
		// this proves the JSON round-trip populates non-scalar fields correctly.
		if got.Spec.Balance == nil || got.Spec.Balance.Algorithm == nil {
			t.Fatal("expected Balance.Algorithm to be populated")
		}
		if *got.Spec.Balance.Algorithm != "roundrobin" {
			t.Errorf("Balance.Algorithm: want %q, got %q", "roundrobin", *got.Spec.Balance.Algorithm)
		}
	})

	t.Run("Ingress cannot borrow a HUG Backend CR", func(t *testing.T) {
		// Only a HUG Backend CR exists under the name; an Ingress reference must not
		// resolve it (type confusion across API groups).
		mgr := newTestBackendMgr(
			map[k8stypes.NamespacedName]*v3.Backend{nsName: {}},
			nil,
		)
		if _, ok := mgr.resolveBackendCR(nsName, fromIngress); ok {
			t.Fatal("expected an Ingress not to resolve a HUG Backend CR")
		}
	})

	t.Run("Gateway API route cannot borrow a foreign Ingress Backend CR", func(t *testing.T) {
		mgr := newTestBackendMgr(
			nil,
			map[k8stypes.NamespacedName]*unstructured.Unstructured{nsName: newIngressBackendUnstructured("http", "roundrobin")},
		)
		if _, ok := mgr.resolveBackendCR(nsName, fromGatewayAPI); ok {
			t.Fatal("expected a Gateway API route not to resolve a foreign Ingress Backend CR")
		}
	})

	t.Run("unknown reference is not found", func(t *testing.T) {
		mgr := newTestBackendMgr(nil, nil)
		if _, ok := mgr.resolveBackendCR(nsName, fromIngress); ok {
			t.Fatal("expected no Backend CR to be found")
		}
	})
}

// newIngressBackendUnstructured builds a generic (unstructured) foreign
// kubernetes-ingress Backend CR whose spec mirrors client-native's models.Backend.
func newIngressBackendUnstructured(mode, balanceAlgorithm string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "ingress.v3.haproxy.org/v3",
		"kind":       "Backend",
		"metadata": map[string]any{
			"name":      "my-backend",
			"namespace": "default",
		},
		"spec": map[string]any{
			"mode": mode,
			"balance": map[string]any{
				"algorithm": balanceAlgorithm,
			},
		},
	}}
}

// A redirect rule dropped by match validation must not create or keep a
// redirect pseudo-backend, mirroring what the route-map layer already does.
func TestRedirectRuleWithInvalidMatchesKeepsNoBackend(t *testing.T) {
	redirectFilters := []gatewayv1.HTTPRouteFilter{{
		Type:            gatewayv1.HTTPRouteFilterRequestRedirect,
		RequestRedirect: &gatewayv1.HTTPRequestRedirectFilter{},
	}}
	routeKey := k8stypes.NamespacedName{Namespace: "ns", Name: "route"}

	newMgr := func() *HaproxyConfMgrImpl {
		mgr := newTestBackendMgr(nil, nil)
		mgr.params.LinkID = "hug"
		mgr.backendOwners = NewBackendOwners()
		mgr.backendsImpactedInCycle = BackendsImpactedInCycle{
			Upserted:     map[string]map[client.ObjectKey]BackendImpactedInCycle{},
			Deleted:      map[string]struct{}{},
			Unreferenced: map[string]struct{}{},
		}
		return mgr
	}
	newRoute := func(matchesValid bool) *tree.HTTPRoute {
		return &tree.HTTPRoute{
			K8sResource: &gatewayv1.HTTPRoute{
				Namespace: "ns", Name: "route", Generation: 1,
			},
			Valid: matchesValid,
			Rules: []*tree.HTTPRouteRule{{
				K8sResource:  gatewayv1.HTTPRouteRule{Filters: redirectFilters},
				CheckFilters: tree.CheckResult{Valid: true},
				CheckMatches: tree.CheckResult{Valid: matchesValid},
				Valid:        matchesValid,
			}},
		}
	}

	t.Run("invalid matches create no redirect backend", func(t *testing.T) {
		mgr := newMgr()
		if err := mgr.upsertHTTPRouteBackends(routeKey, newRoute(false)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(mgr.backendsImpactedInCycle.Upserted) != 0 {
			t.Errorf("expected no upserted backend, got %v", mgr.backendsImpactedInCycle.Upserted)
		}
		if owned := mgr.backendOwners.getBackendsReferencedByHTTPRoute(routeKey); len(owned) != 0 {
			t.Errorf("expected no owned backend, got %v", owned)
		}
	})

	t.Run("previously created redirect backend is released", func(t *testing.T) {
		mgr := newMgr()
		beName := mgr.getRedirectBackendName(redirectFilters)
		if err := mgr.backendOwners.addHTTPRoute(beName, routeKey, newRoute(true).K8sResource); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mgr.upsertHTTPRouteBackends(routeKey, newRoute(false)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := mgr.backendsImpactedInCycle.Unreferenced[beName]; !ok {
			t.Errorf("expected %q to be unreferenced", beName)
		}
		if owned := mgr.backendOwners.getBackendsReferencedByHTTPRoute(routeKey); len(owned) != 0 {
			t.Errorf("expected the route to own no backend, got %v", owned)
		}
	})

	t.Run("valid matches still create the redirect backend", func(t *testing.T) {
		mgr := newMgr()
		beName := mgr.getRedirectBackendName(redirectFilters)
		if err := mgr.upsertHTTPRouteBackends(routeKey, newRoute(true)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := mgr.backendsImpactedInCycle.Upserted[beName][routeKey]; !ok {
			t.Errorf("expected the redirect backend %q to be upserted", beName)
		}
	})
}
