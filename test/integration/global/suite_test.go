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

package global

import (
	"fmt"
	"testing"
	"time"

	"github.com/haproxytech/haproxy-unified-gateway/test/integration/base"
	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"
	v1 "k8s.io/api/core/v1"

	"github.com/stretchr/testify/suite"
)

const (
	timeout  = time.Second * 15
	interval = time.Second * 1

	// hugConfNamespace is the namespace where HugConf must reside.
	// It must match the controller configuration in base/inttest.go (hugConfNsName.Namespace = "test").
	hugConfNamespace = "test"

	// defaultMaxconn is the maxconn value set in the default HAProxy global config.
	defaultMaxconn = int64(32000)
)

type GlobalSuite struct {
	base.BaseSuite
}

func TestGlobalSuite(t *testing.T) {
	suite.Run(t, new(GlobalSuite))
}

func (s *GlobalSuite) SetupSuite() {
	s.BaseSuite.SetupSuite("", 0)

	// Create the "test" namespace required for HugConf.
	// The controller is configured to watch HugConf at namespace "test", name "hugconf".
	err := utils.CreateRuntimeObject(s.Test().Ctx, s.Test().Client, &v1.Namespace{
		Name: hugConfNamespace,
	}, true)
	s.Require().NoError(err)
}

func (s *GlobalSuite) TearDownSuite() {
	_ = utils.DeleteRuntimeObject(s.Test().Ctx, s.Test().Client, &v1.Namespace{
		Name: hugConfNamespace,
	}, false)
	s.BaseSuite.TearDownSuite()
}

// expectMaxconn waits until HAProxy's active global maxconn matches the expected value.
func (s *GlobalSuite) expectMaxconn(expected int64) {
	s.Eventually(func() bool {
		global, err := s.Test().HaproxyClient.GlobalGet()
		if err != nil {
			return false
		}
		if global.PerformanceOptions == nil {
			return false
		}
		return global.PerformanceOptions.Maxconn == expected
	}, timeout, interval, fmt.Sprintf("expected maxconn %d", expected))
}

// expectLogTargetCount waits until HAProxy's active global has exactly n log targets.
func (s *GlobalSuite) expectLogTargetCount(n int) {
	s.Eventually(func() bool {
		global, err := s.Test().HaproxyClient.GlobalGet()
		if err != nil {
			return false
		}
		return len(global.LogTargetList) == n
	}, timeout, interval, fmt.Sprintf("expected %d log target(s)", n))
}
