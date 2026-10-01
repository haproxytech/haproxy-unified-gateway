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
	"slices"
	"strings"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/protocols"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// computeListenerConflicts detects the conflicts between listeners sharing a port
// and records them in b.conflictsByPort, along with the category each port serves
// in b.protocolCategoryByPort.
//
// Two kinds of conflict are detected, and a listener can be in both:
//
//   - protocol, when a listener asks for a protocol category the port does not
//     serve. The first listener on a port decides that category;
//   - hostname, when a listener claims a hostname another listener of the same
//     Gateway already claims on that port and category.
//
// Listeners of *different* Gateways may claim the same hostname, and are merged
// into one VirtualListener. That follows from the API's model: each Gateway requests
// its own Addresses and its listeners are bound to those, so the same port on two
// Gateways is two sockets and no conflict. The API accordingly defines conflicts
// within one Gateway's listener list: the Conflicted condition lives in that
// Gateway's status, and the CEL rule forbidding a duplicate (port, protocol,
// hostname) is scoped to one resource.
//
// Several Gateways on one port is a case the conformance suite exercises heavily --
// its base manifest alone keeps three on port 80 for the whole run -- and it passes
// because the suite gives each Gateway its own instance and Service, which is the
// API's premise honoured. Do not read that as evidence the shared mode is safe: it
// is evidence of the opposite, the premise having to be restored for the suite to
// hold.
//
// This implementation makes that premise false, and says so itself:
// prepareGatewayUpdates computes the controller's addresses once and assigns the
// same list to every valid Gateway. Two Gateways on one port therefore advertise the
// same address:port and a client reaching it reaches one socket. So they do collide,
// on the hostname as on everything else, while both are reported valid -- and the
// API gives no vocabulary for it, HostnameConflict being about a listener list this
// one does not span.
//
// --gateway-ns-name narrows it without closing it: one instance per Gateway, its
// watch predicate keeping the others out of the store. Address discovery still goes
// through a configured Service label, so two instances left on the default label
// each advertise both Services; and the synthetic Ingress Gateway is injected
// straight into ClusterStore.Updates, never passing that predicate.
//
// Recorded as a gap rather than papered over here: what to report, and whether the
// shared mode is a supported one, is a decision this restructuring must not take.
//
// The comparison scope is therefore the Gateway, and it stays the Gateway when
// ListenerSets contribute entries -- the resource that contributed a listener drives
// its status and its references, never the scope it is compared in. That is also
// where HostnameConflict earns its keep: within a lone Gateway the CEL rule already
// rejects the duplicate triple, so the reason is nearly unreachable, and a merged
// listener list is what it was meant for.
//
// The three passes below are separate on purpose: grouping decides nothing, and
// detection decides without removing anyone from the next pass. What that buys is
// stated on detectProtocolConflicts, where it is load-bearing.
func (b *GatewayBuilderImpl) computeListenerConflicts() {
	// Conflicts are computed between non-deleted Gateways only, sorted by creation
	// timestamp.
	sortedGws := utils.MapToSortedListByCreationTimestamp(b.nonDeletedGateways())

	// The synthetic ingress gateway must win port conflicts against real Gateways so
	// Ingress traffic is never displaced. The first listener seen on a port sets that
	// port's protocol category, so process ingress gateways first. The sort is
	// stable, so the oldest-wins order is kept within each group. This makes the win
	// explicit via GatewayForIngress rather than relying on the synthetic gateway's
	// zero creation timestamp.
	slices.SortStableFunc(sortedGws, func(a, b *Gateway) int {
		switch {
		case a.GatewayForIngress == b.GatewayForIngress:
			return 0
		case a.GatewayForIngress:
			return -1
		default:
			return 1
		}
	})

	listenersByPort := groupListenersByPort(sortedGws)
	b.detectProtocolConflicts(listenersByPort)
	b.detectHostnameConflicts(listenersByPort)
}

// nonDeletedGateways returns a filtered copy of b.GateTree.Gateways containing
// only Gateways that have not been marked for deletion.
func (b *GatewayBuilderImpl) nonDeletedGateways() map[types.NamespacedName]*Gateway {
	result := make(map[types.NamespacedName]*Gateway, len(b.GateTree.Gateways))
	for k, gw := range b.GateTree.Gateways {
		if gw == nil || gw.K8sResource == nil {
			continue
		}
		if gw.TreeStatus.Status == store.StatusDeleted {
			continue
		}
		result[k] = gw
	}
	return result
}

// groupListenersByPort gathers the merged listener list of every Gateway under the
// port each listener asks for, keeping within a port the precedence order of the
// Gateways it was given -- which is what makes "the first one wins" a specified
// outcome rather than a map-iteration accident.
//
// It decides nothing and writes nowhere. That is the point: the pass that used to
// group also fixed each port's category and marked the divergent listeners, so its
// name described half of what it did and the other half was a side effect its
// callers had to know about.
//
// It reads the Gateway's merged list, not its spec, so a listener contributed by a
// ListenerSet is grouped and compared exactly like a declared one -- the reason
// construction was pulled ahead of this pass. Reading the spec would have meant a
// second representation of a listener, kept in step by hand, and the entries would
// have had no way into the comparison at all.
func groupListenersByPort(sortedGws []*Gateway) map[gatewayv1.PortNumber][]*Listener {
	listenersByPort := make(map[gatewayv1.PortNumber][]*Listener)

	for _, treeGw := range sortedGws {
		if treeGw == nil || treeGw.K8sResource == nil {
			continue
		}
		if treeGw.TreeStatus.Status == store.StatusDeleted {
			continue
		}

		for _, listener := range treeGw.Listeners {
			listenersByPort[listener.Port()] = append(listenersByPort[listener.Port()], listener)
		}
	}

	return listenersByPort
}

// detectProtocolConflicts records the category each port serves -- decided by the
// first listener claiming that port -- and marks every listener asking for another
// one.
//
// It is also where the conflict table gets *built*: it writes an entry for every
// listener, the empty set included. That totality is what makes the verdict diff
// sound, an absent entry being a real difference and not a gap, and it now holds
// because one pass establishes it. It used to be the union of two disjoint writes in
// two functions, one of which maintained it by *omitting* to iterate the listeners
// the other had written -- an invariant spread over two places, verified nowhere,
// and breakable in silence by a change to either.
//
// No listener is withheld from the following pass either. Excluding the ones that
// conflict on protocol is what made the composite case unreachable: a listener that
// never meets the hostname pass cannot accumulate two kinds, so ListenerConflict --
// the Gateway API's reason for exactly that case -- could never be reported.
func (b *GatewayBuilderImpl) detectProtocolConflicts(listenersByPort map[gatewayv1.PortNumber][]*Listener) {
	for port, listeners := range listenersByPort {
		conflictsOnPort := make(conflictsByListener, len(listeners))
		b.ControllerStore.conflictsByPort[port] = conflictsOnPort

		for _, listener := range listeners {
			portCategory, decided := b.ControllerStore.protocolCategoryByPort[port]
			if !decided {
				portCategory = listener.protocolCategory()
				b.ControllerStore.protocolCategoryByPort[port] = portCategory
			}

			var conflicts conflictKinds
			if listener.protocolCategory() != portCategory {
				conflicts |= conflictOnProtocol
			}
			conflictsOnPort[listener.Key()] = conflicts
		}
	}
}

// hostnameClaim is what a listener claims by asking for a hostname, and what a
// later listener has to find free.
//
// The category is part of the claim, which is what keeps the two kinds of conflict
// from contaminating each other: a listener already conflicting on protocol has, by
// definition, a category the port does not serve, so it can never take the claim of
// a listener that does not conflict. Without it, one mismatched protocol arriving on
// a port would make an innocent listener look like a hostname squatter.
//
// The Gateway is part of it because the comparison scope is the Gateway. That scope
// is inherited, not chosen, and it is the questionable half of this design: the API
// forbids a duplicate (port, protocol, hostname) *within a listener list* -- by CEL,
// at admission -- and permits it across Gateways only because it assumes each
// Gateway has its own addresses, hence no shared list. Serving every Gateway from one
// frontend makes a listener list out of several Gateways, so the two cases describe
// one physical situation and are treated differently for no remaining reason.
//
// Removing this field is what would apply the API's own rule to the list this
// controller actually builds. The objection is not that it would invent a conflict
// but that it lets one Gateway invalidate another's claim, which is a decision about
// what this controller promises its tenants -- deliberately not taken here.
//
// It says nothing about precedence, which is the other half of the question: the
// field decides *whom* a listener is compared with, never *who wins*. Precedence is
// the order of the merged list, and it is settled once, upstream. The contribution
// pass orders the ListenerSets (oldest first, ties on "{namespace}/{name}") and
// Gateway.constructListeners puts the Gateway's own listeners before them
// unconditionally -- the API gives the parent's own listeners the highest priority
// whatever the timestamps say. groupListenersByPort preserves that order, and this
// pass reads it as "the first claimant keeps the claim". Nothing here re-sorts by
// origin, and nothing should: a second sort would be a second statement of the same
// contract, free to drift from the first.
type hostnameClaim struct {
	gatewayKey client.ObjectKey
	hostname   string
	category   protocols.ProtocolCategory
	port       gatewayv1.PortNumber
}

// detectHostnameConflicts marks every listener whose hostname claim is already
// taken, the first claimant in the merged list's precedence order keeping it.
//
// That order is the only thing deciding who wins: see hostnameClaim for why the
// claim key carries the scope and the order carries the precedence.
//
// It only ever adds a kind. The entry it writes into was built by the protocol
// pass, so no protocol verdict can be overwritten here -- which is the other half
// of why the exclusion could go: dropping it without making the earlier pass the
// builder would have had this one reset the verdicts it just recorded.
func (b *GatewayBuilderImpl) detectHostnameConflicts(listenersByPort map[gatewayv1.PortNumber][]*Listener) {
	claimed := make(map[hostnameClaim]struct{})

	for port, listeners := range listenersByPort {
		for _, listener := range listeners {
			claim := hostnameClaim{
				port: port,
				// The Gateway the listener is served on, not the resource declaring
				// it: the comparison scope is the merged list, so an entry
				// contributed by a ListenerSet competes with its parent Gateway's
				// own listeners and with its siblings' entries.
				gatewayKey: listener.Key().GatewayKey,
				category:   listener.protocolCategory(),
				hostname:   normalizedHostname(listener.Hostname()),
			}
			if _, taken := claimed[claim]; taken {
				b.ControllerStore.conflictsByPort[port][listener.Key()] |= conflictOnHostname
				continue
			}
			claimed[claim] = struct{}{}
		}
	}
}

// normalizedHostname is the form two hostnames have to share in order to conflict.
//
// Per the Gateway API, two listeners conflict only when their hostname is the same
// value. Different hostnames -- even when one is a wildcard that would match the
// other -- do NOT conflict, because the specificity rules (exact > wildcard >
// empty) always name an unambiguous winner for any request. So this is an equality,
// not a match:
//
//   - "" vs ""                     → conflict  (two catch-all listeners)
//   - "" vs "foo.com"              → no conflict  (catch-all coexists with specific)
//   - "*.ex.com" vs "foo.ex.com"   → no conflict  (specific beats wildcard)
//   - "*.ex.com" vs "*.ex.com"     → conflict  (identical wildcards)
//
// An unset hostname is a catch-all, and a value like any other here: two catch-all
// listeners on one port are indistinguishable.
//
// The trailing dot is stripped, an absolute name denoting the same host as its
// relative form. Lowercasing makes the comparison case-insensitive; the API's own
// pattern for Hostname admits no uppercase, so it is a no-op on every value the API
// server accepts and a safeguard for anything reaching the tree another way.
func normalizedHostname(hostname *gatewayv1.Hostname) string {
	if hostname == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(string(*hostname), "."))
}
