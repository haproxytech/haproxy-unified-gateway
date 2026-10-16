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

package listenerset

import (
	"testing"

	"github.com/haproxytech/haproxy-unified-gateway/test/integration/base"
	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"

	"github.com/stretchr/testify/suite"
	v1 "k8s.io/api/core/v1"
)

// secretNamespace holds the Secret a ListenerSet's cross-namespace certificateRef
// points at, and the ReferenceGrant that permits it. It is a second namespace
// because that is what a ReferenceGrant is for: HUG owns no plumbing for a fixture
// outside the test namespace, so the case creates one.
const secretNamespace = "listenerset-secrets"

type ListenerSetSuite struct {
	base.BaseSuite
}

func TestListenerSetSuite(t *testing.T) {
	suite.Run(t, new(ListenerSetSuite))
}

func (s *ListenerSetSuite) SetupSuite() {
	s.BaseSuite.SetupSuite("", 0)

	err := utils.CreateRuntimeObject(s.Test().Ctx, s.Test().Client, &v1.Namespace{
		Name: secretNamespace,
	}, true)
	s.Require().NoError(err)
}

func (s *ListenerSetSuite) TearDownSuite() {
	_ = utils.DeleteRuntimeObject(s.Test().Ctx, s.Test().Client, &v1.Namespace{
		Name: secretNamespace,
	}, false)
	s.BaseSuite.TearDownSuite()
}
