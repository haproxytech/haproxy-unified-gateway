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
package configuration

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	v3 "github.com/haproxytech/haproxy-unified-gateway/api/gate/v3"
	defaults "github.com/haproxytech/haproxy-unified-gateway/fs/usr/local/hug"
	"github.com/haproxytech/haproxy-unified-gateway/hug/version"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/config"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	"github.com/peterbourgon/ff/v4"
	"github.com/peterbourgon/ff/v4/ffhelp"
)

//revive:disable:line-length-limit
type HUGConfig struct {
	LogSettings map[v3.Category]slog.Level
	haproxy.HaproxyDirs
	ControllerConfCRD        NamespaceNameValue `ff:"          long: hugconf-crd,                         usage: 'namespace/name of the HugConf CRD'"`
	GatewayNsName            NamespaceNameValue `ff:"          long: gateway-ns-name,                 usage: 'if specified, only watch the Gateway with this namespace/name (format: namespace/name), otherwise watch all Gateways'"`
	ControllerName           string             `ff:"          long: controller-name,                     usage: 'spec.controllerName' GatewayClass selector'"`
	IPV4BindAddr             string             `ff:"          long: ipv4-bind-address,                   usage: 'IPv4 address to bind to'"`
	IPV6BindAddr             string             `ff:"          long: ipv6-bind-address,	               usage: 'IPv6 address to bind to'"`
	LogType                  string             `ff:"          long: log-type,	      		               usage: 'sets up the log output type (possible values: text, json)"`
	JobGWAPI                 string             `ff:"          long: job-gwapi,                       usage: 'install Gateway API experimental CRDs for given version (e.g. 1.3.0) and exit'"`
	MetricsAuth              string             `ff:"          long: metrics-auth, default: none,     usage: 'metrics endpoint auth mode: none, kube-rbac, basic'"`
	MetricsBasicAuthUser     string             `ff:"          long: metrics-basic-auth-user,         usage: 'basic auth username for metrics endpoint'"`
	MetricsBasicAuthPassword string             `ff:"          long: metrics-basic-auth-password,     usage: 'basic auth password for metrics endpoint'"`
	External
	Namespaces                   CommaSeparatedValues `ff:"          long: namespaces,                      usage: 'comma separated list of namespaces that controller will monitor'"`
	StatsPort                    int64                `ff:"          long: stats-port, default: 1024,       usage: 'port to listen on for HAProxy stats'"`
	ControllerPort               int                  `ff:"          long: controller-port,                 usage: 'port to listen on for controller data: prometheus'"`
	SyncPeriod                   time.Duration        `ff:"          long: sync-period, default: 0,         usage: 'sets the period at which the controller computes HAProxy configuration file (e.g. 5s, 1m)'"`
	StartupSyncPeriod            time.Duration        `ff:"          long: startup-sync-period, default: 0, usage: 'sets the startup period at which the controller computes HAProxy configuration file (e.g. 5s, 1m)'"`
	CacheResyncPeriod            time.Duration        `ff:"          long: cache-resync-period, default: 0, usage: 'sets the controller-runtime manager cache SyncPeriod. If not set, defaults to controller-runtime defaults (10 hours)'"`
	DefaultLogLevel              slog.Level
	AddStatsPortToFrontend       bool `ff:"          long: add-stats-port,default: true,    usage: 'add stats port bind to existing stats frontend'"`
	ForceRestartHaproxyAtStartup bool `ff:"          long: force-restart-haproxy,           usage: 'forces HAProxy restart at controller startup'"`
	Help                         bool `ff:"          long: help,                            usage: 'help'"`
	Test                         bool `ff:"short:t,                                         usage: 'simulate running HAProxy'"`
	LeaderElectionEnabled        bool `ff:"          long: leader-election-enabled,         usage: 'enable leader election'"`
	UseWiths6Overlay             bool `ff:"          long: with-s6-overlay,                 usage: 'use s6 overlay to start/stop/restart HAProxy'"`
	UseWithPebble                bool `ff:"          long: with-pebble,                     usage: 'use pebble start/stop/restart HAProxy'"`
	DisableIPv4                  bool `ff:"          long: disable-ipv4,                    usage: 'disable IPv4 support'"`
	DisableIPv6                  bool `ff:"          long: disable-ipv6,			        usage: 'disable IPv6 support'"`
	Version                      bool `ff:"          long: version,                         usage: 'print version and exit'"`
	JobCheckCRD                  bool `ff:"          long: job-check-crd,                   usage: 'run CRD refresh job and exit'"`
}

//revive:enable:line-length-limit

type External struct {
	CfgDir        string `ff:"                     long: external-config-dir,     usage: 'path to HAProxy configuration directory.'"`
	HaproxyBinary string `ff:"                     long: external-haproxy-binary, usage: 'path to HAProxy binary.'"`
	RuntimeDir    string `ff:"                     long: external-runtime-dir,    usage: 'path to HAProxy runtime directory.'"`
	StateDir      string `ff:"                     long: external-state-dir,      usage: 'path to HAProxy state directory.'"`
	AuxDir        string `ff:"                     long: external-aux-dir,        usage: 'path to HAProxy aux directory.'"`
	External      bool   `ff:"short: e,            long: external,                usage: 'use as external Ingress Controller (out of k8s cluster).'"`
}

// NamespaceNameValue used to automatically distinct namespace/name string
type NamespaceNameValue struct {
	Namespace, Name string
}

func (nv *NamespaceNameValue) String() string {
	if nv == nil {
		return ""
	}
	return fmt.Sprintf("%s/%s", nv.Namespace, nv.Name)
}

func (nv *NamespaceNameValue) Set(s string) error {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return errors.New("invalid format: expected namespace/name")
	}
	nv.Namespace = parts[0]
	nv.Name = parts[1]
	return nil
}

type CommaSeparatedValues []string

func (c *CommaSeparatedValues) String() string {
	return strings.Join(*c, ",")
}

func (c *CommaSeparatedValues) Set(s string) error {
	if s == "" {
		*c = nil
		return nil
	}
	*c = strings.Split(s, ",")
	return nil
}

func Get() (HUGConfig, error) {
	configuration := HUGConfig{}

	external := External{}
	osArgsFF := ff.NewFlagSet("unified-kubernetes-gateway")
	err := osArgsFF.AddStruct(&configuration)
	if err != nil {
		return HUGConfig{}, err
	}

	err = osArgsFF.AddStruct(&external)
	if err != nil {
		return HUGConfig{}, err
	}

	err = ff.Parse(osArgsFF, os.Args[1:], ff.WithEnvVars())
	if err != nil {
		return HUGConfig{}, err
	}
	if configuration.Help {
		fmt.Println(ffhelp.Flags(osArgsFF))
		os.Exit(0) //revive:disable:deep-exit
	}
	if configuration.Version {
		version.PrintVersion()
		os.Exit(0)
	}
	// --------------
	// Init and apply defaults
	// --------------
	if err = configuration.Init(external); err != nil {
		return HUGConfig{}, err
	}

	return configuration, nil
}

func (c *HUGConfig) initExternal(external External) error {
	if external.External {
		externalDefaults := externalDefaults()
		if externalDefaults.HaproxyBinary != "" {
			if external.HaproxyBinary == "" {
				external.HaproxyBinary = externalDefaults.HaproxyBinary
			}
			if external.CfgDir == "" {
				external.CfgDir = externalDefaults.CfgDir
			}
			if external.RuntimeDir == "" {
				external.RuntimeDir = externalDefaults.RuntimeDir
			}
			if external.StateDir == "" {
				external.StateDir = externalDefaults.StateDir
			}
			if external.AuxDir == "" {
				external.AuxDir = filepath.Join(external.CfgDir, "aux")
			}
		}
		c.External = external
		c.HaproxyDirs.CfgDir = external.CfgDir
		c.HaproxyDirs.HaproxyBinary = external.HaproxyBinary
		c.HaproxyDirs.RuntimeDir = external.RuntimeDir
		c.HaproxyDirs.StateDir = external.StateDir
		c.HaproxyDirs.AuxDir = external.AuxDir
	}
	for _, dir := range []string{
		c.HaproxyDirs.CfgDir, c.HaproxyDirs.RuntimeDir,
		c.HaproxyDirs.StateDir, c.HaproxyDirs.AuxDir,
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func (c *HUGConfig) Init(external External) error {
	// --------------
	// Apply defaults
	// --------------
	c.HaproxyDirs = HaproxyDefaults()
	if c.ControllerPort == 0 {
		c.ControllerPort = defaultControllerPort
	}
	if c.LogType == "" {
		c.LogType = string(logging.LogHandlerTypeJSON)
	}

	// values for external
	err := c.initExternal(external)
	if err != nil {
		return err
	}

	for _, dir := range []string{
		c.HaproxyDirs.CfgDir, c.HaproxyDirs.RuntimeDir,
		c.HaproxyDirs.StateDir, c.HaproxyDirs.AuxDir,
	} {
		if dir == "" {
			return fmt.Errorf("failed to init controller config: missing config directories [%s]", dir)
		}
	}

	// Log levels
	if len(c.LogSettings) == 0 {
		c.LogSettings = map[v3.Category]slog.Level{
			logging.LogCategoryK8s:          slog.LevelError,
			logging.LogCategoryGate:         slog.LevelInfo,
			logging.LogCategoryStatus:       slog.LevelInfo,
			logging.LogCategoryBatch:        slog.LevelError,
			logging.LogCategoryApp:          slog.LevelInfo,
			logging.LogCategoryCertsStorage: slog.LevelInfo,
		}
	}

	// Binary and main files
	c.MainCfgFile = filepath.Join(c.HaproxyDirs.CfgDir, "haproxy.cfg")
	c.RouteLuaFile = filepath.Join(c.HaproxyDirs.CfgDir, "route.lua")
	c.PIDFile = filepath.Join(c.HaproxyDirs.RuntimeDir, "haproxy.pid")
	c.RuntimeSocket = filepath.Join(c.HaproxyDirs.RuntimeDir, "haproxy-runtime-api.sock")
	c.MasterSocket = filepath.Join(c.HaproxyDirs.RuntimeDir, "haproxy-master.sock")
	if c.Test {
		c.HaproxyDirs.HaproxyBinary = "echo"
		c.RuntimeSocket = ""
		c.MasterSocket = ""
	} else if _, err = os.Stat(c.HaproxyDirs.HaproxyBinary); err != nil {
		return err
	}

	// Create haproxy.cfg if not exists
	_, err = os.Stat(c.MainCfgFile)
	if os.IsNotExist(err) {
		defaultCfg := defaults.HaproxyCfg
		err = os.WriteFile(c.MainCfgFile, []byte(defaultCfg), 0o644)
		if err != nil {
			return err
		}
	} else if err != nil {
		// Handle other potential errors, like permission denied.
		return err
	}
	// Create route.lua if not exists
	_, err = os.Stat(c.RouteLuaFile)
	if os.IsNotExist(err) {
		defaultRouteLua := defaults.RouteLua
		err = os.WriteFile(c.RouteLuaFile, []byte(defaultRouteLua), 0o644)
		if err != nil {
			return err
		}
	} else if err != nil {
		// Handle other potential errors, like permission denied.
		return err
	}

	// Directories
	c.CertsDir = filepath.Join(c.HaproxyDirs.CfgDir, config.DefaultCertsDirName)
	c.CertListDir = filepath.Join(c.HaproxyDirs.CfgDir, config.DefaultCertFilesDirName)
	c.MapsDir = filepath.Join(c.HaproxyDirs.CfgDir, config.DefaultMapsDirName)
	c.PatternDir = filepath.Join(c.HaproxyDirs.CfgDir, config.DefaultPatternDirName)
	c.ErrFileDir = filepath.Join(c.HaproxyDirs.CfgDir, config.DefaultErrFilesDirName)
	for _, d := range []string{
		c.CertsDir,
		c.CertListDir,
		c.MapsDir,
		c.ErrFileDir,
		c.HaproxyDirs.StateDir,
		c.PatternDir,
	} {
		err = os.MkdirAll(d, 0o755)
		if err != nil {
			return err
		}
	}
	return nil
}
