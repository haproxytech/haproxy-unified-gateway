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
package haproxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	cp_params "github.com/haproxytech/client-native/v6/config-parser/params"
	"github.com/haproxytech/client-native/v6/configuration"
	cn_options "github.com/haproxytech/client-native/v6/configuration/options"
	"github.com/haproxytech/client-native/v6/models"
	"github.com/haproxytech/haproxy-unified-gateway/hug/reload"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/constants"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"

	"github.com/imdario/mergo"
)

// BufferSize is the default value of HAproxy tune.bufsize. Not recommended to change it
// Map payload or socket data cannot be bigger than tune.bufsize
const BufferSize = 16000

type RuntimeServerStateData struct {
	BackendName string
	ServerName  string
	IP          string
	State       string
	Port        int
}

// runtimeDeleteServers sets deleted servers to MAINT state and then deletes them via the runtime API.
func (b *HaproxyConfMgrImpl) runtimeDeleteServers(backendName string, serverDiffs ServerDiff) error {
	// If RuntimeUpdateHaproxy is false, the gate library does not need runtime.Runtime and does not perform runtime updates.
	if !b.params.RuntimeUpdateHaproxy {
		return nil
	}

	runtimeServerStateData := make([]RuntimeServerStateData, 0, len(serverDiffs.deleted))
	for serverName := range serverDiffs.deleted {
		runtimeServerStateData = append(runtimeServerStateData, RuntimeServerStateData{
			BackendName: backendName,
			ServerName:  serverName,
			IP:          "127.0.0.1",
			Port:        1,
			State:       "maint",
		})
	}
	if len(runtimeServerStateData) != 0 {
		reload.Instance().AttemptDynamicServerStateUpdate("backend %s", backendName)
		if err := b.runtimeSetServerAddrAndState(runtimeServerStateData); err != nil {
			reload.Instance().SetDynamicServerStateUpdateFailure("backend %s", backendName)
			b.logger.LogAttrs(context.Background(), slog.LevelError, "Runtime update of server state failed",
				logging.LogAttrError(err))
			return err
		}
	}
	for _, runtimeServerData := range runtimeServerStateData {
		err := b.runtimeDeleteServer(runtimeServerData.BackendName, runtimeServerData.ServerName)
		if err != nil {
			// A stale server may stay in the running HAProxy: force a reload from the config.
			reload.Instance().SetDynamicServerStateUpdateFailure("backend %s", backendName)
			b.logger.LogAttrs(context.Background(), slog.LevelError, "Runtime delete of server failed",
				logging.LogAttrError(err), logging.LogAttrBackendName(runtimeServerData.BackendName),
				logging.LogAttrServerName(runtimeServerData.ServerName))
		} else {
			b.logger.LogAttrs(context.Background(), slog.LevelDebug, "Runtime delete of server success",
				logging.LogAttrBackendName(runtimeServerData.BackendName),
				logging.LogAttrServerName(runtimeServerData.ServerName))
		}
	}
	return nil
}

func (b *HaproxyConfMgrImpl) runtimeSetServerAddrAndState(servers []RuntimeServerStateData) error {
	if len(servers) == 0 {
		return nil
	}

	backendNameSize := len(servers[0].BackendName)
	oneServerCommandSize := 75 + 2*backendNameSize
	size := min(oneServerCommandSize*len(servers), BufferSize)

	var sb strings.Builder
	sb.Grow(size)
	var cmdBuilder strings.Builder
	cmdBuilder.Grow(oneServerCommandSize)
	for _, server := range servers {
		// if new commands are added recalculate oneServerCommandSize
		cmdBuilder.WriteString("set server ")
		cmdBuilder.WriteString(server.BackendName)
		cmdBuilder.WriteString("/")
		cmdBuilder.WriteString(server.ServerName)
		cmdBuilder.WriteString(" addr ")
		cmdBuilder.WriteString(server.IP)
		if server.Port > 0 {
			cmdBuilder.WriteString(" port ")
			cmdBuilder.WriteString(strconv.Itoa(server.Port))
		}
		cmdBuilder.WriteString(";set server ")
		cmdBuilder.WriteString(server.BackendName)
		cmdBuilder.WriteString("/")
		cmdBuilder.WriteString(server.ServerName)
		cmdBuilder.WriteString(" state ")
		cmdBuilder.WriteString(server.State)
		cmdBuilder.WriteString(";")
		// if new commands are added recalculate oneServerCommandSize

		if sb.Len()+cmdBuilder.Len() >= BufferSize {
			err := b.runRaw(sb, server.BackendName)
			if err != nil {
				return err
			}
			sb.Reset()
			sb.Grow(size)
		}
		sb.WriteString(cmdBuilder.String())
		cmdBuilder.Reset()
		cmdBuilder.Grow(oneServerCommandSize)
	}
	if sb.Len() > 0 {
		err := b.runRaw(sb, servers[0].BackendName)
		if err != nil {
			return err
		}
	}
	return nil
}

func (b *HaproxyConfMgrImpl) runRaw(sb strings.Builder, backendName string) error {
	runtime := b.haproxyClient.RuntimeClient()
	result, err := runtime.ExecuteRaw(sb.String())
	if err != nil {
		return err
	}
	if len(result) > 5 {
		switch result[0:4] {
		case "[3]:", "[2]:", "[1]:", "[0]:":
			b.logger.LogAttrs(context.Background(), slog.LevelError, "runtime update failed for "+backendName, logging.LogAttrBackendName(backendName))
			err := errors.New("runtime update failed for " + backendName)
			return err
		}
	}
	return nil
}

// runtimeDeleteServer deletes a single server from a backend via the runtime API.
func (b *HaproxyConfMgrImpl) runtimeDeleteServer(backendName, serverName string) error {
	runtime := b.haproxyClient.RuntimeClient()
	err := runtime.DeleteServer(backendName, serverName)
	if err != nil {
		return err
	}
	return nil
}

// runtimeCreateServers creates all servers added in serverDiffs via the runtime API.
// servers is the full server map used to look up each added server's configuration.
func (b *HaproxyConfMgrImpl) runtimeCreateServers(backendName string, serverDiffs ServerDiff, servers map[string]models.Server) error {
	// If RuntimeUpdateHaproxy is false, the gate library does not need runtime.Runtime and does not perform runtime updates.
	if !b.params.RuntimeUpdateHaproxy {
		return nil
	}

	if len(serverDiffs.added) != 0 {
		reload.Instance().AttemptDynamicServerStateUpdate("backend %s", backendName)
	}

	for serverName := range serverDiffs.added {
		be, ok := b.configuration.structured.Backends[backendName]
		if !ok {
			return fmt.Errorf("could not find backend %s", backendName)
		}
		server := servers[serverName]
		err := b.runtimeCreateServer(backendName, server, be.DefaultServer)
		if err != nil {
			reload.Instance().SetDynamicServerStateUpdateFailure("backend %s", backendName)
			b.logger.LogAttrs(context.Background(), slog.LevelError, "Runtime create of server state failed",
				logging.LogAttrError(err))
			return err
		}
		b.logger.LogAttrs(context.Background(), slog.LevelDebug, "Runtime create of server success",
			logging.LogAttrBackendName(backendName),
			logging.LogAttrServerName(server.Name))
	}
	return nil
}

// runtimeCreateServer will use the Runtime socket to create a Backend server
// It will first:
// - merge the server-defaults from defaults section and the server-defaults from the backend
// and serialize those options on the `add server` line
// Some options are not accepted on the `add server` line, in this situation the `add server` will return an error
// The runtime commands status are stored in the RuntimeUpdateTracker
func (b *HaproxyConfMgrImpl) runtimeCreateServer(backendName string, server models.Server, defaultServer *models.DefaultServer) (err error) {
	// haproxytech defaults from config
	haproxyTechDefaults, err := b.haproxyClient.DefaultsSectionGet(constants.DefaultsSectionName)
	if err != nil {
		return err
	}

	// runtime socket
	runtime := b.haproxyClient.RuntimeClient()

	// server
	var cmdBuilder strings.Builder
	cmdBuilder.WriteString(server.Address)
	if server.Port != nil {
		portS := strconv.FormatInt(*server.Port, 10)
		cmdBuilder.WriteString(":")
		cmdBuilder.WriteString(portS)
	}
	isServerDisabled := server.Maintenance == "enabled"

	if isServerDisabled {
		cmdBuilder.WriteString(" disabled")
	}

	// Merge default-server from "haproxytech" defaults and backend
	mergedDefaultServer := &models.DefaultServer{}
	if haproxyTechDefaults.DefaultServer != nil {
		err = mergo.MergeWithOverwrite(mergedDefaultServer, haproxyTechDefaults.DefaultServer)
		if err != nil {
			msg := "runtime - add server . Failed to merge with haproxytech defaults"
			b.logger.LogAttrs(context.Background(), slog.LevelError, msg, logging.LogAttrBackendName(backendName), logging.LogAttrServerName(server.Name))
		}
	}

	if defaultServer != nil {
		err = mergo.MergeWithOverwrite(mergedDefaultServer, defaultServer)
		if err != nil {
			msg := "runtime - add server . Failed to merge with backend default-server"
			b.logger.LogAttrs(context.Background(), slog.LevelError, msg, logging.LogAttrBackendName(backendName), logging.LogAttrServerName(server.Name))
		}
	}

	// default-server options
	if defaultServer != nil {
		// If some default options can not be set though the runtime API, fallback to writing the config and reload
		defaultServerOptions := serializeDefaultServerOptions(mergedDefaultServer)
		cmdBuilder.WriteString(" ")
		cmdBuilder.WriteString(defaultServerOptions)
	}

	command := cmdBuilder.String()
	b.logger.LogAttrs(context.Background(), slog.LevelInfo,
		"runtime - add server", logging.LogAttrBackendName(backendName), logging.LogAttrServerName(server.Name), logging.LogAttrCommand(command))
	if err := runtime.AddServer(backendName, server.Name, command); err != nil {
		return err
	}

	// explicit enable health
	// Use the "check" keyword to enable health-check support. Note that the
	// health-check is disabled by default and must be enabled independently from
	// the server using the "enable health" command
	if defaultServer != nil && defaultServer.Check == "enabled" {
		b.logger.LogAttrs(context.Background(), slog.LevelDebug,
			"runtime - enable health", logging.LogAttrBackendName(backendName), logging.LogAttrServerName(server.Name))
		if err := runtime.EnableServerHealth(backendName, server.Name); err != nil {
			return err
		}
	}

	// For agent checks, use the
	// "agent-check" keyword and the "enable agent" command
	if defaultServer != nil && defaultServer.AgentCheck == "enabled" {
		b.logger.LogAttrs(context.Background(), slog.LevelDebug,
			"runtime - enable agent", logging.LogAttrBackendName(backendName), logging.LogAttrServerName(server.Name))
		if err := runtime.EnableAgentCheck(backendName, server.Name); err != nil {
			return err
		}
	}

	if !isServerDisabled {
		b.logger.LogAttrs(context.Background(), slog.LevelDebug,
			"runtime - enable server", logging.LogAttrBackendName(backendName), logging.LogAttrServerName(server.Name))
		if err := runtime.EnableServer(backendName, server.Name); err != nil {
			return err
		}
	}

	return nil
}

func serializeDefaultServerOptions(s *models.DefaultServer) string {
	serverParams := configuration.SerializeServerParams(s.ServerParams, &cn_options.ConfigurationOptions{
		PreferredTimeSuffix: "d",
	})
	res := cp_params.ServerOptionsString(serverParams)
	return res
}
