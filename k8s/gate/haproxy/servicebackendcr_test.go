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

	"github.com/haproxytech/client-native/v6/models"
	v3 "github.com/haproxytech/haproxy-unified-gateway/api/gate/v3"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/constants"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func serviceCRMgr(
	services map[k8stypes.NamespacedName]*apiv1.Service,
	backendCRs map[k8stypes.NamespacedName]*v3.Backend,
) *HaproxyConfMgrImpl {
	return &HaproxyConfMgrImpl{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		controllerStore: &tree.ControllerStore{
			ClusterStore: &store.ClusterStore{Services: services, BackendCRs: backendCRs},
		},
	}
}

func annotatedService(ns, name, annotationValue string) *apiv1.Service {
	s := &apiv1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
	if annotationValue != "" {
		s.Annotations = map[string]string{constants.ServiceBackendCRAnnotation: annotationValue}
	}
	return s
}

func serviceHTTPBackendRef(name string) gatewayv1.HTTPBackendRef {
	return gatewayv1.HTTPBackendRef{
		BackendRef: gatewayv1.BackendRef{
			BackendObjectReference: gatewayv1.BackendObjectReference{Name: gatewayv1.ObjectName(name)},
		},
	}
}

func tuningCR(mode, algo string) *v3.Backend {
	be := &v3.Backend{}
	be.Spec.Mode = mode
	if algo != "" {
		be.Spec.Balance = &models.Balance{Algorithm: new(algo)}
	}
	return be
}

func TestMergeServiceBackendCR(t *testing.T) {
	svcNN := k8stypes.NamespacedName{Namespace: "team-a", Name: "echo"}
	crNN := k8stypes.NamespacedName{Namespace: "team-a", Name: "tuning"}

	t.Run("service CR overrides the route result", func(t *testing.T) {
		mgr := serviceCRMgr(
			map[k8stypes.NamespacedName]*apiv1.Service{svcNN: annotatedService("team-a", "echo", "tuning")},
			map[k8stypes.NamespacedName]*v3.Backend{crNN: tuningCR("http", "")},
		)
		// newBackend simulates the route-level result: Mode already set to tcp.
		be := &models.Backend{BackendBase: models.BackendBase{Mode: "tcp"}}
		if err := mgr.mergeServiceBackendCR(be, serviceHTTPBackendRef("echo"), "team-a"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if be.Mode != "http" {
			t.Errorf("expected the service CR to win (Mode http), got %q", be.Mode)
		}
	})

	t.Run("route-only fields survive when the service CR does not set them", func(t *testing.T) {
		mgr := serviceCRMgr(
			map[k8stypes.NamespacedName]*apiv1.Service{svcNN: annotatedService("team-a", "echo", "tuning")},
			map[k8stypes.NamespacedName]*v3.Backend{crNN: tuningCR("", "leastconn")},
		)
		be := &models.Backend{BackendBase: models.BackendBase{Mode: "http", Abortonclose: "enabled"}}
		if err := mgr.mergeServiceBackendCR(be, serviceHTTPBackendRef("echo"), "team-a"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if be.Abortonclose != "enabled" {
			t.Errorf("expected the route-only field to survive, Abortonclose=%q", be.Abortonclose)
		}
		if be.Balance == nil || be.Balance.Algorithm == nil || *be.Balance.Algorithm != "leastconn" {
			t.Error("expected the service CR to add Balance.Algorithm=leastconn")
		}
	})

	t.Run("namespace/name value resolves in the same namespace", func(t *testing.T) {
		mgr := serviceCRMgr(
			map[k8stypes.NamespacedName]*apiv1.Service{svcNN: annotatedService("team-a", "echo", "team-a/tuning")},
			map[k8stypes.NamespacedName]*v3.Backend{crNN: tuningCR("http", "")},
		)
		be := &models.Backend{BackendBase: models.BackendBase{Mode: "tcp"}}
		_ = mgr.mergeServiceBackendCR(be, serviceHTTPBackendRef("echo"), "team-a")
		if be.Mode != "http" {
			t.Errorf("expected same-namespace ns/name to resolve, Mode=%q", be.Mode)
		}
	})

	t.Run("cross-namespace reference is skipped (not supported yet)", func(t *testing.T) {
		mgr := serviceCRMgr(
			map[k8stypes.NamespacedName]*apiv1.Service{svcNN: annotatedService("team-a", "echo", "other/tuning")},
			map[k8stypes.NamespacedName]*v3.Backend{
				{Namespace: "other", Name: "tuning"}: tuningCR("http", ""),
			},
		)
		be := &models.Backend{BackendBase: models.BackendBase{Mode: "tcp"}}
		_ = mgr.mergeServiceBackendCR(be, serviceHTTPBackendRef("echo"), "team-a")
		if be.Mode != "tcp" {
			t.Errorf("expected cross-namespace to be skipped, Mode=%q", be.Mode)
		}
	})

	t.Run("no annotation is a no-op", func(t *testing.T) {
		mgr := serviceCRMgr(
			map[k8stypes.NamespacedName]*apiv1.Service{svcNN: annotatedService("team-a", "echo", "")},
			nil,
		)
		be := &models.Backend{BackendBase: models.BackendBase{Mode: "tcp"}}
		if err := mgr.mergeServiceBackendCR(be, serviceHTTPBackendRef("echo"), "team-a"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if be.Mode != "tcp" {
			t.Errorf("expected no change without annotation, Mode=%q", be.Mode)
		}
	})

	t.Run("missing Backend CR is a no-op", func(t *testing.T) {
		mgr := serviceCRMgr(
			map[k8stypes.NamespacedName]*apiv1.Service{svcNN: annotatedService("team-a", "echo", "absent")},
			nil,
		)
		be := &models.Backend{BackendBase: models.BackendBase{Mode: "tcp"}}
		if err := mgr.mergeServiceBackendCR(be, serviceHTTPBackendRef("echo"), "team-a"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if be.Mode != "tcp" {
			t.Errorf("expected no change for a missing CR, Mode=%q", be.Mode)
		}
	})
}
