// Copyright 2026 HAProxy Technologies LLC
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

package process

import (
	"log/slog"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/haproxytech/haproxy-unified-gateway/hug/configuration"
	"github.com/haproxytech/haproxy-unified-gateway/hug/haproxy/params"
)

func TestMain(m *testing.M) {
	masterSocketRetryBudget = 10 * time.Millisecond
	m.Run()
}

func TestNewSelectsGopherdControl(t *testing.T) {
	p := New(params.Params{UseWithGopherd: true, Test: true}, nil, slog.Default())
	if _, ok := p.(*goInitControl); !ok {
		t.Fatalf("expected *goInitControl, got %T", p)
	}
}

func TestNewDefaultsToDirectControl(t *testing.T) {
	p := New(params.Params{Test: true}, nil, slog.Default())
	if _, ok := p.(*directControl); !ok {
		t.Fatalf("expected *directControl, got %T", p)
	}
}

func TestGoInitControlServiceNoOps(t *testing.T) {
	c := newGoInitControl(nil, params.Params{}, slog.Default())

	for _, action := range []string{"start", "stop"} {
		msg, err := c.Service(action)
		if err != nil || msg != "" {
			t.Fatalf("Service(%q) = %q, %v; want no-op", action, msg, err)
		}
	}

	if _, err := c.Service("frobnicate"); err == nil {
		t.Fatal("Service with unknown action should error")
	}
}

func TestGoInitControlServiceTestMode(t *testing.T) {
	c := newGoInitControl(nil, params.Params{Test: true}, slog.Default())
	msg, err := c.Service("reload")
	if err != nil || msg != "" {
		t.Fatalf("Service(reload) in test mode = %q, %v; want no-op", msg, err)
	}
}

// Pins the contracts between this package, the CLI flag, and the shipped
// gopherd config.
func TestGopherdConfigContracts(t *testing.T) {
	yml, err := os.ReadFile("../../../fs/etc/gopherd/gopherd.yml")
	if err != nil {
		t.Fatalf("reading gopherd.yml: %v", err)
	}
	cfg := string(yml)

	if !strings.Contains(cfg, MASTER_SOCKET_PATH+",level,admin") {
		t.Errorf("gopherd.yml does not bind haproxy -S to %s", MASTER_SOCKET_PATH)
	}
	if !strings.Contains(cfg, `"--with-gopherd"`) {
		t.Error("gopherd.yml does not pass --with-gopherd to hug")
	}

	field, ok := reflect.TypeFor[configuration.HUGConfig]().FieldByName("UseWithGopherd")
	if !ok {
		t.Fatal("HUGConfig has no UseWithGopherd field")
	}
	if !strings.Contains(field.Tag.Get("ff"), "long: with-gopherd") {
		t.Error("UseWithGopherd flag tag does not define --with-gopherd")
	}
}
