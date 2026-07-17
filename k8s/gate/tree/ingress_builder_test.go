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
package tree

import (
	"io"
	"log/slog"
	"path"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
)

const isDefaultClassAnnotation = "ingressclass.kubernetes.io/is-default-class"

// ingressClassFixture describes an IngressClass to seed the ClusterStore with.
type ingressClassFixture struct {
	name       string
	controller string
	isDefault  bool
}

func mkIngressClass(f ingressClassFixture) *networkingv1.IngressClass {
	ic := &networkingv1.IngressClass{
		ObjectMeta: metav1.ObjectMeta{Name: f.name},
		Spec:       networkingv1.IngressClassSpec{Controller: f.controller},
	}
	if f.isDefault {
		ic.Annotations = map[string]string{isDefaultClassAnnotation: "true"}
	}
	return ic
}

func TestIsIngressClassSupported(t *testing.T) {
	// ingressClassParameter used by the "default" branch (controller started with
	// --ingress-class). "matching" the controller then means the IngressClass
	// Spec.Controller equals haproxy.org/ingress-controller/<param>.
	const param = "prod"
	matching := path.Join(CONTROLLER, param) // haproxy.org/ingress-controller/prod
	nonMatching := "example.com/other-controller"

	tests := []struct {
		name            string
		ingressClasses  []ingressClassFixture
		ingressClass    string // Ingress spec.ingressClassName ("" == none)
		controllerParam string // --ingress-class value ("" == unset)
		allowEmptyClass bool   // --empty-ingress-class
		want            bool
	}{
		// -------------------------------------------------------------------
		// Group A — controller configured with --ingress-class (default branch)
		// -------------------------------------------------------------------

		// IngressClass matching the controller
		{
			name:            "match: ingress references the matching class -> accepted",
			ingressClasses:  []ingressClassFixture{{name: "hug", controller: matching}},
			ingressClass:    "hug",
			controllerParam: param,
			want:            true,
		},
		{
			name: "match: ingress references a non-matching class -> rejected",
			ingressClasses: []ingressClassFixture{
				{name: "hug", controller: matching},
				{name: "other", controller: nonMatching},
			},
			ingressClass:    "other",
			controllerParam: param,
			want:            false,
		},
		{
			name:            "match: ingress without class (no default) -> rejected",
			ingressClasses:  []ingressClassFixture{{name: "hug", controller: matching}},
			ingressClass:    "",
			controllerParam: param,
			want:            false,
		},

		// IngressClass NOT matching the controller
		{
			name:            "no-match: ingress references the (non-matching) class -> rejected",
			ingressClasses:  []ingressClassFixture{{name: "hug", controller: nonMatching}},
			ingressClass:    "hug",
			controllerParam: param,
			want:            false,
		},
		{
			name:            "no-match: ingress references a different class -> rejected",
			ingressClasses:  []ingressClassFixture{{name: "hug", controller: nonMatching}},
			ingressClass:    "absent",
			controllerParam: param,
			want:            false,
		},
		{
			name:            "no-match: ingress without class -> rejected",
			ingressClasses:  []ingressClassFixture{{name: "hug", controller: nonMatching}},
			ingressClass:    "",
			controllerParam: param,
			want:            false,
		},

		// Default IngressClass (is-default-class: "true")
		{
			name:            "default-match: ingress references the default matching class -> accepted",
			ingressClasses:  []ingressClassFixture{{name: "hug", controller: matching, isDefault: true}},
			ingressClass:    "hug",
			controllerParam: param,
			want:            true,
		},
		{
			name:            "default-no-match: ingress references the default non-matching class -> rejected",
			ingressClasses:  []ingressClassFixture{{name: "hug", controller: nonMatching, isDefault: true}},
			ingressClass:    "hug",
			controllerParam: param,
			want:            false,
		},
		{
			name:            "default-match: ingress without class picks the default -> accepted",
			ingressClasses:  []ingressClassFixture{{name: "hug", controller: matching, isDefault: true}},
			ingressClass:    "",
			controllerParam: param,
			want:            true,
		},

		// --empty-ingress-class option
		{
			name:            "empty-class option: ingress without class -> accepted",
			ingressClasses:  []ingressClassFixture{{name: "hug", controller: matching}},
			ingressClass:    "",
			controllerParam: param,
			allowEmptyClass: true,
			want:            true,
		},
		{
			name: "empty-class option: ingress with a non-matching class -> rejected",
			ingressClasses: []ingressClassFixture{
				{name: "hug", controller: matching},
				{name: "other", controller: nonMatching},
			},
			ingressClass:    "other",
			controllerParam: param,
			allowEmptyClass: true,
			want:            false,
		},

		// -------------------------------------------------------------------
		// Group B — controller without --ingress-class (case "" branch): it
		// handles the bare controller name haproxy.org/ingress-controller.
		// -------------------------------------------------------------------
		{
			name:            "no-param: ingress references a class bound to the bare controller -> accepted",
			ingressClasses:  []ingressClassFixture{{name: "hug", controller: CONTROLLER}},
			ingressClass:    "hug",
			controllerParam: "",
			want:            true,
		},
		{
			// Everything matches in the cluster except the controller is not tied
			// to this IngressClass: the class carries a suffixed controller while
			// this instance runs with no --ingress-class.
			name:            "no-param: class bound to a suffixed controller -> rejected",
			ingressClasses:  []ingressClassFixture{{name: "hug", controller: matching}},
			ingressClass:    "hug",
			controllerParam: "",
			want:            false,
		},
		{
			name:            "no-param: ingress without class and no IngressClass at all -> accepted",
			ingressClasses:  nil,
			ingressClass:    "",
			controllerParam: "",
			want:            true,
		},
		{
			name:            "no-param: ingress without class picks a default bound to the bare controller -> accepted",
			ingressClasses:  []ingressClassFixture{{name: "hug", controller: CONTROLLER, isDefault: true}},
			ingressClass:    "",
			controllerParam: "",
			want:            true,
		},
		{
			name:            "no-param: ingress references a class bound to another controller -> rejected",
			ingressClasses:  []ingressClassFixture{{name: "hug", controller: nonMatching}},
			ingressClass:    "hug",
			controllerParam: "",
			want:            false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			classes := make(map[types.NamespacedName]*networkingv1.IngressClass, len(tc.ingressClasses))
			for _, f := range tc.ingressClasses {
				classes[types.NamespacedName{Name: f.name}] = mkIngressClass(f)
			}
			cs := &ControllerStore{ClusterStore: &store.ClusterStore{IngressClasses: classes}}

			got := cs.isIngressClassSupported(tc.ingressClass, tc.controllerParam, tc.allowEmptyClass)
			assert.Equal(t, tc.want, got)
		})
	}
}

// mkIngressWithRule builds an Ingress with a single numeric-port rule (so it
// converts to one synthetic HTTPRoute without a Service lookup).
func mkIngressWithRule(ns, name, className string) *networkingv1.Ingress {
	ing := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
	if className != "" {
		ing.Spec.IngressClassName = &className
	}
	ing.Spec.Rules = []networkingv1.IngressRule{{
		Host: "example.test",
		IngressRuleValue: networkingv1.IngressRuleValue{
			HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{
					Path: "/",
					Backend: networkingv1.IngressBackend{
						Service: &networkingv1.IngressServiceBackend{
							Name: "svc",
							Port: networkingv1.ServiceBackendPort{Number: 80},
						},
					},
				}},
			},
		},
	}}
	return ing
}

// TestComputeTreeUpdatesReevaluatesOnIngressClassChange covers the re-evaluation
// of Ingresses driven purely by an IngressClass change in the batch (with the
// Ingress itself absent from Updates.Ingresses), which is what happens when an
// IngressClass deletion and the Ingress re-enqueue land in different batches.
func TestComputeTreeUpdatesReevaluatesOnIngressClassChange(t *testing.T) {
	const ns = "app"
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))

	// newBuilder wires an IngressBuilder over a store seeded with a single
	// Ingress and a synthetic route already present in the GateTree, plus an
	// IngressClass update to react to.
	newBuilder := func(
		ingress *networkingv1.Ingress,
		classes map[types.NamespacedName]*networkingv1.IngressClass,
		classUpdate map[types.NamespacedName]store.Update[*networkingv1.IngressClass],
		controllerParam string, allowEmpty bool,
	) (*IngressBuilderImpl, types.NamespacedName) {
		ingKey := types.NamespacedName{Namespace: ingress.Namespace, Name: ingress.Name}
		syntheticName := utils.MangleIngressName(ingress, "0")
		syntheticKey := types.NamespacedName{Namespace: ns, Name: syntheticName}

		updates := store.NewClusterUpdates()
		updates.IngressClasses = classUpdate

		cs := &store.ClusterStore{
			Ingresses:      map[types.NamespacedName]*networkingv1.Ingress{ingKey: ingress},
			IngressClasses: classes,
			Updates:        updates,
		}
		gt := &GateTree{HTTPRoutes: map[types.NamespacedName]*HTTPRoute{
			syntheticKey: {K8sResource: &gatewayv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: syntheticName},
			}},
		}}
		b := &IngressBuilderImpl{ControllerStore: &ControllerStore{
			ClusterStore:      cs,
			GateTree:          gt,
			Logger:            discard,
			IngressClass:      controllerParam,
			EmptyIngressClass: allowEmpty,
		}}
		return b, syntheticKey
	}

	t.Run("named class deleted -> ineligible -> synthetic route deleted", func(t *testing.T) {
		ingress := mkIngressWithRule(ns, "web", "haproxy")
		deleted := mkIngressClass(ingressClassFixture{name: "haproxy", controller: CONTROLLER})
		b, syntheticKey := newBuilder(
			ingress,
			map[types.NamespacedName]*networkingv1.IngressClass{}, // class gone from the store
			map[types.NamespacedName]store.Update[*networkingv1.IngressClass]{
				{Name: "haproxy"}: {Status: store.StatusDeleted, OldObject: deleted},
			},
			"", false,
		)

		b.ComputeTreeUpdates()

		got, ok := b.ClusterStore.Updates.HTTPRoutes[syntheticKey]
		assert.True(t, ok, "expected the synthetic route to be scheduled")
		assert.Equal(t, store.StatusDeleted, got.Status)
	})

	t.Run("named class still eligible on update -> synthetic route re-converted", func(t *testing.T) {
		ingress := mkIngressWithRule(ns, "web", "haproxy")
		class := mkIngressClass(ingressClassFixture{name: "haproxy", controller: CONTROLLER})
		b, syntheticKey := newBuilder(
			ingress,
			map[types.NamespacedName]*networkingv1.IngressClass{{Name: "haproxy"}: class},
			map[types.NamespacedName]store.Update[*networkingv1.IngressClass]{
				{Name: "haproxy"}: {Status: store.StatusUpserted, NewObject: class},
			},
			"", false,
		)

		b.ComputeTreeUpdates()

		got, ok := b.ClusterStore.Updates.HTTPRoutes[syntheticKey]
		assert.True(t, ok, "expected the synthetic route to be scheduled")
		assert.Equal(t, store.StatusUpserted, got.Status)
	})

	t.Run("default class deleted -> unclassed ingress ineligible -> route deleted", func(t *testing.T) {
		ingress := mkIngressWithRule(ns, "web", "") // unclassed: depends on the default class
		// A default class with a matching suffixed controller made the unclassed
		// Ingress eligible under --ingress-class=prod; it is now deleted.
		deleted := mkIngressClass(ingressClassFixture{
			name:       "haproxy-default",
			controller: path.Join(CONTROLLER, "prod"),
			isDefault:  true,
		})
		b, syntheticKey := newBuilder(
			ingress,
			map[types.NamespacedName]*networkingv1.IngressClass{}, // default gone
			map[types.NamespacedName]store.Update[*networkingv1.IngressClass]{
				{Name: "haproxy-default"}: {Status: store.StatusDeleted, OldObject: deleted},
			},
			"prod", false,
		)

		b.ComputeTreeUpdates()

		got, ok := b.ClusterStore.Updates.HTTPRoutes[syntheticKey]
		assert.True(t, ok, "expected the unclassed Ingress to be re-evaluated via the default class")
		assert.Equal(t, store.StatusDeleted, got.Status)
	})
}

func TestResolveServicePort(t *testing.T) {
	const ns = "app"

	mkSvc := func(name string, ports ...corev1.ServicePort) *corev1.Service {
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
			Spec:       corev1.ServiceSpec{Ports: ports},
		}
	}
	svcPort := func(name string, num int32) corev1.ServicePort {
		return corev1.ServicePort{Name: name, Port: num}
	}
	ingBackend := func(service string, port networkingv1.ServiceBackendPort) *networkingv1.IngressServiceBackend {
		return &networkingv1.IngressServiceBackend{Name: service, Port: port}
	}

	web := mkSvc("web", svcPort("http", 80), svcPort("https", 443))

	tests := []struct {
		name     string
		services []*corev1.Service
		backend  *networkingv1.IngressServiceBackend
		wantPort gatewayv1.PortNumber
		wantOK   bool
	}{
		{
			name:     "numeric port is used as-is (no Service lookup)",
			backend:  ingBackend("web", networkingv1.ServiceBackendPort{Number: 8080}),
			wantPort: 8080,
			wantOK:   true,
		},
		{
			name:     "named port resolved to the Service port number",
			services: []*corev1.Service{web},
			backend:  ingBackend("web", networkingv1.ServiceBackendPort{Name: "https"}),
			wantPort: 443,
			wantOK:   true,
		},
		{
			name:     "named port absent from the Service is rejected",
			services: []*corev1.Service{web},
			backend:  ingBackend("web", networkingv1.ServiceBackendPort{Name: "grpc"}),
			wantOK:   false,
		},
		{
			name:    "Service not in the store is rejected (retried on Service change)",
			backend: ingBackend("web", networkingv1.ServiceBackendPort{Name: "http"}),
			wantOK:  false,
		},
		{
			name:     "neither number nor name is rejected",
			services: []*corev1.Service{web},
			backend:  ingBackend("web", networkingv1.ServiceBackendPort{}),
			wantOK:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			services := make(map[types.NamespacedName]*corev1.Service, len(tc.services))
			for _, s := range tc.services {
				services[types.NamespacedName{Namespace: s.Namespace, Name: s.Name}] = s
			}
			b := &IngressBuilderImpl{ControllerStore: &ControllerStore{
				ClusterStore: &store.ClusterStore{Services: services},
				Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
			}}
			ingress := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "ing"}}

			got, ok := b.resolveServicePort(ingress, tc.backend)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Equal(t, tc.wantPort, got)
			}
		})
	}
}
