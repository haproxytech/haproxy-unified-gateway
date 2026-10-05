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
package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// crdInstalled is the whole of the CRD probe: a Kind the cluster serves is watched,
// one it does not is skipped. The error is returned rather than swallowed so the
// caller can tell "not installed" (expected) from "the RESTMapper is unusable" (not).
func TestCRDInstalled(t *testing.T) {
	gvk := schema.GroupVersionKind{
		Group:   "gateway.networking.k8s.io",
		Version: "v1",
		Kind:    "ListenerSet",
	}

	t.Run("the cluster serves the Kind", func(t *testing.T) {
		mapper := meta.NewDefaultRESTMapper(nil)
		mapper.Add(gvk, meta.RESTScopeNamespace)

		installed, err := crdInstalled(mapper, gvk)
		require.NoError(t, err)
		assert.True(t, installed)
	})

	t.Run("the cluster does not serve the Kind", func(t *testing.T) {
		mapper := meta.NewDefaultRESTMapper(nil)

		installed, err := crdInstalled(mapper, gvk)
		require.Error(t, err)
		assert.False(t, installed)
		assert.True(t, meta.IsNoMatchError(err),
			"an absent Kind is the expected case and must be recognisable as such")
	})
}
