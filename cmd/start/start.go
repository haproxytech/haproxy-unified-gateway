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
package start

import (
	hugconfig "github.com/haproxytech/haproxy-unified-gateway/hug/configuration"
	"github.com/haproxytech/haproxy-unified-gateway/hug/startup"
	gateconfig "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/config"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/constants"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/diffs"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/storage"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	opt "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options"

	"k8s.io/apimachinery/pkg/types"
)

func SetupGateConfig(hugConfig hugconfig.HUGConfig) gateconfig.GateConfigOptions {
	metricsConfig := gateconfig.MetricsConfig{
		Port:              hugConfig.ControllerPort,
		Enabled:           true,
		Secure:            false,
		AuthMode:          gateconfig.MetricsAuthMode(hugConfig.MetricsAuth),
		BasicAuthUser:     hugConfig.MetricsBasicAuthUser,
		BasicAuthPassword: hugConfig.MetricsBasicAuthPassword,
	}

	// kubeconfig := testKubeConfig
	kubeconfig := ""

	haproxyConfCh := make(chan diffs.HaproxyConfDiffs, 100)

	// Read the haproy.cfg file at startup, and initializes the library with the initial haproxy configuration
	initialStructured, err := startup.StructuredFromFile(hugConfig)
	if err != nil {
		panic(err)
	}

	opts := gateconfig.GateConfigOptions{
		opt.KubeConfig(kubeconfig),
		opt.ControllerConfCRD(types.NamespacedName{
			Namespace: hugConfig.ControllerConfCRD.Namespace,
			Name:      hugConfig.ControllerConfCRD.Name,
		}),
		opt.SyncPeriod(hugConfig.SyncPeriod),
		opt.StartupSyncPeriod(hugConfig.StartupSyncPeriod),
		opt.MetricsConfig(metricsConfig),
		opt.LeaderElectionConfig(hugConfig.LeaderElectionEnabled),
		opt.ControllerName(hugConfig.ControllerName),
		opt.Namespaces(hugConfig.Namespaces),
		opt.Logging(logging.LogHandlerType(hugConfig.LogType), hugConfig.DefaultLogLevel, hugConfig.LogSettings),
		opt.HaproxyConfChannel(haproxyConfCh),
		opt.IPV4BindAddr(hugConfig.IPV4BindAddr),
		opt.IPV6BindAddr(hugConfig.IPV6BindAddr),
		opt.HaproxyDirs(hugConfig.HaproxyDirs),
		opt.LinkID("hug"),
		opt.InitialStructured(initialStructured),
		opt.CacheReSyncPeriod(hugConfig.CacheResyncPeriod),
		opt.DefaultsSectionName(constants.DefaultsSectionName),
		opt.RuntimeUpdate(gateconfig.DefaultWaitForRuntimeTimeout),   // Send commands through runtime in Gate library
		opt.StoreCertificateOnDisk(storage.StructureTypeCertDefault), // Store the certificates on disk
		opt.StoreMapsOnDisk(storage.StructureTypeMapsDefault),        // Store the maps on disk
		opt.GatewayNsName(types.NamespacedName{
			Namespace: hugConfig.GatewayNsName.Namespace,
			Name:      hugConfig.GatewayNsName.Name,
		}), // If specified, only this Gateway will be watched, otherwise all Gateways will be watched.
	}
	if hugConfig.DisableIPv4 {
		opts = append(opts, opt.DisableIPv4())
	}
	if hugConfig.DisableIPv6 {
		opts = append(opts, opt.DisableIPv6())
	}
	return opts
}
