// Copyright 2026 HAProxy Technologies LLC
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
	"os"
	"strings"
	"testing"

	"github.com/peterbourgon/ff/v4"
)

// parseFlags mirrors Get's flag set so tests can drive parsing with explicit args.
func parseFlags(t *testing.T, args []string) HUGConfig {
	t.Helper()
	cfg := HUGConfig{}
	external := External{}
	fs := ff.NewFlagSet("unified-kubernetes-gateway")
	if err := fs.AddStruct(&cfg); err != nil {
		t.Fatalf("adding HUGConfig flags: %v", err)
	}
	if err := fs.AddStruct(&external); err != nil {
		t.Fatalf("adding External flags: %v", err)
	}
	if err := ff.Parse(fs, args, ff.WithEnvVars()); err != nil {
		t.Fatalf("parsing args %v: %v", args, err)
	}
	return cfg
}

func TestGWAPICompatDefault(t *testing.T) {
	// An empty-but-set variable is a parse error; only unset means default.
	if err := os.Unsetenv("GWAPI_COMPAT"); err != nil {
		t.Fatalf("unsetting GWAPI_COMPAT: %v", err)
	}
	if cfg := parseFlags(t, nil); cfg.GWAPICompat {
		t.Error("GWAPICompat must default to false")
	}
}

func TestGWAPICompatEnv(t *testing.T) {
	for _, value := range []string{"1", "true", "TRUE", "True"} {
		t.Run("env="+value, func(t *testing.T) {
			t.Setenv("GWAPI_COMPAT", value)
			if cfg := parseFlags(t, nil); !cfg.GWAPICompat {
				t.Errorf("GWAPI_COMPAT=%q must set GWAPICompat=true", value)
			}
		})
	}
	for _, value := range []string{"0", "false", "FALSE", "False"} {
		t.Run("env="+value, func(t *testing.T) {
			t.Setenv("GWAPI_COMPAT", value)
			if cfg := parseFlags(t, nil); cfg.GWAPICompat {
				t.Errorf("GWAPI_COMPAT=%q must set GWAPICompat=false", value)
			}
		})
	}
}

func TestGWAPICompatCLI(t *testing.T) {
	t.Setenv("GWAPI_COMPAT", "1")
	for _, args := range [][]string{
		{"--gwapi-compat=false"},
		{"--gwapi-compat=0"},
		{"--gwapi-compat", "false"},
	} {
		t.Run("args="+args[0], func(t *testing.T) {
			if cfg := parseFlags(t, args); cfg.GWAPICompat {
				t.Errorf("args %v must override GWAPI_COMPAT=1 to false", args)
			}
		})
	}
	for _, args := range [][]string{
		{"--gwapi-compat"},
		{"--gwapi-compat=true"},
	} {
		t.Run("args="+args[0], func(t *testing.T) {
			if cfg := parseFlags(t, args); !cfg.GWAPICompat {
				t.Errorf("args %v must set GWAPICompat=true", args)
			}
		})
	}
}

// An upgrade reuses the configuration files an earlier release wrote, so the
// pieces TCPRoute needs are appended when missing, once.
func TestEnsureConfigAdditions(t *testing.T) {
	dir := t.TempDir()
	cfg := HUGConfig{
		MainCfgFile:  dir + "/haproxy.cfg",
		RouteLuaFile: dir + "/route.lua",
	}

	// Files as written by the previous release: neither addition present.
	oldCfg := "defaults haproxytech\n  mode tcp\n\nbackend backend_not_found from haproxytech\n  mode http\n"
	if err := os.WriteFile(cfg.MainCfgFile, []byte(oldCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	oldLua := "function route(txn)\nend\ncore.register_action(\"route\", { \"http-req\" }, route)\n"
	if err := os.WriteFile(cfg.RouteLuaFile, []byte(oldLua), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := cfg.ensureConfigAdditions(); err != nil {
		t.Fatalf("ensureConfigAdditions: %v", err)
	}

	mainCfg, err := os.ReadFile(cfg.MainCfgFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mainCfg), tcpNotFoundBackendMarker) {
		t.Errorf("fallback backend not appended:\n%s", mainCfg)
	}

	routeLua, err := os.ReadFile(cfg.RouteLuaFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(routeLua), tcpRouteRegistrationMarker) {
		t.Errorf("tcp-req registration not appended:\n%s", routeLua)
	}

	// A second run must not duplicate anything.
	if err := cfg.ensureConfigAdditions(); err != nil {
		t.Fatalf("second ensureConfigAdditions: %v", err)
	}
	mainCfgAgain, err := os.ReadFile(cfg.MainCfgFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(mainCfg) != string(mainCfgAgain) {
		t.Errorf("second run changed the file:\n%s", mainCfgAgain)
	}
}
