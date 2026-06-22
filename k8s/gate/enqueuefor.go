//
// Copyright 2025 HAProxy Technologies LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controller

import (
	"context"
	"strings"

	v3 "github.com/haproxytech/haproxy-unified-gateway/api/gate/v3"
	"github.com/haproxytech/haproxy-unified-gateway/hug/configuration"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/constants"
	utilsk8s "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils-k8s"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	apiv1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

// enqueueGatewayClassForHugGate returns a handler.EventHandler that enqueues all GatewayClasses
// related to an observed HugGate.
// The relationship is built via the `spec.parametersRef` field in the GatewayClass.
func enqueueGatewayClassForHugGate(ctrlclient client.Client, _ utilsk8s.ExtractGVK) handler.MapFunc {
	return func(ctx context.Context, o client.Object) []reconcile.Request {
		var requests []reconcile.Request

		gwcList := &gatewayv1.GatewayClassList{}

		listOpts := &client.ListOptions{}
		if err := ctrlclient.List(ctx, gwcList, listOpts); err != nil {
			return []reconcile.Request{}
		}

		for _, gwc := range gwcList.Items {
			if paramsRef, ok := getGatewayClassParamsRefKey(gwc); ok {
				if paramsRef.Name == o.GetName() && paramsRef.Namespace == o.GetNamespace() {
					requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
						Namespace: gwc.GetNamespace(),
						Name:      gwc.GetName(),
					}})
				}
			}
		}
		return requests
	}
}

func getGatewayClassParamsRefKey(gwc gatewayv1.GatewayClass) (types.NamespacedName, bool) {
	paramsRef := gwc.Spec.ParametersRef
	if paramsRef == nil {
		return types.NamespacedName{}, false
	}
	return client.ObjectKey{Namespace: utils.NamespaceAsString(paramsRef.Namespace), Name: paramsRef.Name}, true
}

// enqueueGatewayForHugGate returns a handler.EventHandler that enqueues all Gateway:
// Direct:
// - related to an observed HugGate.
// - The relationship is built via the `spec.parametersRef` field in the GatewayClass.
// Indirect:
// - related to the referenced GatewayClass that references this HugGate
// dg is used to restrict processing to a single dedicated Gateway when configured.
func enqueueGatewayForHugGate(dg utils.DedicatedGateway, dns utils.DedicatedNamespaces) func(ctrlclint client.Client, _ utilsk8s.ExtractGVK) handler.MapFunc {
	return func(ctrlclint client.Client, _ utilsk8s.ExtractGVK) handler.MapFunc {
		return func(ctx context.Context, o client.Object) []reconcile.Request {
			var requests []reconcile.Request

			// Gateways
			gwList := &gatewayv1.GatewayList{}

			listOpts := &client.ListOptions{}
			if err := ctrlclint.List(ctx, gwList, listOpts); err != nil {
				return []reconcile.Request{}
			}

			// GatewayClasses
			gwcList := &gatewayv1.GatewayClassList{}
			if err := ctrlclint.List(ctx, gwcList, listOpts); err != nil {
				return []reconcile.Request{}
			}

			for _, gw := range gwList.Items {
				// If dedicated Gateway is configured, skip if the Gateway doesn't match the dedicated Gateway
				if !dg.Check(types.NamespacedName{Namespace: gw.GetNamespace(), Name: gw.GetName()}) {
					continue
				}
				if !dns.Check(types.NamespacedName{Namespace: gw.Namespace, Name: gw.Name}) {
					continue
				}
				// 1. Direct HugGate reference
				if paramsRef, ok := getGatewayParamsRefKey(gw); ok {
					if paramsRef.Name == o.GetName() && paramsRef.Namespace == o.GetNamespace() {
						requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
							Namespace: gw.GetNamespace(),
							Name:      gw.GetName(),
						}})
					}
				}
				// 2. Gateway references a GatewayClass that refenrences this HugGate
				gcName := string(gw.Spec.GatewayClassName)
				for _, gwc := range gwcList.Items {
					// If dedicated Gateway is configured, skip if the Gateway doesn't match the dedicated Gateway
					if gcName == gwc.Name {
						if paramsRef, ok := getGatewayClassParamsRefKey(gwc); ok {
							if paramsRef.Name == o.GetName() && paramsRef.Namespace == o.GetNamespace() {
								requests = append(
									requests, reconcile.Request{NamespacedName: types.NamespacedName{
										Namespace: gw.GetNamespace(),
										Name:      gw.GetName(),
									}},
								)
							}
						}
					}
				}
			}
			return requests
		}
	}
}

func getGatewayParamsRefKey(gw gatewayv1.Gateway) (types.NamespacedName, bool) {
	if gw.Spec.Infrastructure == nil {
		return types.NamespacedName{}, false
	}
	if gw.Spec.Infrastructure.ParametersRef == nil {
		return types.NamespacedName{}, false
	}
	paramsRef := gw.Spec.Infrastructure.ParametersRef
	if paramsRef == nil {
		return types.NamespacedName{}, false
	}
	return client.ObjectKey{Namespace: gw.Namespace, Name: paramsRef.Name}, true
}

// enqueueGatewayForGatewayClass returns a handler.EventHandler that enqueues all Gateways
// related to an observed GatewayClass.
// dg is used to restrict processing to a single dedicated Gateway when configured.
func enqueueGatewayForGatewayClass(dg utils.DedicatedGateway, dns utils.DedicatedNamespaces) func(ctrlclient client.Client, _ utilsk8s.ExtractGVK) handler.MapFunc {
	return func(ctrlclient client.Client, _ utilsk8s.ExtractGVK) handler.MapFunc {
		return func(ctx context.Context, o client.Object) []reconcile.Request {
			var requests []reconcile.Request

			// Gateways
			gwList := &gatewayv1.GatewayList{}

			listOpts := &client.ListOptions{}
			if err := ctrlclient.List(ctx, gwList, listOpts); err != nil {
				return []reconcile.Request{}
			}

			for _, gw := range gwList.Items {
				// If dedicated Gateway is configured, skip if the Gateway doesn't match the dedicated Gateway
				if !dg.Check(types.NamespacedName{Namespace: gw.GetNamespace(), Name: gw.GetName()}) {
					continue
				}
				if !dns.Check(types.NamespacedName{Namespace: gw.Namespace, Name: gw.Name}) {
					continue
				}
				gwcName := string(gw.Spec.GatewayClassName)
				if gwcName == o.GetName() {
					requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
						Namespace: gw.GetNamespace(),
						Name:      gw.GetName(),
					}})
				}
			}
			return requests
		}
	}
}

// enqueueGatewayForHugService returns a handler.EventHandler that enqueues all Gateways
// when the HUG controller service (identified by label app.kubernetes.io/name=haproxy-unified-gateway)
// changes. This is needed so that Gateway.Status.Addresses is refreshed when the service
// type or ingress addresses change (e.g. a LoadBalancer IP is assigned).
// dg is used to restrict processing to a single dedicated Gateway when configured.
func enqueueGatewayForHugService(dg utils.DedicatedGateway, dns utils.DedicatedNamespaces) func(ctrlclient client.Client, _ utilsk8s.ExtractGVK) handler.MapFunc {
	return func(ctrlclient client.Client, _ utilsk8s.ExtractGVK) handler.MapFunc {
		return func(ctx context.Context, _ client.Object) []reconcile.Request {
			gwList := &gatewayv1.GatewayList{}
			if err := ctrlclient.List(ctx, gwList); err != nil {
				return []reconcile.Request{}
			}
			requests := make([]reconcile.Request, 0, len(gwList.Items))
			for _, gw := range gwList.Items {
				// If dedicated Gateway is configured, skip if the Gateway doesn't match the dedicated Gateway
				if !dg.Check(types.NamespacedName{Namespace: gw.GetNamespace(), Name: gw.GetName()}) {
					continue
				}
				if !dns.Check(types.NamespacedName{Namespace: gw.Namespace, Name: gw.Name}) {
					continue
				}
				requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
					Namespace: gw.GetNamespace(),
					Name:      gw.GetName(),
				}})
			}
			return requests
		}
	}
}

// enqueueGatewayForSecret returns a handler.EventHandler that enqueues all Gateways
// related to an observed Secret.
// dg is used to restrict processing to a single dedicated Gateway when configured.
func enqueueGatewayForSecret(dg utils.DedicatedGateway, dns utils.DedicatedNamespaces) func(ctrlclient client.Client, _ utilsk8s.ExtractGVK) handler.MapFunc {
	return func(ctrlclient client.Client, _ utilsk8s.ExtractGVK) handler.MapFunc {
		return func(ctx context.Context, o client.Object) []reconcile.Request {
			var requests []reconcile.Request

			// Gateways
			gwList := &gatewayv1.GatewayList{}

			listOpts := &client.ListOptions{}
			if err := ctrlclient.List(ctx, gwList, listOpts); err != nil {
				return []reconcile.Request{}
			}

			for _, gw := range gwList.Items {
				// If dedicated Gateway is configured, skip if the Gateway doesn't match the dedicated Gateway
				if !dg.Check(types.NamespacedName{Namespace: gw.GetNamespace(), Name: gw.GetName()}) {
					continue
				}
				if !dns.Check(types.NamespacedName{Namespace: gw.Namespace, Name: gw.Name}) {
					continue
				}
				for _, listener := range gw.Spec.Listeners {
					if listener.TLS == nil {
						continue
					}
					for _, certRef := range listener.TLS.CertificateRefs {
						// We only accept v1.Secret
						if !utilsk8s.IsSecretGroupKindSupported(certRef) {
							continue
						}
						secretNsName := utils.GetNamespacedName(certRef.Name, certRef.Namespace, gw.GetNamespace())

						if secretNsName.Name == o.GetName() && secretNsName.Namespace == o.GetNamespace() {
							requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
								Namespace: gw.GetNamespace(),
								Name:      gw.GetName(),
							}})
						}
					}
				}
			}
			return requests
		}
	}
}

// enqueueHTTPRouteForGateway returns a handler.EventHandler that enqueues all HTTPRoutes
// related to an observed Gateway.
// dns is used to restrict processing to the watched namespaces when configured.
func enqueueHTTPRouteForGateway(dns utils.DedicatedNamespaces) func(ctrlclient client.Client, extractGVK utilsk8s.ExtractGVK) handler.MapFunc {
	return func(ctrlclient client.Client, extractGVK utilsk8s.ExtractGVK) handler.MapFunc {
		return func(ctx context.Context, o client.Object) []reconcile.Request {
			var requests []reconcile.Request

			// HTTPRoutes
			routeList := &gatewayv1.HTTPRouteList{}

			listOpts := &client.ListOptions{}
			if err := ctrlclient.List(ctx, routeList, listOpts); err != nil {
				return []reconcile.Request{}
			}

			for _, route := range routeList.Items {
				if !dns.Check(types.NamespacedName{Namespace: route.GetNamespace(), Name: route.GetName()}) {
					continue
				}
				for _, parentRef := range route.Spec.ParentRefs {
					// We only accept v1.Gateway
					if !utilsk8s.IsParentRefGroupKindSupported(parentRef, extractGVK) {
						continue
					}
					gwNsName := utils.GetNamespacedName(parentRef.Name, parentRef.Namespace, route.GetNamespace())

					if gwNsName.Name == o.GetName() && gwNsName.Namespace == o.GetNamespace() {
						requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
							Namespace: route.GetNamespace(),
							Name:      route.GetName(),
						}})
					}
				}
			}

			return requests
		}
	}
}

// enqueueHTTPRouteForService returns a handler.EventHandler that enqueues all HTTPRoutes
// related to an observed Service.
// dns is used to restrict processing to the watched namespaces when configured.
func enqueueHTTPRouteForService(dns utils.DedicatedNamespaces) func(ctrlclient client.Client, extractGVK utilsk8s.ExtractGVK) handler.MapFunc {
	return func(ctrlclient client.Client, extractGVK utilsk8s.ExtractGVK) handler.MapFunc {
		return func(ctx context.Context, o client.Object) []reconcile.Request {
			var requests []reconcile.Request

			// HTTPRoutes
			routeList := &gatewayv1.HTTPRouteList{}

			listOpts := &client.ListOptions{}
			if err := ctrlclient.List(ctx, routeList, listOpts); err != nil {
				return []reconcile.Request{}
			}

			for _, route := range routeList.Items {
				if !dns.Check(types.NamespacedName{Namespace: route.GetNamespace(), Name: route.GetName()}) {
					continue
				}
				for _, rule := range route.Spec.Rules {
					for _, backendRef := range rule.BackendRefs {
						// We only accept v1.Service
						if !utilsk8s.IsBackendRefGroupKindSupported(backendRef.BackendObjectReference, extractGVK) {
							continue
						}
						serviceNsName := utils.GetNamespacedName(backendRef.Name, backendRef.Namespace, route.GetNamespace())

						if serviceNsName.Name == o.GetName() && serviceNsName.Namespace == o.GetNamespace() {
							requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
								Namespace: route.GetNamespace(),
								Name:      route.GetName(),
							}})
						}
					}
				}
			}

			return requests
		}
	}
}

// enqueueHTTPRouteForBackendCR returns a handler.EventHandler that enqueues all HTTPRoutes
// related to an observed Backend.
// dns is used to restrict processing to the watched namespaces when configured.
func enqueueHTTPRouteForBackendCR(dns utils.DedicatedNamespaces) func(ctrlclient client.Client, extractGVK utilsk8s.ExtractGVK) handler.MapFunc {
	return func(ctrlclient client.Client, extractGVK utilsk8s.ExtractGVK) handler.MapFunc {
		return func(ctx context.Context, o client.Object) []reconcile.Request {
			var requests []reconcile.Request

			// HTTPRoutes
			routeList := &gatewayv1.HTTPRouteList{}

			listOpts := &client.ListOptions{}
			if err := ctrlclient.List(ctx, routeList, listOpts); err != nil {
				return []reconcile.Request{}
			}

			// Services whose backend-cr annotation points at the changed Backend CR:
			// routes targeting them must also be re-reconciled so the Service-level
			// merge picks up the change.
			annotatedSvcs := servicesReferencingBackendCR(ctx, ctrlclient, types.NamespacedName{
				Namespace: o.GetNamespace(),
				Name:      o.GetName(),
			})

			for _, route := range routeList.Items {
				if !dns.Check(types.NamespacedName{Namespace: route.GetNamespace(), Name: route.GetName()}) {
					continue
				}
				if !routeUsesBackendCR(&route, o, annotatedSvcs, extractGVK) {
					continue
				}
				requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
					Namespace: route.GetNamespace(),
					Name:      route.GetName(),
				}})
			}
			return requests
		}
	}
}

// routeUsesBackendCR reports whether a route is affected by a change to the
// Backend CR o, either through a route-level cr-backend ExtensionRef or because
// one of its target Services carries a backend-cr annotation resolving to o.
func routeUsesBackendCR(route *gatewayv1.HTTPRoute, o client.Object, annotatedSvcs map[types.NamespacedName]struct{}, extractGVK utilsk8s.ExtractGVK) bool {
	for _, rule := range route.Spec.Rules {
		for _, backendRef := range rule.BackendRefs {
			// Route-level cr-backend ExtensionRef.
			for _, filter := range backendRef.Filters {
				if filter.Type != gatewayv1.HTTPRouteFilterExtensionRef {
					continue
				}
				if !utilsk8s.IsFilterExtensionRefKindSupported(filter.ExtensionRef, extractGVK) {
					continue
				}
				if string(filter.ExtensionRef.Name) == o.GetName() && route.Namespace == o.GetNamespace() {
					return true
				}
			}
			// Service-level backend-cr annotation.
			svcNsName := utils.GetNamespacedName(backendRef.Name, backendRef.Namespace, route.Namespace)
			if _, ok := annotatedSvcs[svcNsName]; ok {
				return true
			}
		}
	}
	return false
}

// servicesReferencingBackendCR lists the Services whose backend-cr annotation
// resolves to the given Backend CR, including cross-namespace references. This is
// used only to decide which routes to re-enqueue, so it does not check the
// ReferenceGrant: over-enqueuing is harmless, and the merge re-validates the
// grant and skips a cross-namespace CR that is not permitted.
func servicesReferencingBackendCR(ctx context.Context, ctrlclient client.Client, cr types.NamespacedName) map[types.NamespacedName]struct{} {
	result := map[types.NamespacedName]struct{}{}
	svcList := &apiv1.ServiceList{}
	if err := ctrlclient.List(ctx, svcList); err != nil {
		return result
	}
	for i := range svcList.Items {
		svc := &svcList.Items[i]
		value := svc.Annotations[constants.ServiceBackendCRAnnotation]
		if value == "" {
			continue
		}
		crNamespace, crName := svc.Namespace, value
		if ns, name, found := strings.Cut(value, "/"); found {
			crNamespace, crName = ns, name
		}
		if crNamespace == cr.Namespace && crName == cr.Name {
			result[types.NamespacedName{Namespace: svc.Namespace, Name: svc.Name}] = struct{}{}
		}
	}
	return result
}

// enqueueTLSRouteForService returns a handler.EventHandler that enqueues all TLSRoutes
// related to an observed Service.
// dns is used to restrict processing to the watched namespaces when configured.
func enqueueTLSRouteForService(dns utils.DedicatedNamespaces) func(ctrlclient client.Client, extractGVK utilsk8s.ExtractGVK) handler.MapFunc {
	return func(ctrlclient client.Client, extractGVK utilsk8s.ExtractGVK) handler.MapFunc {
		return func(ctx context.Context, o client.Object) []reconcile.Request {
			var requests []reconcile.Request

			// TLSRoutes
			routeList := &gatewayv1alpha2.TLSRouteList{}

			listOpts := &client.ListOptions{}
			if err := ctrlclient.List(ctx, routeList, listOpts); err != nil {
				return []reconcile.Request{}
			}

			for _, route := range routeList.Items {
				if !dns.Check(types.NamespacedName{Namespace: route.GetNamespace(), Name: route.GetName()}) {
					continue
				}
				for _, rule := range route.Spec.Rules {
					for _, backendRef := range rule.BackendRefs {
						// We only accept v1.Service
						if !utilsk8s.IsBackendRefGroupKindSupported(backendRef.BackendObjectReference, extractGVK) {
							continue
						}
						serviceNsName := utils.GetNamespacedName(backendRef.Name, backendRef.Namespace, route.GetNamespace())

						if serviceNsName.Name == o.GetName() && serviceNsName.Namespace == o.GetNamespace() {
							requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
								Namespace: route.GetNamespace(),
								Name:      route.GetName(),
							}})
						}
					}
				}
			}

			return requests
		}
	}
}

// enqueueTLSRouteForGateway returns a handler.EventHandler that enqueues all TLSRoutes
// related to an observed Gateway.
// dns is used to restrict processing to the watched namespaces when configured.
func enqueueTLSRouteForGateway(dns utils.DedicatedNamespaces) func(ctrlclient client.Client, extractGVK utilsk8s.ExtractGVK) handler.MapFunc {
	return func(ctrlclient client.Client, extractGVK utilsk8s.ExtractGVK) handler.MapFunc {
		return func(ctx context.Context, o client.Object) []reconcile.Request {
			var requests []reconcile.Request

			// TLSRoutes
			routeList := &gatewayv1alpha2.TLSRouteList{}

			listOpts := &client.ListOptions{}
			if err := ctrlclient.List(ctx, routeList, listOpts); err != nil {
				return []reconcile.Request{}
			}

			for _, route := range routeList.Items {
				if !dns.Check(types.NamespacedName{Namespace: route.GetNamespace(), Name: route.GetName()}) {
					continue
				}
				for _, parentRef := range route.Spec.ParentRefs {
					// We only accept v1.Gateway
					if !utilsk8s.IsParentRefGroupKindSupported(parentRef, extractGVK) {
						continue
					}
					gwNsName := utils.GetNamespacedName(parentRef.Name, parentRef.Namespace, route.GetNamespace())

					if gwNsName.Name == o.GetName() && gwNsName.Namespace == o.GetNamespace() {
						requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
							Namespace: route.GetNamespace(),
							Name:      route.GetName(),
						}})
					}
				}
			}

			return requests
		}
	}
}

// servicesWithBackendCRInNamespace returns the Services whose backend-cr
// annotation resolves cross-namespace to grantNamespace (the referenced Backend
// CR lives in grantNamespace, a different namespace than the Service). These are
// the Services a ReferenceGrant in grantNamespace can affect; a same-namespace
// reference never consults a grant, so it is excluded to avoid spurious enqueues.
func servicesWithBackendCRInNamespace(ctx context.Context, ctrlclient client.Client, grantNamespace string) map[configuration.NamespaceNameValue]struct{} {
	result := map[configuration.NamespaceNameValue]struct{}{}
	svcList := &apiv1.ServiceList{}
	if err := ctrlclient.List(ctx, svcList); err != nil {
		return result
	}
	for _, svc := range svcList.Items {
		value := svc.Annotations[constants.ServiceBackendCRAnnotation]
		if value == "" {
			continue
		}
		crBackendNamespaceName := configuration.NamespaceNameValueFromStringWithDefaultNs(value, svc.Namespace)
		if crBackendNamespaceName.Namespace == grantNamespace &&
			crBackendNamespaceName.Namespace != svc.Namespace {
			result[configuration.NamespaceNameValue{Namespace: svc.Namespace, Name: svc.Name}] = struct{}{}
		}
	}
	return result
}

// enqueueHTTPRouteForReferenceGrant returns a handler.EventHandler that enqueues all HTTPRoutes
// that have a cross-namespace backendRef pointing to the changed ReferenceGrant's namespace.
// A ReferenceGrant lives in the *target* namespace (the namespace of the referenced resource),
// so only routes whose backendRef.Namespace matches the grant's namespace are affected.
func enqueueHTTPRouteForReferenceGrant(ctrlclient client.Client, _ utilsk8s.ExtractGVK) handler.MapFunc {
	return func(ctx context.Context, o client.Object) []reconcile.Request {
		routeList := &gatewayv1.HTTPRouteList{}
		if err := ctrlclient.List(ctx, routeList); err != nil {
			return nil
		}
		svcWithBackendCRInRGNamespace := servicesWithBackendCRInNamespace(ctx, ctrlclient, o.GetNamespace())

		var requests []reconcile.Request
		for _, route := range routeList.Items {
			if httpRouteHasCrossNamespaceRefTo(route, o.GetNamespace(), svcWithBackendCRInRGNamespace) {
				requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
					Namespace: route.Namespace,
					Name:      route.Name,
				}})
			}
		}
		return requests
	}
}

// enqueueGatewayForReferenceGrant returns a handler.EventHandler that enqueues all Gateways
// that have a cross-namespace backendRef pointing to the changed ReferenceGrant's namespace.
// A ReferenceGrant lives in the *target* namespace (the namespace of the referenced resource),
// so only routes whose backendRef.Namespace matches the grant's namespace are affected.
func enqueueGatewayForReferenceGrant(dg utils.DedicatedGateway) func(ctrlclient client.Client, _ utilsk8s.ExtractGVK) handler.MapFunc {
	return func(ctrlclient client.Client, _ utilsk8s.ExtractGVK) handler.MapFunc {
		return func(ctx context.Context, o client.Object) []reconcile.Request {
			gatewayList := &gatewayv1.GatewayList{}
			if err := ctrlclient.List(ctx, gatewayList); err != nil {
				return nil
			}
			var requests []reconcile.Request
			for _, gateway := range gatewayList.Items {
				if !dg.Check(types.NamespacedName{Namespace: gateway.Namespace, Name: gateway.Name}) {
					continue
				}
				if gatewayHasCrossNamespaceRefTo(gateway, o.GetNamespace()) {
					requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
						Namespace: gateway.Namespace,
						Name:      gateway.Name,
					}})
				}
			}
			return requests
		}
	}
}

// httpRouteHasCrossNamespaceRefTo reports whether any backendRef in the route targets
// a resource in targetNamespace from a different namespace.
func httpRouteHasCrossNamespaceRefTo(route gatewayv1.HTTPRoute, targetNamespace string, svcWithBackendCRInRGNamespace map[configuration.NamespaceNameValue]struct{}) bool {
	for _, rule := range route.Spec.Rules {
		for _, backendRef := range rule.BackendRefs {
			backendRefNs := backendRef.Namespace
			backendRefName := backendRef.Name
			if backendRefNs != nil && string(*backendRefNs) == targetNamespace &&
				targetNamespace != route.Namespace {
				return true
			}
			backendRefNsAfterInference := route.Namespace
			if backendRefNs != nil {
				backendRefNsAfterInference = string(*backendRefNs)
			}
			if _, svcFound := svcWithBackendCRInRGNamespace[configuration.NamespaceNameValue{
				Namespace: backendRefNsAfterInference,
				Name:      string(backendRefName),
			}]; svcFound {
				return true
			}
		}
	}
	return false
}

// gatewayHasCrossNamespaceRefTo reports whether any certificateRef in the gateway targets
// a resource in targetNamespace from a different namespace.
func gatewayHasCrossNamespaceRefTo(gateway gatewayv1.Gateway, targetNamespace string) bool {
	for _, listener := range gateway.Spec.Listeners {
		tls := listener.TLS
		if tls == nil {
			continue
		}
		for _, certificateRefs := range tls.CertificateRefs {
			nsNamedCertificateRefs := utils.GetNamespacedName(certificateRefs.Name, certificateRefs.Namespace, gateway.GetNamespace())
			if nsNamedCertificateRefs.Namespace == targetNamespace &&
				targetNamespace != gateway.Namespace {
				return true
			}
		}
	}
	return false
}

// enqueueTLSRouteForReferenceGrant returns a handler.EventHandler that enqueues all TLSRoutes
// that have a cross-namespace backendRef pointing to the changed ReferenceGrant's namespace.
func enqueueTLSRouteForReferenceGrant(ctrlclient client.Client, _ utilsk8s.ExtractGVK) handler.MapFunc {
	return func(ctx context.Context, o client.Object) []reconcile.Request {
		routeList := &gatewayv1alpha2.TLSRouteList{}
		if err := ctrlclient.List(ctx, routeList); err != nil {
			return nil
		}
		svcWithBackendCRInRGNamespace := servicesWithBackendCRInNamespace(ctx, ctrlclient, o.GetNamespace())

		var requests []reconcile.Request
		for _, route := range routeList.Items {
			if tlsRouteHasCrossNamespaceRefTo(route, o.GetNamespace(), svcWithBackendCRInRGNamespace) {
				requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
					Namespace: route.Namespace,
					Name:      route.Name,
				}})
			}
		}
		return requests
	}
}

// tlsRouteHasCrossNamespaceRefTo reports whether the route is affected by a grant in
// targetNamespace: either a backendRef directly targets a resource in targetNamespace
// from a different namespace, or one of its target Services carries a backend-cr
// annotation resolving cross-namespace to targetNamespace (svcWithBackendCRInRGNamespace).
func tlsRouteHasCrossNamespaceRefTo(route gatewayv1alpha2.TLSRoute, targetNamespace string, svcWithBackendCRInRGNamespace map[configuration.NamespaceNameValue]struct{}) bool {
	for _, rule := range route.Spec.Rules {
		for _, backendRef := range rule.BackendRefs {
			backendRefNs := backendRef.Namespace
			if backendRefNs != nil && string(*backendRefNs) == targetNamespace &&
				targetNamespace != route.Namespace {
				return true
			}
			backendRefNsAfterInference := route.Namespace
			if backendRefNs != nil {
				backendRefNsAfterInference = string(*backendRefNs)
			}
			if _, svcFound := svcWithBackendCRInRGNamespace[configuration.NamespaceNameValue{
				Namespace: backendRefNsAfterInference,
				Name:      string(backendRef.Name),
			}]; svcFound {
				return true
			}
		}
	}
	return false
}

// enqueueTLSRouteForBackendCR re-enqueues every TLSRoute whose target Service carries a
// backend-cr annotation pointing at the changed Backend CR, so the Service-level merge
// picks up the change. TLSRoutes have no route-level cr-backend ExtensionRef, so only the
// Service-annotation path applies here (unlike enqueueHTTPRouteForBackendCR).
func enqueueTLSRouteForBackendCR(dns utils.DedicatedNamespaces) func(ctrlclient client.Client, extractGVK utilsk8s.ExtractGVK) handler.MapFunc {
	return func(ctrlclient client.Client, _ utilsk8s.ExtractGVK) handler.MapFunc {
		return func(ctx context.Context, o client.Object) []reconcile.Request {
			routeList := &gatewayv1alpha2.TLSRouteList{}
			if err := ctrlclient.List(ctx, routeList); err != nil {
				return nil
			}

			annotatedSvcs := servicesReferencingBackendCR(ctx, ctrlclient, types.NamespacedName{
				Namespace: o.GetNamespace(),
				Name:      o.GetName(),
			})

			var requests []reconcile.Request
			for _, route := range routeList.Items {
				if !dns.Check(types.NamespacedName{Namespace: route.GetNamespace(), Name: route.GetName()}) {
					continue
				}
				if !tlsRouteUsesServiceBackendCR(&route, annotatedSvcs) {
					continue
				}
				requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
					Namespace: route.GetNamespace(),
					Name:      route.GetName(),
				}})
			}
			return requests
		}
	}
}

// tlsRouteUsesServiceBackendCR reports whether any backendRef of the route targets one of
// the Services in annotatedSvcs (Services whose backend-cr annotation points at a given CR).
func tlsRouteUsesServiceBackendCR(route *gatewayv1alpha2.TLSRoute, annotatedSvcs map[types.NamespacedName]struct{}) bool {
	for _, rule := range route.Spec.Rules {
		for _, backendRef := range rule.BackendRefs {
			svcNsName := utils.GetNamespacedName(backendRef.Name, backendRef.Namespace, route.Namespace)
			if _, ok := annotatedSvcs[svcNsName]; ok {
				return true
			}
		}
	}
	return false
}

// enqueueHugConfForDefaultsCR returns a handler.EventHandler that enqueues all HugConf
// objects whose DefaultsRef points to the observed Defaults CR.
func enqueueHugConfForDefaultsCR(ctrlclient client.Client, extractGVK utilsk8s.ExtractGVK) handler.MapFunc {
	return func(ctx context.Context, o client.Object) []reconcile.Request {
		var requests []reconcile.Request

		// HugConfs
		hugConfList := &v3.HugConfList{}

		listOpts := &client.ListOptions{}
		if err := ctrlclient.List(ctx, hugConfList, listOpts); err != nil {
			return []reconcile.Request{}
		}

		for _, hugConf := range hugConfList.Items {
			defaultsRef := hugConf.Spec.DefaultsRef

			if defaultsRef == nil {
				continue
			}
			if !utilsk8s.IsDefaultsRefGroupKindSupported(*defaultsRef, extractGVK) { // defaultsRef is not nil, checked before
				continue
			}

			// We only accept v3.Defaults
			defaultsNsName := utils.GetNamespacedName(defaultsRef.Name, defaultsRef.Namespace, hugConf.Namespace)
			if defaultsNsName.Name == o.GetName() && defaultsNsName.Namespace == o.GetNamespace() {
				{
					requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
						Namespace: hugConf.GetNamespace(),
						Name:      hugConf.GetName(),
					}})
				}
			}
		}
		return requests
	}
}

// enqueueHugConfForGlobal returns a handler.EventHandler that enqueues all HugConf
// related to an observed Global CR.
func enqueueHugConfForGlobalCR(ctrlclient client.Client, extractGVK utilsk8s.ExtractGVK) handler.MapFunc {
	return func(ctx context.Context, o client.Object) []reconcile.Request {
		var requests []reconcile.Request

		// HugConfs
		hugConfList := &v3.HugConfList{}

		listOpts := &client.ListOptions{}
		if err := ctrlclient.List(ctx, hugConfList, listOpts); err != nil {
			return []reconcile.Request{}
		}

		for _, hugConf := range hugConfList.Items {
			globalRef := hugConf.Spec.GlobalRef

			if globalRef == nil {
				continue
			}
			if !utilsk8s.IsGlobalRefGroupKindSupported(*globalRef, extractGVK) { // globalRef is not nil, checked before
				continue
			}

			// We only accept v3.Global
			globalNsName := utils.GetNamespacedName(globalRef.Name, globalRef.Namespace, hugConf.Namespace)
			if globalNsName.Name == o.GetName() && globalNsName.Namespace == o.GetNamespace() {
				{
					requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
						Namespace: hugConf.GetNamespace(),
						Name:      hugConf.GetName(),
					}})
				}
			}
		}
		return requests
	}
}

// enqueueIngressesForIngressClass returns a handler.EventHandler that enqueues all Ingresses
// that reference the IngressClass.
func enqueueIngressesForIngressClass(ctrlclient client.Client, _ utilsk8s.ExtractGVK) handler.MapFunc {
	return func(ctx context.Context, o client.Object) []reconcile.Request {
		ingressList := &networkingv1.IngressList{}
		if err := ctrlclient.List(ctx, ingressList); err != nil {
			return nil
		}
		var requests []reconcile.Request
		ingressClass := o.(*networkingv1.IngressClass)
		for _, ingress := range ingressList.Items {
			if utils.PointerDefaultValueIfNil(ingress.Spec.IngressClassName) == ingressClass.ObjectMeta.Name {
				requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
					Namespace: ingress.Namespace,
					Name:      ingress.Name,
				}})
			}
		}
		return requests
	}
}
