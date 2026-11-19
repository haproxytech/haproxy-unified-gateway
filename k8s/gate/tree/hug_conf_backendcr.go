// Copyright 2026 HAProxy Technologies LLC
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
	"path"
	"slices"

	v3 "github.com/haproxytech/haproxy-unified-gateway/api/gate/v3"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

// onDeletedSubsystemBackendCR removes every Backend CR trust when the HugConf
// resource is deleted.
func (b *HugConfBuilderImpl) onDeletedSubsystemBackendCR() {
	b.setTrustedBackendCRNamespaces(nil)
}

// onUpsertedSubsystemBackendCR applies the trusted Backend CR namespaces of
// the HugConf spec.
func (b *HugConfBuilderImpl) onUpsertedSubsystemBackendCR(hugConfUpdate store.Update[*v3.HugConf]) {
	if hugConfUpdate.NewObject == nil {
		return
	}
	b.setTrustedBackendCRNamespaces(hugConfUpdate.NewObject.Spec.TrustedBackendCRNamespaces)
}

// setTrustedBackendCRNamespaces stores the trusted namespace patterns. When
// the list changes it schedules every known route for this cycle, because the content
// of a backend depends on whether its Backend CR is trusted.
func (b *HugConfBuilderImpl) setTrustedBackendCRNamespaces(namespaces []string) {
	trusted := slices.Compact(slices.Sorted(slices.Values(namespaces)))
	if slices.Equal(trusted, b.TrustedBackendCRNamespaces) {
		return
	}
	b.TrustedBackendCRNamespaces = trusted
	b.Logger.LogAttrs(
		context.Background(), slog.LevelInfo,
		"Reconciled trusted Backend CR namespaces",
		slog.Any("namespaces", trusted),
	)
	for _, pattern := range trusted {
		if _, err := path.Match(pattern, ""); err != nil {
			b.Logger.LogAttrs(
				context.Background(), slog.LevelError,
				"Malformed trusted Backend CR namespace pattern, it matches nothing",
				slog.String("pattern", pattern),
			)
		}
	}

	// A route already updated in this cycle keeps its own status.
	updates := &b.ClusterStore.Updates
	for key, route := range b.GateTree.HTTPRoutes {
		if _, pending := updates.HTTPRoutes[key]; !pending && route != nil && route.K8sResource != nil {
			updates.HTTPRoutes[key] = store.Update[*gatewayv1.HTTPRoute]{NewObject: route.K8sResource, Status: store.StatusUpserted}
		}
	}
	for key, route := range b.GateTree.TLSRoutes {
		if _, pending := updates.TLSRoutes[key]; !pending && route != nil && route.K8sResource != nil {
			updates.TLSRoutes[key] = store.Update[*gatewayv1alpha2.TLSRoute]{NewObject: route.K8sResource, Status: store.StatusUpserted}
		}
	}
	for key, route := range b.GateTree.TCPRoutes {
		if _, pending := updates.TCPRoutes[key]; !pending && route != nil && route.K8sResource != nil {
			updates.TCPRoutes[key] = store.Update[*gatewayv1.TCPRoute]{NewObject: route.K8sResource, Status: store.StatusUpserted}
		}
	}
}
