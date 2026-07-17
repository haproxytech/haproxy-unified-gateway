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
package controller

import (
	"context"
	"testing"

	v3 "github.com/haproxytech/haproxy-unified-gateway/api/gate/v3"
	"github.com/haproxytech/haproxy-unified-gateway/hug/configuration"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/constants"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	apiv1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

func testExtractGVKBackend(client.Object) schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: v3.GroupName, Version: "v3", Kind: "Backend"}
}

func crObject(ns, name string) client.Object {
	return &v3.Backend{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
}

func routeWithCRBackendFilter(routeNs, crName string) *gatewayv1.HTTPRoute {
	return &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: routeNs, Name: "r"},
		Spec: gatewayv1.HTTPRouteSpec{Rules: []gatewayv1.HTTPRouteRule{{
			BackendRefs: []gatewayv1.HTTPBackendRef{{
				Filters: []gatewayv1.HTTPRouteFilter{{
					Type: gatewayv1.HTTPRouteFilterExtensionRef,
					ExtensionRef: &gatewayv1.LocalObjectReference{
						Group: gatewayv1.Group(v3.GroupName),
						Kind:  "Backend",
						Name:  gatewayv1.ObjectName(crName),
					},
				}},
				BackendRef: gatewayv1.BackendRef{
					BackendObjectReference: gatewayv1.BackendObjectReference{Name: "somesvc"},
				},
			}},
		}}},
	}
}

func routeToService(routeNs, svcName string) *gatewayv1.HTTPRoute {
	return &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: routeNs, Name: "r"},
		Spec: gatewayv1.HTTPRouteSpec{Rules: []gatewayv1.HTTPRouteRule{{
			BackendRefs: []gatewayv1.HTTPBackendRef{{
				BackendRef: gatewayv1.BackendRef{
					BackendObjectReference: gatewayv1.BackendObjectReference{Name: gatewayv1.ObjectName(svcName)},
				},
			}},
		}}},
	}
}

func TestRouteUsesBackendCR(t *testing.T) {
	noSvc := map[types.NamespacedName]struct{}{}

	t.Run("route-level cr-backend ExtensionRef matches", func(t *testing.T) {
		route := routeWithCRBackendFilter("team-a", "tuning")
		if !routeUsesBackendCR(route, crObject("team-a", "tuning"), noSvc, testExtractGVKBackend) {
			t.Error("expected a route-level ExtensionRef match")
		}
	})

	t.Run("route-level match requires the same namespace", func(t *testing.T) {
		route := routeWithCRBackendFilter("team-a", "tuning")
		if routeUsesBackendCR(route, crObject("other", "tuning"), noSvc, testExtractGVKBackend) {
			t.Error("expected no match when the route namespace differs from the CR namespace")
		}
	})

	t.Run("service-annotation match via a target Service", func(t *testing.T) {
		route := routeToService("team-a", "echo")
		annotated := map[types.NamespacedName]struct{}{
			{Namespace: "team-a", Name: "echo"}: {},
		}
		if !routeUsesBackendCR(route, crObject("team-a", "tuning"), annotated, testExtractGVKBackend) {
			t.Error("expected a match through the annotated target Service")
		}
	})

	t.Run("no match when neither applies", func(t *testing.T) {
		route := routeToService("team-a", "echo")
		if routeUsesBackendCR(route, crObject("team-a", "tuning"), noSvc, testExtractGVKBackend) {
			t.Error("expected no match without an ExtensionRef or an annotated Service")
		}
	})
}

func svcWithAnnotation(ns, name, value string) *apiv1.Service {
	s := &apiv1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
	if value != "" {
		s.Annotations = map[string]string{constants.ServiceBackendCRAnnotation: value}
	}
	return s
}

func TestServicesReferencingBackendCR(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := apiv1.AddToScheme(scheme); err != nil {
		t.Fatalf("scheme: %v", err)
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		svcWithAnnotation("team-a", "svc-name-only", "tuning"),      // matches (same ns)
		svcWithAnnotation("team-a", "svc-ns-name", "team-a/tuning"), // matches (explicit ns)
		svcWithAnnotation("team-b", "svc-crossns", "team-a/tuning"), // matches (cross-ns, grant checked at merge)
		svcWithAnnotation("team-a", "svc-other-cr", "other/tuning"), // other CR, ignored
		svcWithAnnotation("team-a", "svc-none", ""),                 // no annotation
		svcWithAnnotation("team-b", "svc-other-ns-name", "tuning"),  // resolves to team-b/tuning, ignored
	).Build()

	got := servicesReferencingBackendCR(context.Background(), cl, types.NamespacedName{Namespace: "team-a", Name: "tuning"})

	want := map[types.NamespacedName]struct{}{
		{Namespace: "team-a", Name: "svc-name-only"}: {},
		{Namespace: "team-a", Name: "svc-ns-name"}:   {},
		{Namespace: "team-b", Name: "svc-crossns"}:   {},
	}
	if len(got) != len(want) {
		t.Fatalf("want %d services, got %d: %v", len(want), len(got), got)
	}
	for k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("expected %s to be matched", k)
		}
	}
}

// routeToServiceNs builds an HTTPRoute whose single backendRef targets svcName,
// with an explicit svcNs when non-empty (a cross-namespace Service reference).
func routeToServiceNs(routeNs, svcName, svcNs string) *gatewayv1.HTTPRoute {
	ref := gatewayv1.BackendObjectReference{Name: gatewayv1.ObjectName(svcName)}
	if svcNs != "" {
		ns := gatewayv1.Namespace(svcNs)
		ref.Namespace = &ns
	}
	return &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: routeNs, Name: "r"},
		Spec: gatewayv1.HTTPRouteSpec{Rules: []gatewayv1.HTTPRouteRule{{
			BackendRefs: []gatewayv1.HTTPBackendRef{{
				BackendRef: gatewayv1.BackendRef{BackendObjectReference: ref},
			}},
		}}},
	}
}

func TestHTTPRouteHasCrossNamespaceRefTo(t *testing.T) {
	empty := map[configuration.NamespaceNameValue]struct{}{}

	t.Run("direct cross-namespace backendRef matches", func(t *testing.T) {
		route := routeToServiceNs("team-a", "echo", "shared")
		if !httpRouteHasCrossNamespaceRefTo(*route, "shared", empty) {
			t.Error("expected a direct cross-namespace backendRef to match")
		}
	})

	t.Run("same-namespace backendRef does not panic and matches only via the set", func(t *testing.T) {
		route := routeToServiceNs("team-a", "echo", "") // backendRef.Namespace nil (same-ns)
		// No annotated service: must not panic, must not match.
		if httpRouteHasCrossNamespaceRefTo(*route, "shared", empty) {
			t.Error("expected no match for a same-namespace backendRef with an empty set")
		}
		// Service resolved in the route namespace is in the set: match.
		set := map[configuration.NamespaceNameValue]struct{}{
			{Namespace: "team-a", Name: "echo"}: {},
		}
		if !httpRouteHasCrossNamespaceRefTo(*route, "shared", set) {
			t.Error("expected a match through the annotated Service set")
		}
	})

	t.Run("no match when the service is not in the set", func(t *testing.T) {
		route := routeToServiceNs("team-a", "echo", "")
		set := map[configuration.NamespaceNameValue]struct{}{
			{Namespace: "other", Name: "echo"}: {},
		}
		if httpRouteHasCrossNamespaceRefTo(*route, "shared", set) {
			t.Error("expected no match when the target service is not in the set")
		}
	})
}

// tlsRouteToServiceNs builds a TLSRoute whose single backendRef targets svcName,
// with an explicit svcNs when non-empty (a cross-namespace Service reference).
func tlsRouteToServiceNs(routeNs, svcName, svcNs string) *gatewayv1alpha2.TLSRoute {
	ref := gatewayv1.BackendObjectReference{Name: gatewayv1.ObjectName(svcName)}
	if svcNs != "" {
		ns := gatewayv1.Namespace(svcNs)
		ref.Namespace = &ns
	}
	return &gatewayv1alpha2.TLSRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: routeNs, Name: "r"},
		Spec: gatewayv1alpha2.TLSRouteSpec{Rules: []gatewayv1alpha2.TLSRouteRule{{
			BackendRefs: []gatewayv1.BackendRef{{BackendObjectReference: ref}},
		}}},
	}
}

func TestTLSRouteHasCrossNamespaceRefTo(t *testing.T) {
	empty := map[configuration.NamespaceNameValue]struct{}{}

	t.Run("direct cross-namespace backendRef matches", func(t *testing.T) {
		route := tlsRouteToServiceNs("team-a", "echo", "shared")
		if !tlsRouteHasCrossNamespaceRefTo(*route, "shared", empty) {
			t.Error("expected a direct cross-namespace backendRef to match")
		}
	})

	t.Run("same-namespace backendRef does not panic and matches only via the set", func(t *testing.T) {
		route := tlsRouteToServiceNs("team-a", "echo", "") // backendRef.Namespace nil (same-ns)
		if tlsRouteHasCrossNamespaceRefTo(*route, "shared", empty) {
			t.Error("expected no match for a same-namespace backendRef with an empty set")
		}
		set := map[configuration.NamespaceNameValue]struct{}{
			{Namespace: "team-a", Name: "echo"}: {},
		}
		if !tlsRouteHasCrossNamespaceRefTo(*route, "shared", set) {
			t.Error("expected a match through the annotated Service set")
		}
	})

	t.Run("no match when the service is not in the set", func(t *testing.T) {
		route := tlsRouteToServiceNs("team-a", "echo", "")
		set := map[configuration.NamespaceNameValue]struct{}{
			{Namespace: "other", Name: "echo"}: {},
		}
		if tlsRouteHasCrossNamespaceRefTo(*route, "shared", set) {
			t.Error("expected no match when the target service is not in the set")
		}
	})
}

func TestTLSRouteUsesServiceBackendCR(t *testing.T) {
	t.Run("match through an annotated target Service", func(t *testing.T) {
		route := tlsRouteToServiceNs("team-a", "echo", "")
		annotated := map[types.NamespacedName]struct{}{
			{Namespace: "team-a", Name: "echo"}: {},
		}
		if !tlsRouteUsesServiceBackendCR(route, annotated) {
			t.Error("expected a match through the annotated target Service")
		}
	})

	t.Run("no match without an annotated Service", func(t *testing.T) {
		route := tlsRouteToServiceNs("team-a", "echo", "")
		if tlsRouteUsesServiceBackendCR(route, map[types.NamespacedName]struct{}{}) {
			t.Error("expected no match with an empty set")
		}
	})
}

func TestEnqueueTLSRouteForReferenceGrant(t *testing.T) {
	t.Run("all same-namespace does not enqueue", func(t *testing.T) {
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
			svcWithAnnotation("ns1", "echo", "tuning"),
			tlsRouteToServiceNs("ns1", "echo", ""),
		).Build()

		reqs := enqueueTLSRouteForReferenceGrant(cl, testExtractGVKBackend)(context.Background(), refGrant("ns1"))
		if len(reqs) != 0 {
			t.Fatalf("expected no enqueue for an all-same-namespace setup, got %v", reqs)
		}
	})

	t.Run("cross-namespace annotation enqueues the route", func(t *testing.T) {
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
			svcWithAnnotation("team-a", "echo", "shared/tuning"),
			tlsRouteToServiceNs("team-a", "echo", ""),
		).Build()

		reqs := enqueueTLSRouteForReferenceGrant(cl, testExtractGVKBackend)(context.Background(), refGrant("shared"))
		if len(reqs) != 1 || reqs[0].Namespace != "team-a" || reqs[0].Name != "r" {
			t.Fatalf("expected the team-a route to be enqueued, got %v", reqs)
		}
	})
}

func TestEnqueueTLSRouteForBackendCR(t *testing.T) {
	t.Run("route to an annotated service is enqueued", func(t *testing.T) {
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
			svcWithAnnotation("team-a", "echo", "tuning"),
			tlsRouteToServiceNs("team-a", "echo", ""),
		).Build()

		reqs := enqueueTLSRouteForBackendCR(utils.NewDedicatedNamespaces(nil))(cl, testExtractGVKBackend)(
			context.Background(), crObject("team-a", "tuning"),
		)
		if len(reqs) != 1 || reqs[0].Namespace != "team-a" || reqs[0].Name != "r" {
			t.Fatalf("expected the team-a route to be enqueued, got %v", reqs)
		}
	})

	t.Run("route to a non-annotated service is not enqueued", func(t *testing.T) {
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
			svcWithAnnotation("team-a", "echo", ""),
			tlsRouteToServiceNs("team-a", "echo", ""),
		).Build()

		reqs := enqueueTLSRouteForBackendCR(utils.NewDedicatedNamespaces(nil))(cl, testExtractGVKBackend)(
			context.Background(), crObject("team-a", "tuning"),
		)
		if len(reqs) != 0 {
			t.Fatalf("expected no enqueue when the target service is not annotated, got %v", reqs)
		}
	})
}

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{apiv1.AddToScheme, gatewayv1.Install, gatewayv1alpha2.Install, gatewayv1beta1.Install} {
		if err := add(scheme); err != nil {
			t.Fatalf("scheme: %v", err)
		}
	}
	return scheme
}

func refGrant(ns string) *gatewayv1beta1.ReferenceGrant {
	return &gatewayv1beta1.ReferenceGrant{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "grant"}}
}

func TestEnqueueHTTPRouteForReferenceGrant(t *testing.T) {
	t.Run("all same-namespace does not enqueue", func(t *testing.T) {
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
			svcWithAnnotation("ns1", "echo", "tuning"), // same-namespace CR reference
			routeToServiceNs("ns1", "echo", ""),
		).Build()

		reqs := enqueueHTTPRouteForReferenceGrant(cl, testExtractGVKBackend)(context.Background(), refGrant("ns1"))
		if len(reqs) != 0 {
			t.Fatalf("expected no enqueue for an all-same-namespace setup, got %v", reqs)
		}
	})

	t.Run("cross-namespace annotation enqueues the route", func(t *testing.T) {
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
			svcWithAnnotation("team-a", "echo", "shared/tuning"), // cross-ns CR in the grant namespace
			routeToServiceNs("team-a", "echo", ""),
		).Build()

		reqs := enqueueHTTPRouteForReferenceGrant(cl, testExtractGVKBackend)(context.Background(), refGrant("shared"))
		if len(reqs) != 1 || reqs[0].Namespace != "team-a" || reqs[0].Name != "r" {
			t.Fatalf("expected the team-a route to be enqueued, got %v", reqs)
		}
	})
}

func ingressWithClass(ns, name, className string) *networkingv1.Ingress {
	ing := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
	if className != "" {
		ing.Spec.IngressClassName = &className
	}
	return ing
}

func ingressClass(name string, isDefault bool) *networkingv1.IngressClass {
	ic := &networkingv1.IngressClass{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if isDefault {
		ic.Annotations = map[string]string{constants.DefaultIngressClassAnnotation: "true"}
	}
	return ic
}

func TestEnqueueIngressesForIngressClass(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := networkingv1.AddToScheme(scheme); err != nil {
		t.Fatalf("scheme: %v", err)
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		ingressWithClass("team-a", "named", "haproxy"), // references haproxy by name
		ingressWithClass("team-a", "unclassed", ""),    // depends on the default class
		ingressWithClass("team-b", "other", "nginx"),   // unrelated class
	).Build()

	enqueued := func(o *networkingv1.IngressClass) map[types.NamespacedName]struct{} {
		reqs := enqueueIngressesForIngressClass(cl, nil)(context.Background(), o)
		got := make(map[types.NamespacedName]struct{}, len(reqs))
		for _, r := range reqs {
			got[r.NamespacedName] = struct{}{}
		}
		return got
	}

	t.Run("named class enqueues only the Ingresses referencing it", func(t *testing.T) {
		got := enqueued(ingressClass("haproxy", false))
		if _, ok := got[types.NamespacedName{Namespace: "team-a", Name: "named"}]; !ok {
			t.Error("expected the named-referencing Ingress to be enqueued")
		}
		if _, ok := got[types.NamespacedName{Namespace: "team-a", Name: "unclassed"}]; ok {
			t.Error("did not expect the unclassed Ingress for a non-default class")
		}
		if len(got) != 1 {
			t.Fatalf("expected exactly one request, got %v", got)
		}
	})

	t.Run("default class also enqueues unclassed Ingresses", func(t *testing.T) {
		got := enqueued(ingressClass("haproxy", true))
		if _, ok := got[types.NamespacedName{Namespace: "team-a", Name: "named"}]; !ok {
			t.Error("expected the named-referencing Ingress to be enqueued")
		}
		if _, ok := got[types.NamespacedName{Namespace: "team-a", Name: "unclassed"}]; !ok {
			t.Error("expected the unclassed Ingress to be enqueued for a default class")
		}
		if len(got) != 2 {
			t.Fatalf("expected two requests, got %v", got)
		}
	})

	t.Run("unrelated non-default class enqueues nothing here", func(t *testing.T) {
		got := enqueued(ingressClass("does-not-exist", false))
		if len(got) != 0 {
			t.Fatalf("expected no requests, got %v", got)
		}
	})
}
