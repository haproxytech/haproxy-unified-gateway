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
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	utilsk8s "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils-k8s"

	"github.com/google/go-cmp/cmp"
	rc "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/routes"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/gateway-api/apis/v1alpha2"
)

type StatusPatcher interface {
	StatusEqual(client.Object) (bool, error)
	SetStatus(client.Object) error
}

// ---------------------------
// GatewayClass
func newGatewayClassStatusPatcher(gwc *tree.GatewayClass, logger *slog.Logger) StatusPatcher {
	return &gatewayClassStatusPatcher{
		conditions: gwc.Conditions,
		logger:     logger,
	}
}

var _ StatusPatcher = &gatewayClassStatusPatcher{}

type gatewayClassStatusPatcher struct {
	conditions generic.Conditions
	logger     *slog.Logger
}

func (sp *gatewayClassStatusPatcher) StatusEqual(obj client.Object) (bool, error) {
	gwc, ok := obj.(*gatewayv1.GatewayClass)
	if !ok {
		return false, fmt.Errorf("wrong type %T", obj)
	}
	conds := generic.NewConditionsFromMetav1Conditions(gwc.Status.Conditions)
	return sp.conditions.Equal(conds), nil
}

func (sp *gatewayClassStatusPatcher) SetStatus(obj client.Object) error {
	gwc, ok := obj.(*gatewayv1.GatewayClass)
	if !ok {
		return fmt.Errorf("wrong type %T", obj)
	}
	metav1conds := sp.conditions.ToMetav1Conditions()
	gwc.Status = gatewayv1.GatewayClassStatus{
		Conditions: metav1conds,
	}
	return nil
}

// ---------------------------
// Gateway
func newGatewayStatusPatcher(gw *tree.Gateway, addresses []gatewayv1.GatewayStatusAddress, extractGVK utilsk8s.ExtractGVK, logger *slog.Logger) StatusPatcher {
	listenerStatuses := make([]gatewayv1.ListenerStatus, 0, len(gw.Listeners))
	if gw.Valid {
		// If the Gateway is invalid, the listeners status should be empty
		for _, listener := range gw.Listeners {
			conds := listener.Conditions.ToMetav1Conditions()
			listenerStatuses = append(listenerStatuses, gatewayv1.ListenerStatus{
				Name:           listener.K8sResource.Name,
				Conditions:     conds,
				SupportedKinds: listener.AllowedRouteKinds,
				AttachedRoutes: int32(len(listener.AttachedRoutes)), // To be changed with the correct value
			})
		}
	}

	return &gatewayStatusPatcher{
		conditions:       gw.Conditions,
		listenerStatuses: listenerStatuses,
		addresses:        addresses,
		logger:           logger,
		extractGVK:       extractGVK,
	}
}

var _ StatusPatcher = &gatewayStatusPatcher{}

type gatewayStatusPatcher struct {
	conditions       generic.Conditions
	logger           *slog.Logger
	extractGVK       utilsk8s.ExtractGVK
	listenerStatuses []gatewayv1.ListenerStatus
	addresses        []gatewayv1.GatewayStatusAddress
}

func (sp *gatewayStatusPatcher) StatusEqual(obj client.Object) (bool, error) {
	gw, ok := obj.(*gatewayv1.Gateway)
	if !ok {
		return false, fmt.Errorf("wrong type %T", obj)
	}
	gwConds := generic.NewConditionsFromMetav1Conditions(gw.Status.Conditions)
	if !sp.conditions.Equal(gwConds) {
		return false, nil
	}
	if !ListenerStatusesEqual(sp.listenerStatuses, gw.Status.Listeners) {
		return false, nil
	}
	return gatewayAddressesEqual(sp.addresses, gw.Status.Addresses), nil
}

func ListenerStatusesEqual(a, b []gatewayv1.ListenerStatus) bool {
	sortListenerStatusByName(a)
	sortListenerStatusByName(b)
	listenerStatusEqual := func(a, b gatewayv1.ListenerStatus) bool {
		if a.Name != b.Name {
			return false
		}

		if a.AttachedRoutes != b.AttachedRoutes {
			return false
		}

		aConds := generic.NewConditionsFromMetav1Conditions(a.Conditions)
		bConds := generic.NewConditionsFromMetav1Conditions(b.Conditions)
		if !aConds.Equal(bConds) {
			return false
		}

		return cmp.Equal(a.SupportedKinds, b.SupportedKinds)
	}
	return slices.EqualFunc(a, b, listenerStatusEqual)
}

func (sp *gatewayStatusPatcher) SetStatus(obj client.Object) error {
	gw, ok := obj.(*gatewayv1.Gateway)
	if !ok {
		return fmt.Errorf("wrong type %T", obj)
	}
	metav1conds := sp.conditions.ToMetav1Conditions()

	gw.Status = gatewayv1.GatewayStatus{
		Conditions: metav1conds,
		Listeners:  sp.listenerStatuses,
		Addresses:  sp.addresses,
	}

	sp.logger.LogAttrs(context.Background(), slog.LevelDebug, "[UPDATE] Gateway Status",
		logging.LogAttrResource(gw, sp.extractGVK(gw)))
	sp.logger.LogAttrs(context.Background(), slog.LevelDebug, fmt.Sprintf("listener status: %v", gw.Status.Listeners))

	return nil
}

func gatewayAddressesEqual(a, b []gatewayv1.GatewayStatusAddress) bool {
	if len(a) != len(b) {
		return false
	}
	sortFunc := func(x, y gatewayv1.GatewayStatusAddress) int {
		return strings.Compare(x.Value, y.Value)
	}
	sortedA := slices.Clone(a)
	sortedB := slices.Clone(b)
	slices.SortFunc(sortedA, sortFunc)
	slices.SortFunc(sortedB, sortFunc)
	for i := range sortedA {
		if sortedA[i].Value != sortedB[i].Value {
			return false
		}
		if (sortedA[i].Type == nil) != (sortedB[i].Type == nil) {
			return false
		}
		if sortedA[i].Type != nil && *sortedA[i].Type != *sortedB[i].Type {
			return false
		}
	}
	return true
}

// sortListenerStatusByName sorts a slice of gatewayapi.ListenerStatus structs
// in place, in ascending order based on the `Name` field.
// It uses the standard library's `slices.SortFunc` for efficient sorting.
func sortListenerStatusByName(listeners []gatewayv1.ListenerStatus) {
	slices.SortFunc(listeners, func(a, b gatewayv1.ListenerStatus) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
}

// ---------------------------
// HTTPRoute
func newHTTPRouteStatusPatcher(route *tree.HTTPRoute) StatusPatcher {
	return &httpRouteStatusPatcher{
		controllerName: route.ControllerName,
		conditions:     route.Conditions,
	}
}

var _ StatusPatcher = &httpRouteStatusPatcher{}

type httpRouteStatusPatcher struct {
	controllerName string
	conditions     rc.RouteConditions
}

func (sp *httpRouteStatusPatcher) StatusEqual(obj client.Object) (bool, error) {
	route, ok := obj.(*gatewayv1.HTTPRoute)
	if !ok {
		return false, fmt.Errorf("wrong type %T", obj)
	}
	filteredCondFromClusterRoute := FilterStatusByControllerName(route.Status, sp.controllerName)
	clusterConds := rc.NewRouteConditionsFromV1RouteConditions(filteredCondFromClusterRoute.Parents, sp.controllerName)

	// conditions from the sp
	expectedConds := sp.conditions
	// should be equal to conditions from the clusterObj

	return RouteStatusesEqual(clusterConds, expectedConds), nil
}

func (sp *httpRouteStatusPatcher) SetStatus(obj client.Object) error {
	route, ok := obj.(*gatewayv1.HTTPRoute)
	if !ok {
		return fmt.Errorf("wrong type %T", obj)
	}

	// Start with a list of statuses from other controllers.
	preservedStatuses := make([]gatewayv1.RouteParentStatus, 0, len(route.Status.Parents))
	for _, parentStatus := range route.Status.Parents {
		if parentStatus.ControllerName != gatewayv1.GatewayController(sp.controllerName) {
			preservedStatuses = append(preservedStatuses, parentStatus)
		}
	}

	// Append our controller's new statuses.
	newParentStatuses := append(preservedStatuses, sp.conditions.ToV1RouteConditions().Parents...)
	sortRouteParentStatusByParentRef(newParentStatuses)
	route.Status.Parents = newParentStatuses
	return nil
}

// sortRouteParentStatusByParentRef sorts a slice of RouteParentStatus by a
// deterministic order based on their ParentReference fields.
func sortRouteParentStatusByParentRef(parents []gatewayv1.RouteParentStatus) {
	slices.SortFunc(parents, func(a, b gatewayv1.RouteParentStatus) int {
		if c := utils.ComparePointers(a.ParentRef.Group, b.ParentRef.Group); c != 0 {
			return c
		}
		if c := utils.ComparePointers(a.ParentRef.Kind, b.ParentRef.Kind); c != 0 {
			return c
		}
		if c := utils.ComparePointers(a.ParentRef.Namespace, b.ParentRef.Namespace); c != 0 {
			return c
		}
		if a.ParentRef.Name != b.ParentRef.Name {
			if a.ParentRef.Name < b.ParentRef.Name {
				return -1
			}
			return 1
		}
		if c := utils.ComparePointers(a.ParentRef.SectionName, b.ParentRef.SectionName); c != 0 {
			return c
		}
		return utils.ComparePointers(a.ParentRef.Port, b.ParentRef.Port)
	})
}

func FilterStatusByControllerName(routeStatus gatewayv1.HTTPRouteStatus, controllerName string) gatewayv1.HTTPRouteStatus {
	filtered := make([]gatewayv1.RouteParentStatus, 0, len(routeStatus.RouteStatus.Parents))
	for _, parent := range routeStatus.RouteStatus.Parents {
		if parent.ControllerName == gatewayv1.GatewayController(controllerName) {
			filtered = append(filtered, parent)
		}
	}
	return gatewayv1.HTTPRouteStatus{
		RouteStatus: gatewayv1.RouteStatus{Parents: filtered},
	}
}

func RouteStatusesEqual(a, b rc.RouteConditions) bool {
	return a.Equal(b)
}

// ---------------------------
// TLSRoute
func newTLSRouteStatusPatcher(tlsRoute *tree.TLSRoute) StatusPatcher {
	return &tlsRouteStatusPatcher{
		controllerName: tlsRoute.ControllerName,
		conditions:     tlsRoute.Conditions,
	}
}

var _ StatusPatcher = &tlsRouteStatusPatcher{}

type tlsRouteStatusPatcher struct {
	controllerName string
	conditions     rc.RouteConditions
}

func (sp *tlsRouteStatusPatcher) StatusEqual(obj client.Object) (bool, error) {
	tlsRoute, ok := obj.(*v1alpha2.TLSRoute)
	if !ok {
		return false, fmt.Errorf("wrong type %T", obj)
	}
	filteredCondFromClusterRoute := FilterTLSRouteStatusByControllerName(tlsRoute.Status, sp.controllerName)
	clusterConds := rc.NewRouteConditionsFromV1RouteConditions(filteredCondFromClusterRoute.Parents, sp.controllerName)

	// conditions from the sp
	expectedConds := sp.conditions
	// should be equal to conditions from the clusterObj

	return RouteStatusesEqual(clusterConds, expectedConds), nil
}

func (sp *tlsRouteStatusPatcher) SetStatus(obj client.Object) error {
	tlsRoute, ok := obj.(*v1alpha2.TLSRoute)
	if !ok {
		return fmt.Errorf("wrong type %T", obj)
	}

	// Start with a list of statuses from other controllers.
	preservedStatuses := make([]gatewayv1.RouteParentStatus, 0, len(tlsRoute.Status.Parents))
	for _, parentStatus := range tlsRoute.Status.Parents {
		if parentStatus.ControllerName != gatewayv1.GatewayController(sp.controllerName) {
			preservedStatuses = append(preservedStatuses, parentStatus)
		}
	}

	// Append our controller's new statuses.
	newParentStatuses := append(preservedStatuses, sp.conditions.ToV1RouteConditions().Parents...)
	sortRouteParentStatusByParentRef(newParentStatuses)
	tlsRoute.Status.Parents = newParentStatuses
	return nil
}

// sortRouteParentStatusByParentRef sorts a slice of RouteParentStatus by a
// deterministic order based on their ParentReference fields.

func FilterTLSRouteStatusByControllerName(tlsRouteStatus v1alpha2.TLSRouteStatus, controllerName string) v1alpha2.TLSRouteStatus {
	filtered := make([]gatewayv1.RouteParentStatus, 0, len(tlsRouteStatus.RouteStatus.Parents))
	for _, parent := range tlsRouteStatus.RouteStatus.Parents {
		if parent.ControllerName == gatewayv1.GatewayController(controllerName) {
			filtered = append(filtered, parent)
		}
	}
	return v1alpha2.TLSRouteStatus{
		RouteStatus: gatewayv1.RouteStatus{Parents: filtered},
	}
}

// ---------------------------
// Gateway listener feedback (Programmed conditions only, sourced from the tree)

// newGatewayListenerFeedbackStatusPatcher snapshots the Programmed condition
// from each listener in gw at call time. SetStatus applies those conditions to
// the live k8s gateway status.
func newGatewayListenerFeedbackStatusPatcher(gw *tree.Gateway, logger *slog.Logger) StatusPatcher {
	condType := generic.ConditionType(gatewayv1.ListenerConditionProgrammed)
	snapshot := make(map[gatewayv1.SectionName]generic.Condition, len(gw.Listeners))
	logger.LogAttrs(context.Background(), slog.LevelDebug, "[feedback] gateway",
		logging.LogAttrObjectKey(gw.K8sResource))
	for _, listener := range gw.Listeners {
		if cond, ok := listener.Conditions.GetCondition(condType); ok {
			snapshot[listener.K8sResource.Name] = cond
			logger.LogAttrs(context.Background(), slog.LevelDebug, "[feedback] listener",
				slog.String("listener ", string(listener.K8sResource.Name)),
				slog.Any("cond", cond))
		}
	}
	return &gatewayListenerFeedbackStatusPatcher{listenerProgrammedConditions: snapshot, logger: logger}
}

var _ StatusPatcher = &gatewayListenerFeedbackStatusPatcher{}

type gatewayListenerFeedbackStatusPatcher struct {
	// listenerProgrammedConditions maps listener name → snapshotted Programmed condition.
	listenerProgrammedConditions map[gatewayv1.SectionName]generic.Condition
	logger                       *slog.Logger
}

// StatusEqual returns true (skip update) when all live listener Programmed
// conditions already match the snapshot or are newer (stale result guard).
func (sp *gatewayListenerFeedbackStatusPatcher) StatusEqual(obj client.Object) (bool, error) {
	gw, ok := obj.(*gatewayv1.Gateway)
	if !ok {
		return false, fmt.Errorf("wrong type %T", obj)
	}
	for _, listener := range gw.Status.Listeners {
		want, ok := sp.listenerProgrammedConditions[listener.Name]
		if !ok {
			continue
		}
		found := false
		for _, cond := range listener.Conditions {
			if cond.Type != string(gatewayv1.ListenerConditionProgrammed) {
				continue
			}
			found = true
			// Live has a higher generation: our snapshot is stale — skip all.
			if want.ObservedGeneration < cond.ObservedGeneration {
				return true, nil
			}
			if cond.Status != want.Status || cond.Reason != want.Reason {
				return false, nil
			}
		}
		if !found {
			return false, nil // condition absent in k8s, needs to be written
		}
	}
	return true, nil
}

// SetStatus patches only the Programmed condition on each listener whose name
// is present in the snapshot. The rest of the gateway status is left untouched.
func (sp *gatewayListenerFeedbackStatusPatcher) SetStatus(obj client.Object) error {
	gw, ok := obj.(*gatewayv1.Gateway)
	if !ok {
		return fmt.Errorf("wrong type %T", obj)
	}
	for i, listener := range gw.Status.Listeners {
		want, ok := sp.listenerProgrammedConditions[listener.Name]
		if !ok {
			continue
		}
		newCond := metav1.Condition{
			Type:               string(gatewayv1.ListenerConditionProgrammed),
			Status:             want.Status,
			Reason:             want.Reason,
			Message:            want.Message,
			ObservedGeneration: want.ObservedGeneration,
			LastTransitionTime: metav1.Now(),
		}
		replaced := false
		for j, c := range gw.Status.Listeners[i].Conditions {
			if c.Type == string(gatewayv1.ListenerConditionProgrammed) {
				gw.Status.Listeners[i].Conditions[j] = newCond
				replaced = true
				break
			}
		}
		if !replaced {
			gw.Status.Listeners[i].Conditions = append(gw.Status.Listeners[i].Conditions, newCond)
		}
	}
	return nil
}
