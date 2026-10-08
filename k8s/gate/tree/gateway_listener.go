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
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/protocols"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// The kinds that can declare a listener. They name the "from" side of a
// ReferenceGrant, so they are the API's spelling and not ours: a grant is matched
// on this string.
const (
	kindGateway     gatewayv1.Kind = "Gateway"
	kindListenerSet gatewayv1.Kind = "ListenerSet"
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
	// ListenerKey identifies this listener. Computed once at construction, and read
	// through Key.
	//
	// It has to be exported: DeepCopy round-trips the Listener through JSON, which
	// silently drops unexported fields -- and a copy with a zero key produces a
	// wrong origin prefix, so the map cleanup of the previous state would purge
	// nothing.
	ListenerKey listenerKey
	// OwnerKind is the kind of the resource that declares this listener, "Gateway"
	// or "ListenerSet". Object references the listener holds are resolved and
	// granted against that resource, so the kind is part of the question asked of a
	// ReferenceGrant, not a label.
	//
	// Stamped at construction, together with the key it completes: nothing in a
	// listener's own payload says which kind produced it once the source object is
	// read through the accessors, and the checks must not have to ask.
	OwnerKind gatewayv1.Kind
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
	// ExemptFromReferenceGrant lets this listener reach a Secret in another
	// namespace without a ReferenceGrant permitting it. It is set for the listeners
	// of the synthetic Gateway that serves Ingress resources, whose cross-namespace
	// references were already authorised by the Ingress they come from -- there is
	// no user-authored Gateway to write a grant against.
	//
	// Stamped rather than derived from the Gateway's synthetic name: that decision
	// is made once, where the synthetic Gateway is recognised, and re-deriving it
	// here would be a second place to keep in agreement with the first.
	ExemptFromReferenceGrant bool
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

// protocolCategory is the category the listener's protocol falls in -- what the
// HAProxy frontend serving its port has to be, and the scope a hostname claim is
// compared within.
//
// Blind to tls.mode, as the category table itself is: two listeners differing only
// by Terminate versus Passthrough share a category and are not reported as
// conflicting on protocol.
func (l *Listener) protocolCategory() protocols.ProtocolCategory {
	return protocols.ProtocolCategories[l.Protocol()]
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
//
// The port is read through the listener's own accessors, which know which resource
// declared it: a contributed entry holds no spec listener of any Gateway, so a port read
// straight off that field would decide nothing -- or panic.
func (l *Listener) MatchesParentRefPort(port *gatewayv1.PortNumber) bool {
	return port == nil || l.Port() == *port
}

type RouteGroupKind struct {
	// Group is the group of the RouteGroupKind.
	Group gatewayv1.Group `json:"group,omitempty"`
	// Kind is the kind of the RouteGroupKind.
	Kind gatewayv1.Kind `json:"kind"`
}

// managedRouteGroupKindsByProtocol maps a listener protocol to the Route kinds this
// controller manages on it -- what it can actually serve, as opposed to what a
// listener allows.
//
// It is passed to the checks rather than read from the package variable, so that a
// test can inject a different one without mutating shared state.
type managedRouteGroupKindsByProtocol map[gatewayv1.ProtocolType][]gatewayv1.RouteGroupKind

var gateManagedRouteGroupKindsByProtocol = managedRouteGroupKindsByProtocol{
	gatewayv1.HTTPProtocolType:  {objtypes.RouteKindHTTP},
	gatewayv1.HTTPSProtocolType: {objtypes.RouteKindHTTP},
	gatewayv1.TLSProtocolType:   {objtypes.RouteKindTLS},
}

// supports reports whether the protocol can carry any Route kind at all.
func (t managedRouteGroupKindsByProtocol) manages(protocol gatewayv1.ProtocolType) bool {
	return len(t[protocol]) > 0
}

// sortedFor returns the kinds managed on a protocol, as a sorted *copy*.
//
// The copy is not defensive verbosity: the slices are shared with the package
// variable, and slices.SortFunc sorts in place, so sorting the stored slice would
// permanently reorder the shared mapping for every listener and every subsequent
// cycle.
// Concentrating that in one place is what keeps the invariant from depending on
// each caller remembering it.
func (t managedRouteGroupKindsByProtocol) sortedFor(protocol gatewayv1.ProtocolType) []gatewayv1.RouteGroupKind {
	managed := t[protocol]
	sorted := make([]gatewayv1.RouteGroupKind, 0, len(managed))
	sorted = append(sorted, managed...)
	slices.SortStableFunc(sorted, sortRouteKinds)
	return sorted
}

// protocolNames returns the protocols this controller manages, sorted, for use in
// user-facing messages.
func (t managedRouteGroupKindsByProtocol) protocolNames() []string {
	names := make([]string, 0, len(t))
	for protocol := range t {
		names = append(names, string(protocol))
	}
	slices.Sort(names)
	return names
}

// partitionRouteKinds splits the Route kinds a listener allows into those this
// controller can serve on that protocol and those it cannot.
//
// A single pass produces both halves. They were computed twice before, by two
// loops with inverted tests: one keeping the managed kinds for
// status.supportedKinds, the other looking for an unsupported one to build a
// condition.
//
// kept is what status.supportedKinds must report -- the specification requires
// that "if both valid and invalid Route kinds are specified, the implementation
// MUST reference the valid Route kinds that have been specified". rejected is
// what the ResolvedRefs/InvalidRouteKinds message must name, all of them rather
// than an arbitrary one.
//
// Both halves are sorted so that the status field and the message are stable
// across cycles: supportedKinds is a listType=atomic, so an order that follows
// what the user happened to write would produce spurious status patches.
func partitionRouteKinds(
	protocol gatewayv1.ProtocolType,
	allowedRoutes *gatewayv1.AllowedRoutes,
	managedKinds managedRouteGroupKindsByProtocol,
) (kept, rejected []gatewayv1.RouteGroupKind) {
	// Without an explicit list, the listener allows whatever the protocol supports.
	if allowedRoutes == nil || len(allowedRoutes.Kinds) == 0 {
		return managedKinds.sortedFor(protocol), nil
	}

	managed := managedKinds[protocol]
	kept = make([]gatewayv1.RouteGroupKind, 0, len(allowedRoutes.Kinds))
	for _, allowedKind := range allowedRoutes.Kinds {
		if isManagedRouteGroupKind(allowedKind, managed) {
			kept = append(kept, allowedKind)
			continue
		}
		rejected = append(rejected, allowedKind)
	}

	slices.SortStableFunc(kept, sortRouteKinds)
	slices.SortStableFunc(rejected, sortRouteKinds)
	return kept, rejected
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

func isManagedRouteGroupKind(kind gatewayv1.RouteGroupKind, managedKinds []gatewayv1.RouteGroupKind) bool {
	if kind.Group != nil && *kind.Group != gatewayv1.GroupName {
		return false
	}
	for _, k := range managedKinds {
		if k.Kind == kind.Kind {
			return true
		}
	}

	return false
}

// checkRouteGroupKind rejects the listener when one of the Route kinds it allows
// cannot be served on its protocol.
//
// It takes the rejected half of the partition rather than recomputing it: the
// caller needs the managed half anyway, to record it as the listener's
// AllowedRouteKinds, and that must happen whatever the parent's state -- whereas
// the condition below only makes sense for a valid parent, which is why the caller
// does not reach this check otherwise.
func (l *Listener) checkRouteGroupKind(rejected []gatewayv1.RouteGroupKind, managedKinds managedRouteGroupKindsByProtocol) {
	if len(rejected) > 0 {
		// Name every rejected kind, group-qualified. The group matters: a kind is
		// most often rejected *because* of it, and rendering the kind alone
		// produces a message that contradicts itself, such as
		// "HTTPRoute is not supported. Supported Route Kinds ... [HTTPRoute]".
		msg := fmt.Sprintf(
			"%s not supported. Supported Route Kinds for this protocol %s",
			utils.RouteGroupKindsToString(rejected),
			utils.RouteGroupKindsToString(managedKinds.sortedFor(l.Protocol())),
		)
		l.CheckRouteGroupKind = CheckResult{
			Valid:      false,
			Conditions: l.conditionsFor(conditions.VerdictInvalidRouteKinds, msg),
		}
		return
	}

	l.CheckRouteGroupKind = CheckResult{
		Valid: true,
	}
}

func (l *Listener) checkProtocol(managedKinds managedRouteGroupKindsByProtocol) {
	// UnsupportedProtocol, then UnsupportedValue: the protocol is served, but not in
	// the TLS mode asked for. The API spells those two apart, and so does the
	// Gateway vocabulary -- an entry, which has no word for the second, is handled
	// where its vocabulary is picked.
	if !l.checkManagedProtocol(managedKinds) {
		return
	}

	// Allowlist, not denylist: only Passthrough is served on a TLS listener, so a mode
	// this controller does not know -- one added to the API later -- is rejected here
	// rather than admitted by default.
	tls := l.TLS()
	if l.Protocol() == gatewayv1.TLSProtocolType &&
		(tls == nil || tls.Mode == nil || *tls.Mode != gatewayv1.TLSModePassthrough) {
		valErr := field.NotSupported(
			field.NewPath("protocol"),
			l.Protocol(),
			[]string{"TLS/Passthrough"},
		)
		l.CheckProtocol = CheckResult{
			Valid:      false,
			Conditions: l.conditionsFor(conditions.VerdictUnsupportedValue, valErr.Error()),
		}
		return
	}

	l.CheckProtocol = CheckResult{
		Valid: true,
	}
}

// checkManagedProtocol reports whether the listener protocol is one this controller
// manages. When it is not, it records the condition on CheckProtocol and returns
// false so the caller stops and keeps that invalid verdict.
//
// "Managed" is our term; the condition it produces uses the Gateway API's, whose
// reason for this case is UnsupportedProtocol.
func (l *Listener) checkManagedProtocol(managedKinds managedRouteGroupKindsByProtocol) bool {
	if !managedKinds.manages(l.Protocol()) {
		managedProtocols := managedKinds.protocolNames()

		valErr := field.NotSupported(
			field.NewPath("protocol"),
			l.Protocol(),
			managedProtocols,
		)
		l.CheckProtocol = CheckResult{
			Valid:      false,
			Conditions: l.conditionsFor(conditions.VerdictUnsupportedProtocol, valErr.Error()),
		}
		return false
	}
	return true
}

func (l *Listener) checkCertificateRefs(gateSecrets map[types.NamespacedName]*Secret, referenceGrantManager *ReferenceGrantManager) {
	tls := l.TLS()

	// Check if we miss the TLS section for HTTPS protocol
	if l.Protocol() == gatewayv1.HTTPSProtocolType {
		if tls == nil || len(tls.CertificateRefs) == 0 {
			msg := fmt.Sprintf("listener %s has protocol HTTPS but no TLS section", l.Name())
			l.CheckSecret = CheckResult{
				Valid:      false,
				Conditions: l.conditionsFor(conditions.VerdictInvalidCertificateRef, msg),
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
				Conditions: l.conditionsFor(conditions.VerdictInvalidCertificateRef, msg),
			}
			break
		}

		nsName := l.certificateRefKey(certRef)
		treeSecret, ok := gateSecrets[nsName]
		if !ok || treeSecret.TreeStatus.Status == store.StatusDeleted {
			msg := fmt.Sprintf("Secret %s/%s does not exist", nsName.Namespace, nsName.Name)
			l.CheckSecret = CheckResult{
				Valid:      false,
				Conditions: l.conditionsFor(conditions.VerdictInvalidCertificateRef, msg),
			}
			continue
		}
		isAccessGranted := l.ExemptFromReferenceGrant || referenceGrantManager.IsAccessGranted(
			l.referenceGrantFrom(),
			GrantTo{Group: "", Kind: "Secret", Namespace: treeSecret.K8sResource.GetNamespace(), Name: treeSecret.K8sResource.GetName()},
		)
		if !isAccessGranted {
			msg := fmt.Sprintf("Secret reference %s/%s is not granted for listener in %s", nsName.Namespace, nsName.Name, l.ownerDescription())
			l.CheckSecret = CheckResult{
				Valid:      false,
				Conditions: l.conditionsFor(conditions.VerdictRefNotPermitted, msg),
			}
			continue
		}
		certPEM := treeSecret.K8sResource.Data[v1.TLSCertKey]
		keyPEM := treeSecret.K8sResource.Data[v1.TLSPrivateKeyKey]

		if bytes.Equal(certPEM, keyPEM) {
			msg := fmt.Sprintf("Secret %s/%s: certificate and key are identical", nsName.Namespace, nsName.Name)
			l.CheckSecret = CheckResult{
				Valid:      false,
				Conditions: l.conditionsFor(conditions.VerdictInvalidCertificateRef, msg),
			}
			continue
		}

		if block, _ := pem.Decode(certPEM); block == nil {
			msg := fmt.Sprintf("Secret %s/%s: certificate contains an invalid PEM", nsName.Namespace, nsName.Name)
			l.CheckSecret = CheckResult{
				Valid:      false,
				Conditions: l.conditionsFor(conditions.VerdictInvalidCertificateRef, msg),
			}
			continue
		}

		if block, _ := pem.Decode(keyPEM); block == nil {
			msg := fmt.Sprintf("Secret %s/%s: key contains an invalid PEM", nsName.Namespace, nsName.Name)
			l.CheckSecret = CheckResult{
				Valid:      false,
				Conditions: l.conditionsFor(conditions.VerdictInvalidCertificateRef, msg),
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
		Conditions: l.conditionsFor(conditions.VerdictPortNotBindable, msg),
	}
}

// checkConflict checks if the listener has conflict with other listeners
// If it has, it set the listener condition type gatewayv1.ListenerConditionConflicted
// with the reason:
// - gatewayv1.ListenerReasonProtocolConflict
// - gatewayv1.ListenerReasonHostnameConflict
func (l *Listener) checkConflict(conflictsByPort map[gatewayv1.PortNumber]conflictsByListener) {
	conflictsOnThisPort, ok := conflictsByPort[l.Port()]
	if !ok {
		// Should not happen
		return
	}

	conflicts, ok := conflictsOnThisPort[l.Key()]
	if !ok {
		// should not happen
		return
	}

	// 1- No conflict for this listener, it's the winnier
	if !conflicts.any() {
		l.CheckConflict = CheckResult{}
		return
	}

	// 2- Conflicts: name the other listeners sharing the port, which is the part of
	// the message a user can act on.
	peerNames := make([]string, 0, len(conflictsOnThisPort)-1)
	for peerKey := range conflictsOnThisPort {
		if peerKey == l.Key() {
			continue
		}
		peerNames = append(peerNames, string(peerKey.ListenerName))
	}
	if len(peerNames) > 0 {
		slices.Sort(peerNames)
		msg := fmt.Sprintf("Conflicting listeners: %s", strings.Join(peerNames, ", "))
		// Only when there is more than one kind, so no existing message changes. The
		// reason can name a single kind; it has no word for two on a Gateway listener,
		// and says "several" without saying which on an entry.
		if kinds := conflicts.names(); len(kinds) > 1 {
			msg += fmt.Sprintf(" (conflicts on %s)", strings.Join(kinds, " and "))
		}
		l.CheckConflict = CheckResult{
			Valid:      false,
			Conditions: l.conditionsFor(l.conflictVerdict(conflicts), msg),
		}
	}
}

func (*Listener) isSupportedCertKindGroup(certRef gatewayv1.SecretObjectReference) bool {
	supportedKind := certRef.Kind == nil || *certRef.Kind == "Secret"
	supportedGroup := certRef.Group == nil || *certRef.Group == ""
	return supportedKind && supportedGroup
}

// BuildConditions settles the final conditions of a listener. previousStatus is what the
// resource declaring it published about it last time -- the Gateway for a listener of
// its own spec, the ListenerSet for an entry -- since that is where the condition it
// preserves lives and what its observedGeneration must match.
func (l *Listener) BuildConditions(treeGw *Gateway, previousStatus listenerPreviousStatus) {
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
		l.Conditions.MergeOverrideConditions(l.conditionsFor(conditions.VerdictAccepted, ""))
	} else {
		l.Conditions.MergeOverrideConditions(l.conditionsFor(conditions.VerdictNotProgrammable, ""))
		shouldProgramm = false
		isAccepted = false
	}

	_, exists = l.Conditions.GetCondition(generic.ConditionType(gatewayv1.ListenerConditionResolvedRefs))
	if !exists {
		// ResolvedRefs = OK
		l.Conditions.MergeOverrideConditions(l.conditionsFor(conditions.VerdictRefsResolved, ""))
	} else {
		l.Conditions.MergeOverrideConditions(l.conditionsFor(conditions.VerdictNotProgrammable, ""))
		shouldProgramm = false
		// ResolvedRefs=False does NOT prevent route attachment per the Gateway API spec
	}

	_, exists = l.Conditions.GetCondition(generic.ConditionType(gatewayv1.ListenerConditionConflicted))
	if exists {
		l.Conditions.MergeOverrideConditions(l.conditionsFor(conditions.VerdictNotProgrammable, ""))
		shouldProgramm = false
		isAccepted = false
	}

	if shouldProgramm {
		// Preserve the Programmed condition the declaring resource already published
		// rather than resetting the listener to Pending: at startup HAProxy may already
		// be programmed, and both the reported status and the frontend builders treat
		// Unknown as a change, so a spurious Pending costs a status round-trip and a
		// redundant apply.
		if preserved, ok := previousStatus.freshProgrammed(); ok {
			l.Conditions[l.programmedConditionType()] = preserved
		} else {
			l.Conditions.MergeOverrideConditions(l.conditionsFor(conditions.VerdictProgrammingPending, ""))
		}
	}

	l.Valid = shouldProgramm
	l.Accepted = isAccepted
	// Still the Gateway's generation, even for an entry. The HAProxy feedback path
	// compares what it wrote against this value before overwriting it, and it still
	// reports per Gateway, so stamping an entry with its ListenerSet's generation here
	// would make its feedback look permanently stale. Both sides move together when
	// the feedback learns to report per origin.
	l.Conditions.SetGeneration(treeGw.K8sResource.GetGeneration())
}

// listenerPreviousStatus is what the resource declaring a listener published about it
// last time: the Programmed condition it carries in its own status.listeners, and the
// identity of that resource -- its generation and creation timestamp -- which is what
// makes the condition current or stale.
//
// Resolved from the declaring resource, never from the Gateway the listener is served
// on. A listener name is unique only within the resource that declares it, and the API
// explicitly allows an entry to share a name with a Gateway listener, so looking an
// entry up in its parent Gateway's status finds a homonym: a condition about a
// different object, adopted as though it described this one.
type listenerPreviousStatus struct {
	creationTimestamp metav1.Time
	programmed        metav1.Condition
	generation        int64
	hasProgrammed     bool
}

// freshProgrammed returns the Programmed condition to preserve instead of resetting the
// listener to Pending, when the declaring resource published one that still describes
// it.
//
// "Still describes it" is the observedGeneration contract: stamped with the resource's
// current generation, and written after that resource was created. Anything older is
// discarded -- the resource moved, so the condition may no longer hold -- and the caller
// re-derives Pending.
func (p listenerPreviousStatus) freshProgrammed() (generic.Condition, bool) {
	if !p.hasProgrammed {
		return generic.Condition{}, false
	}
	if p.programmed.Status == metav1.ConditionUnknown {
		// Unknown is not programmed: the caller re-derives Pending for it.
		return generic.Condition{}, false
	}
	if p.programmed.ObservedGeneration != p.generation {
		return generic.Condition{}, false
	}
	if p.programmed.LastTransitionTime.Time.Before(p.creationTimestamp.Time) {
		return generic.Condition{}, false
	}
	return generic.Condition{
		Type:               generic.ConditionType(p.programmed.Type),
		Status:             p.programmed.Status,
		Reason:             p.programmed.Reason,
		Message:            p.programmed.Message,
		ObservedGeneration: p.programmed.ObservedGeneration,
	}, true
}

// programmedConditionType is the Programmed condition type in the vocabulary this
// listener is reported in. The two vocabularies hold the same value today, so the
// branch is written out rather than inferred: a condition type is a bare string once
// rendered, and reading an entry's through the Gateway's would keep working until the
// day the two diverge.
func (l *Listener) programmedConditionType() generic.ConditionType {
	if l.FromListenerSet() {
		return generic.ConditionType(gatewayv1.ListenerEntryConditionProgrammed)
	}
	return generic.ConditionType(gatewayv1.ListenerConditionProgrammed)
}

func (l *Listener) resetChecks() {
	l.CheckRouteGroupKind = CheckResult{}
	l.CheckProtocol = CheckResult{}
	l.CheckSecret = CheckResult{}
	l.CheckConflict = CheckResult{}
	l.CheckPort = CheckResult{}
}

// Key returns the identity of this listener. It is stored rather than recomputed:
// the conflict table is keyed by it, and a second code path rebuilding it would
// only have to disagree once for a listener to silently get no verdict at all.
func (l *Listener) Key() listenerKey {
	return l.ListenerKey
}

// conflictVerdict is the conflict this listener reports for the kinds it is in.
//
// It lives here, and not on conflictKinds, because the answer to "in both" depends
// on the vocabulary and the set of kinds does not know it. The Gateway API gives a
// ListenerSet entry the reason ListenerConflict for exactly that case, and gives a
// Gateway's own listeners no word for it -- for Conflicted it declares only
// HostnameConflict and ProtocolConflict.
//
// So a Gateway listener in both reports the protocol conflict, the coarser of the
// two: it condemns the port for that listener whatever hostname it asks for. That
// choice hides the other conflict rather than ranking it, which is why the message
// names every kind whenever there is more than one -- otherwise a user would fix
// the protocol and discover a hostname conflict nothing had mentioned.
func (l *Listener) conflictVerdict(conflicts conflictKinds) conditions.ListenerConditionVerdict {
	switch {
	case conflicts.has(conflictOnProtocol) && conflicts.has(conflictOnHostname):
		if l.FromListenerSet() {
			return conditions.VerdictSeveralConflicts
		}
		return conditions.VerdictProtocolConflict
	case conflicts.has(conflictOnProtocol):
		return conditions.VerdictProtocolConflict
	case conflicts.has(conflictOnHostname):
		return conditions.VerdictHostnameConflict
	}
	return ""
}

// conditionsFor renders a verdict in the vocabulary of whatever declared this
// listener. Every check goes through it, so it is the single place where the
// choice of vocabulary is made: a contributed entry is reported with the
// ListenerEntry constants, a listener the Gateway declares with the Gateway ones.
//
// A verdict the vocabulary cannot spell is a programming error -- the tables are
// total over the verdicts, which a test enforces -- and it must not be answered
// with an empty condition set. That would be read as "this check has nothing to
// report", and for a conflicting listener the consequence is not a missing line in
// a status: no Conflicted condition means Accepted stays true and Valid stays set,
// so the HAProxy feedback marks the listener Programmed while the virtual-listener
// builder, which excludes it from the conflict table, never put it in a frontend.
// A listener reported as programmed that serves nothing.
//
// So the fallback rejects the listener and carries the gap in its message, where a
// user reading the resource can see it. It is deliberately not silent, and
// deliberately not a panic: an internal inconsistency about how to word a status
// is not a reason to stop serving traffic.
func (l *Listener) conditionsFor(verdict conditions.ListenerConditionVerdict, msg string) generic.Conditions {
	rendered, err := l.conditionsInOwnVocabulary(verdict, msg)
	if err == nil {
		return rendered
	}

	rejected, fallbackErr := l.conditionsInOwnVocabulary(
		conditions.VerdictUnreportable,
		fmt.Sprintf("listener %s cannot be reported: %s", l.Key(), err),
	)
	if fallbackErr != nil {
		// Unreachable unless the table lost VerdictUnreportable too, in which case there
		// is nothing left to report with.
		return generic.Conditions{}
	}
	return rejected
}

// conditionsInOwnVocabulary is the dispatch by origin, and the only place the two
// listener vocabularies are told apart. The two families hold the same values
// today, which is exactly why the branch must be explicit here rather than
// inherited by accident at every call site: Reason is a bare string once rendered,
// so a mix-up is invisible afterwards.
func (l *Listener) conditionsInOwnVocabulary(verdict conditions.ListenerConditionVerdict, msg string) (generic.Conditions, error) {
	if l.FromListenerSet() {
		return conditions.NewListenerEntryConditions(verdict, msg)
	}
	return conditions.NewGatewayListenerConditions(verdict, msg)
}

// programmedCondition returns this listener's Programmed condition, read in
// whichever vocabulary the listener is reported in.
func (l *Listener) programmedCondition() (generic.Condition, bool) {
	return l.Conditions.GetCondition(l.programmedConditionType())
}

// referenceGrantFrom is the "from" side of the ReferenceGrant question this
// listener's object references ask: which document is reaching out.
//
// GEP-1713 is explicit that a ReferenceGrant naming a Gateway does not cover the
// ListenerSets attached to it, nor the other way round. A hard-coded "Gateway"
// here would therefore let a grant written for the Gateway authorise a
// ListenerSet's reference into another namespace, and ignore the grant that was
// correctly written for that ListenerSet.
func (l *Listener) referenceGrantFrom() GrantFrom {
	return GrantFrom{
		Group:     gatewayv1.GroupName,
		Kind:      string(l.OwnerKind),
		Namespace: l.Key().OwnerKey().Namespace,
	}
}

// ownerDescription names the resource that declares this listener, for the
// condition messages that have to say where a rejected reference was written.
func (l *Listener) ownerDescription() string {
	return strings.ToLower(string(l.OwnerKind)) + " " + l.Key().OwnerKey().String()
}

// certificateRefKey resolves one of this listener's certificateRefs, in the
// namespace of the resource that declares the listener when the reference names
// none.
func (l *Listener) certificateRefKey(certRef gatewayv1.SecretObjectReference) types.NamespacedName {
	return GetCertificateRefNamespacedName(certRef, l.Key().OwnerKey().Namespace)
}

func (l *Listener) addAttachedRoute(routeKey client.ObjectKey, controllerStore ControllerStore) {
	l.AttachedRoutes[routeKey] = struct{}{}

	// Find the corresponding Gateway and set it as upserted
	treeGw, ok := controllerStore.GateTree.Gateways[l.ListenerKey.GatewayKey]
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
	treeGw, ok := controllerStore.GateTree.Gateways[l.ListenerKey.GatewayKey]
	if !ok || treeGw.TreeStatus.Status == store.StatusDeleted {
		// no action needed, Gateway is Deleted or not manager by our controller
		return
	}

	treeGw.TreeStatus.Status = store.StatusUpserted
}
