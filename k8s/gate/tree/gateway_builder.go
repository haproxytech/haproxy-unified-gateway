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
package tree

import (
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/protocols"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	utilsk8s "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils-k8s"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

var _ Builder = &GatewayBuilderImpl{}

type GatewayBuilderImpl struct {
	// certStorage storage.CertificateStorage
	*ControllerStore
	referenceGrantManager *ReferenceGrantManager
}

// conflictKinds is the set of conflicts one listener is in on its port.
//
// A set rather than a single kind, because a listener can be in both -- and the
// Gateway API has a reason for exactly that case, ListenerConflict, which only a
// ListenerSet entry can report. A detection pass that had to pick one kind could
// never express it.
//
// The zero value is "no conflict", which is what lets the table hold an entry for
// every listener without a separate flag saying whether it conflicts: the state
// "no conflict, but a leftover reason" becomes unrepresentable rather than merely
// unused. It is also what keeps the value comparable, so the verdict diff that
// decides which Gateways to re-upsert is a == on a uint8, with no equality
// function to write or to forget.
type conflictKinds uint8

const (
	conflictOnProtocol conflictKinds = 1 << iota
	conflictOnHostname
)

func (k conflictKinds) any() bool { return k != 0 }

func (k conflictKinds) has(kind conflictKinds) bool { return k&kind != 0 }

// names lists the kinds in the set, for the condition message. The order is fixed
// rather than derived from the bits, so a message is stable across cycles and
// therefore assertable.
func (k conflictKinds) names() []string {
	names := make([]string, 0, 2)
	if k.has(conflictOnProtocol) {
		names = append(names, "protocol")
	}
	if k.has(conflictOnHostname) {
		names = append(names, "hostname")
	}
	return names
}

// conflictsByListener is every listener bound to one port and the conflicts it is
// in, keyed by listener so that both readers -- the check that reports one
// listener, and the diff that asks what a listener's verdict was -- are a direct
// lookup.
//
// It holds an entry for every listener on the port, in no conflict included. That
// totality is what makes the diff sound: an absent entry is a real difference and
// not a gap.
type conflictsByListener map[listenerKey]conflictKinds

type GatewayBuilderParams struct {
	// storage.CertificateStorage
	*ControllerStore
	*ReferenceGrantManager
}

func NewGatewayBuilder(params GatewayBuilderParams) Builder {
	return &GatewayBuilderImpl{
		ControllerStore: params.ControllerStore,
		// certStorage:           params.CertificateStorage,
		referenceGrantManager: params.ReferenceGrantManager,
	}
}

// --------------------
// GateTree Updates
// --------------------

func (b *GatewayBuilderImpl) ComputeTreeUpdates() {
	b.resetListenerConflicts()

	// Class events must recover Gateways whose first batch preceded their class.
	for classKey := range b.ClusterStore.Updates.GatewayClasses {
		for gatewayKey, gateway := range b.ClusterStore.Gateways {
			if string(gateway.Spec.GatewayClassName) != classKey.Name {
				continue
			}
			if _, present := b.ClusterStore.Updates.Gateways[gatewayKey]; present {
				continue
			}
			b.ClusterStore.Updates.Gateways[gatewayKey] = store.Update[*gatewayv1.Gateway]{
				OldObject: gateway,
				NewObject: gateway,
				Status:    store.StatusUpserted,
				Indirect:  true,
			}
		}
	}

	// After this step, the clusterStore.Updates contains all impacted Gateways
	// Including the one impacted by:
	// - GatewayClass updates
	b.computeGateTreeUpdates()
}

func (b *GatewayBuilderImpl) computeGateTreeUpdates() {
	// Compute the upserted/delete Tree Gateways
	for gwKey, gwUpdate := range b.ClusterStore.Updates.Gateways {
		b.computeTreeGatewayUpdate(gwKey, gwUpdate)
	}

	// The listener lists have to exist before conflicts are detected: detection
	// compares listeners with each other, and a verdict depends on the others
	// sharing the port. So every list is built, then the whole tree is compared,
	// then each listener is judged.
	b.constructListenersOfUpsertedGateways()

	// Check for conflicts
	// Add all Gateways that have a conflict in the list of updated Gateways
	// in order to recompute the checks and status (both Gateway and listeners)
	b.checkListenerConflicts()

	// A Gateway re-upserted by the conflict diff has its listener list rebuilt too, so
	// the routes that serve it have to be marked after this point, not before.
	b.markRoutesOfRebuiltGateways()

	for _, treeGw := range b.ControllerStore.GateTree.Gateways {
		if treeGw == nil || treeGw.TreeStatus.Status != store.StatusUpserted {
			continue
		}

		if treeGw.isManaged() {
			// Process Listeners
			b.checkListeners(treeGw)

			// Compute status only if managed Gateway
			// If not managed, then we should not update the status
			// Build Listener conditions
			for _, listener := range treeGw.Listeners {
				listener.BuildConditions(treeGw, b.previousStatusOf(listener))
			}
			treeGw.checkListenerConflicts(b.ControllerStore.conflictsByPort)
			treeGw.BuildConditions()
		}
	}
}

func (b *GatewayBuilderImpl) computeTreeGatewayUpdate(gwKey client.ObjectKey, gwUpdate store.Update[*gatewayv1.Gateway]) {
	var treeGw *Gateway
	alreadyManagedTreeGw, alreadyManagedTreeGwOK := b.GateTree.Gateways[gwKey]
	alreadyUnmanagedTreeGw, alreadyUnmanagedTreeGwOK := b.UnmanagedGateTree.Gateways[gwKey]

	if alreadyManagedTreeGwOK {
		treeGw = alreadyManagedTreeGw
	} else if alreadyUnmanagedTreeGwOK {
		treeGw = alreadyUnmanagedTreeGw
	}

	switch gwUpdate.Status {
	case store.StatusUpserted:
		if treeGw != nil {
			treeGw.SetAsUpserted(b.Logger, gwUpdate.NewObject)
		} else {
			treeGw = NewGateway(gwUpdate.NewObject)
		}
		if utils.IsSyntheticName(treeGw.K8sResource.Name) {
			treeGw.GatewayForIngress = true
		}
		// Do we keep it in Managed or Unmanaged???
		b.processManagementChecks(treeGw)

	case store.StatusDeleted:
		if treeGw != nil {
			treeGw.SetAsDeleted(b.Logger, gwUpdate.OldObject)
		}
		// else nothing to do
		// It did not exists, it's deleted, noop
	}
}

func (b *GatewayBuilderImpl) processManagementChecks(treeGw *Gateway) {
	// If the GatewayClass is not in the store, it means that the GatewayClass is not managed by our controller
	if ok := b.ControllerStore.CheckGatewayClassExists(string(treeGw.K8sResource.Spec.GatewayClassName)); !ok {
		return
	}

	treeGw.checkParametersRef(*b.ControllerStore)
	treeGw.checkGatewayClassIsValid(*b.ControllerStore)
	treeGw.Valid = treeGw.CheckParamsRef.Valid && treeGw.CheckValidGatewayClass.Valid

	if treeGw.isManaged() {
		treeGw.SetAsManaged(b.Logger, *b.ControllerStore)
	} else {
		treeGw.SetAsUnmanaged(b.Logger, *b.ControllerStore)
	}
}

// -----------------------------------------------

func (b *GatewayBuilderImpl) CleanTreeUpdates() {
	cleanTreeUpdates(b.GateTree.Gateways)
	cleanTreeUpdates(b.UnmanagedGateTree.Gateways)
}

// markRoutesOfRebuiltGateways re-marks for processing every route that serves a Gateway
// rebuilt or deleted this cycle.
//
// constructMergedListeners recreates the whole listening surface of an upserted Gateway
// -- its own listeners and the entries its ListenerSets contribute -- with fresh
// AttachedRoutes maps, and the conflict diff can re-upsert a Gateway on top of that. A
// route attached to one of those listeners and not re-processed in the same cycle would
// keep a count of zero and stale Accepted conditions, so the routes that serve the
// Gateway have to come back through the route builders, which run later in this cycle.
//
// "Serves" is deliberately wider than "is attached to": a route must also be reached when
// the Gateway change is what makes it attachable at all -- a listener added, or
// allowedListeners relaxed to admit one of its ListenerSets. That is why the relation is
// resolved from the routes' parentRefs and the ListenerSet mirror, rather than read back
// from the listeners the rebuild is about to discard.
//
// It is also the one place that sees both parent kinds at once: the enqueue edges are
// per-kind, and neither kind's event can reach a route that named the other.
func (b *GatewayBuilderImpl) markRoutesOfRebuiltGateways() {
	rebuilt := make(map[client.ObjectKey]struct{})
	for gwKey, treeGw := range b.ControllerStore.GateTree.Gateways {
		if treeGw == nil || (treeGw.TreeStatus.Status != store.StatusUpserted && treeGw.TreeStatus.Status != store.StatusDeleted) {
			continue
		}
		rebuilt[gwKey] = struct{}{}
	}
	if len(rebuilt) == 0 {
		return
	}

	for key, route := range b.ControllerStore.GateTree.HTTPRoutes {
		if route == nil || route.K8sResource == nil {
			continue
		}
		if b.routeServesAnyRebuiltGateway(route.K8sResource.Spec.ParentRefs, route.K8sResource.Namespace, rebuilt) {
			injectIndirectUpdate(b.ClusterStore.Updates.HTTPRoutes, key, route.K8sResource)
		}
	}
	for key, route := range b.ControllerStore.UnmanagedGateTree.HTTPRoutes {
		if route == nil || route.K8sResource == nil {
			continue
		}
		if b.routeServesAnyRebuiltGateway(route.K8sResource.Spec.ParentRefs, route.K8sResource.Namespace, rebuilt) {
			injectIndirectUpdate(b.ClusterStore.Updates.HTTPRoutes, key, route.K8sResource)
		}
	}
	for key, route := range b.ControllerStore.GateTree.TLSRoutes {
		if route == nil || route.K8sResource == nil {
			continue
		}
		if b.routeServesAnyRebuiltGateway(route.K8sResource.Spec.ParentRefs, route.K8sResource.Namespace, rebuilt) {
			injectIndirectUpdate(b.ClusterStore.Updates.TLSRoutes, key, route.K8sResource)
		}
	}
	for key, route := range b.ControllerStore.UnmanagedGateTree.TLSRoutes {
		if route == nil || route.K8sResource == nil {
			continue
		}
		if b.routeServesAnyRebuiltGateway(route.K8sResource.Spec.ParentRefs, route.K8sResource.Namespace, rebuilt) {
			injectIndirectUpdate(b.ClusterStore.Updates.TLSRoutes, key, route.K8sResource)
		}
	}
}

// routeServesAnyRebuiltGateway reports whether one of a route's parentRefs designates a
// rebuilt or deleted Gateway -- directly, or through a ListenerSet it names. A
// ListenerSet not (or not yet) allowed to attach still resolves to its parent Gateway: whether it may
// attach is the ListenerSet's own verdict, not a reason to leave a route that named it
// behind.
func (b *GatewayBuilderImpl) routeServesAnyRebuiltGateway(
	parentRefs []gatewayv1.ParentReference,
	routeNamespace string,
	rebuilt map[client.ObjectKey]struct{},
) bool {
	for _, parentRef := range parentRefs {
		switch utilsk8s.ResolveParentRefKind(parentRef, b.ControllerStore.ExtractGVK) {
		case utilsk8s.ParentRefKindGateway:
			key := GetParentRefNamespacedNameFromResource(parentRef, getNamespaceOrDefault(parentRef.Namespace, routeNamespace))
			if _, ok := rebuilt[key]; ok {
				return true
			}
		case utilsk8s.ParentRefKindListenerSet:
			lsKey := GetParentRefNamespacedNameFromResource(parentRef, getNamespaceOrDefault(parentRef.Namespace, routeNamespace))
			treeLs := b.ControllerStore.GateTree.ListenerSets[lsKey]
			if treeLs == nil {
				continue
			}
			// A deleted ListenerSet has no current resource, and a reparented one
			// may now name an absent Gateway. Both still had routes on the old
			// parent, so resolve both versions while the snapshot is available.
			resources := []*gatewayv1.ListenerSet{treeLs.K8sResource}
			if previous := treeLs.TreeStatus.OldTreeResource; previous != nil {
				resources = append(resources, previous.K8sResource)
			}
			for _, resource := range resources {
				if gwKey, namesGateway := parentGatewayKey(resource); namesGateway {
					if _, ok := rebuilt[gwKey]; ok {
						return true
					}
				}
			}
		}
	}
	return false
}

// injectIndirectUpdate marks a linked object for processing without it having changed,
// the same way a GatewayClass picks up an installed-versions change. An update already
// queued for that object -- a real change, or a deletion -- is left alone.
func injectIndirectUpdate[T client.Object](updates map[client.ObjectKey]store.Update[T], key client.ObjectKey, object T) {
	if _, alreadyPresent := updates[key]; alreadyPresent {
		return
	}
	updates[key] = store.Update[T]{
		NewObject: object,
		OldObject: object, // old = new when indirect update
		Status:    store.StatusUpserted,
		Indirect:  true,
	}
}

// constructListenersOfUpsertedGateways builds the merged listener list of every
// Gateway upserted this cycle, ahead of conflict detection.
//
// Only the upserted ones: an untouched Gateway keeps the list it was built with,
// its spec not having moved, and detection needs the lists of the whole tree. The
// two sets meet because a Gateway enters the tree upserted, so every Gateway in it
// has been through this pass at least once.
//
// Not gated on isManaged, unlike the checks: detection spans every non-deleted
// Gateway of the tree, so withholding a list here would silently remove its
// listeners from the comparison -- and the first listener on a port is what fixes
// that port's category for all the others.
func (b *GatewayBuilderImpl) constructListenersOfUpsertedGateways() {
	for _, treeGw := range b.ControllerStore.GateTree.Gateways {
		if treeGw == nil || treeGw.K8sResource == nil {
			continue
		}
		if treeGw.TreeStatus.Status != store.StatusUpserted {
			continue
		}
		b.constructMergedListeners(treeGw)
	}
}

// constructMergedListeners rebuilds one Gateway's merged listener list, own
// listeners then contributed entries.
//
// The contributions are looked up rather than passed in because the two callers
// reach this from different places -- the pass above, and the re-upsert inside
// conflict detection -- and neither should know where the table lives.
func (b *GatewayBuilderImpl) constructMergedListeners(treeGw *Gateway) {
	gwKey := client.ObjectKeyFromObject(treeGw.K8sResource)
	treeGw.constructListeners(b.ControllerStore.contributedListenersByGatewayKey[gwKey])
}

// checkListeners runs every listener check of a Gateway, the conflict verdicts
// having been settled beforehand.
func (b *GatewayBuilderImpl) checkListeners(treeGw *Gateway) {
	for _, listener := range treeGw.Listeners {
		listener.resetChecks()

		// The route kinds a listener can serve are a property of the listener alone,
		// so they are computed for every one of them and whatever the parent's state:
		// only the condition derived from the rejected half depends on the Gateway
		// being valid. status.supportedKinds must be reported even for a listener
		// whose Gateway is not accepted, and even when nothing can be served on it --
		// an empty list says that, an absent one says nothing.
		kept, rejectedRouteKinds := partitionRouteKinds(
			listener.Protocol(), listener.AllowedRoutes(), gateManagedRouteGroupKindsByProtocol,
		)
		listener.AllowedRouteKinds = kept

		// Every check below states something about the listener's own
		// configuration, which is only worth stating once the resource holding it
		// is itself valid: on an invalid Gateway the cause is reported on the
		// Gateway, and a listener verdict would only add noise -- or contradict it,
		// by declaring a listener acceptable under a parent that will serve nothing.
		//
		// This is the single decision point for that: the checks used to carry the
		// guard individually, which left checkProtocol and checkPort without one, so
		// an invalid Gateway still published a protocol or port verdict on its
		// listeners while the other three stayed silent.
		if !treeGw.Valid {
			continue
		}

		switch listener.Protocol() {
		// This switch will be completed with all needed checks per protocol
		case gatewayv1.HTTPProtocolType, gatewayv1.TLSProtocolType:
			listener.checkRouteGroupKind(rejectedRouteKinds, gateManagedRouteGroupKindsByProtocol)
			listener.checkProtocol(gateManagedRouteGroupKindsByProtocol)
			listener.checkConflict(b.ControllerStore.conflictsByPort)
		case gatewayv1.HTTPSProtocolType:
			listener.checkRouteGroupKind(rejectedRouteKinds, gateManagedRouteGroupKindsByProtocol)
			listener.checkCertificateRefs(b.GateTree.Secrets, b.referenceGrantManager)
			listener.checkProtocol(gateManagedRouteGroupKindsByProtocol)
			listener.checkConflict(b.ControllerStore.conflictsByPort)
		default:
			listener.checkProtocol(gateManagedRouteGroupKindsByProtocol)
			listener.checkConflict(b.ControllerStore.conflictsByPort)
		}
		listener.checkPort(b.ControllerStore.PortBinder)
	}
}

func (b *GatewayBuilderImpl) resetListenerConflicts() {
	b.ControllerStore.previousConflictsByPort = b.ControllerStore.conflictsByPort
	b.ControllerStore.conflictsByPort = make(map[gatewayv1.PortNumber]conflictsByListener)
	// Not kept from one cycle to the next: a port's category is decided by whichever
	// listener claims it first this cycle, and carrying a stale one over would let a
	// deleted Gateway keep dictating the category of a port it no longer serves.
	b.ControllerStore.protocolCategoryByPort = make(map[gatewayv1.PortNumber]protocols.ProtocolCategory)
}

// checkListenerConflicts recomputes the listener conflict table, then re-upserts
// the Gateways whose conflict verdict changed since the previous cycle.
//
// A conflict is a relational property: it depends on the listeners of the other
// Gateways, so a Gateway can enter or leave the conflict set without any event of
// its own — adding a listener to an older Gateway silently invalidates a younger
// Gateway's listener on the same port. The conflict table is rebuilt for the whole
// tree on every cycle, but the transfer of a verdict onto a listener
// (listener.checkConflict -> CheckConflict -> BuildConditions) only runs for
// Gateways marked StatusUpserted. Re-upserting is what routes an untouched Gateway
// through that single transfer point.
//
// Only Gateways whose verdict actually changed are re-upserted. Selecting every
// Gateway that merely *has* a conflict would re-run all five listener checks — down
// to re-reading the Secrets and PEM-decoding the certificates in
// checkCertificateRefs — on every single cycle for as long as a misconfiguration
// lasts, whatever the event that triggered the cycle.
func (b *GatewayBuilderImpl) checkListenerConflicts() {
	// Detect conflicts. The table is rebuilt from scratch and holds an entry for
	// every listener of the tree, conflicting or not.
	b.computeListenerConflicts()

	for gwKey := range b.gatewaysWithChangedConflictVerdict() {
		treeGw, ok := b.ControllerStore.GateTree.Gateways[gwKey]
		if !ok {
			continue
		}
		// Set the treeGw as UPSERTED
		if treeGw.TreeStatus.Status != store.StatusUpserted && treeGw.TreeStatus.Status != store.StatusDeleted {
			treeGw.SetAsUpserted(b.Logger, treeGw.K8sResource)
			b.processManagementChecks(treeGw)
			// SetAsUpserted resets the Gateway, which drops the listener list built
			// ahead of detection -- and the checks the re-upsert exists to trigger
			// iterate that list, so it has to come back. Rebuilding is sound rather
			// than merely convenient: neither the spec nor the contributions have
			// moved, so the listeners come back with the same keys, and the verdicts
			// just recorded still resolve.
			b.constructMergedListeners(treeGw)
		}
	}
}

// gatewaysWithChangedConflictVerdict returns the Gateway keys whose conflict
// verdict changed between the previous and the current cycle.
func (b *GatewayBuilderImpl) gatewaysWithChangedConflictVerdict() map[client.ObjectKey]struct{} {
	return gatewaysWithChangedConflictVerdict(
		b.ControllerStore.previousConflictsByPort,
		b.ControllerStore.conflictsByPort,
	)
}

// gatewaysWithChangedConflictVerdict returns the set of Gateway keys owning at
// least one listener whose conflict verdict differs between the previous and the
// current conflict table.
//
// The comparison is by value, not by membership of the conflicting set, so it also
// catches a listener that stays conflicted while its reason changes (e.g.
// ProtocolConflict becoming HostnameConflict). Both tables hold an entry for every
// listener, so an absent verdict is a real difference and not a gap: a listener
// that appeared or disappeared belongs to a Gateway that was created, deleted or
// edited, hence already present in ClusterStore.Updates — selecting it is a no-op
// filtered out by the StatusUpserted guard in checkListenerConflicts.
func gatewaysWithChangedConflictVerdict(
	previous, current map[gatewayv1.PortNumber]conflictsByListener,
) map[client.ObjectKey]struct{} {
	gwKeys := map[client.ObjectKey]struct{}{}

	for port, currentConflicts := range current {
		previousConflicts := previous[port]
		for key, currentKinds := range currentConflicts {
			previousKinds, existed := previousConflicts[key]
			if existed && previousKinds == currentKinds {
				continue
			}
			gwKeys[key.GatewayKey] = struct{}{}
		}
	}

	// Listeners that were in the previous table and are gone from the current one.
	for port, previousConflicts := range previous {
		currentConflicts := current[port]
		for key := range previousConflicts {
			if _, stillPresent := currentConflicts[key]; stillPresent {
				continue
			}
			gwKeys[key.GatewayKey] = struct{}{}
		}
	}

	return gwKeys
}

type nbListeners struct {
	withConflict    int32
	withoutConflict int32
}

// nbListenersWithAndWithoutConflict returns the number of listeners that have a conflict
// and the number of listeners that do not have a conflict for a given Gateway.
func nbListenersWithAndWithoutConflict(conflictsByPort map[gatewayv1.PortNumber]conflictsByListener, treeGw *Gateway) nbListeners {
	nbListeners := nbListeners{}
	gwKey := client.ObjectKeyFromObject(treeGw.K8sResource)
	for _, conflictsOnPort := range conflictsByPort {
		for key, conflicts := range conflictsOnPort {
			// Not our Gateway, continue
			if key.GatewayKey != gwKey {
				continue
			}

			if conflicts.any() {
				nbListeners.withConflict++
			} else {
				nbListeners.withoutConflict++
			}
		}
	}
	return nbListeners
}
