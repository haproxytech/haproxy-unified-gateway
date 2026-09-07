package tree

import (
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	v1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

var _ Builder = &ReferenceGrantBuilderImpl{}

type ReferenceGrantBuilderImpl struct {
	controllerStore       *ControllerStore
	referenceGrantManager *ReferenceGrantManager
}

func NewReferenceGrantBuilder(controllerStore *ControllerStore, referenceGrantManager *ReferenceGrantManager) Builder {
	return &ReferenceGrantBuilderImpl{
		controllerStore:       controllerStore,
		referenceGrantManager: referenceGrantManager,
	}
}

// --------------------
// GateTree Updates
// --------------------

func (b *ReferenceGrantBuilderImpl) ComputeTreeUpdates() {
	for rgKey, rgUpdate := range b.controllerStore.ClusterStore.Updates.ReferenceGrants {
		existingReferenceGrant := b.controllerStore.GateTree.ReferenceGrants[rgKey]
		switch {
		case rgUpdate.Status == store.StatusUpserted:
			if existingReferenceGrant != nil {
				existingReferenceGrant.SetAsUpserted(b.controllerStore.Logger, rgUpdate.NewObject)
				b.referenceGrantManager.UpsertReferenceGrant(*existingReferenceGrant)
			} else {
				newReferenceGrant := NewReferenceGrant(rgUpdate.NewObject)
				b.controllerStore.GateTree.ReferenceGrants[rgKey] = newReferenceGrant
				b.referenceGrantManager.UpsertReferenceGrant(*newReferenceGrant)
			}
		case rgUpdate.Status == store.StatusDeleted && existingReferenceGrant != nil:
			existingReferenceGrant.SetAsDeleted(b.controllerStore.Logger)
			b.referenceGrantManager.RemoveReferenceGrant(*existingReferenceGrant)
		}
	}
	if len(b.controllerStore.ClusterStore.Updates.ReferenceGrants) > 0 {
		b.referenceGrantManager.ComputeToFrom()
		// The grants index is authoritative from here on, so the objects it
		// governs must be rechecked in this very cycle, whatever order their
		// watch events arrive in.
		b.recheckGrantDependents()
	}
}

// recheckGrantDependents injects indirect updates for the managed objects whose
// cross-namespace references are governed by the grants changed in this cycle.
// Without it, a revocation would keep serving traffic when the dependent's
// watch-triggered re-enqueue lands in a later batch than the grant change.
func (b *ReferenceGrantBuilderImpl) recheckGrantDependents() {
	namespaces := map[string]struct{}{}
	for _, rgUpdate := range b.controllerStore.ClusterStore.Updates.ReferenceGrants {
		if rgUpdate.NewObject != nil {
			namespaces[rgUpdate.NewObject.Namespace] = struct{}{}
		}
		if rgUpdate.OldObject != nil {
			namespaces[rgUpdate.OldObject.Namespace] = struct{}{}
		}
	}
	if len(namespaces) == 0 {
		return
	}

	for routeKey, route := range b.controllerStore.GateTree.HTTPRoutes {
		if route == nil || route.K8sResource == nil || route.TreeStatus.Status == store.StatusDeleted {
			continue
		}
		if _, present := b.controllerStore.ClusterStore.Updates.HTTPRoutes[routeKey]; present {
			continue
		}
		if !routeReferencesNamespace(route.Rules, route.K8sResource.Namespace, namespaces) {
			continue
		}
		b.controllerStore.ClusterStore.Updates.HTTPRoutes[routeKey] = store.Update[*gatewayv1.HTTPRoute]{
			NewObject: route.K8sResource,
			OldObject: route.K8sResource,
			Status:    store.StatusUpserted,
			Indirect:  true,
		}
	}

	for routeKey, route := range b.controllerStore.GateTree.TLSRoutes {
		if route == nil || route.K8sResource == nil || route.TreeStatus.Status == store.StatusDeleted {
			continue
		}
		if _, present := b.controllerStore.ClusterStore.Updates.TLSRoutes[routeKey]; present {
			continue
		}
		if !tlsRouteReferencesNamespace(route, namespaces) {
			continue
		}
		b.controllerStore.ClusterStore.Updates.TLSRoutes[routeKey] = store.Update[*v1alpha2.TLSRoute]{
			NewObject: route.K8sResource,
			OldObject: route.K8sResource,
			Status:    store.StatusUpserted,
			Indirect:  true,
		}
	}

	for gwKey, gw := range b.controllerStore.GateTree.Gateways {
		if gw == nil || gw.K8sResource == nil || gw.TreeStatus.Status == store.StatusDeleted {
			continue
		}
		if _, present := b.controllerStore.ClusterStore.Updates.Gateways[gwKey]; present {
			continue
		}
		if !gatewayReferencesNamespace(gw, namespaces) {
			continue
		}
		b.controllerStore.ClusterStore.Updates.Gateways[gwKey] = store.Update[*gatewayv1.Gateway]{
			NewObject: gw.K8sResource,
			OldObject: gw.K8sResource,
			Status:    store.StatusUpserted,
			Indirect:  true,
		}
	}
}

// refNamespaceHits reports whether the reference namespace is cross-namespace
// and targets one of the changed grants' namespaces.
func refNamespaceHits(refNs *gatewayv1.Namespace, ownerNs string, namespaces map[string]struct{}) bool {
	if refNs == nil || string(*refNs) == ownerNs {
		return false
	}
	_, hit := namespaces[string(*refNs)]
	return hit
}

// routeReferencesNamespace reports whether any rule of the HTTPRoute holds a
// cross-namespace backend reference into one of the namespaces.
func routeReferencesNamespace(rules []*HTTPRouteRule, routeNs string, namespaces map[string]struct{}) bool {
	for _, rule := range rules {
		if rule == nil {
			continue
		}
		for _, backendRef := range rule.K8sResource.BackendRefs {
			if refNamespaceHits(backendRef.BackendObjectReference.Namespace, routeNs, namespaces) {
				return true
			}
		}
	}
	return false
}

// tlsRouteReferencesNamespace reports whether any rule of the TLSRoute holds a
// cross-namespace backend reference into one of the namespaces.
func tlsRouteReferencesNamespace(route *TLSRoute, namespaces map[string]struct{}) bool {
	for _, rule := range route.Rules {
		if rule == nil {
			continue
		}
		for _, backendRef := range rule.K8sResource.BackendRefs {
			if refNamespaceHits(backendRef.BackendObjectReference.Namespace, route.K8sResource.Namespace, namespaces) {
				return true
			}
		}
	}
	return false
}

// gatewayReferencesNamespace reports whether any listener of the Gateway holds
// a cross-namespace certificate reference into one of the namespaces.
func gatewayReferencesNamespace(gw *Gateway, namespaces map[string]struct{}) bool {
	// The merged list on purpose: an entry a ListenerSet contributes holds a
	// certificateRef of its own, and a grant that permits it must revoke it the same
	// way. The accessor is what knows where that entry's TLS config lives.
	for _, listener := range gw.Listeners {
		if listener == nil || listener.TLS() == nil {
			continue
		}
		for _, certRef := range listener.TLS().CertificateRefs {
			if refNamespaceHits(certRef.Namespace, gw.K8sResource.Namespace, namespaces) {
				return true
			}
		}
	}
	return false
}

func (b *ReferenceGrantBuilderImpl) CleanTreeUpdates() {
	cleanTreeUpdates(b.controllerStore.GateTree.ReferenceGrants)
}
