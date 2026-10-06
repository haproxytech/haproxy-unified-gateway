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
	"context"
	"fmt"
	"log/slog"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	utilsk8s "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils-k8s"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const (
	// ListenerSetCertificateRefIndexField maps a ListenerSet to the Secrets its entries'
	// TLS certificateRefs name.
	ListenerSetCertificateRefIndexField = "listenerSetCertificateRef"
	// ListenerSetCertificateRefNamespaceIndexField maps a ListenerSet to the namespaces its
	// cross-namespace certificateRefs target -- the namespaces a ReferenceGrant can be
	// written in to permit them. Same-namespace references are left out: they need no
	// grant, so a grant event cannot concern them.
	ListenerSetCertificateRefNamespaceIndexField = "listenerSetCertificateRefNamespace"
)

// ListenerSetCertificateRefFieldIndices is the FieldIndices a ListenerSet controller has
// to register before its informer starts, so a Secret or a ReferenceGrant event can be
// routed to the ListenerSets that depend on it by a List rather than a scan of the whole
// type.
func ListenerSetCertificateRefFieldIndices(logger *slog.Logger) FieldIndices {
	return FieldIndices{
		ListenerSetCertificateRefIndexField:          ListenerSetCertificateRefIndexFunc(logger),
		ListenerSetCertificateRefNamespaceIndexField: ListenerSetCertificateRefNamespaceIndexFunc(logger),
	}
}

// ListenerSetCertificateRefs returns the v1 Secrets a ListenerSet's entries' TLS
// certificateRefs point at; the ListenerSet's own namespace defaults the ones that name
// none, and duplicates are collapsed.
//
// The key carries the Secret's namespace because a certificateRef may target another one,
// and a bare name would then also match a same-named Secret elsewhere.
func ListenerSetCertificateRefs(listenerSet *gatewayv1.ListenerSet) []types.NamespacedName {
	if listenerSet == nil {
		return nil
	}
	var refs []types.NamespacedName
	seen := make(map[types.NamespacedName]struct{})
	for _, entry := range listenerSet.Spec.Listeners {
		if entry.TLS == nil {
			continue
		}
		for _, certRef := range entry.TLS.CertificateRefs {
			if !utilsk8s.IsSecretGroupKindSupported(certRef) {
				continue
			}
			ref := utils.GetNamespacedName(certRef.Name, certRef.Namespace, listenerSet.Namespace)
			if _, duplicate := seen[ref]; duplicate {
				continue
			}
			seen[ref] = struct{}{}
			refs = append(refs, ref)
		}
	}
	return refs
}

// ListenerSetCertificateRefIndexFunc indexes a ListenerSet by the namespaced names of the
// Secrets it references. It only routes an event: nothing here decides whether the
// reference resolves (a ReferenceGrant may be missing) or whether the ListenerSet is
// attached, which the reference manager re-checks when it derives ReferencedSecrets.
func ListenerSetCertificateRefIndexFunc(logger *slog.Logger) client.IndexerFunc {
	return func(obj client.Object) []string {
		listenerSet, ok := obj.(*gatewayv1.ListenerSet)
		if !ok {
			logRefusedObject(logger, obj, "ListenerSet")
			return nil
		}
		refs := ListenerSetCertificateRefs(listenerSet)
		keys := make([]string, 0, len(refs))
		for _, ref := range refs {
			keys = append(keys, ref.String())
		}
		return keys
	}
}

// ListenerSetCertificateRefNamespaceIndexFunc indexes a ListenerSet by the namespaces its
// cross-namespace certificateRefs target, so a ReferenceGrant event can be routed to the
// ListenerSets it could permit.
func ListenerSetCertificateRefNamespaceIndexFunc(logger *slog.Logger) client.IndexerFunc {
	return func(obj client.Object) []string {
		listenerSet, ok := obj.(*gatewayv1.ListenerSet)
		if !ok {
			logRefusedObject(logger, obj, "ListenerSet")
			return nil
		}
		var namespaces []string
		seen := make(map[string]struct{})
		for _, ref := range ListenerSetCertificateRefs(listenerSet) {
			if ref.Namespace == listenerSet.Namespace {
				continue
			}
			if _, duplicate := seen[ref.Namespace]; duplicate {
				continue
			}
			seen[ref.Namespace] = struct{}{}
			namespaces = append(namespaces, ref.Namespace)
		}
		return namespaces
	}
}

func logRefusedObject(logger *slog.Logger, obj client.Object, expected string) {
	logger.LogAttrs(context.Background(), slog.LevelError,
		fmt.Sprintf("expected a %s; got %T", expected, obj),
		logging.LogAttrCategory(logging.LogCategoryK8s))
}
