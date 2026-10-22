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
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/templates"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"
	corev1 "k8s.io/api/core/v1"
	discoveryV1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func newTestServersMgr(namespace, name string, ports []corev1.ServicePort) *HaproxyConfMgrImpl {
	svc := &corev1.Service{}
	svc.Namespace, svc.Name = namespace, name
	svc.Spec.Ports = ports

	return &HaproxyConfMgrImpl{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		params: HaproxyConfMgrParams{
			HaproxyConfParams: HaproxyConfParams{
				Templates: templates.Templates{ServerNameTemplate: "SRV_{{ .POD_IP_PORT_HASH}}"},
			},
		},
		controllerStore: &tree.ControllerStore{
			ClusterStore: &store.ClusterStore{
				Services: map[client.ObjectKey]*corev1.Service{
					{Namespace: namespace, Name: name}: svc,
				},
			},
		},
	}
}

func TestGetServersForBackendNamedTargetPort(t *testing.T) {
	svcKey := client.ObjectKey{Namespace: "monitoring", Name: "vm-stack-grafana"}

	tests := []struct {
		name             string
		svcPorts         []corev1.ServicePort
		epsPorts         []discoveryV1.EndpointPort
		bePort           int32
		expectedAddrPort string
	}{
		{
			// Regression: a named targetPort different from the Service port name
			// (github issues #31 and #32). The EndpointSlice port name is the
			// Service port name, so matching on the targetPort name found nothing.
			name: "named targetPort differing from the Service port name",
			svcPorts: []corev1.ServicePort{
				{Name: "service", Port: 80, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromString("grafana")},
			},
			epsPorts: []discoveryV1.EndpointPort{
				{Name: new("service"), Protocol: new(corev1.ProtocolTCP), Port: new(int32(3000))},
			},
			bePort:           80,
			expectedAddrPort: "10.42.27.226:3000",
		},
		{
			name: "named targetPort equal to the Service port name",
			svcPorts: []corev1.ServicePort{
				{Name: "http", Port: 80, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromString("http")},
			},
			epsPorts: []discoveryV1.EndpointPort{
				{Name: new("http"), Protocol: new(corev1.ProtocolTCP), Port: new(int32(8080))},
			},
			bePort:           80,
			expectedAddrPort: "10.42.27.226:8080",
		},
		{
			name: "numeric targetPort",
			svcPorts: []corev1.ServicePort{
				{Name: "http", Port: 8080, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromInt32(8080)},
			},
			epsPorts: []discoveryV1.EndpointPort{
				{Name: new("http"), Protocol: new(corev1.ProtocolTCP), Port: new(int32(8080))},
			},
			bePort:           8080,
			expectedAddrPort: "10.42.27.226:8080",
		},
		{
			name: "unnamed single Service port",
			svcPorts: []corev1.ServicePort{
				{Port: 80, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromInt32(3000)},
			},
			epsPorts: []discoveryV1.EndpointPort{
				{Protocol: new(corev1.ProtocolTCP), Port: new(int32(3000))},
			},
			bePort:           80,
			expectedAddrPort: "10.42.27.226:3000",
		},
		{
			name: "multi-port Service selects the referenced port",
			svcPorts: []corev1.ServicePort{
				{Name: "metrics", Port: 8080, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromString("metrics")},
				{Name: "service", Port: 80, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromString("grafana")},
			},
			epsPorts: []discoveryV1.EndpointPort{
				{Name: new("metrics"), Protocol: new(corev1.ProtocolTCP), Port: new(int32(8080))},
				{Name: new("service"), Protocol: new(corev1.ProtocolTCP), Port: new(int32(3000))},
			},
			bePort:           80,
			expectedAddrPort: "10.42.27.226:3000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr := newTestServersMgr(svcKey.Namespace, svcKey.Name, tt.svcPorts)

			svcEndpoints := SvcEndpoints{
				discoveryV1.AddressTypeIPv4: {
					ports: tt.epsPorts,
					endpoints: []discoveryV1.Endpoint{
						{Addresses: []string{"10.42.27.226"}},
					},
				},
			}

			servers, err := mgr.getServersForBackend(svcKey, BackendPort{backendName: "be", svcPort: tt.bePort}, svcEndpoints)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(servers) != 1 {
				t.Fatalf("expected 1 server, got %d (%v)", len(servers), servers)
			}
			for _, server := range servers {
				got := fmt.Sprintf("%s:%d", server.Address, *server.Port)
				if got != tt.expectedAddrPort {
					t.Errorf("expected server %s, got %s", tt.expectedAddrPort, got)
				}
			}
		})
	}
}

func TestEndpointSliceUpdatesSkipDeletionWithoutOldObject(t *testing.T) {
	mgr := newTestServersMgr("default", "svc", nil)
	mgr.controllerStore.ClusterStore.Updates = store.NewClusterUpdates()
	updates := mgr.controllerStore.ClusterStore.Updates.EndpointSlices

	updates[types.NamespacedName{Namespace: "default", Name: "gone"}] = store.Update[*discoveryV1.EndpointSlice]{Status: store.StatusDeleted}

	live := &discoveryV1.EndpointSlice{
		Namespace: "default",
		Name:      "live",
		Labels:    map[string]string{"kubernetes.io/service-name": "svc"},
	}
	updates[types.NamespacedName{Namespace: "default", Name: "live"}] = store.Update[*discoveryV1.EndpointSlice]{Status: store.StatusUpserted, NewObject: live}

	result := mgr.endpointSliceUpdatedByService()

	if len(result) != 1 {
		t.Fatalf("expected only the live service, got %v", result)
	}
	if _, ok := result[client.ObjectKey{Namespace: "default", Name: "svc"}]; !ok {
		t.Fatalf("expected live endpoint slice to map to its service, got %v", result)
	}
}
