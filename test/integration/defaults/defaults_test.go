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
	"path"
	"time"

	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"
)

// Test_Defaults_Override creates a Defaults CR with spec.name "haproxytech" and merge_strategy: override
// and a custom connect_timeout, and verifies that HAProxy picks up the configured value.
func (s *DefaultsSuite) Test_Defaults_Override() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "basic-override")
	s.CreateFixturesInNamespace(fixturePath, hugConfNamespace, nil)
	defer s.CleanupFixturesInNamespace(fixturePath, hugConfNamespace, nil)

	s.expectConnectTimeout(10000)
}

// Test_Defaults_Append creates a Defaults CR with spec.name "haproxytech" and merge_strategy: append
// and a custom connect_timeout. Scalar fields behave the same as override, so the configured value is applied.
func (s *DefaultsSuite) Test_Defaults_Append() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "basic-append")
	s.CreateFixturesInNamespace(fixturePath, hugConfNamespace, nil)
	defer s.CleanupFixturesInNamespace(fixturePath, hugConfNamespace, nil)

	s.expectConnectTimeout(10000)
}

// Test_Defaults_Mode_Override_Replaces_LogTargets verifies that with merge_strategy: override
// the log_target_list from the Defaults CR fully replaces the default list.
// Default has 1 entry (global: true); CR provides 1 custom entry → result is exactly 1 entry.
func (s *DefaultsSuite) Test_Defaults_Mode_Override_Replaces_LogTargets() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "mode-override")
	s.CreateFixturesInNamespace(fixturePath, hugConfNamespace, nil)
	defer s.CleanupFixturesInNamespace(fixturePath, hugConfNamespace, nil)

	s.expectLogTargetCount(1)
}

// Test_Defaults_Mode_Append_Extends_LogTargets verifies that with merge_strategy: append
// the log_target_list from the Defaults CR is appended to the default list.
// Default has 1 entry (global: true); CR adds 1 custom entry → result is 2 entries.
func (s *DefaultsSuite) Test_Defaults_Mode_Append_Extends_LogTargets() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "mode-append")
	s.CreateFixturesInNamespace(fixturePath, hugConfNamespace, nil)
	defer s.CleanupFixturesInNamespace(fixturePath, hugConfNamespace, nil)

	s.expectLogTargetCount(2)
}

// Test_Defaults_Dynamic_Update verifies that updating a Defaults CR causes HAProxy to
// pick up the new configuration.
// Step 1: create Defaults v1 (connect_timeout 10000), verify applied.
// Step 2: update to Defaults v2 (connect_timeout 20000), verify updated.
func (s *DefaultsSuite) Test_Defaults_Dynamic_Update() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "dynamic-update")

	s.CreateFixturesInNamespace(fixturePath, hugConfNamespace, []string{"defaults-v1.yaml"})
	defer s.CleanupFixturesInNamespace(fixturePath, hugConfNamespace, []string{"defaults-v1.yaml"})

	s.expectConnectTimeout(10000)

	// Applying defaults-v2.yaml updates the existing Defaults CR (same name) to connect_timeout 20000.
	s.CreateFixturesInNamespace(fixturePath, hugConfNamespace, []string{"defaults-v2.yaml"})
	// Nothing extra to cleanup: defaults-v2.yaml references the same CR, cleaned with the previous defer.

	s.expectConnectTimeout(20000)
}

// Test_Defaults_Delete_CR verifies that deleting the Defaults CR causes HAProxy to revert
// to the default defaults configuration.
// Step 1: create Defaults CR (connect_timeout 10000), verify applied.
// Step 2: delete Defaults CR, verify HAProxy reverts to default connect_timeout (5000).
func (s *DefaultsSuite) Test_Defaults_Delete_CR() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "delete")

	s.CreateFixturesInNamespace(fixturePath, hugConfNamespace, []string{"defaults.yaml"})

	s.expectConnectTimeout(10000)

	// Delete the Defaults CR; the controller marks it as deleted and resets to default values.
	s.CleanupFixturesInNamespace(fixturePath, hugConfNamespace, []string{"defaults.yaml"})

	// HAProxy should revert to the default connect_timeout.
	s.expectConnectTimeout(defaultConnectTimeout)
}

// Test_Defaults_ACLs_And_HTTPErrorRules creates a Defaults CR with two ACLs and two HTTP error
// rules, and verifies that HAProxy picks up both lists.
//
//   - acl is_internal: matches source IPs in 192.168.0.0/16
//   - acl is_health:   matches path /healthz
//   - http-error status 503
//   - http-error status 404
func (s *DefaultsSuite) Test_Defaults_ACLs_And_HTTPErrorRules() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "acls-and-http-errors")
	s.CreateFixturesInNamespace(fixturePath, hugConfNamespace, nil)
	defer s.CleanupFixturesInNamespace(fixturePath, hugConfNamespace, nil)

	s.expectACLCount(2)
	s.expectHTTPErrorRuleCount(2)
}

// Test_Defaults_Ignored_Name verifies that a Defaults CR with a spec.name other than "haproxytech"
// is discarded by the controller and does not affect the HAProxy defaults section.
func (s *DefaultsSuite) Test_Defaults_Ignored_Name() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "ignored-name")
	s.CreateFixturesInNamespace(fixturePath, hugConfNamespace, nil)
	defer s.CleanupFixturesInNamespace(fixturePath, hugConfNamespace, nil)

	// The CR sets connect_timeout 10000, but it must be ignored because its spec.name is not "haproxytech".
	// The connect_timeout must never change to the value specified in the ignored CR.
	neverTimeout := 5 * time.Second
	s.Never(func() bool {
		defaults, err := s.IntTest().HaproxyClient.DefaultsSectionGet("haproxytech")
		if err != nil || defaults == nil || defaults.ConnectTimeout == nil {
			return false
		}
		return *defaults.ConnectTimeout == 10000
	}, neverTimeout, interval, "Defaults CR with spec.name other than 'haproxytech' must be ignored")
}
