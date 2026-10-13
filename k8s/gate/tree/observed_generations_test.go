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
package tree

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
)

// The apply result is logged as a whole, and it travels to the controller as this map. A map
// whose member names are structs cannot be marshalled, so without a text form the log reports
// "object member name must be a string" instead of the generations it was handed -- which is
// how this was found.
func TestObservedGenerationsMarshal(t *testing.T) {
	generations := ObservedGenerations{
		{Kind: OwnerKindGateway, Key: types.NamespacedName{Namespace: "infra", Name: "gw"}}:      7,
		{Kind: OwnerKindListenerSet, Key: types.NamespacedName{Namespace: "tenant", Name: "ls"}}: 11,
	}

	raw, err := json.Marshal(generations)

	require.NoError(t, err)
	require.JSONEq(t, `{"Gateway/infra/gw":7,"ListenerSet/tenant/ls":11}`, string(raw))
}
