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
	"bytes"
	"cmp"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"slices"
	"strings"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	objtypes "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/object-types"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type Listener struct {
	// K8sResource is the source resource.
	K8sResource gatewayv1.Listener
	// Final Conditions
	Conditions     generic.Conditions
	AttachedRoutes AttachedRoutes
	// Owner is the gateway that this listener is connected to
	Owner client.ObjectKey
	// VirtualListenerName is the virtual listener name that this listener is attached to, identified by its port and protocol category
	VirtualListenerName string
	// Checks results
	CheckRouteGroupKind CheckResult
	CheckProtocol       CheckResult
	CheckSecret         CheckResult
	CheckConflict       CheckResult
	// AllowedRouteKinds is the list of allowed route kinds for this listener.
	AllowedRouteKinds []gatewayv1.RouteGroupKind
	// Valid indicates the listener is fully programmed (Accepted, no conflicts, all refs resolved).
	Valid bool
	// Accepted indicates the listener configuration is accepted (valid protocol, no conflicts).
	// Routes can attach to an accepted listener even if refs are unresolved.
	Accepted bool
}

// DeepCopy creates a deep copy of the Listener.
func (l *Listener) DeepCopy() *Listener {
	if l == nil {
		return nil
	}

	var copied Listener
	// We can ignore the error here, as we are controlling the input
	data, _ := json.Marshal(l)
	_ = json.Unmarshal(data, &copied)

	return &copied
}

type RouteGroupKind struct {
	// Group is the group of the RouteGroupKind.
	Group gatewayv1.Group `json:"group,omitempty"`
	// Kind is the kind of the RouteGroupKind.
	Kind gatewayv1.Kind `json:"kind"`
}

var gateSupportedRouteKindsByProtocol = map[gatewayv1.ProtocolType][]gatewayv1.RouteGroupKind{
	gatewayv1.HTTPProtocolType:  {objtypes.RouteKindHTTP},
	gatewayv1.HTTPSProtocolType: {objtypes.RouteKindHTTP},
	gatewayv1.TLSProtocolType:   {objtypes.RouteKindTLS},
}

func supportedKinds(listener gatewayv1.Listener, gateSupportedRouteKinds map[gatewayv1.ProtocolType][]gatewayv1.RouteGroupKind) []gatewayv1.RouteGroupKind {
	gateSupportedRouteKindsForProtocol := gateSupportedRouteKinds[listener.Protocol]
	kinds := []gatewayv1.RouteGroupKind{}

	// If the listener does not have allowedRoutes.Kinds defined, then list of supported routes types only depends
	// on the listener protocol and is what is supported by the gateway.
	if listener.AllowedRoutes == nil || len(listener.AllowedRoutes.Kinds) == 0 {
		kinds = append(kinds, gateSupportedRouteKindsForProtocol...)

		slices.SortFunc(kinds, sortRouteKinds)
		return kinds
	}

	// If the listener has some allowedRoutes.Kinds, we take the intersection
	// of what the listener allows and what the gateway supports for the protocol.
	if listener.AllowedRoutes != nil {
		for _, listenerAllowedKind := range listener.AllowedRoutes.Kinds {
			if ok := isSupportedProtocolRouteKind(listenerAllowedKind, gateSupportedRouteKindsForProtocol); ok {
				kinds = append(kinds, listenerAllowedKind)
			}
		}
	}

	slices.SortFunc(kinds, sortRouteKinds)
	return kinds
}

func sortRouteKinds(a, b gatewayv1.RouteGroupKind) int {
	// A nil group defaults to gateway.networking.k8s.io
	groupA := gatewayv1.GroupName
	if a.Group != nil {
		groupA = string(*a.Group)
	}
	groupB := gatewayv1.GroupName
	if b.Group != nil {
		groupB = string(*b.Group)
	}
	// Compare by Group first, then by Kind.
	if c := cmp.Compare(groupA, groupB); c != 0 {
		return c
	}
	return cmp.Compare(string(a.Kind), string(b.Kind))
}

func isSupportedProtocolRouteKind(kind gatewayv1.RouteGroupKind, supportedRouteKinds []gatewayv1.RouteGroupKind) bool {
	if kind.Group != nil && *kind.Group != gatewayv1.GroupName {
		return false
	}
	for _, k := range supportedRouteKinds {
		if k.Kind == kind.Kind {
			return true
		}
	}

	return false
}

func (l *Listener) checkRouteGroupKind(treeGw *Gateway, gateSupportedRouteKinds map[gatewayv1.ProtocolType][]gatewayv1.RouteGroupKind) {
	if !treeGw.Valid {
		l.CheckRouteGroupKind = CheckResult{}
		return
	}

	listener := l.K8sResource
	gateSupportedRouteKindsForProtocol := gateSupportedRouteKinds[listener.Protocol]

	// If the listener does not have allowedRoutes.Kinds defined, then list of supported routes types only depends
	// on the listener protocol and is what is supported by the gateway.
	if listener.AllowedRoutes == nil || len(listener.AllowedRoutes.Kinds) == 0 {
		l.CheckRouteGroupKind = CheckResult{
			Valid: true,
		}
		return
	}

	// If the listener has some allowedRoutes.Kinds, we take the intersection
	// of what the listener allows and what the gateway supports for the protocol.
	unsupportedRoute := ""
	if listener.AllowedRoutes != nil {
		for _, listenerAllowedKind := range listener.AllowedRoutes.Kinds {
			if ok := isSupportedProtocolRouteKind(listenerAllowedKind, gateSupportedRouteKindsForProtocol); !ok {
				unsupportedRoute = string(listenerAllowedKind.Kind)
			}
		}
	}

	checkResult := CheckResult{}
	if unsupportedRoute != "" {
		acceptedKinds := make([]gatewayv1.RouteGroupKind, 0, len(gateSupportedRouteKindsForProtocol))
		acceptedKinds = append(acceptedKinds, gateSupportedRouteKindsForProtocol...)
		slices.SortFunc(acceptedKinds, sortRouteKinds)

		msg := fmt.Sprintf("%s is not supported. Supported Route Kinds for this protocol %s", unsupportedRoute, utils.RouteGroupKindsToString(acceptedKinds))
		checkResult.Conditions = conditions.NewListenerResolvedRefInvalidRouteKinds(msg)
		checkResult.Valid = false

		l.CheckRouteGroupKind = checkResult
		return
	}

	l.CheckRouteGroupKind = CheckResult{
		Valid: true,
	}
}

func (l *Listener) checkProtocol(gateSupportedRouteKinds map[gatewayv1.ProtocolType][]gatewayv1.RouteGroupKind) {
	listener := l.K8sResource
	gateSupportedRouteKindsForProtocol := gateSupportedRouteKinds[listener.Protocol]
	// UnsupportedProtocol
	if len(gateSupportedRouteKindsForProtocol) == 0 {
		supportedProtocols := make([]string, 0, len(gateSupportedRouteKinds))

		for protocol := range gateSupportedRouteKinds {
			supportedProtocols = append(supportedProtocols, string(protocol))
		}
		slices.Sort(supportedProtocols)

		valErr := field.NotSupported(
			field.NewPath("protocol"),
			listener.Protocol,
			supportedProtocols,
		)
		l.CheckProtocol = CheckResult{
			Valid:      false,
			Conditions: conditions.NewListenerAcceptedUnsupportedProtocol(valErr.Error()),
		}
		return
	}
	if listener.Protocol == gatewayv1.TLSProtocolType &&
		listener.TLS != nil &&
		listener.TLS.Mode != nil &&
		*listener.TLS.Mode == gatewayv1.TLSModeTerminate {
		valErr := field.NotSupported(
			field.NewPath("protocol"),
			listener.Protocol,
			[]string{"TLS/Passthrough"},
		)
		l.CheckProtocol = CheckResult{
			Valid:      false,
			Conditions: conditions.NewListenerAcceptedUnsupportedProtocol(valErr.Error()),
		}
		return
	}

	l.CheckProtocol = CheckResult{
		Valid: true,
	}
}

func (l *Listener) checkCertificateRefs(treeGw *Gateway, gateSecrets map[types.NamespacedName]*Secret) {
	if !treeGw.Valid {
		l.CheckSecret = CheckResult{}
		return
	}

	listener := l.K8sResource

	// Check if we miss the TLS section for HTTPS protocol
	if listener.Protocol == gatewayv1.HTTPSProtocolType {
		if listener.TLS == nil || len(listener.TLS.CertificateRefs) == 0 {
			msg := fmt.Sprintf("listener %s has protocol HTTPS but no TLS section", listener.Name)
			l.CheckSecret = CheckResult{
				Valid:      false,
				Conditions: conditions.NewListenerResolvedRefInvalidCertificateRefs(msg),
			}
			return
		}
	}

	// Check is the secret exists
	// We only accept Secret as CertificateRefs
	// We only accept v1.Secret
	for _, certRef := range listener.TLS.CertificateRefs {
		if !l.isSupportedCertKindGroup(certRef) {
			msg := "Listener CertificateRefs must be of Group/Kind Secret"
			l.CheckSecret = CheckResult{
				Valid:      false,
				Conditions: conditions.NewListenerResolvedRefInvalidCertificateRefs(msg),
			}
			break
		}

		nsName := GetCertificateRefNamespacedName(certRef, treeGw.K8sResource)
		treeSecret, ok := gateSecrets[nsName]
		if !ok || treeSecret.TreeStatus.Status == store.StatusDeleted {
			msg := fmt.Sprintf("Secret %s/%s does not exist", nsName.Namespace, nsName.Name)
			l.CheckSecret = CheckResult{
				Valid:      false,
				Conditions: conditions.NewListenerResolvedRefInvalidCertificateRefs(msg),
			}
			continue
		}

		certPEM := treeSecret.K8sResource.Data[v1.TLSCertKey]
		keyPEM := treeSecret.K8sResource.Data[v1.TLSPrivateKeyKey]

		if bytes.Equal(certPEM, keyPEM) {
			msg := fmt.Sprintf("Secret %s/%s: certificate and key are identical", nsName.Namespace, nsName.Name)
			l.CheckSecret = CheckResult{
				Valid:      false,
				Conditions: conditions.NewListenerResolvedRefInvalidCertificateRefs(msg),
			}
			continue
		}

		if block, _ := pem.Decode(certPEM); block == nil {
			msg := fmt.Sprintf("Secret %s/%s: certificate contains an invalid PEM", nsName.Namespace, nsName.Name)
			l.CheckSecret = CheckResult{
				Valid:      false,
				Conditions: conditions.NewListenerResolvedRefInvalidCertificateRefs(msg),
			}
			continue
		}

		if block, _ := pem.Decode(keyPEM); block == nil {
			msg := fmt.Sprintf("Secret %s/%s: key contains an invalid PEM", nsName.Namespace, nsName.Name)
			l.CheckSecret = CheckResult{
				Valid:      false,
				Conditions: conditions.NewListenerResolvedRefInvalidCertificateRefs(msg),
			}
			continue
		}
	}
}

// checkConflict checks if the listener has conflict with other listeners
// If it has, it set the listener condition type gatewayv1.ListenerConditionConflicted
// with the reason:
// - gatewayv1.ListenerReasonProtocolConflict
// - gatewayv1.ListenerReasonHostnameConflict
func (l *Listener) checkConflict(treeGw *Gateway, listenersPerPort map[gatewayv1.PortNumber]listenerConflict) {
	if !treeGw.Valid {
		l.CheckConflict = CheckResult{}
		return
	}
	listener := l.K8sResource
	listenersOnPort, ok := listenersPerPort[listener.Port]
	if !ok {
		// Should not happen
		return
	}

	conflictingKeys := make([]string, 0)
	lk := NewListenerKey(treeGw.K8sResource, listener)
	if _, ok := listenersOnPort[lk]; !ok {
		// should not happen
		return
	}

	// 1- No conflict for this listener, it's the winnier
	if !listenersOnPort[lk].hasConflict {
		// If there is no conflict for this listener
		l.CheckConflict = CheckResult{}
		return
	}

	// 2- Conflicts
	for glk := range listenersOnPort {
		// if the listener is itself, just continue
		if glk.String() == NewListenerKey(treeGw.K8sResource, listener).String() {
			continue
		}
		_, listenerName, err := ConvertListenerKeyToGatewayKeyAndListenerName(glk)
		if err != nil {
			listenerName = glk.Name
		}
		conflictingKeys = append(conflictingKeys, listenerName)
	}
	if len(conflictingKeys) > 0 {
		slices.Sort(conflictingKeys)
		msg := fmt.Sprintf("Conflicting listeners: %s", strings.Join(conflictingKeys, ", "))
		cond := conditions.NewListenerConflicted(msg, listenersOnPort[lk].reason)
		l.CheckConflict = CheckResult{
			Valid:      false,
			Conditions: cond,
		}
	}
}

// hostnameConflicts reports whether two listener hostnames conflict for the purpose
// of the Gateway API "HostnameConflict" condition.
//
// Per the Gateway API spec, two listeners conflict only when they have the exact
// same hostname value (case-insensitive). Different hostnames — even when one is a
// wildcard that would match the other — do NOT conflict, because specificity rules
// (exact > wildcard > empty) always produce an unambiguous winner for any request.
//
// Concretely:
//   - "" vs ""           → conflict  (two catch-all listeners, identical)
//   - "" vs "foo.com"    → no conflict  (catch-all coexists with specific)
//   - "*.ex.com" vs "foo.ex.com" → no conflict  (specific beats wildcard, unambiguous)
//   - "*.ex.com" vs "*.ex.com"   → conflict  (identical wildcards)
func hostnameConflicts(h1, h2 string) bool {
	return strings.EqualFold(strings.TrimSuffix(h1, "."), strings.TrimSuffix(h2, "."))
}

func (*Listener) isSupportedCertKindGroup(certRef gatewayv1.SecretObjectReference) bool {
	supportedKind := certRef.Kind == nil || *certRef.Kind == "Secret"
	supportedGroup := certRef.Group == nil || *certRef.Group == ""
	return supportedKind && supportedGroup
}

func (l *Listener) BuildConditions(treeGw *Gateway) {
	l.Conditions = make(generic.Conditions)
	l.Conditions.MergeOverrideConditions(l.CheckRouteGroupKind.Conditions)
	l.Conditions.MergeOverrideConditions(l.CheckProtocol.Conditions)
	l.Conditions.MergeOverrideConditions(l.CheckSecret.Conditions)
	l.Conditions.MergeOverrideConditions(l.CheckConflict.Conditions)
	// Should we process with Haproxy programmation
	shouldProgramm := true
	// isAccepted tracks whether the listener is accepted (routes can attach even with unresolved refs)
	isAccepted := true

	_, exists := l.Conditions.GetCondition(generic.ConditionType(gatewayv1.ListenerConditionAccepted))
	if !exists {
		// Accepted = OK
		l.Conditions.MergeOverrideConditions(conditions.NewListenerAcceptedOK())
	} else {
		l.Conditions.MergeOverrideConditions(conditions.NewListenerProgrammedInvalid())
		shouldProgramm = false
		isAccepted = false
	}

	_, exists = l.Conditions.GetCondition(generic.ConditionType(gatewayv1.ListenerConditionResolvedRefs))
	if !exists {
		// ResolvedRefs = OK
		l.Conditions.MergeOverrideConditions(conditions.NewListenerResolvedRefOK())
	} else {
		l.Conditions.MergeOverrideConditions(conditions.NewListenerProgrammedInvalid())
		shouldProgramm = false
		// ResolvedRefs=False does NOT prevent route attachment per the Gateway API spec
	}

	_, exists = l.Conditions.GetCondition(generic.ConditionType(gatewayv1.ListenerConditionConflicted))
	if exists {
		l.Conditions.MergeOverrideConditions(conditions.NewListenerProgrammedInvalid())
		shouldProgramm = false
		isAccepted = false
	}

	if shouldProgramm {
		// If the Gateway k8s resource already has a listener Programmed condition  Status!=Pending and the same
		// ObservedGeneration as the gateway, do not reset the status to Pending.
		// Probable cause is a startup phase where HAProxy is already programmed.
		existingProgrammed := false
		for _, listenerStatus := range treeGw.K8sResource.Status.Listeners {
			if listenerStatus.Name != l.K8sResource.Name {
				continue
			}
			for _, cond := range listenerStatus.Conditions {
				if cond.Type != string(gatewayv1.ListenerConditionProgrammed) {
					continue
				}
				if cond.Status == metav1.ConditionUnknown {
					// If the condition is in Unknown status, we consider that the listener is not programmed
					// and we set the condition to Pending to trigger a status update when the controller will process this listener.
					continue
				}

				if cond.ObservedGeneration == treeGw.K8sResource.GetGeneration() &&
					!cond.LastTransitionTime.Time.Before(treeGw.K8sResource.GetCreationTimestamp().Time) {
					l.Conditions[generic.ConditionType(gatewayv1.ListenerConditionProgrammed)] = generic.Condition{
						Type:               generic.ConditionType(gatewayv1.ListenerConditionProgrammed),
						Status:             cond.Status,
						Reason:             cond.Reason,
						Message:            cond.Message,
						ObservedGeneration: cond.ObservedGeneration,
					}
					existingProgrammed = true
				}
			}
		}
		if !existingProgrammed {
			l.Conditions.MergeOverrideConditions(conditions.NewListenerProgrammedPending())
		}
	}

	l.Valid = shouldProgramm
	l.Accepted = isAccepted
	l.Conditions.SetGeneration(treeGw.K8sResource.GetGeneration())
}

func (l *Listener) resetChecks() {
	l.CheckRouteGroupKind = CheckResult{}
	l.CheckProtocol = CheckResult{}
	l.CheckSecret = CheckResult{}
	l.CheckConflict = CheckResult{}
}

// NewListenerKey returns the Listener owner key appending the listener name to it
// For Gateway ns/gateway, if the Listener name is "https", will return
// ns/gateway_https
// = Listener Key
//
//	Listener Key = NamespaceName {
//	  Namespace : <gateway_ns>
//	  Name:     : <gateway-name>_<listener_name>
//	}
func NewListenerKey(gw *gatewayv1.Gateway, listener gatewayv1.Listener) client.ObjectKey {
	return ListenerKeyFromListenerName(gw, listener.Name)
}

func ListenerKeyFromListenerName(gw *gatewayv1.Gateway, listenerName gatewayv1.SectionName) client.ObjectKey {
	return client.ObjectKey{
		Namespace: gw.Namespace,
		Name: fmt.Sprintf("%s_%s",
			gw.Name,
			listenerName,
		),
	}
}

func (l Listener) Key() client.ObjectKey {
	return client.ObjectKey{
		Namespace: l.Owner.Namespace,
		Name: fmt.Sprintf("%s_%s",
			l.Owner.Name,
			l.K8sResource.Name,
		),
	}
}

// ConvertListenerKeyToGatewayKey converts a listener key back to a gateway key.
// It assumes the listener key is in the format "gateway-name_listener-name", built by the previous ListenerKey function.
// For a listener key with namespace "ns" and name "my-gateway_https",
// it returns a gateway key with namespace "ns" and name "my-gateway".
func ConvertListenerKeyToGatewayKey(listenerKey client.ObjectKey) client.ObjectKey {
	gatewayKey, _, err := ConvertListenerKeyToGatewayKeyAndListenerName(listenerKey)
	if err != nil {
		return listenerKey
	}
	return gatewayKey
}

// ConvertListenerKeyToGatewayKeyAndListenerName converts a listener key back to a gateway key and listener name.
// It assumes the listener key is in the format "gateway-name_listener-name", built by the ListenerKey function.
// For a listener key with namespace "ns" and name "my-gateway_https",
// it returns a gateway key with namespace "ns" and name "my-gateway", the listener name "https", and no error.
// If the format is invalid, it returns an error.
func ConvertListenerKeyToGatewayKeyAndListenerName(listenerKey client.ObjectKey) (client.ObjectKey, string, error) {
	parts := strings.Split(listenerKey.Name, "_")
	if len(parts) != 2 {
		return client.ObjectKey{}, "", fmt.Errorf("invalid listener key format: %s", listenerKey.Name)
	}
	gatewayKey := client.ObjectKey{
		Namespace: listenerKey.Namespace,
		Name:      parts[0],
	}
	return gatewayKey, parts[1], nil
}

func (l *Listener) addAttachedRoute(routeKey client.ObjectKey, controllerStore ControllerStore) {
	l.AttachedRoutes[routeKey] = struct{}{}

	// Find the corresponding Gateway and set it as upserted
	gwKey := l.Owner
	treeGw, ok := controllerStore.GateTree.Gateways[gwKey]
	if !ok || treeGw.TreeStatus.Status == store.StatusDeleted {
		// no action needed, Gateway is Deleted or not manager by our controller
		return
	}

	treeGw.TreeStatus.Status = store.StatusUpserted
	treeGw.TreeStatus.OldTreeResource = treeGw.DeepCopy()
}

func (l *Listener) deleteAttachedRoute(routeKey client.ObjectKey, controllerStore ControllerStore) {
	if l.AttachedRoutes == nil {
		return
	}
	delete(l.AttachedRoutes, routeKey)

	// Find the corresponding Gateway and set it as upserted
	gwKey := l.Owner
	treeGw, ok := controllerStore.GateTree.Gateways[gwKey]
	if !ok || treeGw.TreeStatus.Status == store.StatusDeleted {
		// no action needed, Gateway is Deleted or not manager by our controller
		return
	}

	treeGw.TreeStatus.Status = store.StatusUpserted
}
