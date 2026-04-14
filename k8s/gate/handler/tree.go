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
package handler

import (
	"github.com/haproxytech/haproxy-unified-gateway/hug/reload"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"
)

type GateTreeBuilder struct {
	cfg GateTreeConfig
	*tree.ControllerStore
	referenceManager *tree.ReferenceManager
	builder          []tree.Builder
}

func (b *GateTreeBuilder) GetTree() *tree.GateTree {
	return b.GateTree
}

func NewGateTreeBuilder(controllerStore *tree.ControllerStore, cfg GateTreeConfig) GateTreeBuilder {
	// --------------
	// Update References
	// --------------
	referenceManager := tree.NewReferenceManager(controllerStore)

	// --------------
	// GatewayClass
	gatewayClassBuilderParams := tree.GatewayClassBuilderParams{
		ControllerStore: controllerStore,
	}
	gatewayClassBuilder := tree.NewGatewayClassBuilder(gatewayClassBuilderParams)

	// --------------
	// Gateway
	gatewayBuilder := tree.NewGatewayBuilder(tree.GatewayBuilderParams{
		ControllerStore:    controllerStore,
		CertificateStorage: cfg.CertificateStorage,
	})

	// VirtualListener
	virtualListenerBuilder := tree.NewVirtualListenerBuilder(controllerStore)

	// --------------
	// Secret
	secretBuilder := tree.NewSecretBuilder(controllerStore)

	// --------------
	// Certificate
	certificateBuilder := tree.NewCertificateBuilder(controllerStore, cfg.StoreCertificateOnDisk, cfg.RuntimeUpdateHaproxy, cfg.CertificateStorage)

	// --------------
	// Service
	serviceBuilder := tree.NewServiceBuilder(controllerStore)

	// --------------
	// HTTPRoute
	httpRouteBuilder := tree.NewHTTPRouteBuilder(tree.HTTPRouteBuilderParams{
		ControllerStore: controllerStore,
		MapsStorage:     cfg.MapsStorage,
		RuntimeUpdate:   cfg.RuntimeUpdateHaproxy,
	})

	// --------------
	// TLSRoute
	tlsRouteBuilder := tree.NewTLSRouteBuilder(tree.TLSRouteBuilderParams{
		ControllerStore: controllerStore,
		MapsStorage:     cfg.MapsStorage,
	})

	defaultsCRBuilder := tree.NewDefaultsCRBuilder(controllerStore)

	treeBuilder := GateTreeBuilder{
		cfg:              cfg,
		referenceManager: referenceManager,
		ControllerStore:  controllerStore,
		builder: []tree.Builder{
			secretBuilder,
			gatewayClassBuilder,
			gatewayBuilder,
			virtualListenerBuilder,
			certificateBuilder,
			serviceBuilder,
			httpRouteBuilder,
			tlsRouteBuilder,
			defaultsCRBuilder,
		},
	}

	return treeBuilder
}

func (b *GateTreeBuilder) buildGateTree() {
	// --------------
	// Start the build process: cleanups
	b.startBuild()

	// --------------
	// Update the references
	// --------------
	b.referenceManager.UpdateRefences()

	// ControllerConf CRD
	b.buildControllerConfCRDUpdates()
	// installed Versions
	b.buildInstalledVersionsUpdates()

	// --------------
	// Build the GateTree
	// --------------
	for _, builder := range b.builder {
		builder.ComputeTreeUpdates()
	}
}

func (b *GateTreeBuilder) startBuild() {
	// reloadMgr reset
	reload.Instance().Reset()
	// --------------
	// Clean TreeUpdates
	// --------------
	for _, builder := range b.builder {
		builder.CleanTreeUpdates()
	}
	b.ControllerStore.CleanInstalledVersionsUpdates()
	b.ControllerStore.ResetCertificateUpdates()
	b.ControllerStore.ResetCrtListUpdates()
}

func (b *GateTreeBuilder) buildControllerConfCRDUpdates() {
	// --------------
	// controllerConf CRD
	controllerConfBuilderParams := tree.HugConfBuilderParams{
		ControllerStore:          b.ControllerStore,
		LogCategoryFilterHandler: b.cfg.LogCategoryFilterHandler,
		HugConfNsName:            b.cfg.ControllerConfNsName,
	}
	hugConfBuilder := tree.NewHugConfBuilder(controllerConfBuilderParams)
	hugConfBuilder.Build()
}

func (b *GateTreeBuilder) buildInstalledVersionsUpdates() {
	// --------------
	// installed Versions
	installedVersionBuilder := tree.NewInstalledVersionsBuilder(b.ControllerStore)
	installedVersionBuilder.Build()
}
