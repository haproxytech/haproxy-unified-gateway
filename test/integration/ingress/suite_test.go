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

package ingress

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/haproxytech/haproxy-unified-gateway/test/integration/base"
	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"
	networkingv1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	timeout  = 30 * time.Second
	interval = 1 * time.Second
)

type IngressSuite struct {
	base.BaseSuite
}

func (s *IngressSuite) SetupSuite() {
	s.BaseSuite.SetupSuite("", 0, func(t *base.IntTest) { t.EnableIngress = true })
}

func (s *IngressSuite) TearDownSuite() {
	s.BaseSuite.TearDownSuite()
}

// expectBackendExists waits until a backend with the given name is present in
// the running HAProxy configuration. An Ingress that is translated into a
// synthetic HTTPRoute produces the backend of its referenced Service, so its
// presence proves the Ingress was accepted and wired through the pipeline.
func (s *IngressSuite) expectBackendExists(name string) {
	if !utils.WaitFor(s.Test().Ctx, interval, timeout, func() bool {
		backends, err := s.Test().HaproxyClient.BackendsGet()
		if err != nil {
			return false
		}
		for _, be := range backends {
			if be.Name == name {
				return true
			}
		}
		return false
	}) {
		s.T().Fatalf("expected backend %q to exist", name)
	}
}

// expectBackendByPrefix waits until a backend whose name starts with prefix
// exists and returns it. It is used when the backend name carries a filter hash
// that cannot be predicted (e.g. an Ingress with a cr-backend ExtensionRef).
func (s *IngressSuite) expectBackendByPrefix(prefix string) *models.Backend {
	var found *models.Backend
	if !utils.WaitFor(s.Test().Ctx, interval, timeout, func() bool {
		backends, err := s.Test().HaproxyClient.BackendsGet()
		if err != nil {
			return false
		}
		for _, be := range backends {
			if strings.HasPrefix(be.Name, prefix) {
				found = be
				return true
			}
		}
		return false
	}) {
		s.T().Fatalf("expected a backend with prefix %q to exist", prefix)
	}
	return found
}

// expectIngressLBHostname waits until the named Ingress's
// status.loadBalancer.ingress advertises the given hostname.
func (s *IngressSuite) expectIngressLBHostname(name, hostname string) {
	if !utils.WaitFor(s.Test().Ctx, interval, timeout, func() bool {
		var ing networkingv1.Ingress
		if err := s.Test().Client.Get(s.Test().Ctx, client.ObjectKey{Namespace: s.Test().Namespace, Name: name}, &ing); err != nil {
			return false
		}
		for _, lb := range ing.Status.LoadBalancer.Ingress {
			if lb.Hostname == hostname {
				return true
			}
		}
		return false
	}) {
		s.T().Fatalf("expected Ingress %q LoadBalancer status to advertise hostname %q", name, hostname)
	}
}

// mapFileContains reports whether any line of the given map file contains the
// value (a path or a host). It matches on the value as a line substring rather
// than the full "key value" line, so it does not depend on the backend's
// filter-hash name.
func (s *IngressSuite) mapFileContains(mapRelPath, value string) bool {
	lines, err := s.GetMapFileFrom(mapRelPath)
	if err != nil {
		return false
	}
	for _, l := range lines {
		if strings.Contains(l, value) {
			return true
		}
	}
	return false
}

// expectMapFileContains waits until the given map file has a line containing the
// value.
func (s *IngressSuite) expectMapFileContains(mapRelPath, value string) {
	if !utils.WaitFor(s.Test().Ctx, interval, timeout, func() bool {
		return s.mapFileContains(mapRelPath, value)
	}) {
		s.T().Fatalf("expected map %q to contain %q", mapRelPath, value)
	}
}

// expectMapFileExists waits until the given map file exists (readable), which
// implies its frontend has been programmed.
func (s *IngressSuite) expectMapFileExists(mapRelPath string) {
	if !utils.WaitFor(s.Test().Ctx, interval, timeout, func() bool {
		_, err := s.GetMapFileFrom(mapRelPath)
		return err == nil
	}) {
		s.T().Fatalf("expected map file %q to exist", mapRelPath)
	}
}

// expectMapDirAbsent asserts that no maps directory with the given name exists.
// It guards against an empty-named frontend ("hug_"), which appeared when a
// synthetic listener with no virtual listener name was written to.
func (s *IngressSuite) expectMapDirAbsent(dirName string) {
	dir := filepath.Join(s.Test().HaproxyCfgDir, "maps", dirName)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		s.T().Fatalf("expected maps directory %q to be absent, stat err=%v", dir, err)
	}
}

// expectBackendServerTimeout waits until the named backend exists and carries the
// given server timeout. It is used to observe a Service-level Backend CR merge:
// the built backend defaults server_timeout to 50000, so a distinct value proves
// the CR was merged into the (ingress-origin) backend.
func (s *IngressSuite) expectBackendServerTimeout(name string, want int64) {
	if !utils.WaitFor(s.Test().Ctx, interval, timeout, func() bool {
		backends, err := s.Test().HaproxyClient.BackendsGet()
		if err != nil {
			return false
		}
		for _, be := range backends {
			if be.Name == name {
				return be.ServerTimeout != nil && *be.ServerTimeout == want
			}
		}
		return false
	}) {
		s.T().Fatalf("expected backend %q to have server timeout %d", name, want)
	}
}
