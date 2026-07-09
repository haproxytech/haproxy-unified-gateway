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
