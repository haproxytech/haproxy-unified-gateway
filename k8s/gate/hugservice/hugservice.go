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
package hugservice

import (
	"context"
	"log/slog"
	"strings"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/constants"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// reservedPortNames lists service port names that are not managed by
// VirtualListeners and must be preserved as-is when reconciling.
var reservedPortNames = map[string]bool{
	"stat":    true,
	"metrics": true,
}

// ServiceReconciler reconciles the HUG Kubernetes Service ports against the
// active set of VirtualListeners.
type ServiceReconciler struct {
	client client.Client
	logger *slog.Logger
}

// New creates a ServiceReconciler. The provided logger is wrapped with the
// "hugservice" category so its output can be filtered independently.
func New(k8sClient client.Client, logger *slog.Logger) *ServiceReconciler {
	return &ServiceReconciler{
		client: k8sClient,
		logger: logger.With(logging.LogAttrCategory(logging.LogCategoryHugService)),
	}
}

// ReconcilePorts finds all Services labelled with the HUG label and reconciles
// their ports so that:
//   - Reserved ports (stat, metrics) are left untouched.
//   - All other ports are replaced by the current set of VirtualListener ports.
func (r *ServiceReconciler) ReconcilePorts(ctx context.Context, virtualListeners map[string]*tree.VirtualListener) {
	svcList := &corev1.ServiceList{}
	if err := r.client.List(ctx, svcList, client.MatchingLabels{
		constants.HugServiceLabelKey: constants.HugServiceLabelVal,
	}); err != nil {
		r.logger.LogAttrs(ctx, slog.LevelError,
			"Failed to list HUG services for port reconciliation",
			logging.LogAttrError(err),
		)
		return
	}
	if len(svcList.Items) == 0 {
		return
	}

	desiredVLPorts := r.buildVirtualListenerServicePorts(virtualListeners)

	for i := range svcList.Items {
		r.reconcileServicePorts(ctx, &svcList.Items[i], desiredVLPorts)
	}
}

// buildVirtualListenerServicePorts converts the active VirtualListeners into
// ServicePort entries. Port names are derived from the VirtualListener name with
// underscores replaced by hyphens to satisfy the DNS-label requirement.
func (*ServiceReconciler) buildVirtualListenerServicePorts(virtualListeners map[string]*tree.VirtualListener) []corev1.ServicePort {
	ports := make([]corev1.ServicePort, 0, len(virtualListeners))
	for _, vl := range virtualListeners {
		portNum := int32(vl.Port)
		name := strings.ReplaceAll(vl.Name, "_", "-")
		ports = append(ports, corev1.ServicePort{
			Name:       name,
			Protocol:   corev1.ProtocolTCP,
			Port:       portNum,
			TargetPort: intstr.FromInt32(portNum),
		})
	}
	return ports
}

// reconcileServicePorts patches a single Service's port list if it differs from
// the desired state (reserved ports + VirtualListener ports).
func (r *ServiceReconciler) reconcileServicePorts(ctx context.Context, svc *corev1.Service, desiredVLPorts []corev1.ServicePort) {
	desiredPorts := buildDesiredPorts(svc.Spec.Ports, desiredVLPorts)

	if portsEqual(svc.Spec.Ports, desiredPorts) {
		return
	}

	patch := client.MergeFrom(svc.DeepCopy())
	svc.Spec.Ports = desiredPorts
	if err := r.client.Patch(ctx, svc, patch); err != nil {
		r.logger.LogAttrs(ctx, slog.LevelError,
			"Failed to patch HUG service ports",
			slog.String("service", svc.Namespace+"/"+svc.Name),
			logging.LogAttrError(err),
		)
		return
	}
	r.logger.LogAttrs(ctx, slog.LevelInfo,
		"Reconciled HUG service ports",
		slog.String("service", svc.Namespace+"/"+svc.Name),
		slog.Int("ports", len(desiredPorts)),
	)
}

// buildDesiredPorts merges the current Service ports with the desired set of
// VirtualListener-derived ports (keeps reserved ports as-is, append the
// VL ports, carrying over the NodePort assigned on the existing Service
// whenever the port number matches - to keep it stable).
func buildDesiredPorts(existing, desiredVLPorts []corev1.ServicePort) []corev1.ServicePort {
	existingNodePortByPort := make(map[int32]int32, len(existing))
	var reservedPorts []corev1.ServicePort
	for _, p := range existing {
		if p.NodePort != 0 {
			existingNodePortByPort[p.Port] = p.NodePort
		}
		if reservedPortNames[p.Name] {
			reservedPorts = append(reservedPorts, p)
		}
	}

	merged := make([]corev1.ServicePort, 0, len(reservedPorts)+len(desiredVLPorts))
	merged = append(merged, reservedPorts...)
	for _, dp := range desiredVLPorts {
		if dp.NodePort == 0 {
			if np, ok := existingNodePortByPort[dp.Port]; ok {
				dp.NodePort = np
			}
		}
		merged = append(merged, dp)
	}
	return merged
}

// portsEqual returns true if both slices describe the same set of ports
// (matched by name, port, and targetPort, order-independent).
// NodePort is intentionally excluded from the comparison: it is assigned by
// Kubernetes (or the user, via NodePort-typed Services) and carried over by
// buildDesiredPorts, so a reconcile that only differs by NodePort should not
// trigger a patch.
func portsEqual(a, b []corev1.ServicePort) bool {
	if len(a) != len(b) {
		return false
	}
	type portKey struct {
		targetport intstr.IntOrString
		name       string
		port       int32
	}
	aMap := make(map[portKey]bool, len(a))
	for _, p := range a {
		aMap[portKey{name: p.Name, port: p.Port, targetport: p.TargetPort}] = true
	}
	for _, p := range b {
		if !aMap[portKey{name: p.Name, port: p.Port, targetport: p.TargetPort}] {
			return false
		}
	}
	return true
}
