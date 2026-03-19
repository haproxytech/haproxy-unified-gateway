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
	"errors"
	"log/slog"
	"net"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/constants"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"
	utilsk8s "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils-k8s"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type StatusUpdater interface {
	UpdateStatus(ctx context.Context)
}

type StatusUpdaterConf struct {
	logger         *slog.Logger
	client         client.Client
	extractGVK     utilsk8s.ExtractGVK
	controllerName string
	disableIPv4    bool
	disableIPv6    bool
}

type StatusUpdaterImpl struct {
	GatewayClasses map[types.NamespacedName]*tree.GatewayClass
	Gateways       map[types.NamespacedName]*tree.Gateway
	HTTPRoutes     map[types.NamespacedName]*tree.HTTPRoute
	TLSRoutes      map[types.NamespacedName]*tree.TLSRoute
	config         StatusUpdaterConf
}

func NewStatusUpdater(
	cfg StatusUpdaterConf,
	gatewayClasses map[types.NamespacedName]*tree.GatewayClass,
	gateways map[types.NamespacedName]*tree.Gateway,
	httpRoutes map[types.NamespacedName]*tree.HTTPRoute,
	tlsRoutes map[types.NamespacedName]*tree.TLSRoute,
) StatusUpdater {
	return &StatusUpdaterImpl{
		config:         cfg,
		GatewayClasses: gatewayClasses,
		Gateways:       gateways,
		HTTPRoutes:     httpRoutes,
		TLSRoutes:      tlsRoutes,
	}
}

func NewStatusUpdaterConf(
	k8sClient client.Client,
	extractGVK utilsk8s.ExtractGVK,
	controllerName string,
	logger *slog.Logger,
	disableIPv4 bool,
	disableIPv6 bool,
) StatusUpdaterConf {
	return StatusUpdaterConf{
		logger:         logger.With(logging.LogAttrCategory(logging.LogCategoryStatus)),
		extractGVK:     extractGVK,
		client:         k8sClient,
		controllerName: controllerName,
		disableIPv4:    disableIPv4,
		disableIPv6:    disableIPv6,
	}
}

var _ StatusUpdater = &StatusUpdaterImpl{}

func (s *StatusUpdaterImpl) fetchControllerAddresses(ctx context.Context) []gatewayv1.GatewayStatusAddress {
	svcList := &corev1.ServiceList{}
	if err := s.config.client.List(ctx, svcList, client.MatchingLabels{
		constants.HugServiceLabelKey: constants.HugServiceLabelVal,
	}); err != nil {
		s.config.logger.LogAttrs(ctx, slog.LevelError,
			"Failed to list controller service",
			logging.LogAttrError(err),
		)
		return nil
	}
	if len(svcList.Items) == 0 {
		s.config.logger.LogAttrs(ctx, slog.LevelWarn,
			"No controller service found (label app.kubernetes.io/name=haproxy-unified-gateway)",
		)
		return nil
	}

	var addresses []gatewayv1.GatewayStatusAddress
	for i := range svcList.Items {
		svc := &svcList.Items[i]
		switch svc.Spec.Type {
		case corev1.ServiceTypeLoadBalancer:
			addresses = append(addresses, s.addressesFromLoadBalancer(ctx, svc)...)
		case corev1.ServiceTypeNodePort:
			addresses = append(addresses, s.addressesFromNodePort(ctx)...)
		case corev1.ServiceTypeExternalName:
			addresses = append(addresses, s.addressesFromExternalName(svc)...)
		default: // ClusterIP (and unset, which defaults to ClusterIP)
			addresses = append(addresses, s.addressesFromClusterIP(svc)...)
		}
	}
	return addresses
}

// addressesFromLoadBalancer returns addresses from a LoadBalancer service.
// If the ingress list is still empty (provisioning pending), it returns nil so
// callers know the gateway is not yet reachable.
func (s *StatusUpdaterImpl) addressesFromLoadBalancer(_ context.Context, svc *corev1.Service) []gatewayv1.GatewayStatusAddress {
	if len(svc.Status.LoadBalancer.Ingress) == 0 {
		return nil
	}
	ipType := gatewayv1.IPAddressType
	hostnameType := gatewayv1.HostnameAddressType
	var addresses []gatewayv1.GatewayStatusAddress
	for _, ingress := range svc.Status.LoadBalancer.Ingress {
		if ingress.IP != "" {
			if !s.isIPAllowed(ingress.IP) {
				continue
			}
			addresses = append(addresses, gatewayv1.GatewayStatusAddress{
				Type:  &ipType,
				Value: ingress.IP,
			})
		}
		if ingress.Hostname != "" {
			addresses = append(addresses, gatewayv1.GatewayStatusAddress{
				Type:  &hostnameType,
				Value: ingress.Hostname,
			})
		}
	}
	return addresses
}

// addressesFromNodePort returns node ExternalIPs for a NodePort service,
// falling back to InternalIPs if no external addresses are found.
func (s *StatusUpdaterImpl) addressesFromNodePort(ctx context.Context) []gatewayv1.GatewayStatusAddress {
	nodeList := &corev1.NodeList{}
	if err := s.config.client.List(ctx, nodeList); err != nil {
		s.config.logger.LogAttrs(ctx, slog.LevelError,
			"Failed to list nodes for NodePort service address",
			logging.LogAttrError(err),
		)
		return nil
	}
	ipType := gatewayv1.IPAddressType
	var externalIPs, internalIPs []gatewayv1.GatewayStatusAddress
	if len(nodeList.Items) > 0 {
		for _, addr := range nodeList.Items[0].Status.Addresses {
			switch addr.Type {
			case corev1.NodeExternalIP:
				if !s.isIPAllowed(addr.Address) {
					continue
				}
				externalIPs = append(externalIPs, gatewayv1.GatewayStatusAddress{
					Type:  &ipType,
					Value: addr.Address,
				})
			case corev1.NodeInternalIP:
				if !s.isIPAllowed(addr.Address) {
					continue
				}
				internalIPs = append(internalIPs, gatewayv1.GatewayStatusAddress{
					Type:  &ipType,
					Value: addr.Address,
				})
			}
		}
	}
	if len(externalIPs) > 0 {
		return externalIPs
	}
	return internalIPs
}

// addressesFromClusterIP returns ClusterIPs from a ClusterIP service,
// filtering out headless ("None") entries and respecting IPv4/IPv6 disable flags.
func (s *StatusUpdaterImpl) addressesFromClusterIP(svc *corev1.Service) []gatewayv1.GatewayStatusAddress {
	ipType := gatewayv1.IPAddressType
	var addresses []gatewayv1.GatewayStatusAddress
	for _, ip := range svc.Spec.ClusterIPs {
		if ip == "" || ip == "None" {
			continue
		}
		if !s.isIPAllowed(ip) {
			continue
		}
		addresses = append(addresses, gatewayv1.GatewayStatusAddress{
			Type:  &ipType,
			Value: ip,
		})
	}
	return addresses
}

// addressesFromExternalName returns the externalName of an ExternalName service as a Hostname address.
func (*StatusUpdaterImpl) addressesFromExternalName(svc *corev1.Service) []gatewayv1.GatewayStatusAddress {
	if svc.Spec.ExternalName == "" {
		return nil
	}
	hostnameType := gatewayv1.HostnameAddressType
	return []gatewayv1.GatewayStatusAddress{{
		Type:  &hostnameType,
		Value: svc.Spec.ExternalName,
	}}
}

// isIPAllowed returns true if the IP address is permitted by the IPv4/IPv6 disable flags.
func (s *StatusUpdaterImpl) isIPAllowed(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	if parsed.To4() != nil && s.config.disableIPv4 {
		return false
	}
	if parsed.To4() == nil && s.config.disableIPv6 {
		return false
	}
	return true
}

func (s *StatusUpdaterImpl) UpdateStatus(ctx context.Context) {
	// this is just a beginning, needs to be better design
	// let's start with something very basic that updates only GatewayClass status for now

	// GatewayClasses
	for _, gwc := range s.GatewayClasses {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Do not set Status for Deleted or unchanged GatewayClasses
		if gwc.TreeStatus.Status == store.StatusDeleted || gwc.TreeStatus.Status == "" {
			continue
		}
		// Do not set Status for Not managed GatewayClasses
		if !gwc.Managed {
			continue
		}

		s.config.logger.LogAttrs(context.Background(), slog.LevelDebug,
			"Updating status for resource",
			logging.LogAttrResource(gwc.K8sResource, s.config.extractGVK(gwc.K8sResource)),
		)

		s.writeGatewayClassStatus(ctx, gwc)
	}

	// Gateways
	controllerAddresses := s.fetchControllerAddresses(ctx)
	for _, gw := range s.Gateways {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Do not set Status for Deleted or unchanged Gateways
		if gw.TreeStatus.Status == store.StatusDeleted || gw.TreeStatus.Status == "" {
			continue
		}

		s.config.logger.LogAttrs(context.Background(), slog.LevelDebug,
			"Updating status for resource",
			logging.LogAttrResource(gw.K8sResource, s.config.extractGVK(gw.K8sResource)),
		)

		var gwAddresses []gatewayv1.GatewayStatusAddress
		if gw.Valid {
			gwAddresses = controllerAddresses
		}
		s.writeGatewayStatus(ctx, gw, gwAddresses)
	}

	// HTTPRoutes
	for _, route := range s.HTTPRoutes {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Do not set Status for Deleted or unchanged HTTPRoutes
		if route.TreeStatus.Status == store.StatusDeleted || route.TreeStatus.Status == "" {
			continue
		}

		s.config.logger.LogAttrs(context.Background(), slog.LevelDebug,
			"Updating status for resource",
			logging.LogAttrResource(route.K8sResource, s.config.extractGVK(route.K8sResource)),
		)
		s.writeHTTPRouteStatus(ctx, route)
	}

	// TLSRoutes
	for _, tlsRoute := range s.TLSRoutes {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Do not set Status for Deleted or unchanged HTTPRoutes
		if tlsRoute.TreeStatus.Status == store.StatusDeleted || tlsRoute.TreeStatus.Status == "" {
			continue
		}

		s.config.logger.LogAttrs(context.Background(), slog.LevelDebug,
			"Updating status for resource",
			logging.LogAttrResource(tlsRoute.K8sResource, s.config.extractGVK(tlsRoute.K8sResource)),
		)
		s.writeTLSRouteStatus(ctx, tlsRoute)
	}
}

type StatusUpdateParams[T client.Object] struct {
	Object        T
	StatusPatcher StatusPatcher
	Getter        client.Client
	StatusUpdater client.SubResourceWriter
	Logger        *slog.Logger
	extractGVK    utilsk8s.ExtractGVK
	NsName        types.NamespacedName
}

func TryUpdateStatusFunc[T client.Object](param StatusUpdateParams[T]) func(ctx context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		objAttr := logging.LogAttrKeyGVK(param.NsName, param.extractGVK(param.Object))

		clusterObj, err := getClusterObj(ctx, param)
		if err != nil {
			return false, err
		}

		statusAlreadyUpToDate, err := param.StatusPatcher.StatusEqual(clusterObj)
		if err != nil {
			param.Logger.LogAttrs(context.Background(), slog.LevelError,
				"Encountered error when checking status equality",
				objAttr)
			return false, err
		}

		if statusAlreadyUpToDate {
			param.Logger.LogAttrs(context.Background(), slog.LevelDebug,
				"Status already up to date",
				objAttr)
			return true, nil
		}

		// Status update
		if err := param.StatusPatcher.SetStatus(clusterObj); err != nil {
			param.Logger.LogAttrs(context.Background(), slog.LevelError,
				"Encountered error when setting status",
				objAttr)
			return false, nil
		}
		if err := param.StatusUpdater.Update(ctx, clusterObj); err != nil {
			param.Logger.LogAttrs(context.Background(), slog.LevelError,
				"Encountered error when updating status",
				objAttr,
				logging.LogAttrError(err))
			return false, nil
		}

		param.Logger.LogAttrs(context.Background(), slog.LevelDebug,
			"Successfully updated status",
			objAttr,
		)
		return true, nil
	}
}

func TryPatchStatusFunc[T client.Object](param StatusUpdateParams[T]) func(ctx context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		objAttr := logging.LogAttrKeyGVK(param.NsName, param.extractGVK(param.Object))

		clusterObj, err := getClusterObj(ctx, param)
		if err != nil {
			return false, err
		}

		statusAlreadyUpToDate, err := param.StatusPatcher.StatusEqual(clusterObj)
		if err != nil {
			param.Logger.LogAttrs(context.Background(), slog.LevelError,
				"Encountered error when checking status equality",
				objAttr)
			return false, nil
		}

		if statusAlreadyUpToDate {
			param.Logger.LogAttrs(context.Background(), slog.LevelDebug,
				"Status already up to date",
				objAttr)
			return true, nil
		}

		// Status patch
		originalObj, ok := clusterObj.DeepCopyObject().(T) // Create a copy before modification
		if err := param.StatusPatcher.SetStatus(clusterObj); err != nil {
			param.Logger.LogAttrs(context.Background(), slog.LevelError,
				"Encountered error when setting status",
				objAttr)
			return false, nil
		}

		if !ok {
			param.Logger.LogAttrs(context.Background(), slog.LevelError,
				"Encountered error when copying object",
				objAttr)
			return false, nil
		}
		client.MergeFrom(clusterObj)
		if err := param.StatusUpdater.Patch(ctx, clusterObj, client.MergeFrom(originalObj)); err != nil {
			param.Logger.LogAttrs(context.Background(), slog.LevelError,
				"Encountered error when updating status",
				objAttr,
				logging.LogAttrError(err))
			return false, nil
		}

		param.Logger.LogAttrs(context.Background(), slog.LevelDebug,
			"Successfully updated status",
			objAttr,
		)
		return true, nil
	}
}

func getClusterObj[T client.Object](ctx context.Context, param StatusUpdateParams[T]) (T, error) {
	objAttr := logging.LogAttrKeyGVK(param.NsName, param.extractGVK(param.Object))

	// Create a fresh empty object of type T
	clusterObj, ok := param.Object.DeepCopyObject().(T)
	if !ok {
		param.Logger.LogAttrs(context.Background(), slog.LevelError,
			"Encountered error when copying object",
			objAttr)
		return clusterObj, errors.New("failed to copy object")
	}
	err := param.Getter.Get(ctx, types.NamespacedName{
		Namespace: param.NsName.Namespace,
		Name:      param.NsName.Name,
	}, clusterObj)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return clusterObj, nil
		}
		param.Logger.LogAttrs(context.Background(), slog.LevelError,
			"Encountered error when getting resource to update status",
			objAttr)
		return clusterObj, errors.New("failed to get cluster object")
	}
	return clusterObj, nil
}
