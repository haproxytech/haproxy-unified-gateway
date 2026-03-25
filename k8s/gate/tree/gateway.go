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
	// Listeners include the listeners of the Gateway.
	Listeners map[string]*Listener // map[listenerName]
	// TreeStatus
	TreeStatus TreeUpdate[Gateway]
	// Management Checks
	CheckParamsRef         CheckResult
	CheckValidGatewayClass CheckResult
	// Additional checks
	CheckConflict CheckResult
	// Valid shows whether the Gateway is valid.
	Valid bool
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
	g.Listeners = make(map[string]*Listener)
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
	g.CheckValidGatewayClass = CheckResult{
		Valid:      false,
		Conditions: conditions.NewGatewayAcceptedInvalidConditions(string(g.K8sResource.Spec.GatewayClassName)),
	}
}

// checkListenerConflicts on the Gateway checks if there is at least one Listener that has no conflict
// - if there is at least one listener for this Gateway that has no conflict, then the Gateway status is "Accepted"
// - if none listener are valid, then the Gateway condition type Accepted is False
func (g *Gateway) checkListenerConflicts(mapPort2ListenerConflict map[gatewayv1.PortNumber]listenerConflict) {
	// See if at least one of them does not have conflict
	// Or if they all have confict

	nbListenersWithAndWithoutConflict := nbListenersWithAndWithoutConflict(mapPort2ListenerConflict, g)

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
				Conditions: conditions.NewGatewayAcceptedListenerNotValidAtLeast1Valid(),
			}
		}
	} else {
		// 3- No listener conflict for this Gateway
		g.CheckConflict = CheckResult{
			Valid:      true,
			Conditions: conditions.NewGatewayAcceptedOK(),
		}
	}
	g.Valid = g.Valid && g.CheckConflict.Valid
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

	g.Conditions.MergeOverrideConditions(conditions.NewGatewayAcceptedOK())
	g.Conditions.MergeOverrideConditions(conditions.NewGatewayProgrammedOK())

	g.Conditions.SetGeneration(g.K8sResource.GetGeneration())
}
