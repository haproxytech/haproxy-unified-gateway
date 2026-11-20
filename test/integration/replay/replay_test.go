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

package replay

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	truntime "github.com/haproxytech/haproxy-unified-gateway/test/integration/runtime"
	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"
)

// Test_Replay_CorrectionAfterARefusedConfiguration drives the recovery a failed
// configuration apply owes its caller: the change that follows it must not be lost.
//
// Step 1: a Defaults CR carries a log format naming an unknown sample fetch. The
// CRD accepts it as a plain string, and client-native refuses it when the cycle is
// committed: the rejected transaction never reaches the configuration file, and the
// cycle's diffs stay pending. The worker serving traffic is not disturbed.
// Step 2: the CR is corrected. The correction has to reach the committed
// configuration, which means the pending cycle was neither dropped nor allowed to
// hide the input that followed it.
//
// The assertions read the configuration file rather than the running process: the
// commit is what the replay is about, and it is the only half that does not depend
// on how this environment reloads HAProxy.
func (s *ReplaySuite) Test_Replay_CorrectionAfterARefusedConfiguration() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "1-refused-log-format")

	workerPIDBefore := s.haproxyWorkerPID()
	rejectedTransactionsBefore := s.rejectedTransactions()

	s.CreateFixturesInNamespace(fixturePath, hugConfNamespace, []string{"defaults-refused.yaml"})
	defer s.CleanupFixturesInNamespace(fixturePath, hugConfNamespace, []string{"defaults-corrected.yaml"})

	// The refused cycle is rejected before the file is written: nothing of it may
	// reach the configuration HAProxy is started with.
	s.Never(func() bool {
		return s.committedConfigurationContains("log-format " + refusedLogFormat)
	}, 6*time.Second, 200*time.Millisecond,
		"the refused configuration was committed, so the case did not produce the failure it is built on")

	// A refused apply must not disturb the worker serving traffic.
	s.ConsistentlyNoReload(workerPIDBefore, 3*time.Second)

	// No event follows the refusal: the controller has to come back to the cycle on
	// its own. Each retry is a transaction of its own, so the rejected ones pile up
	// -- one dump per attempt -- while nothing changes in the cluster.
	s.Eventually(func() bool {
		return s.rejectedTransactions() >= rejectedTransactionsBefore+2
	}, 60*time.Second, time.Second,
		"the refused cycle was never retried without a new event: a cluster that stopped changing would keep the failure")

	// Step 2: the correction, and nothing else.
	s.CreateFixturesInNamespace(fixturePath, hugConfNamespace, []string{"defaults-corrected.yaml"})

	s.expectLogFormatInConfigurationFile(correctedLogFormat,
		"the correction never reached the configuration: the failed cycle kept it from being applied")
}

// rejectedTransactions counts the transactions client-native rejected, which is one
// per apply attempt that failed: each of them is written next to the configuration
// so it can be inspected.
func (s *ReplaySuite) rejectedTransactions() int {
	entries, err := os.ReadDir(filepath.Join(s.IntTest().HaproxyCfgDir, "failed"))
	if err != nil {
		return 0
	}

	return len(entries)
}

// haproxyWorkerPID returns the pid of the worker HAProxy is currently running.
func (s *ReplaySuite) haproxyWorkerPID() string {
	info, err := truntime.GetGlobalHAProxyInfo(s.IntTest().RuntimeSocketPath)
	s.Require().NoError(err, "cannot read the HAProxy worker pid")

	return info.Pid
}

// committedConfigurationContains reports whether the committed configuration
// carries the given directive.
func (s *ReplaySuite) committedConfigurationContains(directive string) bool {
	content, err := os.ReadFile(filepath.Join(s.IntTest().HaproxyCfgDir, "haproxy.cfg"))
	if err != nil {
		return false
	}

	return strings.Contains(string(content), directive)
}

// expectLogFormatInConfigurationFile waits until the committed configuration
// carries the expected log format, and fails with the given message otherwise.
//
// It reads the file instead of the parsed model: client-native writes log-format
// but does not read it back into the Defaults model, so the file is the only place
// that says what was committed.
func (s *ReplaySuite) expectLogFormatInConfigurationFile(expected, failureMessage string) {
	expectedDirective := "log-format " + expected

	var found []string
	s.Eventually(func() bool {
		content, err := os.ReadFile(filepath.Join(s.IntTest().HaproxyCfgDir, "haproxy.cfg"))
		if err != nil {
			return false
		}
		found = logFormatDirectives(string(content))

		return strings.Contains(string(content), expectedDirective)
	}, timeout, interval, fmt.Sprintf("%s\nexpected directive %q in the committed configuration\n              found %v",
		failureMessage, expectedDirective, found))
}

// logFormatDirectives returns the log-format directives of a configuration, for
// the failure message.
func logFormatDirectives(configuration string) []string {
	var directives []string
	for line := range strings.SplitSeq(configuration, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "log-format ") || trimmed == "log-format" {
			directives = append(directives, trimmed)
		}
	}

	return directives
}
