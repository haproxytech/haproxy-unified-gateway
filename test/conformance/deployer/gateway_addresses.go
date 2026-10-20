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
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// WaitForGatewayAddresses checks the base Gateways before any traffic scenario
// runs. The caller supplies one deadline for the whole set, not one per Gateway.
// It only reads resources; the suite still owns creation and cleanup.
func WaitForGatewayAddresses(ctx context.Context, reader client.Reader, cfg Config, gatewayNamespace string) error {
	pending := "no Gateway snapshot available"
	err := wait.PollUntilContextCancel(ctx, time.Second, true, func(ctx context.Context) (bool, error) {
		gateways := &gatewayv1.GatewayList{}
		if err := reader.List(ctx, gateways, client.InNamespace(gatewayNamespace)); err != nil {
			return false, fmt.Errorf("listing Gateways for address preflight: %w", err)
		}

		managed := 0
		var missing []string
		for _, gateway := range gateways.Items {
			if !gateway.DeletionTimestamp.IsZero() || (cfg.GatewayClassName != "" && string(gateway.Spec.GatewayClassName) != cfg.GatewayClassName) {
				continue
			}
			managed++
			if gatewayHasIPAddress(gateway) {
				continue
			}

			serviceKey := client.ObjectKey{Namespace: cfg.DeployerNs, Name: resourceName(gateway.Namespace, gateway.Name)}
			service := &corev1.Service{}
			serviceStatus := "not created yet"
			if err := reader.Get(ctx, serviceKey, service); err != nil {
				if !apierrors.IsNotFound(err) {
					return false, fmt.Errorf("reading Service %s for address preflight: %w", serviceKey, err)
				}
			} else {
				serviceStatus = serviceAddressStatus(service)
			}
			missing = append(missing, fmt.Sprintf("Gateway %s has no IP in status.addresses (published: %v); Service %s: %s",
				client.ObjectKeyFromObject(&gateway), gateway.Status.Addresses, serviceKey, serviceStatus))
		}
		if managed == 0 {
			pending = fmt.Sprintf("no Gateways of class %q in namespace %q", cfg.GatewayClassName, gatewayNamespace)
			return false, nil
		}
		if len(missing) == 0 {
			return true, nil
		}
		slices.Sort(missing)
		pending = strings.Join(missing, "\n")
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("gateway address preflight failed: %w\n%s\n"+
			"If a LoadBalancer Service has no ingress address, check the load-balancer provider "+
			"(cloud-provider-kind and its Envoy kindccm-* containers for local Kind). "+
			"If the Service already has an IP, check HUG's Gateway address publication. "+
			"No conformance scenarios were started", err, pending)
	}
	return nil
}

// The traffic helpers require an IP address, not just a nonempty addresses list.
func gatewayHasIPAddress(gateway gatewayv1.Gateway) bool {
	for _, address := range gateway.Status.Addresses {
		if address.Type != nil && *address.Type != gatewayv1.IPAddressType {
			continue
		}
		if ip, err := netip.ParseAddr(address.Value); err == nil && ip.IsValid() {
			return true
		}
	}
	return false
}

func serviceAddressStatus(service *corev1.Service) string {
	if service.Spec.Type == corev1.ServiceTypeLoadBalancer {
		if len(service.Status.LoadBalancer.Ingress) == 0 {
			return "LoadBalancer ingress pending"
		}
		return fmt.Sprintf("LoadBalancer ingress=%v", service.Status.LoadBalancer.Ingress)
	}
	return fmt.Sprintf("type=%s, clusterIP=%s, clusterIPs=%v", service.Spec.Type, service.Spec.ClusterIP, service.Spec.ClusterIPs)
}
