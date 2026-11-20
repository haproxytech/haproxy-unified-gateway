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
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	v1 "k8s.io/api/core/v1"

	"github.com/haproxytech/haproxy-unified-gateway/test/integration/base"
	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"
)

const (
	timeout  = 20 * time.Second
	interval = time.Second

	// hugConfNamespace is the namespace where CRs must reside.
	// It must match the controller configuration in base/inttest.go (hugConfNsName.Namespace = "test").
	hugConfNamespace = "test"

	// refusedLogFormat names a sample fetch HAProxy does not know. Both the CRD and
	// the client-native model accept it, since it is a plain string: the refusal
	// happens when HAProxy loads the configuration, which is the failure this case
	// is built on.
	refusedLogFormat = "%[hug_unknown_fetch]"

	// correctedLogFormat is a format HAProxy accepts. It holds no space on
	// purpose: client-native writes the value verbatim, and HAProxy reads a single
	// argument ("log-format expects only one argument").
	correctedLogFormat = "%ci:%cp-%ft-%b/%s-%ST-%B"
)

type ReplaySuite struct {
	base.BaseSuite
}

func TestReplaySuite(t *testing.T) {
	suite.Run(t, new(ReplaySuite))
}

func (s *ReplaySuite) SetupSuite() {
	s.BaseSuite.SetupSuite("", 0)

	// Create the "test" namespace required for CRs.
	err := utils.CreateRuntimeObject(s.IntTest().Ctx, s.IntTest().Client, &v1.Namespace{
		Name: hugConfNamespace,
	}, true)
	s.Require().NoError(err)
}

func (s *ReplaySuite) TearDownSuite() {
	_ = utils.DeleteRuntimeObject(s.IntTest().Ctx, s.IntTest().Client, &v1.Namespace{
		Name: hugConfNamespace,
	}, false)
	s.BaseSuite.TearDownSuite()
}
