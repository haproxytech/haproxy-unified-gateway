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
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	utilsk8s "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils-k8s"
	"k8s.io/apimachinery/pkg/types"
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
	// PortBinder reports whether the controller process can bind to a given
	// port — derived from CAP_NET_BIND_SERVICE and the netns sysctl.
	PortBinder caps.PortBinder
	// mapPort2Listeners is a map for each port:
	// that contains for each listener if it has a conflict or not
	previousMapPort2Listeners map[gatewayv1.PortNumber]listenerConflict
	mapPort2Listeners         map[gatewayv1.PortNumber]listenerConflict
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
	return ok
}

// GetListenerForKey returns the Listener for the given listenerKey, or nil if not found.
// It parses the listenerKey to extract the Gateway and Listener name, then looks up
// the listener in the GateTree.
func (b *ControllerStore) GetListenerForKey(listenerKey types.NamespacedName) *Listener {
	// Parse the listener key to get Gateway and Listener
	gwKey, listenerName, err := ConvertListenerKeyToGatewayKeyAndListenerName(listenerKey)
	if err != nil {
		return nil
	}

	treeGw, ok := b.GateTree.Gateways[gwKey]
	if !ok || treeGw == nil {
		return nil
	}

	// Get the listener from the gateway
	listener, ok := treeGw.Listeners[listenerName]
	if !ok || listener == nil {
		return nil
	}

	return listener
}

// GetGatewayForListener returns the Gateway for the given Listener, or nil if not found.
// It uses the Listener's Owner field to look up the gateway in the GateTree.
func (b *ControllerStore) GetGatewayForListener(listener *Listener) *Gateway {
	if listener == nil {
		return nil
	}

	treeGw, ok := b.GateTree.Gateways[listener.Owner]
	if !ok || treeGw == nil {
		return nil
	}

	return treeGw
}
