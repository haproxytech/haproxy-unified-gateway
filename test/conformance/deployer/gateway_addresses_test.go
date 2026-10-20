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
package deployer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func addressCheckGateway(name string, addresses ...gatewayv1.GatewayStatusAddress) *gatewayv1.Gateway {
	return &gatewayv1.Gateway{
		Namespace: "infra", Name: name,
		Spec:   gatewayv1.GatewaySpec{GatewayClassName: "hug"},
		Status: gatewayv1.GatewayStatus{Addresses: addresses},
	}
}

func addressCheckService(gatewayName string, serviceType corev1.ServiceType, ingress ...corev1.LoadBalancerIngress) *corev1.Service {
	return &corev1.Service{
		Namespace: "controllers", Name: resourceName("infra", gatewayName),
		Spec:   corev1.ServiceSpec{Type: serviceType, ClusterIP: "10.96.0.1", ClusterIPs: []string{"10.96.0.1"}},
		Status: corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{Ingress: ingress}},
	}
}

func addressCheckClient(t *testing.T, objects ...client.Object) *fake.ClientBuilder {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, gatewayv1.Install(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...)
}

func addressCheckConfig() Config {
	return Config{DeployerNs: "controllers", GatewayClassName: "hug", ServiceType: corev1.ServiceTypeLoadBalancer}
}

func TestWaitForGatewayAddressesAcceptsIPs(t *testing.T) {
	for _, address := range []gatewayv1.GatewayStatusAddress{
		{Value: "192.0.2.10"},
		{Type: new(gatewayv1.IPAddressType), Value: "192.0.2.10"},
		{Type: new(gatewayv1.IPAddressType), Value: "2001:db8::1"},
	} {
		t.Run(address.Value, func(t *testing.T) {
			foreign := addressCheckGateway("foreign")
			foreign.Spec.GatewayClassName = "another-controller"
			elsewhere := addressCheckGateway("elsewhere")
			elsewhere.Namespace = "another-namespace"
			reader := addressCheckClient(t, addressCheckGateway("ready", address), foreign, elsewhere).Build()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			require.NoError(t, WaitForGatewayAddresses(ctx, reader, addressCheckConfig(), "infra"))
		})
	}
}

func TestWaitForGatewayAddressesExplainsMissingIPs(t *testing.T) {
	tests := []struct {
		name    string
		objects []client.Object
		want    string
	}{
		{name: "no Gateways", want: "no Gateways of class"},
		{name: "Service absent", objects: []client.Object{addressCheckGateway("gw")}, want: "not created yet"},
		{
			name:    "provider has not assigned ingress",
			objects: []client.Object{addressCheckGateway("gw"), addressCheckService("gw", corev1.ServiceTypeLoadBalancer)},
			want:    "LoadBalancer ingress pending",
		},
		{
			name:    "Service addressed but Gateway not updated",
			objects: []client.Object{addressCheckGateway("gw"), addressCheckService("gw", corev1.ServiceTypeLoadBalancer, corev1.LoadBalancerIngress{IP: "192.0.2.20"})},
			want:    "192.0.2.20",
		},
		{
			name:    "ClusterIP does not replace a missing Gateway status",
			objects: []client.Object{addressCheckGateway("gw"), addressCheckService("gw", corev1.ServiceTypeClusterIP)},
			want:    "type=ClusterIP, clusterIP=10.96.0.1",
		},
		{
			name:    "hostname is not an IP",
			objects: []client.Object{addressCheckGateway("gw", gatewayv1.GatewayStatusAddress{Type: new(gatewayv1.HostnameAddressType), Value: "lb.example.com"})},
			want:    "lb.example.com",
		},
		{
			name:    "hostname type with an IP-looking value",
			objects: []client.Object{addressCheckGateway("gw", gatewayv1.GatewayStatusAddress{Type: new(gatewayv1.HostnameAddressType), Value: "192.0.2.10"})},
			want:    "192.0.2.10",
		},
		{
			name:    "invalid IP",
			objects: []client.Object{addressCheckGateway("gw", gatewayv1.GatewayStatusAddress{Type: new(gatewayv1.IPAddressType), Value: "invalid"})},
			want:    "invalid",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := addressCheckClient(t, tt.objects...).Build()
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			err := WaitForGatewayAddresses(ctx, reader, addressCheckConfig(), "infra")
			require.ErrorIs(t, err, context.DeadlineExceeded)
			assert.Contains(t, err.Error(), tt.want)
			assert.Contains(t, err.Error(), "cloud-provider-kind")
			assert.Contains(t, err.Error(), "No conformance scenarios were started")
		})
	}
}

func TestWaitForGatewayAddressesRequiresEveryGateway(t *testing.T) {
	reader := addressCheckClient(t,
		addressCheckGateway("ready", gatewayv1.GatewayStatusAddress{Value: "192.0.2.10"}),
		addressCheckGateway("pending-a"), addressCheckGateway("pending-b"),
	).Build()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := WaitForGatewayAddresses(ctx, reader, addressCheckConfig(), "infra")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Contains(t, err.Error(), "infra/pending-a")
	assert.Contains(t, err.Error(), "infra/pending-b")
	assert.NotContains(t, err.Error(), "Gateway infra/ready")
}

func TestWaitForGatewayAddressesWaitsForPublication(t *testing.T) {
	polls := 0
	reader := addressCheckClient(t, addressCheckGateway("gw")).WithInterceptorFuncs(interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if err := c.List(ctx, list, opts...); err != nil {
				return err
			}
			polls++
			if polls >= 2 {
				gateways, ok := list.(*gatewayv1.GatewayList)
				require.True(t, ok)
				gateways.Items[0].Status.Addresses = []gatewayv1.GatewayStatusAddress{{Value: "192.0.2.10"}}
			}
			return nil
		},
	}).Build()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, WaitForGatewayAddresses(ctx, reader, addressCheckConfig(), "infra"))
	assert.Equal(t, 2, polls)
}

func TestWaitForGatewayAddressesReturnsAPIErrors(t *testing.T) {
	apiErr := errors.New("API unavailable")
	for _, operation := range []string{"list", "get"} {
		t.Run(operation, func(t *testing.T) {
			funcs := interceptor.Funcs{}
			if operation == "list" {
				funcs.List = func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error { return apiErr }
			} else {
				funcs.Get = func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
					return apiErr
				}
			}
			reader := addressCheckClient(t, addressCheckGateway("gw")).WithInterceptorFuncs(funcs).Build()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			require.ErrorIs(t, WaitForGatewayAddresses(ctx, reader, addressCheckConfig(), "infra"), apiErr)
		})
	}
}
