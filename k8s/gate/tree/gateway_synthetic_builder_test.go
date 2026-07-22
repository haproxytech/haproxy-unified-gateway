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
	"testing"

	"github.com/stretchr/testify/assert"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
)

// TestIngressTLSCertificateRefs checks that the synthetic https listener's
// certificateRefs are collected only from eligible Ingresses' spec.tls, in the
// Ingress namespace, deduplicated by secret, mirroring how kubernetes-ingress
// loads Ingress TLS secrets.
func TestIngressTLSCertificateRefs(t *testing.T) {
	const ns = "app"
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))

	ingWithTLS := func(name, className string, secrets ...string) *networkingv1.Ingress {
		ing := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
		if className != "" {
			ing.Spec.IngressClassName = &className
		}
		for _, sec := range secrets {
			ing.Spec.TLS = append(ing.Spec.TLS, networkingv1.IngressTLS{SecretName: sec})
		}
		return ing
	}

	byKey := map[types.NamespacedName]*networkingv1.Ingress{}
	for _, ing := range []*networkingv1.Ingress{
		ingWithTLS("web", "hug", "web-tls", "web-tls"), // eligible, duplicate secret name
		ingWithTLS("extra", "hug", "extra-tls"),        // eligible
		ingWithTLS("foreign", "nginx", "foreign-tls"),  // ineligible (unknown class) -> excluded
		ingWithTLS("plain", "hug"),                     // eligible but no TLS -> contributes nothing
	} {
		byKey[types.NamespacedName{Namespace: ing.Namespace, Name: ing.Name}] = ing
	}

	b := &SyntheticGatewayBuilderImpl{ControllerStore: &ControllerStore{
		ClusterStore: &store.ClusterStore{
			Ingresses: byKey,
			IngressClasses: map[types.NamespacedName]*networkingv1.IngressClass{
				{Name: "hug"}: {ObjectMeta: metav1.ObjectMeta{Name: "hug"}, Spec: networkingv1.IngressClassSpec{Controller: CONTROLLER}},
			},
		},
		Logger:            discard,
		IngressClass:      "",
		EmptyIngressClass: false,
	}}

	refs := b.ingressTLSCertificateRefs()

	got := map[string]bool{}
	for _, r := range refs {
		if r.Group == nil || *r.Group != "" {
			t.Errorf("expected empty group, got %v", r.Group)
		}
		if r.Kind == nil || *r.Kind != "Secret" {
			t.Errorf("expected Secret kind, got %v", r.Kind)
		}
		if r.Namespace == nil || string(*r.Namespace) != ns {
			t.Errorf("expected namespace %q, got %v", ns, r.Namespace)
		}
		got[string(r.Name)] = true
	}
	if len(refs) != 2 || !got["web-tls"] || !got["extra-tls"] {
		t.Fatalf("expected deduped eligible-only refs {web-tls, extra-tls}, got %d: %v", len(refs), got)
	}
	if got["foreign-tls"] {
		t.Error("the TLS secret of an ineligible Ingress must be excluded")
	}
}

// TestSyntheticGatewayBuilderHTTPSListener verifies that the synthetic ingress
// gateway declares the https listener only when at least one managed Ingress
// carries a TLS secret. An HTTPS/Terminate listener with no certificateRefs is
// invalid, and a synthetic route attaching to it would write its maps under an
// empty-named frontend directory ("hug_").
func TestSyntheticGatewayBuilderHTTPSListener(t *testing.T) {
	const ns = "app"
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))

	newBuilder := func(ingresses ...*networkingv1.Ingress) *SyntheticGatewayBuilderImpl {
		byKey := make(map[types.NamespacedName]*networkingv1.Ingress, len(ingresses))
		for _, ing := range ingresses {
			byKey[types.NamespacedName{Namespace: ing.Namespace, Name: ing.Name}] = ing
		}
		return &SyntheticGatewayBuilderImpl{ControllerStore: &ControllerStore{
			ClusterStore: &store.ClusterStore{
				Ingresses: byKey,
				Updates:   store.NewClusterUpdates(),
			},
			GateTree:                 &GateTree{Gateways: map[types.NamespacedName]*Gateway{}},
			Logger:                   discard,
			HTTPIngressFrontendPort:  8080,
			HTTPSIngressFrontendPort: 8443,
			// No --ingress-class: an unclassed Ingress with no IngressClass in the
			// cluster is eligible, so ingressTLSCertificateRefs considers its TLS.
			IngressClass:      "",
			EmptyIngressClass: false,
		}}
	}

	listenerNames := func(b *SyntheticGatewayBuilderImpl) []string {
		gw := b.ClusterStore.Updates.Gateways[syntheticGatewayNamespacedName].NewObject
		if gw == nil {
			t.Fatal("expected the synthetic gateway to be injected")
		}
		names := make([]string, 0, len(gw.Spec.Listeners))
		for _, l := range gw.Spec.Listeners {
			names = append(names, string(l.Name))
		}
		return names
	}

	t.Run("no Ingress TLS -> http listener only", func(t *testing.T) {
		b := newBuilder(&networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "web"}})
		b.ComputeTreeUpdates()
		assert.Equal(t, []string{"http"}, listenerNames(b))
	})

	t.Run("an Ingress with a TLS secret -> http and https listeners", func(t *testing.T) {
		ing := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "web"},
			Spec:       networkingv1.IngressSpec{TLS: []networkingv1.IngressTLS{{SecretName: "web-tls"}}},
		}
		b := newBuilder(ing)
		b.ComputeTreeUpdates()

		assert.Equal(t, []string{"http", "https"}, listenerNames(b))

		gw := b.ClusterStore.Updates.Gateways[syntheticGatewayNamespacedName].NewObject
		https := gw.Spec.Listeners[1]
		assert.Equal(t, gatewayv1.HTTPSProtocolType, https.Protocol)
		if assert.NotNil(t, https.TLS) {
			assert.Len(t, https.TLS.CertificateRefs, 1)
			assert.Equal(t, gatewayv1.ObjectName("web-tls"), https.TLS.CertificateRefs[0].Name)
		}
	})
}
