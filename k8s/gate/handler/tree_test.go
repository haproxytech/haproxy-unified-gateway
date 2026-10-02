// Copyright 2025 HAProxy Technologies LLC
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
package handler

import (
	"reflect"
	"testing"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The ListenerSet work is split across two builders sitting on either side of the
// Gateway builder, and that position is what makes each of them correct: the
// contribution has to run before the builder that assembles a Gateway's merged listener
// list from GateTree.ListenerSets, and the verdict pass after the one that judged those
// listeners and settled the parent Gateway's own conditions.
//
// Nothing else states that constraint at run time. The constructors are independent and a
// permutation of the list would compile and run, so a reordering would surface as entries
// missing from the merged list, or as conditions read before they exist -- never as an
// error here.
func TestListenerSetBuildersSurroundTheGatewayBuilder(t *testing.T) {
	builder := NewGateTreeBuilder(&tree.ControllerStore{}, GateTreeConfig{})

	indexOf := func(concrete any) int {
		for i, b := range builder.postBuilders {
			if reflect.TypeOf(b) == reflect.TypeOf(concrete) {
				return i
			}
		}
		return -1
	}

	contribution := indexOf(&tree.ListenerSetContributionBuilder{})
	gateway := indexOf(&tree.GatewayBuilderImpl{})
	verdict := indexOf(&tree.ListenerSetVerdictBuilder{})

	require.NotEqual(t, -1, contribution, "the contribution pass must be registered")
	require.NotEqual(t, -1, gateway, "the Gateway builder must be registered")
	require.NotEqual(t, -1, verdict, "the verdict pass must be registered")

	assert.Less(t, contribution, gateway,
		"the Gateway builder is what reads GateTree.ListenerSets and assembles the merged list, so the contributions have to be published before it runs")
	assert.Less(t, gateway, verdict,
		"the verdicts roll up the conditions the Gateway builder computed, so they cannot be read before it runs")
}
