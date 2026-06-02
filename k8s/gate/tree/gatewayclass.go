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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// GatewayClass represents the GatewayClass resource.
type GatewayClass struct {
	// TreeStatus
	TreeStatus TreeUpdate[GatewayClass]
	// K8sResource is the source resource.
	K8sResource *gatewayv1.GatewayClass
	// HugGate is the linked HugGate from ParamsRef
	HugGate *v3.HugGate
	// Conditions include Conditions for the GatewayClass.
	Conditions generic.Conditions
	// CheckParamsRef shows whether the GatewayClass is valid as for ParamsRef
	CheckParamsRef CheckResult
	// Valid is true if the GatewayClass is Valid (versions + haproxy gate paramsRef)
	Valid bool
	// Managed is true if the GatewayClass is Managed (should be always true)
	Managed bool
}

var _ utils.ObjectWithTimestamp = &GatewayClass{}

func NewGatewayClass(k8sObject *gatewayv1.GatewayClass) *GatewayClass {
	return &GatewayClass{
		K8sResource: k8sObject,
		Valid:       false,
		Managed:     true,
		Conditions:  conditions.NewGatewayClassAcceptedOK(),
		TreeStatus: TreeUpdate[GatewayClass]{
			Status:          store.StatusUpserted,
			OldTreeResource: nil,
		},
	}
}

func (g *GatewayClass) SetAsUpserted(logger *slog.Logger, newK8sResource *gatewayv1.GatewayClass) {
	setResourceStatus(logger, g, newK8sResource, store.StatusUpserted)
	g.K8sResource = newK8sResource
}

func (g *GatewayClass) SetAsDeleted(logger *slog.Logger) {
	setResourceStatus(logger, g, nil, store.StatusDeleted)
	g.K8sResource = nil
}

func (g *GatewayClass) ResetChecks() {
	g.Conditions = conditions.NewGatewayClassAcceptedOK()
	g.HugGate = nil
	g.CheckParamsRef = CheckResult{}
	g.Valid = false
}

func (g *GatewayClass) SetAsManaged(logger *slog.Logger, controllerStore ControllerStore) {
	logger.LogAttrs(context.Background(), slog.LevelDebug, fmt.Sprintf("%T MANAGED", *g),
		logging.LogAttrObjectKey(g.K8sResource))
	// Is it already in Managed
	key := client.ObjectKeyFromObject(g.K8sResource)
	controllerStore.GateTree.GatewayClasses[key] = g
	delete(controllerStore.UnmanagedGateTree.GatewayClasses, key)
}

func (g *GatewayClass) SetAsUnmanaged(logger *slog.Logger, controllerStore ControllerStore) {
	logger.LogAttrs(context.Background(), slog.LevelDebug, fmt.Sprintf("%T UNMANAGED", *g),
		logging.LogAttrObjectKey(g.K8sResource))
	// Is it already in Managed
	key := client.ObjectKeyFromObject(g.K8sResource)
	controllerStore.UnmanagedGateTree.GatewayClasses[key] = g
	delete(controllerStore.GateTree.GatewayClasses, key)
}

func (g *GatewayClass) GetCreationTimestamp() metav1.Time {
	return g.K8sResource.GetCreationTimestamp()
}

// GetName returns the name of the GatewayClass Kubernetes resource.
// If the K8sResource is nil (for example a DELETED GatewayClass), it returns an empty string.
func (g *GatewayClass) GetName() string {
	if g.K8sResource != nil {
		return g.K8sResource.GetName()
	}
	return ""
}

func (g *GatewayClass) checkParametersRef(controllerStore ControllerStore) {
	if g == nil {
		return
	}
	switch g.TreeStatus.Status {
	case store.StatusUpserted:
		paramRef := g.K8sResource.Spec.ParametersRef
		checker := HugGateParamsRefChecker{
			ParamRef:      paramRef,
			StoreHugGates: controllerStore.ClusterStore.HugGates,
		}
		var hugGate *v3.HugGate
		g.CheckParamsRef, hugGate = checker.CheckGatewayClass()
		if g.CheckParamsRef.Valid {
			g.HugGate = hugGate
		}
	case store.StatusDeleted:
		// nothing to do
	}
}

func (g *GatewayClass) BuildConditions(controllerStore ControllerStore) {
	switch g.Managed {
	case true:
		g.buildConditionsManaged(controllerStore.Logger, controllerStore)
	case false:
		g.buildConditionsIgnored(controllerStore.Logger)
	}
}

func (g *GatewayClass) buildConditionsManaged(_ *slog.Logger, cs ControllerStore) {
	// Checks on Supported Versions
	switch cs.InstalledGwAPIVersions.Valid {
	case true:
		g.Conditions.MergeOverrideConditions(
			conditions.NewGatewayClassSupportedVersionOK(),
		)
	case false:
		g.Conditions.MergeOverrideConditions(
			conditions.NewGatewayClassSupportedVersionUnsupportedVersion(SupportedGatewayAPIBundleVersion.String()),
		)
	}

	// Checks on parametersRef
	g.Conditions.MergeOverrideConditions(g.CheckParamsRef.Conditions)
	// Generation
	g.Conditions.SetGeneration(g.K8sResource.GetGeneration())
}

func (g *GatewayClass) buildConditionsIgnored(_ *slog.Logger) {
	g.Conditions.MergeOverrideConditions(conditions.NewGatewayClassAcceptedUnsupported())
	g.Conditions.SetGeneration(g.K8sResource.GetGeneration())
}

func (g *GatewayClass) DeepCopy() *GatewayClass {
	if g == nil {
		return nil
	}
	// Save TreeStatus
	treeStatus := g.TreeStatus
	g.TreeStatus = TreeUpdate[GatewayClass]{}

	var copied GatewayClass
	data, err := json.Marshal(g) // Serialize to JSON
	if err != nil {
		return nil
	}
	_ = json.Unmarshal(data, &copied) // Deserialize to a new struct	return &copied
	// Restore TreeStatus
	g.TreeStatus = treeStatus
	return &copied
}

// GetTreeStatus returns the TreeStatus of the GatewayClass.
func (g *GatewayClass) GetTreeStatus() *TreeUpdate[GatewayClass] {
	return &g.TreeStatus
}

// SetTreeStatus sets the TreeStatus of the GatewayClass.
func (g *GatewayClass) SetTreeStatus(treeStatus TreeUpdate[GatewayClass]) {
	g.TreeStatus = treeStatus
}
