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
package handler

import (
	"log/slog"
	"testing"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/caps"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"
	utilsk8s "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils-k8s"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

func runListenerSetTreeCycle(builder *GateTreeBuilder) {
	builder.startBuild()
	builder.referenceManager.UpdateRefences()
	for _, b := range builder.postBuilders {
		b.ComputeTreeUpdates()
	}
}

// Route-only changes must publish fresh counts even when no frontend needs an
// apply. This runs the real builder chain without Kubernetes or HAProxy processes.
func TestListenerSetAttachedRoutesFollowRouteOnlyChanges(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	scheme := runtime.NewScheme()
	require.NoError(t, gatewayv1.Install(scheme))
	require.NoError(t, gatewayv1alpha2.Install(scheme))
	cs := &tree.ControllerStore{
		Logger: logger, ControllerName: "controller", PortBinder: caps.Static(1024, false),
		ExtractGVK:   utilsk8s.NewExtractGKV(scheme, logger),
		ClusterStore: &store.ClusterStore{Updates: store.NewClusterUpdates()},
		GateTree:     tree.NewGateTree(), UnmanagedGateTree: tree.NewGateTree(),
		ReferencedObjects: tree.NewReferencedObjects(), InstalledGwAPIVersions: &tree.InstalledVersions{},
		CertUpdates: &tree.CertUpdates{}, CrtListUpdates: &tree.CrtListUpdates{},
	}
	classKey := client.ObjectKey{Name: "hug"}
	cs.GateTree.GatewayClasses[classKey] = &tree.GatewayClass{Valid: true, Managed: true}
	gwKey := client.ObjectKey{Namespace: "tenant", Name: "gw"}
	gw := &gatewayv1.Gateway{
		Namespace: gwKey.Namespace, Name: gwKey.Name, Generation: 1,
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "hug",
			AllowedListeners: &gatewayv1.AllowedListeners{Namespaces: &gatewayv1.ListenerNamespaces{From: new(gatewayv1.NamespacesFromAll)}},
			Listeners:        []gatewayv1.Listener{{Name: "own", Port: 8080, Protocol: gatewayv1.HTTPProtocolType}},
		},
	}
	lsKey := client.ObjectKey{Namespace: "tenant", Name: "ls"}
	ls := &gatewayv1.ListenerSet{
		Namespace: lsKey.Namespace, Name: lsKey.Name, Generation: 1,
		Spec: gatewayv1.ListenerSetSpec{
			ParentRef: gatewayv1.ParentGatewayReference{Name: "gw"},
			Listeners: []gatewayv1.ListenerEntry{
				{Name: "http", Port: 8081, Protocol: gatewayv1.HTTPProtocolType},
				{
					Name: "tls", Port: 8443, Protocol: gatewayv1.TLSProtocolType,
					TLS: &gatewayv1.ListenerTLSConfig{Mode: new(gatewayv1.TLSModePassthrough)},
				},
			},
		},
	}
	cs.ClusterStore.Gateways = map[client.ObjectKey]*gatewayv1.Gateway{gwKey: gw}
	cs.ClusterStore.ListenerSets = map[client.ObjectKey]*gatewayv1.ListenerSet{lsKey: ls}
	cs.ClusterStore.Updates.Gateways[gwKey] = store.Update[*gatewayv1.Gateway]{NewObject: gw, Status: store.StatusUpserted}
	cs.ClusterStore.Updates.ListenerSets[lsKey] = store.Update[*gatewayv1.ListenerSet]{NewObject: ls, Status: store.StatusUpserted}
	builder := NewGateTreeBuilder(cs, GateTreeConfig{})
	runListenerSetTreeCycle(&builder)
	require.Len(t, cs.GateTree.ListenerSets[lsKey].Entries, 2)
	cs.GateTree.UpdateListenerProgrammedConditions(logger, tree.ObservedGenerations{
		{Kind: tree.OwnerKindGateway, Key: gwKey}:     1,
		{Kind: tree.OwnerKindListenerSet, Key: lsKey}: 1,
	}, nil)

	parent := func(section string) gatewayv1.ParentReference {
		return gatewayv1.ParentReference{Kind: new(gatewayv1.Kind("ListenerSet")), Name: "ls", SectionName: new(gatewayv1.SectionName(section))}
	}
	httpKey := client.ObjectKey{Namespace: "tenant", Name: "http"}
	httpRoute := &gatewayv1.HTTPRoute{
		Namespace: httpKey.Namespace, Name: httpKey.Name,
		Spec: gatewayv1.HTTPRouteSpec{CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: []gatewayv1.ParentReference{parent("http")}}},
	}
	tlsKey := client.ObjectKey{Namespace: "tenant", Name: "tls"}
	tlsRoute := &gatewayv1alpha2.TLSRoute{
		Namespace: tlsKey.Namespace, Name: tlsKey.Name,
		Spec: gatewayv1alpha2.TLSRouteSpec{CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: []gatewayv1.ParentReference{parent("tls")}}},
	}
	cs.ClusterStore.Updates = store.NewClusterUpdates()
	cs.ClusterStore.Updates.HTTPRoutes[httpKey] = store.Update[*gatewayv1.HTTPRoute]{NewObject: httpRoute, Status: store.StatusUpserted}
	cs.ClusterStore.Updates.TLSRoutes[tlsKey] = store.Update[*gatewayv1alpha2.TLSRoute]{NewObject: tlsRoute, Status: store.StatusUpserted}
	runListenerSetTreeCycle(&builder)
	require.True(t, cs.GateTree.HTTPRoutes[httpKey].Valid)
	require.True(t, cs.GateTree.TLSRoutes[tlsKey].Valid)
	for _, entry := range cs.GateTree.ListenerSets[lsKey].Entries {
		assert.Equal(t, int32(1), entry.AttachedRoutes, "new route must be counted before any feedback")
	}
	for _, listener := range cs.GateTree.VirtualListeners {
		assert.Equal(t, store.StatusUnchanged, listener.Status, "route attachment must not require a frontend apply")
	}

	cs.ClusterStore.Updates = store.NewClusterUpdates()
	cs.ClusterStore.Updates.HTTPRoutes[httpKey] = store.Update[*gatewayv1.HTTPRoute]{OldObject: httpRoute, Status: store.StatusDeleted}
	cs.ClusterStore.Updates.TLSRoutes[tlsKey] = store.Update[*gatewayv1alpha2.TLSRoute]{OldObject: tlsRoute, Status: store.StatusDeleted}
	runListenerSetTreeCycle(&builder)
	for _, entry := range cs.GateTree.ListenerSets[lsKey].Entries {
		assert.Zero(t, entry.AttachedRoutes, "deleted route must be removed before any feedback")
	}
	for _, listener := range cs.GateTree.VirtualListeners {
		assert.Equal(t, store.StatusUnchanged, listener.Status)
	}
}
