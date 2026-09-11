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
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	v3 "github.com/haproxytech/haproxy-unified-gateway/api/gate/v3"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"

	"github.com/imdario/mergo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// Gateway represents the Gateway resource.
type Gateway struct {
	// K8sResource is the source resource.
	K8sResource *gatewayv1.Gateway
	// Conditions include Conditions for the Gateway.
	// HugGate is a merge between the ParamsRef from GatewayClass and the one from Gateway
	// If it is invalid at GatewayClass level, it is ignored and overridden by the one at Gateway level.
	HugGate *v3.HugGate
	// Final Conditions
	Conditions generic.Conditions
	// TreeStatus
	TreeStatus TreeUpdate[Gateway]
	// Management Checks
	CheckParamsRef         CheckResult
	CheckValidGatewayClass CheckResult
	// Additional checks
	CheckConflict CheckResult
	// Listeners is the merged listener list of this Gateway, in precedence order:
	// the listeners the Gateway declares itself, in spec order, then the entries
	// contributed by attached ListenerSets.
	//
	// Not a map keyed by the bare name: a listener name is unique only within the
	// resource declaring it. The Gateway API explicitly allows the same name on a
	// Gateway and on its ListenerSets, and on sibling ListenerSets, so such a map
	// would collapse the homonyms -- the last of the consequences the structured
	// listenerKey was introduced to remove.
	//
	// Not a map keyed by listenerKey either, which would fix the collapse but not
	// the order, and the order is load-bearing: conflict detection gives a port to
	// the first listener claiming it, so a map would make that verdict depend on
	// Go's iteration order, hence on a hash seed.
	//
	// And not a map beside a slice, which would have both properties at the price of
	// an invariant -- "the two hold the same elements" -- maintained procedurally and
	// verified nowhere. That is the shape of defect this package has been removing:
	// one field read as two things, a flag doubling a reason, a table whose totality
	// two writers maintained between them.
	//
	// So a slice, and lookups that scan it. The list is bounded and small: the API
	// caps a Gateway's own listeners at 64, and TooManyListeners exists because the
	// merged total has to be capped as well. A scan runs once per route or per
	// verdict, never per request. If it ever measured, the escape is an index
	// derived from this list once per cycle -- one writer, nothing to keep in sync --
	// rather than a second source of truth.
	Listeners []*Listener
	// Valid shows whether the Gateway is valid.
	Valid bool
	// Ingress Frontend equivalent
	GatewayForIngress bool
}

func NewGateway(k8sObject *gatewayv1.Gateway) *Gateway {
	return &Gateway{
		K8sResource: k8sObject,
		Conditions:  conditions.NewGatewayAcceptedOK(),
		TreeStatus: TreeUpdate[Gateway]{
			Status:          store.StatusUpserted,
			OldTreeResource: nil,
		},
	}
}

func (g *Gateway) GetCreationTimestamp() metav1.Time {
	if g != nil && g.K8sResource != nil {
		return g.K8sResource.GetCreationTimestamp()
	}
	return metav1.Time{}
}

// GetName returns the name of the Gateway Kubernetes resource.
// If the K8sResource is nil (for example a DELETED Gateway), it returns an empty string.
func (g *Gateway) GetName() string {
	if g.K8sResource != nil {
		return g.K8sResource.GetName()
	}
	return ""
}

// GetNamespace returns the namespace of the Gateway Kubernetes resource.
// If the K8sResource is nil (for example a DELETED Gateway), it returns an empty string.
// Note that an empty namespace is legitimate here: the synthetic Ingress Gateway has one.
func (g *Gateway) GetNamespace() string {
	if g.K8sResource != nil {
		return g.K8sResource.GetNamespace()
	}
	return ""
}

func (g *Gateway) GetK8sResource() *gatewayv1.Gateway {
	if g.K8sResource != nil {
		return g.K8sResource
	}

	if g.TreeStatus.OldTreeResource != nil && g.TreeStatus.OldTreeResource.K8sResource != nil {
		return g.TreeStatus.OldTreeResource.K8sResource
	}
	return nil
}

func (g *Gateway) SetAsUpserted(logger *slog.Logger, newK8sResource *gatewayv1.Gateway) {
	setResourceStatus(logger, g, newK8sResource, store.StatusUpserted)
	g.K8sResource = newK8sResource
	g.reset()
}

func (g *Gateway) SetAsDeleted(logger *slog.Logger, oldK8sResource *gatewayv1.Gateway) {
	setResourceStatus(logger, g, oldK8sResource, store.StatusDeleted)
	g.K8sResource = nil
	g.reset()
}

func (g *Gateway) reset() {
	g.Conditions = make(generic.Conditions)
	g.HugGate = nil
	g.CheckParamsRef = CheckResult{}
	g.CheckValidGatewayClass = CheckResult{}
	g.CheckConflict = CheckResult{}
	g.Valid = false
	g.Listeners = nil
}

func (g *Gateway) SetAsManaged(logger *slog.Logger, cs ControllerStore) {
	logger.LogAttrs(context.Background(), slog.LevelDebug, fmt.Sprintf("%T MANAGED", *g),
		logging.LogAttrObjectKey(g.K8sResource))
	// Is it already in Managed
	key := client.ObjectKeyFromObject(g.K8sResource)
	cs.GateTree.Gateways[key] = g
	delete(cs.UnmanagedGateTree.Gateways, key)
}

func (g *Gateway) SetAsUnmanaged(logger *slog.Logger, cs ControllerStore) {
	logger.LogAttrs(context.Background(), slog.LevelDebug, fmt.Sprintf("%T UNMANAGED", *g),
		logging.LogAttrObjectKey(g.K8sResource))
	// Is it already in Managed
	key := client.ObjectKeyFromObject(g.K8sResource)
	cs.UnmanagedGateTree.Gateways[key] = g
	delete(cs.GateTree.Gateways, key)
}

func (g *Gateway) isManaged() bool {
	return g.CheckValidGatewayClass.Valid
}

func (g *Gateway) DeepCopy() *Gateway {
	if g == nil {
		return nil
	}
	// Save TreeStatus
	treeStatus := g.TreeStatus
	g.TreeStatus = TreeUpdate[Gateway]{}

	var copied Gateway
	data, err := json.Marshal(g) // Serialize to JSON
	if err != nil {
		return nil
	}
	_ = json.Unmarshal(data, &copied) // Deserialize to a new struct	return &copied
	// Restore TreeStatus
	g.TreeStatus = treeStatus
	return &copied
}

// GetTreeStatus returns the TreeStatus of the Gateway.
func (g *Gateway) GetTreeStatus() *TreeUpdate[Gateway] {
	return &g.TreeStatus
}

// SetTreeStatus sets the TreeStatus of the Gateway.
func (g *Gateway) SetTreeStatus(treeStatus TreeUpdate[Gateway]) {
	g.TreeStatus = treeStatus
}

func (g *Gateway) checkParametersRef(controllerStore ControllerStore) {
	if g.K8sResource.Spec.Infrastructure == nil || g.K8sResource.Spec.Infrastructure.ParametersRef == nil {
		g.CheckParamsRef = CheckResult{
			Valid:      true,
			Conditions: conditions.NewGatewayAcceptedOK(),
		}
		return
	}

	gwParamRef := g.K8sResource.Spec.Infrastructure.ParametersRef
	paramRef := &gatewayv1.ParametersReference{
		Group:     gwParamRef.Group,
		Kind:      gwParamRef.Kind,
		Name:      gwParamRef.Name,
		Namespace: (*gatewayv1.Namespace)(&g.K8sResource.Namespace),
	}
	checker := HugGateParamsRefChecker{
		ParamRef:      paramRef,
		StoreHugGates: controllerStore.ClusterStore.HugGates,
	}
	var gwHugGate *v3.HugGate
	g.CheckParamsRef, gwHugGate = checker.CheckGatewayClass()
	if g.CheckParamsRef.Valid {
		// Check if the GatewayClass has a valid ParamsRef and if so, merge them
		gwcKey := types.NamespacedName{Name: string(g.K8sResource.Spec.GatewayClassName)}
		treeGwc, ok := controllerStore.GateTree.GatewayClasses[gwcKey]

		if ok && treeGwc.Valid {
			gwcHugGate := treeGwc.HugGate
			if gwcHugGate != nil {
				mergedHugGate := &v3.HugGate{}
				// First Gwc HugGate
				err := mergo.MergeWithOverwrite(mergedHugGate, gwcHugGate)
				if err != nil {
					controllerStore.Logger.LogAttrs(context.Background(), slog.LevelError, "error merging haproxy gate",
						logging.LogAttrError(err))
				}

				// Then Gw HugGate with Override
				if gwHugGate != nil {
					err := mergo.MergeWithOverwrite(mergedHugGate, gwHugGate)
					if err != nil {
						controllerStore.Logger.LogAttrs(context.Background(), slog.LevelError, "error merging haproxy gate",
							logging.LogAttrError(err))
					}
				}

				g.HugGate = mergedHugGate
			}
		} else {
			// No merge needed
			g.HugGate = gwHugGate
		}
	}
}

func (g *Gateway) checkGatewayClassIsValid(controllerStore ControllerStore) {
	gwcKey := types.NamespacedName{Name: string(g.K8sResource.Spec.GatewayClassName)}
	treeGwc, ok := controllerStore.GateTree.GatewayClasses[gwcKey]

	if ok && treeGwc.Valid {
		g.CheckValidGatewayClass = CheckResult{
			Valid:      true,
			Conditions: conditions.NewGatewayAcceptedOK(),
		}
		return
	}
	if !ok && utils.IsSyntheticName(string(g.K8sResource.Spec.GatewayClassName)) {
		// The synthetic ingress gateway references a synthetic GatewayClass that
		// does not exist in the API; treat it as valid (it is controller-owned).
		g.CheckValidGatewayClass = CheckResult{
			Valid:      true,
			Conditions: conditions.NewGatewayAcceptedOK(),
		}
		return
	}
	g.CheckValidGatewayClass = CheckResult{
		Valid:      false,
		Conditions: conditions.NewGatewayAcceptedInvalidConditions(string(g.K8sResource.Spec.GatewayClassName)),
	}
}

// unsupportedProtocolListeners counts listeners that failed their protocol
// check. checkProtocol runs for every listener before BuildConditions, so a
// false verdict always means an unsupported protocol.
func (g *Gateway) unsupportedProtocolListeners() (invalid, total int) {
	for _, l := range g.Listeners {
		if l == nil {
			continue
		}
		total++
		if !l.CheckProtocol.Valid {
			invalid++
		}
	}
	return invalid, total
}

// checkListenerConflicts on the Gateway checks if there is at least one Listener that has no conflict
// - if there is at least one listener for this Gateway that has no conflict, then the Gateway status is "Accepted"
// - if none listener are valid, then the Gateway condition type Accepted is False
func (g *Gateway) checkListenerConflicts(conflictsByPort map[gatewayv1.PortNumber]conflictsByListener) {
	// See if at least one of them does not have conflict
	// Or if they all have confict

	nbListenersWithAndWithoutConflict := nbListenersWithAndWithoutConflict(conflictsByPort, g)

	if nbListenersWithAndWithoutConflict.withConflict > 0 {
		// Some listeners conflict for this Gateway
		if nbListenersWithAndWithoutConflict.withoutConflict == 0 {
			// 1- All listeners conflict for this Gateway
			g.CheckConflict = CheckResult{
				Valid:      false,
				Conditions: conditions.NewGatewayAcceptedListenerNotValidAllInvalid(),
			}
		} else {
			// 2- Some listeners conflict for this Gateway
			// but at least 1 is Valid
			g.CheckConflict = CheckResult{
				Valid:      true,
				Conditions: conditions.NewGatewayAcceptedListenerNotValidAtLeast1Valid("Some listeners are Conflicting"),
			}
		}
	} else {
		// 3- No listener conflict for this Gateway
		// No conditions here: BuildConditions must stay free to derive the
		// Accepted reason from unsupported-protocol listeners.
		g.CheckConflict = CheckResult{
			Valid: true,
		}
	}
	g.Valid = g.Valid && g.CheckConflict.Valid
}

// constructListeners fills the merged listener list: the listeners this Gateway
// declares itself, in spec order, then the entries contributed by the ListenerSets
// allowed to attach to it, in the order they were handed over.
//
// These two lines are the precedence contract the API states, and they are the only
// place it is expressed. The contributed entries arrive already ordered among
// themselves -- ListenerSets oldest first, ties on namespace/name -- because that
// order is a property of the set of contributors, which only the contribution pass
// sees; what belongs here is that the Gateway's own listeners come first.
func (g *Gateway) constructListeners(contributedListeners []*Listener) {
	g.constructOwnListeners()
	g.Listeners = append(g.Listeners, contributedListeners...)
}

// constructOwnListeners fills the merged listener list with the listeners this
// Gateway declares itself, in spec order, and nothing else.
//
// Construction is deliberately separate from the checks those listeners then go
// through, because the two cannot run at the same moment: a listener's conflict
// verdict depends on the other listeners sharing its port, so every Gateway's list
// has to exist before any single listener can be judged. Detection consequently
// compares built listeners rather than re-reading the specs -- one representation of
// a listener instead of two, which is what lets a contributed entry be compared on
// the same footing as a declared one.
//
// A method on the Gateway rather than on the builder: turning a spec into listeners
// needs nothing but the Gateway, and the re-upsert path has to redo it.
func (g *Gateway) constructOwnListeners() {
	ownListeners := make([]*Listener, 0, len(g.K8sResource.Spec.Listeners))
	gwKey := client.ObjectKeyFromObject(g.K8sResource)

	for _, specListener := range g.K8sResource.Spec.Listeners {
		ownListeners = append(ownListeners, &Listener{
			ListenerKey:        gatewayListenerKey(gwKey, specListener.Name),
			K8sGatewayListener: &specListener,
			// This Gateway declares the listener, so it is what its object
			// references are resolved and granted against.
			OwnerKind:                kindGateway,
			ExemptFromReferenceGrant: g.GatewayForIngress,
			AttachedRoutes:           make(map[client.ObjectKey]struct{}),
			Conditions:               make(generic.Conditions),
		})
	}
	g.Listeners = ownListeners
}

// listenerByKey resolves a listener of the merged list by its identity. A linear
// scan over a handful of listeners, which is what lets the list stay ordered.
func (g *Gateway) listenerByKey(key listenerKey) *Listener {
	for _, listener := range g.Listeners {
		if listener.Key() == key {
			return listener
		}
	}
	return nil
}

// OwnListeners are the listeners this Gateway declares itself, without the entries
// contributed by attached ListenerSets.
//
// This is the list a parentRef naming the Gateway attaches to. The Gateway API is
// explicit that a route reaches a ListenerSet's listeners "by specifying it as a
// parentRef": the parent named is the resource whose listeners are candidates, and
// reaching both a Gateway and one of its ListenerSets takes two parentRefs. The
// same asymmetry is what makes the API's own counter-example hold -- a Gateway
// parentRef carrying a sectionName that exists only on a ListenerSet must not
// attach.
//
// Filtered by origin rather than taken as the leading run of the merged list: the
// order is a contract for precedence, not a promise that own listeners come first
// for ever.
func (g *Gateway) OwnListeners() []*Listener {
	own := make([]*Listener, 0, len(g.Listeners))
	for _, listener := range g.Listeners {
		if !listener.FromListenerSet() {
			own = append(own, listener)
		}
	}
	return own
}

// OwnListenerByName resolves a listener the Gateway declares **itself**, by the
// name a sectionName would use.
//
// Deliberately blind to the entries contributed by ListenerSets: a parentRef naming
// this Gateway with a sectionName that exists only on one of its ListenerSets must
// not attach -- reaching such a listener requires naming the ListenerSet as parent.
// The Gateway API gives that exact case as a counter-example, and resolving it here
// by bare name over the whole merged list is how an implementation gets it wrong.
func (g *Gateway) OwnListenerByName(name gatewayv1.SectionName) *Listener {
	for _, listener := range g.Listeners {
		if !listener.FromListenerSet() && listener.Name() == name {
			return listener
		}
	}
	return nil
}

func (g *Gateway) BuildConditions() {
	if !g.isManaged() {
		return
	}
	g.Conditions = make(generic.Conditions)

	if !g.CheckValidGatewayClass.Valid {
		g.Conditions.MergeOverrideConditions(g.CheckValidGatewayClass.Conditions)
		g.Conditions.SetGeneration(g.K8sResource.GetGeneration())
		return
	}
	if !g.CheckParamsRef.Valid {
		g.Conditions.MergeOverrideConditions(g.CheckParamsRef.Conditions)
		// retrieve condition type accepted to get the appropriate message
		messageInvalidParams := g.Conditions.GetMessage(generic.ConditionType(gatewayv1.GatewayConditionAccepted))
		g.Conditions.MergeOverrideConditions(conditions.NewGatewayProgrammedInvalidParameters(messageInvalidParams))
		g.Conditions.SetGeneration(g.K8sResource.GetGeneration())
		return
	}

	_, exists := g.CheckConflict.Conditions.GetCondition(generic.ConditionType(gatewayv1.GatewayConditionAccepted))
	if exists {
		if !g.CheckConflict.Valid {
			// All listeners are conflicted
			g.Conditions.MergeOverrideConditions(g.CheckConflict.Conditions)
			g.Conditions.SetGeneration(g.K8sResource.GetGeneration())
			return
		}
		// Some listeners are conflicted, but at leats 1 is valid
		g.Conditions.MergeOverrideConditions(g.CheckConflict.Conditions)
		g.Conditions.MergeOverrideConditions(conditions.NewGatewayProgrammedOK())
		g.Conditions.SetGeneration(g.K8sResource.GetGeneration())
		return
	}

	// Listeners with unsupported protocols surface in the Gateway Accepted
	// condition through the ListenersNotValid reason, per the spec.
	if invalid, total := g.unsupportedProtocolListeners(); invalid > 0 {
		if invalid == total {
			// A rejected Gateway must not look ready to automation waiting on Programmed.
			g.Conditions.MergeOverrideConditions(conditions.NewGatewayAcceptedListenersNotValid("All listeners have unsupported protocols"))
			g.Conditions.MergeOverrideConditions(conditions.NewGatewayProgrammedListenersNotValid("All listeners have unsupported protocols"))
			g.Conditions.SetGeneration(g.K8sResource.GetGeneration())
			return
		}
		g.Conditions.MergeOverrideConditions(conditions.NewGatewayAcceptedListenerNotValidAtLeast1Valid("Some listeners have unsupported protocols"))
	} else {
		g.Conditions.MergeOverrideConditions(conditions.NewGatewayAcceptedOK())
	}
	g.Conditions.MergeOverrideConditions(conditions.NewGatewayProgrammedOK())

	g.Conditions.SetGeneration(g.K8sResource.GetGeneration())
}
