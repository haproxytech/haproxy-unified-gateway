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
package config

import (
	"log/slog"
	"os"
	"time"

	v3 "github.com/haproxytech/haproxy-unified-gateway/api/gate/v3"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/diffs"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/structured"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"

	"github.com/lmittmann/tint"
	"k8s.io/apimachinery/pkg/types"
)

// func getConfig() (*rest.Config, error) {
// 	kubeconfig := os.Getenv("PROBER_KUBECONFIG")
// 	if len(kubeconfig) > 0 {
// 		return clientcmd.BuildConfigFromFlags("", kubeconfig)
// 	}

// 	kubeconfig = os.Getenv("KUBECONFIG")
// 	if len(kubeconfig) > 0 {
// 		return clientcmd.BuildConfigFromFlags("", kubeconfig)
// 	}

// 	return config.GetConfig()
// }

type GateConfigOptions []func(c *Configuration) error

type Configuration struct {
	// Initial Structured config used at startup
	InitialStructuredHaproxyConf structured.Structured
	Logger                       *slog.Logger
	LogHandler                   *logging.CategoryFilterHandler
	// TransferHaproxyConfChannel is used to send the HaproxyConfDiffs to the application
	TransferHaproxyConfChannel chan diffs.HaproxyConfDiffs
	// MetricsConfig specifies the metrics config.
	MetricsConfig MetricsConfig
	// ControllerPodConfig contains information about this Pod.
	ControllerPodConfig ControllerPodConfig
	//  Namespace and name of the controller conf CRD:  HugConf
	HugConfCRD types.NamespacedName
	// LogHandlerType defines the type of log Handler we want: json or text
	// Default will be json
	LogHandlerType logging.LogHandlerType
	Kubeconfig     string
	// ControllerName is the name of this controller.
	ControllerName string
	// LeaderElectionConfig contains the configuration for leader election.
	LeaderElectionConfig LeaderElectionConfig
	// Namespaces is a list of namespaces to watch.
	// If empty, all namespaces are watched.
	Namespaces []string
	// HaproxyConfParams contains the needed configuration to compute the FE/BE/...
	HaproxyParams haproxy.HaproxyConfParams
	// SyncPeriod is the duration we wait after handling one batch before the next one
	SyncPeriod time.Duration
	// StartupSyncPeriod is the first (at startup) duration we wait after handling one batch before the next one
	// After the first one, SyncPeriod will be used
	StartupSyncPeriod time.Duration
	// CacheResyncPeriod is the manager cache SyncPeriod
	// SyncPeriod determines the minimum frequency at which watched resources are
	// reconciled. A lower period will correct entropy more quickly, but reduce
	// responsiveness to change if there are many watched resources. Change this
	// value only if you know what you are doing. Defaults to 10 hours if unset.
	// there will a 10 percent jitter between the SyncPeriod of all controllers
	// so that all controllers will not send list requests simultaneously.
	//
	// This applies to all controllers.
	CacheResyncPeriod time.Duration
	// InitialStructuredHaproxyConfOK bool
	InitialStructuredHaproxyConfOK bool
}

// ControllerPodConfig contains information about this Pod.
type ControllerPodConfig struct {
	// PodIP is the IP address of this Pod.
	PodIP string
	// Namespace is the namespace of this Pod.
	Namespace string
	// Name is the name of the Pod.
	Name string
}

// MetricsAuthMode defines the authentication mode for the metrics endpoint.
type MetricsAuthMode string

const (
	MetricsAuthNone     MetricsAuthMode = "none"
	MetricsAuthKubeRBAC MetricsAuthMode = "kube-rbac"
	MetricsAuthBasic    MetricsAuthMode = "basic"
)

// MetricsConfig specifies the metrics config.
type MetricsConfig struct {
	// AuthMode is the authentication mode for the metrics endpoint.
	AuthMode MetricsAuthMode
	// BasicAuthUser is the username for basic auth (when AuthMode is "basic").
	BasicAuthUser string
	// BasicAuthPassword is the password for basic auth (when AuthMode is "basic").
	BasicAuthPassword string
	// Port is the port the metrics should be exposed on.
	Port int
	// Enabled is the flag for toggling metrics on or off.
	Enabled bool
	// Secure is the flag for toggling the metrics endpoint to https.
	Secure bool
}

// LeaderElectionConfig contains the configuration for leader election.
type LeaderElectionConfig struct {
	// LockName holds the name of the leader election lock.
	LockName string
	// Enabled indicates whether leader election is enabled.
	Enabled bool
}

func NewBaseLogger(handlerType logging.LogHandlerType, defaultLevel slog.Level, categoryLevels map[v3.Category]slog.Level) (*slog.Logger, *logging.CategoryFilterHandler) {
	var baseHandler slog.Handler
	switch handlerType {
	case logging.LogHandlerTypeJSON:
		baseHandler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: defaultLevel,
			// AddSource: true,
		})
	case logging.LogHandlerTypeText:
		baseHandler = tint.NewHandler(os.Stdout, &tint.Options{
			Level:   slog.LevelDebug,
			NoColor: os.Getenv("NO_COLOR") != "",
			// TimeFormat: time.Kitchen,
			ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
				if a.Key == logging.LogCategoryKey || a.Key == "GVK" {
					return tint.Attr(6, a)
				}
				return a
			},
		})
	}

	handlerParams := logging.CategoryFilterHandlerParams{
		Base:                  baseHandler,
		DefaultLevel:          defaultLevel,
		DefaultCategoryLevels: categoryLevels,
		CategoryKey:           logging.LogCategoryKey,
	}
	handler := logging.NewCategoryFilterHandler(handlerParams)
	slogLogger := slog.New(handler)

	return slogLogger, handler
}
