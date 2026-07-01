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
	"time"

	"github.com/haproxytech/haproxy-unified-gateway/test/integration/base"
	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"
)

const (
	timeout  = 30 * time.Second
	interval = 1 * time.Second
)

type IngressSuite struct {
	base.BaseSuite
}

func (s *IngressSuite) SetupSuite() {
	s.BaseSuite.SetupSuite("", 0)
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
