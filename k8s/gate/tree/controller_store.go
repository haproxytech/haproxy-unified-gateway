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
	"log/slog"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/caps"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/certificate"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/protocols"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	utilsk8s "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils-k8s"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type ControllerStore struct {
	ClusterStore      *store.ClusterStore
	GateTree          *GateTree
	UnmanagedGateTree *GateTree
	ReferencedObjects *ReferencedObjects
	// A Map of installed GwApi CRDs versions
	InstalledGwAPIVersions *InstalledVersions
	Logger                 *slog.Logger
	ExtractGVK             utilsk8s.ExtractGVK
	CertUpdates            *CertUpdates
	CrtListUpdates         *CrtListUpdates
	// ReferenceGrantManager is the shared, per-cycle-refreshed grant index. It is
	// set once by handler.NewGateTreeBuilder and read by consumers that need to
	// validate cross-namespace references outside the tree builders (e.g. the
	// Service-level Backend CR merge in the HAProxy config manager).
	ReferenceGrantManager *ReferenceGrantManager
	// PortBinder reports whether the controller process can bind to a given
	// port — derived from CAP_NET_BIND_SERVICE and the netns sysctl.
	PortBinder caps.PortBinder
	// protocolCategoryByPort is the category a port serves, fixed by the first
	// listener that claims it. It is a property of the port and not of any one
	// listener: every listener on the port either matches it or conflicts on
	// protocol. The VirtualListener, hence the HAProxy frontend, takes its category
	// from here.
	protocolCategoryByPort map[gatewayv1.PortNumber]protocols.ProtocolCategory
	// contributedListenersByGatewayKey holds, per parent Gateway, the listeners the
	// ListenerSets allowed to attach to it contribute, already in the precedence
	// order the API prescribes: ListenerSets oldest first, then alphabetically by
	// namespace/name, and within one ListenerSet its entries in spec order.
	//
	// It exists because the two halves run in different phases. The contribution pass
	// reads only the cluster store, and runs before the Gateway builder has created
	// or reset this cycle's tree Gateways, so it cannot append to a merged list that
	// does not exist yet. It leaves the entries here instead, and the Gateway builder
	// appends them after a Gateway's own listeners -- one writer for the merged list,
	// which is what keeps the precedence order stated in a single place.
	contributedListenersByGatewayKey map[client.ObjectKey][]*Listener
	// previousContributorsByGatewayKey holds, per parent Gateway, the ListenerSets that
	// contributed to it in the previous cycle, in the same order.
	//
	// It exists to tell an attachment that *changed* from one that was simply
	// re-derived: the set of contributors is rebuilt from the whole tree every cycle,
	// so its contents say nothing about what moved. Diffing it against the previous
	// cycle is what finds the Gateways whose merged list is now wrong -- a ListenerSet
	// that changed parent, one whose namespace labels no longer match the Gateway's
	// selector, one that was deleted -- none of which is necessarily a ListenerSet
	// event the parent could be enqueued from.
	previousContributorsByGatewayKey map[client.ObjectKey][]client.ObjectKey
	// conflictsByPort holds, for each port and each listener on it, the conflicts
	// that listener is in -- the empty set included, which is what makes the diff
	// below sound. previousConflictsByPort is the same table as of the previous
	// cycle, kept to find the listeners whose verdict changed.
	conflictsByPort         map[gatewayv1.PortNumber]conflictsByListener
	previousConflictsByPort map[gatewayv1.PortNumber]conflictsByListener
	// from config
	ControllerName string
	// IngressClass is the value of --ingress.class used to select managed Ingresses.
	IngressClass string
	// EmptyIngressClass, when IngressClass is set, also selects classless Ingresses.
	EmptyIngressClass bool
	// HTTPIngressFrontendPort is the listening port of the HTTP listener of the
	// synthetic Ingress gateway.
	HTTPIngressFrontendPort int
	// HTTPSIngressFrontendPort is the listening port of the HTTPS listener of the
	// synthetic Ingress gateway.
	HTTPSIngressFrontendPort int
}

type CertUpdates struct {
	Created map[string]certificate.CertificateData
	Updated map[string]certificate.CertificateData
	Deleted map[string]certificate.CertificateData
}

func (b *ControllerStore) CleanInstalledVersionsUpdates() {
	if b.InstalledGwAPIVersions.Updated != nil {
		*b.InstalledGwAPIVersions.Updated = false
	}
}

func (b *ControllerStore) addCreatedCertificate(certData certificate.CertificateData) {
	b.CertUpdates.Created[certData.MapKey()] = certData
}

func (b *ControllerStore) addUpdatedCertificate(certData certificate.CertificateData) {
	b.CertUpdates.Updated[certData.MapKey()] = certData
}

func (b *ControllerStore) addDeletedCertificate(certData certificate.CertificateData) {
	b.CertUpdates.Deleted[certData.MapKey()] = certData
}

func (b *ControllerStore) ResetCertificateUpdates() {
	b.CertUpdates.Created = make(map[string]certificate.CertificateData)
	b.CertUpdates.Updated = make(map[string]certificate.CertificateData)
	b.CertUpdates.Deleted = make(map[string]certificate.CertificateData)
}

type CrtListUpdates struct {
	Created map[string]certificate.CrtListData
	Updated map[string]certificate.CrtListData
	Deleted map[string]certificate.CrtListData
}

func (b *ControllerStore) addCreatedCrtList(crtListData certificate.CrtListData) {
	b.CrtListUpdates.Created[crtListData.MapKey()] = crtListData
}

func (b *ControllerStore) addUpdatedCrtList(crtListData certificate.CrtListData) {
	b.CrtListUpdates.Updated[crtListData.MapKey()] = crtListData
}

func (b *ControllerStore) addDeletedCrtList(crtListData certificate.CrtListData) {
	b.CrtListUpdates.Deleted[crtListData.MapKey()] = crtListData
}

func (b *ControllerStore) ResetCrtListUpdates() {
	b.CrtListUpdates.Created = make(map[string]certificate.CrtListData)
	b.CrtListUpdates.Updated = make(map[string]certificate.CrtListData)
	b.CrtListUpdates.Deleted = make(map[string]certificate.CrtListData)
}

func (b *ControllerStore) CheckGatewayClassExists(gwcName string) bool {
	gwcKey := types.NamespacedName{Name: gwcName}
	_, ok := b.GateTree.GatewayClasses[gwcKey]
	if !ok && utils.IsSyntheticName(gwcName) {
		return true
	}
	return ok
}

// listenerFor resolves a listener key to the listener it denotes: a map lookup for
// the Gateway, then a scan of its merged listener list for the key itself. The scan
// is what keeps that list ordered -- the order decides which listener wins a port --
// and it runs over a handful of entries.
func (b *ControllerStore) listenerFor(key listenerKey) *Listener {
	treeGw := b.GateTree.Gateways[key.GatewayKey]
	if treeGw == nil {
		return nil
	}
	return treeGw.listenerByKey(key)
}

// GetGatewayForListener returns the Gateway this listener is served on, or nil if
// the tree does not hold it.
//
// The Gateway comes from the listener's key, which carries it whatever declared
// the listener. It is deliberately not the listener's owner: a listener
// contributed by a ListenerSet belongs to that ListenerSet and is served on the
// Gateway it attaches to, and the two are different objects.
func (b *ControllerStore) GetGatewayForListener(listener *Listener) *Gateway {
	if listener == nil {
		return nil
	}

	return b.GateTree.Gateways[listener.Key().GatewayKey]
}

// OwnerK8sResource returns the API object that declares this listener: the ListenerSet whose
// entry it is, or the Gateway that declares it itself. It is where everything owned by the
// declaring resource is read from -- its generation, first of all, which a listener's own
// payload does not carry.
func (b *ControllerStore) OwnerK8sResource(listener *Listener) client.Object {
	if listener == nil {
		return nil
	}

	if listener.FromListenerSet() {
		treeLs := b.GateTree.ListenerSets[listener.Key().ListenerSetKey]
		if treeLs == nil || treeLs.K8sResource == nil {
			return nil
		}
		return treeLs.K8sResource
	}

	treeGw := b.GateTree.Gateways[listener.Key().GatewayKey]
	if treeGw == nil || treeGw.K8sResource == nil {
		return nil
	}
	return treeGw.K8sResource
}

// previousStatusOf returns what the resource declaring this listener published about it
// last time. See listenerPreviousStatus for why it is the declaring resource and not the
// Gateway the listener is served on.
func (b *ControllerStore) previousStatusOf(listener *Listener) listenerPreviousStatus {
	if listener.FromListenerSet() {
		return listenerSetPreviousStatus(b.GateTree.ListenerSets[listener.Key().ListenerSetKey], listener.Name())
	}
	return gatewayPreviousStatus(b.GateTree.Gateways[listener.Key().GatewayKey], listener.Name())
}

// gatewayPreviousStatus reads one listener's published status out of a Gateway's
// status.listeners, keyed by the name a Gateway declares its listeners under.
func gatewayPreviousStatus(treeGw *Gateway, name gatewayv1.SectionName) listenerPreviousStatus {
	if treeGw == nil || treeGw.K8sResource == nil {
		return listenerPreviousStatus{}
	}

	previous := listenerPreviousStatus{
		generation:        treeGw.K8sResource.Generation,
		creationTimestamp: treeGw.K8sResource.CreationTimestamp,
	}
	for _, status := range treeGw.K8sResource.Status.Listeners {
		if status.Name != name {
			continue
		}
		previous.programmed, previous.hasProgrammed = conditionOfType(
			status.Conditions, string(gatewayv1.ListenerConditionProgrammed),
		)
		break
	}
	return previous
}

// listenerSetPreviousStatus reads one entry's published status out of a ListenerSet's
// status.listeners -- its own vocabulary, and its own generation: an entry's conditions
// are published on the ListenerSet and compared against that object, never against the
// Gateway it attaches to.
func listenerSetPreviousStatus(treeLs *ListenerSet, name gatewayv1.SectionName) listenerPreviousStatus {
	if treeLs == nil || treeLs.K8sResource == nil {
		return listenerPreviousStatus{}
	}

	previous := listenerPreviousStatus{
		generation:        treeLs.K8sResource.Generation,
		creationTimestamp: treeLs.K8sResource.CreationTimestamp,
	}
	for _, status := range treeLs.K8sResource.Status.Listeners {
		if status.Name != name {
			continue
		}
		previous.programmed, previous.hasProgrammed = conditionOfType(
			status.Conditions, string(gatewayv1.ListenerEntryConditionProgrammed),
		)
		break
	}
	return previous
}

// conditionOfType finds one condition type in the conditions of a status.listeners
// entry. The type is a parameter and not a constant because the two status lists belong
// to different vocabularies: Programmed is spelled with two distinct Go types, and the
// bare string that comes out cannot tell them apart afterwards.
func conditionOfType(conditions []metav1.Condition, conditionType string) (metav1.Condition, bool) {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return condition, true
		}
	}
	return metav1.Condition{}, false
}
