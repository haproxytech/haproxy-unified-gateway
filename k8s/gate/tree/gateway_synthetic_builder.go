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
	"context"
	"log/slog"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
)

var (
	_                              Builder = &SyntheticGatewayBuilderImpl{}
	syntheticGatewayNamespacedName         = types.NamespacedName{
		Namespace: "default",
		Name:      "ing:gateway",
	}
)

type SyntheticGatewayBuilderImpl struct {
	*ControllerStore
}

func NewSyntheticGatewayBuilder(controllerStore *ControllerStore) Builder {
	return &SyntheticGatewayBuilderImpl{
		ControllerStore: controllerStore,
	}
}

func (b *SyntheticGatewayBuilderImpl) ComputeTreeUpdates() {
	// Re-inject only when an Ingress or a Secret changed, or when the synthetic
	// gateway does not exist yet. Existence is checked against the GateTree (where
	// the GatewayBuilder materialises it): the synthetic gateway is intentionally
	// kept out of ClusterStore.Gateways.
	ingressUpdates := len(b.ClusterStore.Updates.Ingresses)
	secretUpdates := len(b.ClusterStore.Updates.Secrets)
	alreadyInTree := b.GateTree.Gateways[syntheticGatewayNamespacedName] != nil

	if ingressUpdates == 0 && secretUpdates == 0 && alreadyInTree {
		b.Logger.LogAttrs(
			context.Background(), slog.LevelDebug,
			"Synthetic ingress gateway unchanged, skipping re-injection",
			slog.Int("ingressUpdates", ingressUpdates),
			slog.Int("secretUpdates", secretUpdates),
		)
		return
	}

	b.Logger.LogAttrs(
		context.Background(), slog.LevelDebug,
		"Injecting synthetic ingress gateway",
		slog.Int("ingressUpdates", ingressUpdates),
		slog.Int("secretUpdates", secretUpdates),
		slog.Bool("alreadyInTree", alreadyInTree),
		slog.Int("httpPort", b.ControllerStore.HTTPIngressFrontendPort),
		slog.Int("httpsPort", b.ControllerStore.HTTPSIngressFrontendPort),
	)

	// Ingress-derived HTTPRoutes live in their own (real) namespace while the
	// synthetic gateway lives in the empty namespace, so its listeners must allow
	// routes from all namespaces; the default (Same) would reject every Ingress.
	fromAll := gatewayv1.NamespacesFromAll
	allowFromAll := &gatewayv1.AllowedRoutes{
		Namespaces: &gatewayv1.RouteNamespaces{From: &fromAll},
	}

	previousGatewayUpdate := b.ClusterStore.Updates.Gateways[syntheticGatewayNamespacedName]
	previousGatewayUpdate.OldObject = previousGatewayUpdate.NewObject
	previousGatewayUpdate.Status = store.StatusUpserted
	previousGatewayUpdate.NewObject = &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      syntheticGatewayNamespacedName.Name,
			Namespace: syntheticGatewayNamespacedName.Namespace,
		},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "ing:gatewayclass",
			Listeners: []gatewayv1.Listener{
				{
					Name:          "http",
					Port:          gatewayv1.PortNumber(b.ControllerStore.HTTPIngressFrontendPort),
					Protocol:      gatewayv1.HTTPProtocolType,
					AllowedRoutes: allowFromAll,
				},
				{
					Name:          "https",
					Port:          gatewayv1.PortNumber(b.ControllerStore.HTTPSIngressFrontendPort),
					Protocol:      gatewayv1.HTTPSProtocolType,
					TLS:           b.httpsListenerTLS(),
					AllowedRoutes: allowFromAll,
				},
			},
			// Hostname nil to match everything.
		},
	}

	b.ClusterStore.Updates.Gateways[syntheticGatewayNamespacedName] = previousGatewayUpdate
}

func (*SyntheticGatewayBuilderImpl) CleanTreeUpdates() {}

// httpsListenerTLS builds the TLS config of the synthetic https listener. Its
// CertificateRefs are the TLS secrets of every managed Ingress; the listener
// serves plain HTTP while the list is empty (no Ingress TLS yet).
func (b *SyntheticGatewayBuilderImpl) httpsListenerTLS() *gatewayv1.ListenerTLSConfig {
	mode := gatewayv1.TLSModeTerminate
	return &gatewayv1.ListenerTLSConfig{
		Mode:            &mode,
		CertificateRefs: b.ingressTLSCertificateRefs(),
	}
}

// ingressTLSCertificateRefs collects, from every managed Ingress, a
// SecretObjectReference per spec.tls[].secretName. Each ref carries the Ingress
// namespace (SecretObjectReference, unlike ExtensionRef, is namespace-aware);
// cross-namespace access is allowed because the synthetic gateway bypasses
// ReferenceGrant for its certificate refs. Duplicates (same namespace/name) are
// collapsed.
func (b *SyntheticGatewayBuilderImpl) ingressTLSCertificateRefs() []gatewayv1.SecretObjectReference {
	secretGroup := gatewayv1.Group("")
	secretKind := gatewayv1.Kind("Secret")

	seen := make(map[types.NamespacedName]struct{})
	var refs []gatewayv1.SecretObjectReference

	for _, ingress := range b.ClusterStore.Ingresses {
		if ingress == nil {
			continue
		}
		if !b.isIngressClassSupported(
			utils.PointerDefaultValueIfNil(ingress.Spec.IngressClassName),
			b.IngressClass, b.EmptyIngressClass,
		) {
			continue
		}
		for _, tls := range ingress.Spec.TLS {
			if tls.SecretName == "" {
				continue
			}
			key := types.NamespacedName{Namespace: ingress.Namespace, Name: tls.SecretName}
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}

			namespace := gatewayv1.Namespace(ingress.Namespace)
			refs = append(refs, gatewayv1.SecretObjectReference{
				Group:     &secretGroup,
				Kind:      &secretKind,
				Name:      gatewayv1.ObjectName(tls.SecretName),
				Namespace: &namespace,
			})
		}
	}

	return refs
}
