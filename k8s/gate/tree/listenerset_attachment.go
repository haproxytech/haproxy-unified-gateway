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
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// listenerSetAttachment is what the contribution pass decided about one
// ListenerSet: whether its listeners entered the parent Gateway's merged list, and
// if not, whether the failure is ours to report.
//
// Three values rather than a boolean, because the two ways of failing to attach do
// not have the same consequence. A ListenerSet whose parentRef names a Gateway this
// controller does not manage is not ours to comment on -- writing a status would
// claim a resource aimed at another controller. A ListenerSet whose parent is ours
// but refuses the attachment must get a status, and saying nothing would breach the
// spec. A boolean would collapse the two into "did not attach".
//
// The zero value is the silent one on purpose: a ListenerSet that has not been
// through the pass writes no status, which is the outcome that cannot be wrong.
type listenerSetAttachment uint8

const (
	// attachmentUnreported: nothing is contributed and nothing is said. The cause is
	// either a parentRef naming no Gateway of this controller -- absent from the
	// store, of another GatewayClass, or not a Gateway at all -- or a ListenerSet
	// already deleted. Named after the consequence rather than either cause, because
	// the consequence is what the zero value has to guarantee.
	attachmentUnreported listenerSetAttachment = iota
	// attachmentNotAllowed: the parent is ours and does not allow this ListenerSet
	// to attach. No contribution; the parent's own listeners are untouched, and the
	// refusal is reported on the ListenerSet's own status.
	attachmentNotAllowed
	// attachmentAllowed: the listeners entered the parent's merged list, where they
	// are compared for conflicts and judged like any other.
	attachmentAllowed
)

// parentGatewayKey resolves the Gateway named by a ListenerSet's parentRef.
//
// The second result is false when the reference does not name a Gateway of the
// Gateway API group at all, which no amount of looking in the store would fix. An
// unset namespace means the ListenerSet's own, and an unset group or kind means the
// defaults the API stamps in, so both are read as Gateway references.
func parentGatewayKey(listenerSet *gatewayv1.ListenerSet) (client.ObjectKey, bool) {
	if listenerSet == nil {
		return client.ObjectKey{}, false
	}

	parentRef := listenerSet.Spec.ParentRef

	if parentRef.Group != nil && *parentRef.Group != gatewayv1.GroupName && *parentRef.Group != "" {
		return client.ObjectKey{}, false
	}
	if parentRef.Kind != nil && *parentRef.Kind != "Gateway" && *parentRef.Kind != "" {
		return client.ObjectKey{}, false
	}

	namespace := listenerSet.Namespace
	if parentRef.Namespace != nil && *parentRef.Namespace != "" {
		namespace = string(*parentRef.Namespace)
	}

	return client.ObjectKey{Namespace: namespace, Name: string(parentRef.Name)}, true
}

// attachmentOfListenerSet decides whether a ListenerSet attaches to the Gateway its
// parentRef names, and when it does not, whether this controller is the one that has
// to say so.
//
// A method on the ControllerStore because two passes need the same answer and must
// not derive it twice: the contribution pass, to decide which entries enter a
// Gateway's merged list, and the reference rebuild, to decide whose certificateRefs
// belong in the Secret index. Two derivations would eventually disagree, and the
// disagreement would show up as a listener bound to a port with no certificate.
//
// The parent is looked up in the cluster store, not in the tree: the callers run
// before the Gateway builder has created this cycle's tree Gateways, so a Gateway
// created in this batch would not be there yet. The GatewayClass test is the same
// predicate the Gateway builder uses to decide whether a Gateway is its business at
// all, so the two agree on what "ours" means.
//
// Deliberately not a test of the parent's validity: whether the Gateway is accepted
// is settled by the Gateway builder, after this. An entry on an invalid Gateway is
// already inert for the same reason the Gateway's own listeners are -- the checks and
// the frontend builder both gate on it -- so what is missing there is the status,
// which the reporting pass owns.
func (b *ControllerStore) attachmentOfListenerSet(listenerSet *gatewayv1.ListenerSet) listenerSetAttachment {
	if listenerSet == nil {
		return attachmentUnreported
	}

	gwKey, namesGateway := parentGatewayKey(listenerSet)
	if !namesGateway {
		return attachmentUnreported
	}

	parentGw, inStore := b.ClusterStore.Gateways[gwKey]
	if !inStore || parentGw == nil {
		return attachmentUnreported
	}
	if !b.CheckGatewayClassExists(string(parentGw.Spec.GatewayClassName)) {
		return attachmentUnreported
	}

	if !isListenerSetNamespaceAllowed(
		listenerSet.Namespace,
		parentGw.Spec.AllowedListeners,
		parentGw.Namespace,
		b.ClusterStore.Namespaces,
	) {
		return attachmentNotAllowed
	}

	return attachmentAllowed
}

// isListenerSetNamespaceAllowed reports whether a ListenerSet in
// listenerSetNamespace may attach to a Gateway in gatewayNamespace, per that
// Gateway's spec.allowedListeners.namespaces.
//
// The default is None -- no ListenerSet attaches -- and it is the default at three
// levels: an absent allowedListeners, an absent namespaces, and an absent from. The
// API states it twice and defaults the field to {from: None}, so a Gateway that has
// never heard of ListenerSets is not silently extensible by anyone who can create
// one in its namespace.
//
// Note the asymmetry with isRouteNamespaceAllowed, whose default is Same: a route
// only attaches to a listener that already exists, whereas a ListenerSet *adds*
// listeners, hence claims ports and hostnames on the Gateway's addresses. Do not
// "align" the two defaults.
func isListenerSetNamespaceAllowed(
	listenerSetNamespace string,
	allowedListeners *gatewayv1.AllowedListeners,
	gatewayNamespace string,
	namespaces map[types.NamespacedName]*v1.Namespace,
) bool {
	if allowedListeners == nil || allowedListeners.Namespaces == nil || allowedListeners.Namespaces.From == nil {
		return false
	}

	switch *allowedListeners.Namespaces.From {
	case gatewayv1.NamespacesFromAll:
		return true
	case gatewayv1.NamespacesFromSame:
		return listenerSetNamespace == gatewayNamespace
	case gatewayv1.NamespacesFromSelector:
		selector := allowedListeners.Namespaces.Selector
		if selector == nil {
			return false
		}
		labelSelector, err := metav1.LabelSelectorAsSelector(selector)
		if err != nil {
			return false
		}
		ns, ok := namespaces[types.NamespacedName{Name: listenerSetNamespace}]
		if !ok || ns == nil {
			return false
		}
		return labelSelector.Matches(labels.Set(ns.Labels))
	default:
		// None, and anything a future version of the API adds. Both deny: the field
		// decides who may add listeners to this Gateway, so a value this controller
		// does not understand must not be read as permission.
		return false
	}
}
