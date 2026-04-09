//
// Copyright 2025 HAProxy Technologies LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controller

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/go-logr/logr"
	v3 "github.com/haproxytech/haproxy-unified-gateway/api/gate/v3"
	hapi "github.com/haproxytech/haproxy-unified-gateway/hug/haproxy/api"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/config"
	constant "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/constants"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/handler"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/storage"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/index"
	objtypes "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/object-types"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/predicate"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	utilsk8s "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils-k8s"

	apiv1 "k8s.io/api/core/v1"
	discoveryV1 "k8s.io/api/discovery/v1"
	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	runtimelog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	k8spredicate "sigs.k8s.io/controller-runtime/pkg/predicate"
)

type Controller struct {
	// HAProxyClient is set if Configuration.UpdateHaproxyThroughRuntime is true
	HaproxyClient hapi.HAProxyClient
	Configuration config.Configuration
}

func getControllerPodConfig() (config.ControllerPodConfig, error) {
	podIP, err := getValueFromEnv("POD_IP")
	if err != nil {
		return config.ControllerPodConfig{}, err
	}

	ns, err := getValueFromEnv("POD_NAMESPACE")
	if err != nil {
		return config.ControllerPodConfig{}, err
	}

	name, err := getValueFromEnv("POD_NAME")
	if err != nil {
		return config.ControllerPodConfig{}, err
	}

	c := config.ControllerPodConfig{
		PodIP:     podIP,
		Namespace: ns,
		Name:      name,
	}

	return c, nil
}

func getValueFromEnv(key string) (string, error) {
	val := os.Getenv(key)
	if val == "" {
		return "", fmt.Errorf("environment variable %s not set", key)
	}

	return val, nil
}

func New(options config.GateConfigOptions) (Controller, error) {
	ctrl := Controller{Configuration: config.Configuration{}}
	for _, o := range options {
		err := o(&ctrl.Configuration)
		if err != nil {
			return Controller{}, err
		}
	}
	// --------------
	// Apply Defaults
	ctrl.Configuration.ApplyDefaults()

	// Logs inits
	logrLoggerFromSlog := logr.FromSlogHandler(ctrl.Configuration.LogHandler)
	runtimelog.SetLogger(logrLoggerFromSlog)

	// Other
	ctrlPodConfig, err := getControllerPodConfig()
	if err != nil {
		return Controller{}, err
	}
	ctrl.Configuration.ControllerPodConfig = ctrlPodConfig

	return ctrl, nil
}

func (c *Controller) Run(ctx context.Context, wg *sync.WaitGroup, mapsStorage storage.MapsStorage) error {
	wg.Add(1)
	defer wg.Done()

	mgr, err := createManager(c.Configuration)
	if err != nil {
		return fmt.Errorf("cannot build runtime manager: %w", err)
	}

	if err := Add(ctx, c.Configuration, c.HaproxyClient, mgr, mapsStorage); err != nil {
		return err
	}

	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("cannot start runtime manager: %w", err)
	}

	return nil
}

func Add(
	ctx context.Context,
	cfg config.Configuration,
	haproxyClient hapi.HAProxyClient,
	mgr manager.Manager,
	mapsStorage storage.MapsStorage,
) error {
	// Check if the controller configuration is valid
	if err := cfg.Check(); err != nil {
		return fmt.Errorf("invalid controller configuration: %w", err)
	}

	eventCh := make(chan any)
	extractGVK := utilsk8s.NewExtractGKV(scheme, cfg.Logger)

	if err := registerControllers(ctx, extractGVK, cfg, mgr, eventCh); err != nil {
		return fmt.Errorf("cannot register controllers: %w", err)
	}

	clusterStore := &store.ClusterStore{
		GatewayClasses: make(map[types.NamespacedName]*gatewayv1.GatewayClass),
		Gateways:       make(map[types.NamespacedName]*gatewayv1.Gateway),
		HTTPRoutes:     make(map[types.NamespacedName]*gatewayv1.HTTPRoute),
		TLSRoutes:      make(map[types.NamespacedName]*gatewayv1alpha2.TLSRoute),
		Services:       make(map[types.NamespacedName]*apiv1.Service),
		Namespaces:     make(map[types.NamespacedName]*apiv1.Namespace),
		Secrets:        make(map[types.NamespacedName]*apiv1.Secret),
		ConfigMaps:     make(map[types.NamespacedName]*apiv1.ConfigMap),
		GatewayAPICRDs: make(map[types.NamespacedName]*metav1.PartialObjectMetadata),
		HugGates:       make(map[types.NamespacedName]*v3.HugGate),
		HugConfs:       make(map[types.NamespacedName]*v3.HugConf),
		EndpointSlices:  make(map[types.NamespacedName]*discoveryV1.EndpointSlice),
		ReferenceGrants: make(map[types.NamespacedName]*gatewayv1.ReferenceGrant),
		BackendCRs:      make(map[types.NamespacedName]*v3.Backend),
		GlobalCRs:      make(map[types.NamespacedName]*v3.Global),
		DefaultsCRs:    make(map[types.NamespacedName]*v3.Defaults),
		Updates:        store.NewClusterUpdates(),
	}

	var certificateStorage storage.CertificateStorage
	var err error

	certificateStorage, err = storage.NewCertificateStorage(cfg.Logger, extractGVK, cfg.HaproxyParams.StoreCertificateStructureType,
		cfg.HaproxyParams.LinkID, cfg.HaproxyParams.CertsDir, cfg.HaproxyParams.CertListDir)
	if err != nil {
		return err
	}

	gateTreeConfig := handler.GateTreeConfig{
		BaseLogger:                 cfg.Logger,
		LogCategoryFilterHandler:   cfg.LogHandler,
		ExtractGVK:                 extractGVK,
		ControllerConfNsName:       cfg.HugConfCRD,
		TransferHaproxyConfChannel: cfg.TransferHaproxyConfChannel,
		K8sClient:                  mgr.GetClient(),
		K8sReader:                  mgr.GetAPIReader(),
		StoreCertificateOnDisk:     cfg.HaproxyParams.StoreCertificateOnDisk,
		StoreMapsOnDisk:            cfg.HaproxyParams.StoreMapsOnDisk,
		RuntimeUpdateHaproxy:       cfg.HaproxyParams.RuntimeUpdateHaproxy,
		CertificateStorage:         certificateStorage,
		MapsStorage:                mapsStorage,
		ControllerName:             cfg.ControllerName,
		DisableIPv4:                cfg.HaproxyParams.DisableIPv4,
		DisableIPv6:                cfg.HaproxyParams.DisableIPv6,
		HugServiceLabelKey:         cfg.HugServiceLabelKey,
		HugServiceLabelVal:         cfg.HugServiceLabelVal,
	}
	haproxyCfgMgrParams, err := haproxy.NewHaproxyConfMgrParams(extractGVK, cfg.HaproxyParams, certificateStorage, mapsStorage)
	if err != nil {
		return err
	}

	eventHandler := handler.NewEventHandlerImpl(
		clusterStore,
		gateTreeConfig,
		haproxyCfgMgrParams,
		cfg.InitialStructuredHaproxyConf,
		haproxyClient,
	)

	loopCfg := handler.EventLoopConfig{
		SyncPeriod:        cfg.SyncPeriod,
		StartupSyncPeriod: cfg.StartupSyncPeriod,
	}
	eventLoop := handler.NewEventLoop(
		loopCfg,
		eventCh,
		cfg.Logger,
		eventHandler,
		extractGVK,
	)

	if err := mgr.Add(eventLoop); err != nil {
		return fmt.Errorf("cannot register event loop: %w", err)
	}

	return nil
}

//revive:disable:function-length
func registerControllers(ctx context.Context, extractGVK utilsk8s.ExtractGVK, cfg config.Configuration, mgr manager.Manager, eventCh chan any) error {
	type ctlrCfg struct {
		name       string
		objectType ctrlruntimeclient.Object
		options    []Option
	}

	crdWithGVK := apiext.CustomResourceDefinition{}
	crdWithGVK.SetGroupVersionKind(
		schema.GroupVersionKind{Group: apiext.GroupName, Version: "v1", Kind: "CustomResourceDefinition"},
	)

	dedicatedNs := utils.NewDedicatedNamespaces(cfg.Namespaces)

	controllerRegisterCfgs := []ctlrCfg{
		{
			// watch metadata of Gateway API CRDs
			// Gateway API CRDs are filtered with the predicate: AnnotationPredicate
			// on
			name:       "GatewayApiCRD",
			objectType: &crdWithGVK,
			options: []Option{
				WithOnlyMetadata(),
				WithK8sPredicate(
					k8spredicate.And(
						predicate.AnnotationPredicate{Annotation: constant.BundleVersionAnnotation}),
				),
			},
		},
		{
			name:       "GatewayClass",
			objectType: objtypes.ObjectTypeGatewayClass,
			options: []Option{
				WithK8sPredicate(
					k8spredicate.And(
						k8spredicate.GenerationChangedPredicate{},
						predicate.GatewayClassPredicate{ControllerName: cfg.ControllerName},
					),
				),
				// Watch HugGate
				WithEnqueueFor([]enqueueForParams{
					{
						watchSource: objtypes.ObjectTypeHugGate,
						enqueueFunc: enqueueGatewayClassForHugGate,
						predicate: k8spredicate.And(
							k8spredicate.ResourceVersionChangedPredicate{},
							predicate.NewNamespacePredicate(cfg.Namespaces),
						),
					},
				},
				),
			},
		},
		{
			name:       "Gateway",
			objectType: objtypes.ObjectTypeGateway,
			options: []Option{
				WithK8sPredicate(
					k8spredicate.And(
						k8spredicate.GenerationChangedPredicate{},
						predicate.NewNamespacePredicate(cfg.Namespaces),
						predicate.NewGatewayPredicate(cfg.GatewayNsName),
					),
				),
				WithEnqueueFor([]enqueueForParams{
					// Watch HugGate
					{
						watchSource: objtypes.ObjectTypeHugGate,
						enqueueFunc: enqueueGatewayForHugGate(utils.NewDedicatedGateway(cfg.GatewayNsName), dedicatedNs),
						predicate:   predicate.NewNamespacePredicate(cfg.Namespaces),
					},
					// Watch GatewayClass
					{
						watchSource: objtypes.ObjectTypeGatewayClass,
						enqueueFunc: enqueueGatewayForGatewayClass(utils.NewDedicatedGateway(cfg.GatewayNsName), dedicatedNs),
						predicate: k8spredicate.And(
							k8spredicate.GenerationChangedPredicate{},
							predicate.GatewayClassPredicate{ControllerName: cfg.ControllerName},
						),
					},
					// Watch Secrets
					{
						watchSource: objtypes.ObjectTypeSecret,
						enqueueFunc: enqueueGatewayForSecret(utils.NewDedicatedGateway(cfg.GatewayNsName), dedicatedNs),
						predicate: k8spredicate.And(
							k8spredicate.ResourceVersionChangedPredicate{},
							predicate.NewNamespacePredicate(cfg.Namespaces),
						),
					},
					// Watch HUG service â refresh Gateway.Status.Addresses when the
					// controller service (LoadBalancer IP assignment, type change, â¦) changes.
					{
						watchSource: objtypes.ObjectTypeService,
						enqueueFunc: enqueueGatewayForHugService(utils.NewDedicatedGateway(cfg.GatewayNsName), dedicatedNs),
						predicate: k8spredicate.And(
							k8spredicate.ResourceVersionChangedPredicate{},
							k8spredicate.NewPredicateFuncs(func(obj ctrlruntimeclient.Object) bool {
								return obj.GetLabels()[cfg.HugServiceLabelKey] == cfg.HugServiceLabelVal
							}),
						),
					},
				},
				),
			},
		},
		{
			name:       "HTTPRoute",
			objectType: objtypes.ObjectTypeHTTPRoute,
			options: []Option{
				WithK8sPredicate(
					k8spredicate.And(
						k8spredicate.GenerationChangedPredicate{},
						// predicate.GatewayPredicate{GatewayClassNames: cfg.GatewayClasses},
						predicate.NewNamespacePredicate(cfg.Namespaces),
					),
				),
				// Watch Gateway
				WithEnqueueFor([]enqueueForParams{
					{
						watchSource: objtypes.ObjectTypeGateway,
						enqueueFunc: enqueueHTTPRouteForGateway(dedicatedNs),
						predicate: k8spredicate.And(
							k8spredicate.ResourceVersionChangedPredicate{},
							predicate.NewNamespacePredicate(cfg.Namespaces),
						),
					},
					// Watch Services
					{
						watchSource: objtypes.ObjectTypeService,
						enqueueFunc: enqueueHTTPRouteForService(dedicatedNs),
						predicate: k8spredicate.And(
							k8spredicate.ResourceVersionChangedPredicate{},
							predicate.NewNamespacePredicate(cfg.Namespaces),
						),
					},
					// Watch Backend CRs
					{
						watchSource: objtypes.ObjectTypeBackend,
						enqueueFunc: enqueueHTTPRouteForBackendCR(dedicatedNs),
						predicate: k8spredicate.And(
							k8spredicate.ResourceVersionChangedPredicate{},
							predicate.NewNamespacePredicate(cfg.Namespaces),
						),
					},
					// Watch ReferenceGrants — cross-namespace backendRef access may change
					{
						watchSource: objtypes.ObjectTypeRefGrant,
						enqueueFunc: enqueueHTTPRouteForReferenceGrant,
						predicate: k8spredicate.And(
							k8spredicate.ResourceVersionChangedPredicate{},
							predicate.NewNamespacePredicate(cfg.Namespaces),
						),
					},
				},
				),
			},
		},
		{
			name:       "Service",
			objectType: objtypes.ObjectTypeService,
			options: []Option{
				WithK8sPredicate(
					k8spredicate.And(
						k8spredicate.ResourceVersionChangedPredicate{},
						predicate.NewNamespacePredicate(cfg.Namespaces),
					),
				),
			},
		},
		{
			name:       "Secret",
			objectType: objtypes.ObjectTypeSecret,
			options: []Option{
				WithK8sPredicate(
					k8spredicate.And(
						k8spredicate.ResourceVersionChangedPredicate{},
						predicate.NewNamespacePredicate(cfg.Namespaces),
					),
				),
			},
		},
		{
			name:       "EndpointSlice",
			objectType: &discoveryV1.EndpointSlice{},
			options: []Option{
				WithK8sPredicate(
					k8spredicate.And(
						k8spredicate.ResourceVersionChangedPredicate{},
						predicate.NewNamespacePredicate(cfg.Namespaces),
					),
				),
				WithFieldIndices(index.CreateEndpointSliceFieldIndices(cfg.Logger)),
			},
		},
		{
			name:       "Namespace",
			objectType: &apiv1.Namespace{},
			options: []Option{
				WithK8sPredicate(
					k8spredicate.And(
						k8spredicate.ResourceVersionChangedPredicate{},
						predicate.NewNamespacePredicate(cfg.Namespaces),
					),
				),
			},
		},
		{
			name:       "ConfigMap",
			objectType: &apiv1.ConfigMap{},
			options: []Option{
				WithK8sPredicate(
					k8spredicate.And(
						k8spredicate.GenerationChangedPredicate{},
						predicate.NewNamespacePredicate(cfg.Namespaces),
					),
				),
			},
		},
		{
			name:       "HugGate",
			objectType: objtypes.ObjectTypeHugGate,
			options: []Option{
				WithK8sPredicate(
					k8spredicate.And(
						k8spredicate.ResourceVersionChangedPredicate{},
						predicate.NewNamespacePredicate(cfg.Namespaces),
					),
				),
			},
		},
		{
			name:       "HugConf",
			objectType: &v3.HugConf{},
			options: []Option{
				WithK8sPredicate(
					k8spredicate.And(
						k8spredicate.ResourceVersionChangedPredicate{},
						predicate.NewNamespacePredicate(cfg.Namespaces),
						predicate.HugConfPredicate{
							ControllerConfName: cfg.HugConfCRD,
						},
					),
				),
				WithEnqueueFor([]enqueueForParams{
					{
						// Watch GlobalCR
						watchSource: objtypes.ObjectTypeGlobal,
						enqueueFunc: enqueueHugConfForGlobalCR,
						predicate: k8spredicate.And(
							k8spredicate.ResourceVersionChangedPredicate{},
							predicate.NewNamespacePredicate(cfg.Namespaces),
						),
					},
					{
						// Watch DefaultsCR
						watchSource: objtypes.ObjectTypeDefaults,
						enqueueFunc: enqueueHugConfForDefaultsCR,
						predicate: k8spredicate.And(
							k8spredicate.ResourceVersionChangedPredicate{},
							predicate.NewNamespacePredicate(cfg.Namespaces),
							predicate.DefaultsPredicate{},
						),
					},
				}),
			},
		},
		{
			name:       "BackendCR",
			objectType: objtypes.ObjectTypeBackend,
			options: []Option{
				WithK8sPredicate(
					k8spredicate.And(
						k8spredicate.ResourceVersionChangedPredicate{},
						predicate.NewNamespacePredicate(cfg.Namespaces),
					),
				),
			},
		},
		{
			name:       "GlobalCR",
			objectType: objtypes.ObjectTypeGlobal,
			options: []Option{
				WithK8sPredicate(
					k8spredicate.And(
						k8spredicate.ResourceVersionChangedPredicate{},
						predicate.NewNamespacePredicate(cfg.Namespaces),
					),
				),
			},
		},
		{
			name:       "DefaultsCR",
			objectType: objtypes.ObjectTypeDefaults,
			options: []Option{
				WithK8sPredicate(
					k8spredicate.And(
						k8spredicate.ResourceVersionChangedPredicate{},
						predicate.NewNamespacePredicate(cfg.Namespaces),
						predicate.DefaultsPredicate{},
					),
				),
			},
		},
		{
			name:       "TLSRoute",
			objectType: objtypes.ObjectTypeTLSRoute,
			options: []Option{
				WithK8sPredicate(
					k8spredicate.And(
						k8spredicate.GenerationChangedPredicate{},
						predicate.NewNamespacePredicate(cfg.Namespaces),
					),
				),
				// Watch Gateway
				WithEnqueueFor([]enqueueForParams{
					{
						watchSource: objtypes.ObjectTypeGateway,
						enqueueFunc: enqueueTLSRouteForGateway(dedicatedNs),
						predicate: k8spredicate.And(
							k8spredicate.ResourceVersionChangedPredicate{},
							predicate.NewNamespacePredicate(cfg.Namespaces),
						),
					},
					// Watch Services
					{
						watchSource: objtypes.ObjectTypeService,
						enqueueFunc: enqueueTLSRouteForService(dedicatedNs),
						predicate: k8spredicate.And(
							k8spredicate.ResourceVersionChangedPredicate{},
							predicate.NewNamespacePredicate(cfg.Namespaces),
						),
					},
					// Watch ReferenceGrants — cross-namespace backendRef access may change
					{
						watchSource: objtypes.ObjectTypeRefGrant,
						enqueueFunc: enqueueTLSRouteForReferenceGrant,
						predicate: k8spredicate.And(
							k8spredicate.ResourceVersionChangedPredicate{},
							predicate.NewNamespacePredicate(cfg.Namespaces),
						),
					},
				}),
			},
		},
		{
			name:       "ReferenceGrant",
			objectType: objtypes.ObjectTypeRefGrant,
			options: []Option{
				WithK8sPredicate(
					k8spredicate.And(
						k8spredicate.GenerationChangedPredicate{},
						predicate.NewNamespacePredicate(cfg.Namespaces),
					),
				),
				// No WithEnqueueFor: ReferenceGrant has no secondary dependencies.
				// The inverse direction is correct, HTTPRoute and TLSRoute controllers
				// watch ReferenceGrant to re-enqueue affected routes when a grant changes.
			},
		},
	}

	for _, registerConfig := range controllerRegisterCfgs {
		params := registerParams{
			ctx:        ctx,
			logger:     cfg.Logger,
			objectType: registerConfig.objectType,
			name:       registerConfig.name,
			mgr:        mgr,
			eventCh:    eventCh,
			options:    registerConfig.options,
			extractGVK: extractGVK,
		}
		if err := Register(params); err != nil {
			return fmt.Errorf("cannot register controller for %T: %w", registerConfig.objectType, err)
		}
	}
	return nil
}
