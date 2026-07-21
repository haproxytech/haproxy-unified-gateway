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
package status

import (
	"context"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestIngressLBFromAddresses(t *testing.T) {
	got := ingressLBFromAddresses([]string{"10.0.0.1", "lb.example.com", ""})
	if len(got) != 2 {
		t.Fatalf("want 2 entries (empty dropped), got %d: %+v", len(got), got)
	}
	if got[0].IP != "10.0.0.1" || got[0].Hostname != "" {
		t.Errorf("expected an IP entry, got %+v", got[0])
	}
	if got[1].Hostname != "lb.example.com" || got[1].IP != "" {
		t.Errorf("expected a Hostname entry, got %+v", got[1])
	}
}

func TestIngressStatusPatcher(t *testing.T) {
	sp := newIngressStatusPatcher([]string{"10.0.0.1", "lb.example.com"})
	ing := &networkingv1.Ingress{}

	if eq, _ := sp.StatusEqual(ing); eq {
		t.Error("expected not equal before SetStatus (status empty)")
	}
	if err := sp.SetStatus(ing); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if len(ing.Status.LoadBalancer.Ingress) != 2 {
		t.Fatalf("want 2 LoadBalancer entries, got %d", len(ing.Status.LoadBalancer.Ingress))
	}

	// Equality is order-independent.
	if eq, _ := newIngressStatusPatcher([]string{"lb.example.com", "10.0.0.1"}).StatusEqual(ing); !eq {
		t.Error("expected equal regardless of address order")
	}
	// Clearing (no addresses) differs from a populated status.
	if eq, _ := newIngressStatusPatcher(nil).StatusEqual(ing); eq {
		t.Error("expected not equal when clearing a populated status")
	}
}

func TestControllerAddressValues(t *testing.T) {
	ipType := gatewayv1.IPAddressType
	got := controllerAddressValues([]gatewayv1.GatewayStatusAddress{
		{Type: &ipType, Value: "10.0.0.1"},
		{Value: ""},
		{Value: "lb.example.com"},
	})
	if len(got) != 2 || got[0] != "10.0.0.1" || got[1] != "lb.example.com" {
		t.Fatalf("unexpected values: %v", got)
	}
}

func TestPrepareIngressStatusUpdateDisabled(t *testing.T) {
	s := &StatusUpdaterImpl{config: StatusUpdaterConf{disableIngressStatusUpdate: true}}
	writes := s.PrepareIngressStatusUpdate(context.Background(), []IngressStatus{
		{Ingress: &networkingv1.Ingress{}, Eligible: true},
	})
	if writes != nil {
		t.Errorf("expected no writes when Ingress status update is disabled, got %d", len(writes))
	}
}
