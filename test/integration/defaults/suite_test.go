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

package defaults

import (
	"fmt"
	"testing"
	"time"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/haproxytech/haproxy-unified-gateway/test/integration/base"
	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"
	v1 "k8s.io/api/core/v1"

	"github.com/stretchr/testify/suite"
)

const (
	timeout  = time.Second * 15
	interval = time.Second * 1

	// hugConfNamespace is the namespace where CRs must reside.
	// It must match the controller configuration in base/inttest.go (hugConfNsName.Namespace = "test").
	hugConfNamespace = "test"

	// defaultConnectTimeout is the connect_timeout value set in default_defaults.yaml.
	defaultConnectTimeout = int64(5000)
)

type DefaultsSuite struct {
	base.BaseSuite
}

func TestDefaultsSuite(t *testing.T) {
	suite.Run(t, new(DefaultsSuite))
}

func (s *DefaultsSuite) SetupSuite() {
	s.BaseSuite.SetupSuite("", 0)

	// Create the "test" namespace required for CRs.
	err := utils.CreateRuntimeObject(s.Test().Ctx, s.Test().Client, &v1.Namespace{
		Name: hugConfNamespace,
	}, true)
	s.Require().NoError(err)
}

func (s *DefaultsSuite) TearDownSuite() {
	_ = utils.DeleteRuntimeObject(s.Test().Ctx, s.Test().Client, &v1.Namespace{
		Name: hugConfNamespace,
	}, false)
	s.BaseSuite.TearDownSuite()
}

// expectConnectTimeout waits until HAProxy's active defaults section connect_timeout matches the expected value.
func (s *DefaultsSuite) expectConnectTimeout(expected int64) {
	s.Eventually(func() bool {
		defaults, err := s.Test().HaproxyClient.DefaultsSectionGet("haproxytech")
		if err != nil || defaults == nil || defaults.ConnectTimeout == nil {
			return false
		}
		return *defaults.ConnectTimeout == expected
	}, timeout, interval, fmt.Sprintf("expected connect_timeout %d", expected))
}

// expectLogTargetCount waits until HAProxy's active defaults section has exactly n log targets.
func (s *DefaultsSuite) expectLogTargetCount(n int) {
	var got models.LogTargets
	s.Eventually(func() bool {
		defaults, err := s.Test().HaproxyClient.DefaultsSectionGet("haproxytech")
		if err != nil || defaults == nil {
			return false
		}
		got = defaults.LogTargetList

		return len(defaults.LogTargetList) == n
	}, timeout, interval, fmt.Sprintf("expected %d log target(s)\n got %v", n, got))
}

// expectACLCount waits until HAProxy's active defaults section has exactly n ACLs.
func (s *DefaultsSuite) expectACLCount(n int) {
	var got models.Acls
	s.Eventually(func() bool {
		defaults, err := s.Test().HaproxyClient.DefaultsSectionGet("haproxytech")
		if err != nil || defaults == nil {
			return false
		}
		got = defaults.ACLList
		return len(defaults.ACLList) == n
	}, timeout, interval, fmt.Sprintf("expected %d ACL(s)\n got %v", n, got))
}

// expectHTTPErrorRuleCount waits until HAProxy's active defaults section has exactly n HTTP error rules.
func (s *DefaultsSuite) expectHTTPErrorRuleCount(n int) {
	var got models.HTTPErrorRules
	s.Eventually(func() bool {
		defaults, err := s.Test().HaproxyClient.DefaultsSectionGet("haproxytech")
		if err != nil || defaults == nil {
			return false
		}
		got = defaults.HTTPErrorRuleList
		return len(defaults.HTTPErrorRuleList) == n
	}, timeout, interval, fmt.Sprintf("expected %d HTTP error rule(s)\n got %v", n, got))
}
