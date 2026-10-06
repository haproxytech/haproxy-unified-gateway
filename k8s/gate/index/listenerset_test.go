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
package index

import (
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func listenerSetWithCertificateRefs(namespace string, refs ...gatewayv1.SecretObjectReference) *gatewayv1.ListenerSet {
	return &gatewayv1.ListenerSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "ls"},
		Spec: gatewayv1.ListenerSetSpec{Listeners: []gatewayv1.ListenerEntry{
			// No TLS: nothing to index.
			{Name: "http", Port: 8080, Protocol: gatewayv1.HTTPProtocolType},
			{Name: "https", Port: 8443, Protocol: gatewayv1.HTTPSProtocolType,
				TLS: &gatewayv1.ListenerTLSConfig{CertificateRefs: refs}},
		}},
	}
}

func TestListenerSetCertificateRefIndexFunc(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	certs := gatewayv1.Namespace("certs")
	configMap := gatewayv1.Kind("ConfigMap")

	listenerSet := listenerSetWithCertificateRefs("tenant",
		gatewayv1.SecretObjectReference{Name: "own"}, // namespace defaults to the ListenerSet's
		gatewayv1.SecretObjectReference{Name: "own"}, // indexed once
		gatewayv1.SecretObjectReference{Name: "shared", Namespace: &certs},
		gatewayv1.SecretObjectReference{Name: "config", Kind: &configMap}, // not a Secret
	)

	// The key carries the Secret's namespace: a certificateRef may target another one, and a
	// bare name would then also match a same-named Secret elsewhere.
	assert.ElementsMatch(t, []string{"tenant/own", "certs/shared"},
		ListenerSetCertificateRefIndexFunc(logger)(listenerSet))

	// A non-ListenerSet is refused rather than indexed under a wrong key.
	assert.Empty(t, ListenerSetCertificateRefIndexFunc(logger)(&gatewayv1.Gateway{}))
}

// The namespace index is what routes a ReferenceGrant event: it carries the namespaces a
// cross-namespace reference targets, and nothing else, so a grant event does not have to
// walk every ListenerSet to find the ones it could permit.
func TestListenerSetCertificateRefNamespaceIndexFunc(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	certs := gatewayv1.Namespace("certs")
	otherCerts := gatewayv1.Namespace("other-certs")

	listenerSet := listenerSetWithCertificateRefs("tenant",
		gatewayv1.SecretObjectReference{Name: "own"}, // same namespace: no grant can concern it
		gatewayv1.SecretObjectReference{Name: "shared", Namespace: &certs},
		gatewayv1.SecretObjectReference{Name: "also", Namespace: &certs}, // same target namespace: once
		gatewayv1.SecretObjectReference{Name: "third", Namespace: &otherCerts},
	)

	assert.ElementsMatch(t, []string{"certs", "other-certs"},
		ListenerSetCertificateRefNamespaceIndexFunc(logger)(listenerSet))

	assert.Empty(t, ListenerSetCertificateRefNamespaceIndexFunc(logger)(&gatewayv1.Gateway{}))
}
