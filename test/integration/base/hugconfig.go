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

package base

import (
	"log/slog"
	"os"
	"path"
	"testing"
	"time"

	v3 "github.com/haproxytech/haproxy-unified-gateway/api/gate/v3"
	hugconfig "github.com/haproxytech/haproxy-unified-gateway/hug/configuration"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
)

const TestControllerName = "gate.haproxy.org/hug"

func hugConfig(test *IntTest, t *testing.T) hugconfig.HUGConfig {
	cfgDir := os.Getenv("HAPROXY_CFG_DIR")
	if cfgDir == "" {
		tmpDir := os.TempDir()
		cfgDir = path.Join(tmpDir, "hug", test.Namespace)
		t.Logf("Haproxy config path: %s", cfgDir)
	}

	haproxyBinDir := os.Getenv("HAPROXY_BIN")
	external := hugconfig.External{
		External:      true,
		CfgDir:        cfgDir,
		HaproxyBinary: haproxyBinDir,
		RuntimeDir:    cfgDir,
		StateDir:      cfgDir,
	}

	hconfig := hugconfig.HUGConfig{
		SyncPeriod:        time.Second,
		StartupSyncPeriod: 2 * time.Second,
		ControllerConfCRD: hugconfig.NamespaceNameValue{Name: hugConfNsName.Name, Namespace: hugConfNsName.Namespace},
		ControllerName:    TestControllerName,
		Namespaces:        []string{test.Namespace, "other", hugConfNsName.Namespace},
		LogType:           string(logging.LogHandlerTypeText),
		DefaultLogLevel:   slog.LevelDebug,
		LogSettings: map[v3.Category]slog.Level{
			logging.LogCategoryK8s:           slog.LevelInfo,
			logging.LogCategoryGate:          slog.LevelDebug,
			logging.LogCategoryStatus:        slog.LevelDebug,
			logging.LogCategoryBatch:         slog.LevelInfo,
			logging.LogCategoryApp:           slog.LevelDebug,
			logging.LogCategoryCertsStorage:  slog.LevelDebug,
			logging.LogCategoryHaproxyCfgMgr: slog.LevelDebug,
			logging.LogCategoryHugService:    slog.LevelDebug,
			logging.LogCategoryMapsStorage:   slog.LevelInfo,
		},
	}

	_ = hconfig.Init(external)
	return hconfig
}
