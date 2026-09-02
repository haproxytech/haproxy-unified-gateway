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

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/caps"
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

// Listener is a listener of the merged list of a Gateway: either one declared in
// the Gateway's own spec, or one contributed by an attached ListenerSet.
//
// The source object is kept as-is rather than converted, in one of two mutually
// exclusive fields. gatewayv1.Listener and gatewayv1.ListenerEntry happen to have
// identical fields today, so a conversion would compile — but relying on that
// coincidence would silently absorb any future divergence, and the two types
// already have distinct condition vocabularies. Which field is set also *is* the
// origin of the listener: there is no separate flag that could drift away from
// the payload.
//
// Read the shared attributes through the accessors (Name, Port, Protocol,
// Hostname, TLS, AllowedRoutes) so that the branch exists once per attribute
// rather than once per call site.
//
// The source object and everything it points at are borrowed from the store and
// must be treated as read-only, like everything else the tree holds: the store
// owns the per-event deep copy the controller-runtime cache returns on Get, and
// several tree nodes alias it.
type Listener struct {
	// K8sGatewayListener is the source resource when the listener comes from the
	// Gateway's own spec. Exactly one of K8sGatewayListener and
	// K8sListenerSetListenerEntry is set.
	K8sGatewayListener *gatewayv1.Listener
	// K8sListenerSetListenerEntry is the source resource when the listener is
	// contributed by a ListenerSet.
	K8sListenerSetListenerEntry *gatewayv1.ListenerEntry
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
	CheckPort           CheckResult
	// AllowedRouteKinds is the list of allowed route kinds for this listener.
	AllowedRouteKinds []gatewayv1.RouteGroupKind
	// Valid indicates the listener is fully programmed (Accepted, no conflicts, all refs resolved).
	Valid bool
	// Accepted indicates the listener configuration is accepted (valid protocol, no conflicts).
	// Routes can attach to an accepted listener even if refs are unresolved.
	Accepted bool
}

// FromListenerSet reports whether this listener was contributed by a ListenerSet
// rather than declared in the Gateway's own spec.
func (l *Listener) FromListenerSet() bool {
	return l != nil && l.K8sListenerSetListenerEntry != nil
}

// The accessors below are nil-safe and return the zero value when no source
// object is attached. The field they replaced was a value rather than a pointer,
// so reading it from a zero-value Listener could not panic; keeping that property
// avoids turning a construction mistake into a crash of the whole reconcile loop.

// Name returns the listener name. It is unique within the resource that declares
// it, but NOT across a Gateway and its ListenerSets: the Gateway API explicitly
// allows the same name on both sides, and on sibling ListenerSets.
func (l *Listener) Name() gatewayv1.SectionName {
	switch {
	case l.FromListenerSet():
		return l.K8sListenerSetListenerEntry.Name
	case l != nil && l.K8sGatewayListener != nil:
		return l.K8sGatewayListener.Name
	}
	return ""
}

// Port returns the listener port.
func (l *Listener) Port() gatewayv1.PortNumber {
	switch {
	case l.FromListenerSet():
		return l.K8sListenerSetListenerEntry.Port
	case l != nil && l.K8sGatewayListener != nil:
		return l.K8sGatewayListener.Port
	}
	return 0
}

// Protocol returns the listener protocol.
func (l *Listener) Protocol() gatewayv1.ProtocolType {
	switch {
	case l.FromListenerSet():
		return l.K8sListenerSetListenerEntry.Protocol
	case l != nil && l.K8sGatewayListener != nil:
		return l.K8sGatewayListener.Protocol
	}
	return ""
}

// Hostname returns the listener hostname, nil when unset. An unset hostname is a
// catch-all, and it is a value like any other for conflict purposes: two
// catch-all listeners on the same port are indistinguishable.
func (l *Listener) Hostname() *gatewayv1.Hostname {
	switch {
	case l.FromListenerSet():
		return l.K8sListenerSetListenerEntry.Hostname
	case l != nil && l.K8sGatewayListener != nil:
		return l.K8sGatewayListener.Hostname
	}
	return nil
}

// TLS returns the listener TLS configuration, nil when unset. Note that an HTTPS
// listener without a TLS section passes the CRD validation, so nil must be
// handled rather than assumed away.
func (l *Listener) TLS() *gatewayv1.ListenerTLSConfig {
	switch {
	case l.FromListenerSet():
		return l.K8sListenerSetListenerEntry.TLS
	case l != nil && l.K8sGatewayListener != nil:
		return l.K8sGatewayListener.TLS
	}
	return nil
}

// AllowedRoutes returns the listener route-attachment policy, nil when unset.
func (l *Listener) AllowedRoutes() *gatewayv1.AllowedRoutes {
	switch {
	case l.FromListenerSet():
		return l.K8sListenerSetListenerEntry.AllowedRoutes
	case l != nil && l.K8sGatewayListener != nil:
		return l.K8sGatewayListener.AllowedRoutes
	}
	return nil
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

// MatchesParentRefPort reports whether a parentRef port, when set, selects this listener.
func (l *Listener) MatchesParentRefPort(port *gatewayv1.PortNumber) bool {
	return port == nil || l.K8sResource.Port == *port
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

	allowedRoutes := l.AllowedRoutes()
	gateSupportedRouteKindsForProtocol := gateSupportedRouteKinds[l.Protocol()]

	// If the listener does not have allowedRoutes.Kinds defined, then list of supported routes types only depends
	// on the listener protocol and is what is supported by the gateway.
	if allowedRoutes == nil || len(allowedRoutes.Kinds) == 0 {
		l.CheckRouteGroupKind = CheckResult{
			Valid: true,
		}
		return
	}

	// If the listener has some allowedRoutes.Kinds, we take the intersection
	// of what the listener allows and what the gateway supports for the protocol.
	unsupportedRoute := ""
	{
		for _, listenerAllowedKind := range allowedRoutes.Kinds {
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
	// UnsupportedProtocol
	if !l.checkSupportedProtocol(gateSupportedRouteKinds) {
		return
	}

	tls := l.TLS()
	if l.Protocol() == gatewayv1.TLSProtocolType &&
		tls != nil &&
		tls.Mode != nil &&
		*tls.Mode == gatewayv1.TLSModeTerminate {
		valErr := field.NotSupported(
			field.NewPath("protocol"),
			l.Protocol(),
			[]string{"TLS/Passthrough"},
		)
		l.CheckProtocol = CheckResult{
			Valid:      false,
			Conditions: conditions.NewListenerAcceptedUnsupportedValue(valErr.Error()),
		}
		return
	}

	l.CheckProtocol = CheckResult{
		Valid: true,
	}
}

// checkSupportedProtocol reports whether the listener protocol is supported. When
// it is not, it records the UnsupportedProtocol condition on CheckProtocol and
// returns false so the caller stops and keeps that invalid verdict.
func (l *Listener) checkSupportedProtocol(gateSupportedRouteKinds map[gatewayv1.ProtocolType][]gatewayv1.RouteGroupKind) bool {
	gateSupportedRouteKindsForProtocol := gateSupportedRouteKinds[l.Protocol()]
	if len(gateSupportedRouteKindsForProtocol) == 0 {
		supportedProtocols := make([]string, 0, len(gateSupportedRouteKinds))

		for protocol := range gateSupportedRouteKinds {
			supportedProtocols = append(supportedProtocols, string(protocol))
		}
		slices.Sort(supportedProtocols)

		valErr := field.NotSupported(
			field.NewPath("protocol"),
			l.Protocol(),
			supportedProtocols,
		)
		l.CheckProtocol = CheckResult{
			Valid:      false,
			Conditions: conditions.NewListenerAcceptedUnsupportedProtocol(valErr.Error()),
		}
		return false
	}
	return true
}

func (l *Listener) checkCertificateRefs(treeGw *Gateway, gateSecrets map[types.NamespacedName]*Secret, referenceGrantManager *ReferenceGrantManager) {
	if !treeGw.Valid {
		l.CheckSecret = CheckResult{}
		return
	}

	tls := l.TLS()

	// Check if we miss the TLS section for HTTPS protocol
	if l.Protocol() == gatewayv1.HTTPSProtocolType {
		if tls == nil || len(tls.CertificateRefs) == 0 {
			msg := fmt.Sprintf("listener %s has protocol HTTPS but no TLS section", l.Name())
			l.CheckSecret = CheckResult{
				Valid:      false,
				Conditions: conditions.NewListenerResolvedRefInvalidCertificateRefs(msg),
			}
			return
		}
	}

	// A listener of another protocol has no TLS section to check. The caller only
	// reaches this check for HTTPS today, and the HTTPS case above already returned
	// on a missing section, so this is a safety net rather than a live branch.
	if tls == nil {
		return
	}

	// Check is the secret exists
	// We only accept Secret as CertificateRefs
	// We only accept v1.Secret
	for _, certRef := range tls.CertificateRefs {
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
		isAccessGranted := treeGw.GatewayForIngress || referenceGrantManager.IsAccessGranted(
			GrantFrom{Group: gatewayv1.GroupName, Kind: "Gateway", Namespace: treeGw.K8sResource.GetNamespace()},
			GrantTo{Group: "", Kind: "Secret", Namespace: treeSecret.K8sResource.GetNamespace(), Name: treeSecret.K8sResource.GetName()},
		)
		if !isAccessGranted {
			msg := fmt.Sprintf("Secret reference %s/%s is not granted for listener in gateway %s/%s", nsName.Namespace, nsName.Name, treeGw.K8sResource.GetNamespace(), treeGw.K8sResource.GetName())
			l.CheckSecret = CheckResult{
				Valid:      false,
				Conditions: conditions.NewListenerResolvedRefListenerRefNotPermitted(msg),
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

// checkPort rejects the listener when the controller process cannot bind to
// listener.Port (e.g. privileged port without CAP_NET_BIND_SERVICE). Sets
// Accepted=False so routes don't silently attach to a listener that will
// never serve traffic; Programmed=Invalid is derived by BuildConditions.
//
// Skipped when an earlier check already failed, so the user-visible Reason
// reflects the first cause (e.g. UnsupportedProtocol) rather than being
// overwritten by Invalid from this check during the condition merge.
func (l *Listener) checkPort(portBinder caps.PortBinder) {
	if portBinder == nil {
		return
	}
	if len(l.CheckRouteGroupKind.Conditions) > 0 ||
		len(l.CheckProtocol.Conditions) > 0 ||
		len(l.CheckSecret.Conditions) > 0 ||
		len(l.CheckConflict.Conditions) > 0 {
		return
	}
	port := uint16(l.Port())
	if portBinder.CanBind(port) {
		return
	}
	msg := fmt.Sprintf(
		"cannot bind to port %d: process lacks CAP_NET_BIND_SERVICE and net.ipv4.ip_unprivileged_port_start=%d. Use a port >= %d, grant the capability, or lower the sysctl.",
		port, portBinder.MinUnprivilegedPort(), portBinder.MinUnprivilegedPort(),
	)
	l.CheckPort = CheckResult{
		Valid:      false,
		Conditions: conditions.NewListenerAcceptedPortPermissionDenied(msg),
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
	listenersOnPort, ok := listenersPerPort[l.Port()]
	if !ok {
		// Should not happen
		return
	}

	conflictingKeys := make([]string, 0)
	lk := ListenerKeyFromListenerName(treeGw.K8sResource, l.Name())
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
		if glk == lk {
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
	l.Conditions.MergeOverrideConditions(l.CheckPort.Conditions)
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
			if listenerStatus.Name != l.Name() {
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
	l.CheckPort = CheckResult{}
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
		Name: fmt.Sprintf(
			"%s_%s",
			gw.Name,
			listenerName,
		),
	}
}

func (l Listener) Key() client.ObjectKey {
	return client.ObjectKey{
		Namespace: l.Owner.Namespace,
		Name: fmt.Sprintf(
			"%s_%s",
			l.Owner.Name,
			l.Name(),
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
