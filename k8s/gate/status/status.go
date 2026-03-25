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
	objtypes "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/object-types"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"
	utilsk8s "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils-k8s"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/gateway-api/apis/v1alpha2"
)

const (
	updateStatusChanSize = 50
)

// PreparedStatusUpdates holds status write operations prepared from a tree
// snapshot. It is produced synchronously by PrepareStatusUpdate and consumed
// asynchronously by UpdateStatus.
type PreparedStatusUpdates []func(context.Context)

type StatusUpdater interface {
	// Start launches the background goroutine that processes status writes.
	// It must be called once before UpdateStatus.
	Start(ctx context.Context)
	// PrepareStatusUpdate must be called synchronously in the batch loop while
	// the tree is still valid. It snapshots all relevant state from the tree
	// nodes into self-contained write operations and returns them.
	PrepareStatusUpdate(ctx context.Context,
		gatewayClasses map[types.NamespacedName]*tree.GatewayClass,
		gateways map[types.NamespacedName]*tree.Gateway,
		httpRoutes map[types.NamespacedName]*tree.HTTPRoute,
		tlsRoutes map[types.NamespacedName]*tree.TLSRoute) PreparedStatusUpdates
	// UpdateStatus dispatches a previously prepared batch of writes to the
	// background goroutine. Any stale pending batch is replaced by the new one.
	UpdateStatus(ctx context.Context, updates PreparedStatusUpdates)
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
	updates chan PreparedStatusUpdates
	config  StatusUpdaterConf
}

func NewStatusUpdater(cfg StatusUpdaterConf) StatusUpdater {
	cfg.logger = cfg.logger.With(logging.LogAttrCategory(logging.LogCategoryStatus))
	return &StatusUpdaterImpl{
		config:  cfg,
		updates: make(chan PreparedStatusUpdates, updateStatusChanSize),
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
		logger:         logger,
		extractGVK:     extractGVK,
		client:         k8sClient,
		controllerName: controllerName,
		disableIPv4:    disableIPv4,
		disableIPv6:    disableIPv6,
	}
}

var _ StatusUpdater = &StatusUpdaterImpl{}

// Start launches a background goroutine that drains the updates channel and
// executes each write using the provided context for its lifetime.
func (s *StatusUpdaterImpl) Start(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case writes, ok := <-s.updates:
				if !ok {
					return
				}
				for _, write := range writes {
					write(ctx)
				}
			}
		}
	}()
}

// PrepareStatusUpdate must be called synchronously in the batch loop while the
// tree is still valid. It snapshots all relevant state from the tree nodes into
// self-contained write operations and returns them as a PreparedStatusUpdates
// batch ready to be dispatched via UpdateStatus.
func (s *StatusUpdaterImpl) PrepareStatusUpdate(ctx context.Context,
	gatewayClasses map[types.NamespacedName]*tree.GatewayClass,
	gateways map[types.NamespacedName]*tree.Gateway,
	httpRoutes map[types.NamespacedName]*tree.HTTPRoute,
	tlsRoutes map[types.NamespacedName]*tree.TLSRoute,
) PreparedStatusUpdates {
	var writes PreparedStatusUpdates
	writes = append(writes, s.prepareGatewayClassUpdates(gatewayClasses)...)
	writes = append(writes, s.prepareGatewayUpdates(ctx, gateways)...)
	writes = append(writes, s.prepareHTTPRouteUpdates(httpRoutes)...)
	writes = append(writes, s.prepareTLSRouteUpdates(tlsRoutes)...)
	return writes
}

func (s *StatusUpdaterImpl) prepareGatewayClassUpdates(gatewayClasses map[types.NamespacedName]*tree.GatewayClass) PreparedStatusUpdates {
	var writes PreparedStatusUpdates
	for _, gwc := range gatewayClasses {
		if gwc.TreeStatus.Status == store.StatusDeleted || gwc.TreeStatus.Status == "" {
			continue
		}
		if !gwc.Managed {
			continue
		}
		s.config.logger.LogAttrs(context.Background(), slog.LevelDebug,
			"Preparing status update",
			logging.LogAttrResource(gwc.K8sResource, s.config.extractGVK(gwc.K8sResource)),
		)
		params := StatusUpdateParams[*gatewayv1.GatewayClass]{
			Object:        objtypes.ObjectTypeGatewayClass,
			NsName:        types.NamespacedName{Name: gwc.K8sResource.Name, Namespace: gwc.K8sResource.Namespace},
			StatusPatcher: newGatewayClassStatusPatcher(gwc),
			Getter:        s.config.client,
			StatusUpdater: s.config.client.Status(),
			Logger:        s.config.logger,
			extractGVK:    s.config.extractGVK,
		}
		writes = append(writes, func(ctx context.Context) {
			s.writeGatewayClassStatus(ctx, params)
		})
	}
	return writes
}

func (s *StatusUpdaterImpl) prepareGatewayUpdates(ctx context.Context, gateways map[types.NamespacedName]*tree.Gateway) PreparedStatusUpdates {
	controllerAddresses := s.fetchControllerAddresses(ctx)
	var writes PreparedStatusUpdates
	for _, gw := range gateways {
		if gw.TreeStatus.Status == store.StatusDeleted || gw.TreeStatus.Status == "" {
			continue
		}
		s.config.logger.LogAttrs(context.Background(), slog.LevelDebug,
			"Preparing status update for resource",
			logging.LogAttrResource(gw.K8sResource, s.config.extractGVK(gw.K8sResource)),
		)
		var gwAddresses []gatewayv1.GatewayStatusAddress
		if gw.Valid {
			gwAddresses = controllerAddresses
		}
		params := StatusUpdateParams[*gatewayv1.Gateway]{
			Object:        objtypes.ObjectTypeGateway,
			NsName:        types.NamespacedName{Name: gw.K8sResource.Name, Namespace: gw.K8sResource.Namespace},
			StatusPatcher: newGatewayStatusPatcher(gw, gwAddresses),
			Getter:        s.config.client,
			StatusUpdater: s.config.client.Status(),
			Logger:        s.config.logger,
			extractGVK:    s.config.extractGVK,
		}
		writes = append(writes, func(ctx context.Context) {
			s.writeGatewayStatus(ctx, params)
		})
	}
	return writes
}

func (s *StatusUpdaterImpl) prepareHTTPRouteUpdates(httpRoutes map[types.NamespacedName]*tree.HTTPRoute) PreparedStatusUpdates {
	var writes PreparedStatusUpdates
	for _, route := range httpRoutes {
		if route.TreeStatus.Status == store.StatusDeleted || route.TreeStatus.Status == "" {
			continue
		}
		s.config.logger.LogAttrs(context.Background(), slog.LevelDebug,
			"Preparing status update for resource",
			logging.LogAttrResource(route.K8sResource, s.config.extractGVK(route.K8sResource)),
		)
		params := StatusUpdateParams[*gatewayv1.HTTPRoute]{
			Object:        objtypes.ObjectTypeHTTPRoute,
			NsName:        types.NamespacedName{Name: route.K8sResource.Name, Namespace: route.K8sResource.Namespace},
			StatusPatcher: newHTTPRouteStatusPatcher(route),
			Getter:        s.config.client,
			StatusUpdater: s.config.client.Status(),
			Logger:        s.config.logger,
			extractGVK:    s.config.extractGVK,
		}
		writes = append(writes, func(ctx context.Context) {
			s.writeHTTPRouteStatus(ctx, params)
		})
	}
	return writes
}

func (s *StatusUpdaterImpl) prepareTLSRouteUpdates(tlsRoutes map[types.NamespacedName]*tree.TLSRoute) PreparedStatusUpdates {
	var writes PreparedStatusUpdates
	for _, tlsRoute := range tlsRoutes {
		if tlsRoute.TreeStatus.Status == store.StatusDeleted || tlsRoute.TreeStatus.Status == "" {
			continue
		}
		s.config.logger.LogAttrs(context.Background(), slog.LevelDebug,
			"Preparing status update for resource",
			logging.LogAttrResource(tlsRoute.K8sResource, s.config.extractGVK(tlsRoute.K8sResource)),
		)
		params := StatusUpdateParams[*v1alpha2.TLSRoute]{
			Object:        objtypes.ObjectTypeTLSRoute,
			NsName:        types.NamespacedName{Name: tlsRoute.K8sResource.Name, Namespace: tlsRoute.K8sResource.Namespace},
			StatusPatcher: newTLSRouteStatusPatcher(tlsRoute),
			Getter:        s.config.client,
			StatusUpdater: s.config.client.Status(),
			Logger:        s.config.logger,
			extractGVK:    s.config.extractGVK,
		}
		writes = append(writes, func(ctx context.Context) {
			s.writeTLSRouteStatus(ctx, params)
		})
	}
	return writes
}

// UpdateStatus dispatches a previously prepared batch of writes to the
// background goroutine. If the channel buffer is full the batch is discarded
// and a warning is logged so the reconciler never blocks on status writes.
func (s *StatusUpdaterImpl) UpdateStatus(ctx context.Context, updates PreparedStatusUpdates) {
	if len(updates) == 0 {
		return
	}

	select {
	case s.updates <- updates:
	case <-ctx.Done():
	default:
		s.config.logger.LogAttrs(ctx, slog.LevelWarn,
			"Status update channel full, discarding update batch",
		)
	}
}

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
