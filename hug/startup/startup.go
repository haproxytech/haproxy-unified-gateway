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
package startup

import (
	"context"
	"net/netip"
	"path/filepath"

	"github.com/haproxytech/client-native/v6/configuration"
	cfgoptions "github.com/haproxytech/client-native/v6/configuration/options"
	"github.com/haproxytech/client-native/v6/models"
	hugconfig "github.com/haproxytech/haproxy-unified-gateway/hug/configuration"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/constants"
	md "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/metadata"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/structured"
)

type ownerMetaData interface {
	*models.Frontend | *models.Backend
}

// StructuredFromFile read a initial configuration file and returns a Structured HAProxy configuration
// containing:
// Frontends/Backends
// that have the unified gateway metadata
// (the objects that the gateway manages)
func StructuredFromFile(hugConfig hugconfig.HUGConfig) (structured.Structured, error) {
	confClient, err := configuration.New(context.Background(),
		cfgoptions.ConfigurationFile(hugConfig.HaproxyDirs.MainCfgFile),
		cfgoptions.TransactionsDir(hugConfig.HaproxyDirs.CfgDir),
		cfgoptions.UseMd5Hash,
		cfgoptions.HAProxyBin(hugConfig.HaproxyDirs.HaproxyBinary),
	)
	if err != nil {
		return structured.Structured{}, err
	}

	_, backends, err := confClient.GetStructuredBackends("")
	if err != nil {
		return structured.Structured{}, err
	}
	_, frontends, err := confClient.GetStructuredFrontends("")
	if err != nil {
		return structured.Structured{}, err
	}

	structuredCfg := structured.NewStructuredConf()
	for _, backend := range backends {
		if isUnifiedGatewayManaged(backend) {
			structuredCfg.Backends[backend.Name] = backend
		}
	}
	for _, frontend := range frontends {
		if isUnifiedGatewayManaged(frontend) {
			structuredCfg.Frontends[frontend.Name] = frontend
		}
	}

	// check that we have correct settings in global part
	_, global, err := confClient.GetGlobalConfiguration("")
	if err != nil {
		return structured.Structured{}, err
	}
	//  tune.lua.bool-sample-conversion normal
	// lua-load-per-thread <path-to-route.lua>
	// Ensure the configured LoadPerThread points to the route.lua next to the cfgFile
	// so HAProxy can load it regardless of working directory (important for CI).
	if global.LuaOptions == nil {
		global.LuaOptions = &models.LuaOptions{}
	}
	cfgDir := filepath.Dir(hugConfig.MainCfgFile)
	global.LuaOptions.LoadPerThread = filepath.Join(cfgDir, "route.lua")
	// check if we have runtime option enabled
	if len(global.RuntimeAPIs) == 0 {
		global.RuntimeAPIs = []*models.RuntimeAPI{
			{
				Address: &hugConfig.RuntimeSocket,
			},
		}
	} else {
		// check if the first one is the correct one, otherwise add it as first
		if global.RuntimeAPIs[0].Address == nil || *global.RuntimeAPIs[0].Address != hugConfig.RuntimeSocket {
			global.RuntimeAPIs = append([]*models.RuntimeAPI{
				{
					Address: &hugConfig.RuntimeSocket,
				},
			}, global.RuntimeAPIs...)
		}
	}
	// enforce first socket has level admin
	global.RuntimeAPIs[0].Level = "admin"

	version, err := confClient.GetVersion("")
	if err != nil {
		return structured.Structured{}, err
	}
	err = confClient.PushGlobalConfiguration(global, "", version)
	if err != nil {
		return structured.Structured{}, err
	}

	// Add binds to the stats frontend if it exists
	// There is an option to not add it
	if hugConfig.AddStatsPortToFrontend {
		err = addBindPortToStatsFrontend(confClient, frontends, hugConfig)
		if err != nil {
			return structured.Structured{}, err
		}
	}

	return structuredCfg, nil
}

func addBindPortToStatsFrontend(confClient configuration.Configuration,
	frontends models.Frontends,
	hugConfig hugconfig.HUGConfig,
) error {
	var statsFrontend *models.Frontend
	for _, fe := range frontends {
		if fe.Name == constants.StatsFrontendName {
			statsFrontend = fe
		}
	}

	if statsFrontend == nil {
		// Does not exists, skip
		return nil
	}

	var bindv4 *models.Bind
	var bindv6 *models.Bind

	// Do we have a v4/v6 bind already?
	for _, bind := range statsFrontend.Binds {
		if bind.Port != nil {
			addr, err := netip.ParseAddr(bind.Address)
			if err == nil {
				if addr.Is4() {
					bindv4 = &bind
				}
				if addr.Is6() {
					bindv6 = &bind
				}
			}
		}
	}

	if !hugConfig.DisableIPv4 {
		version, err := confClient.GetVersion("")
		if err != nil {
			return err
		}
		if statsFrontend.Binds == nil {
			statsFrontend.Binds = make(map[string]models.Bind)
		}
		if bindv4 == nil {
			name := "stats"
			bind := models.Bind{
				Name:       name,
				Port:       new(hugConfig.StatsPort),
				Address:    "0.0.0.0",
				BindParams: models.BindParams{},
			}
			err := confClient.CreateBind("frontend", constants.StatsFrontendName, &bind, "", version)
			if err != nil {
				return err
			}
			statsFrontend.Binds[name] = bind
		} else {
			// We already have a bind for this port
			bindv4.Port = new(hugConfig.StatsPort)
			err := confClient.EditBind(bindv4.Name, "frontend", constants.StatsFrontendName, bindv4, "", version)
			if err != nil {
				return err
			}
			statsFrontend.Binds[bindv4.Name] = *bindv4
		}
	}
	if !hugConfig.DisableIPv6 {
		version, err := confClient.GetVersion("")
		if err != nil {
			return err
		}
		if statsFrontend.Binds == nil {
			statsFrontend.Binds = make(map[string]models.Bind)
		}
		if bindv6 == nil {
			name := "v6"
			bind := models.Bind{
				Name:       name,
				Port:       new(hugConfig.StatsPort),
				Address:    "::",
				BindParams: models.BindParams{},
			}
			err := confClient.CreateBind("frontend", constants.StatsFrontendName, &bind, "", version)
			if err != nil {
				return err
			}
			statsFrontend.Binds[name] = bind
		} else {
			// We already have a bind for this port
			bindv6.Port = new(hugConfig.StatsPort)
			err := confClient.EditBind(bindv6.Name, "frontend", constants.StatsFrontendName, bindv6, "", version)
			if err != nil {
				return err
			}
			statsFrontend.Binds[bindv6.Name] = *bindv6
		}
	}

	return nil
}

// isUnifiedGatewayManaged returns true if the object is managed by the Unified Gateway
// false otherwise
// based on the MetaData
func isUnifiedGatewayManaged[T ownerMetaData](obj T) bool {
	var metadata map[string]any
	switch o := any(obj).(type) {
	case *models.Frontend:
		metadata = o.Metadata
	case *models.Backend:
		metadata = o.Metadata
	default:
		return false
	}
	if _, ok := metadata[md.UnifiedGatewayMetaDataKey]; ok {
		return true
	}
	return false
}
