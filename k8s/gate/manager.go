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
	"fmt"
	"time"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/config"
	hugmetrics "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/metrics"

	haproxyapiv3 "github.com/haproxytech/haproxy-unified-gateway/api/gate/v3"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	discoveryV1 "k8s.io/api/discovery/v1"
	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctlr "sigs.k8s.io/controller-runtime"
	ctrlcfg "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsfilters "sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/gateway-api/apis/v1alpha2"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(gatewayv1.Install(scheme))
	utilruntime.Must(gatewayv1beta1.Install(scheme))
	utilruntime.Must(v1.AddToScheme(scheme))
	utilruntime.Must(discoveryV1.AddToScheme(scheme))
	utilruntime.Must(apiext.AddToScheme(scheme))
	utilruntime.Must(appsv1.AddToScheme(scheme))
	utilruntime.Must(haproxyapiv3.AddToScheme(scheme))
	utilruntime.Must(v1alpha2.Install(scheme))
}

func createManager(cfg config.Configuration) (manager.Manager, error) {
	options := manager.Options{
		Scheme:                        scheme,
		Metrics:                       getMetricsOptions(cfg.MetricsConfig),
		LeaderElection:                cfg.LeaderElectionConfig.Enabled,
		LeaderElectionNamespace:       cfg.ControllerPodConfig.Namespace,
		LeaderElectionID:              cfg.LeaderElectionConfig.LockName,
		LeaderElectionReleaseOnCancel: false,
		Controller: ctrlcfg.Controller{
			NeedLeaderElection: new(false),
		},
	}
	if cfg.CacheResyncPeriod != 0 {
		options.Cache.SyncPeriod = &cfg.CacheResyncPeriod
	}

	var clusterCfg *rest.Config
	var err error
	if cfg.Kubeconfig != "" {
		clusterCfg, err = getKubeconfigFromString(cfg.Kubeconfig)
	} else {
		clusterCfg, err = ctlr.GetConfig()
	}
	if err != nil {
		return nil, err
	}
	clusterCfg.Timeout = 10 * time.Second // FLAGS ???

	mgr, err := manager.New(clusterCfg, options)
	if err != nil {
		return nil, err
	}

	return mgr, nil
}

func getKubeconfigFromString(kubeconfigString string) (*rest.Config, error) {
	return clientcmd.RESTConfigFromKubeConfig([]byte(kubeconfigString))
}

func getMetricsOptions(cfg config.MetricsConfig) metricsserver.Options {
	metricsOptions := metricsserver.Options{BindAddress: "0"}

	if cfg.Enabled {
		if cfg.Secure {
			metricsOptions.SecureServing = true
		}
		metricsOptions.BindAddress = fmt.Sprintf(":%v", cfg.Port)

		switch cfg.AuthMode {
		case config.MetricsAuthKubeRBAC:
			metricsOptions.SecureServing = true
			metricsOptions.FilterProvider = metricsfilters.WithAuthenticationAndAuthorization
		case config.MetricsAuthBasic:
			metricsOptions.SecureServing = true
			metricsOptions.FilterProvider = hugmetrics.WithBasicAuth(cfg.BasicAuthUser, cfg.BasicAuthPassword)
		}
	}

	return metricsOptions
}
