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
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/constants"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
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
