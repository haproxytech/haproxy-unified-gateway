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
package startup

import (
	"testing"

	"github.com/haproxytech/client-native/v6/models"
	md "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/metadata"
)

// hugMeta builds a metadata map matching the structure produced by the metadata
// package after its internal JSON round-trip:
// { "hug": { kind: { "ns/name": { "LinkID": linkID, "Generation": 1 } } } }
func hugMeta(kind, objKey, linkID string) map[string]any {
	return map[string]any{
		md.UnifiedGatewayMetaDataKey: map[string]any{
			kind: map[string]any{
				objKey: map[string]any{
					"LinkID":     linkID,
					"Generation": float64(1),
				},
			},
		},
	}
}

func newFrontend(meta map[string]any) *models.Frontend {
	fe := &models.Frontend{}
	fe.Metadata = meta
	return fe
}

func newBackend(meta map[string]any) *models.Backend {
	be := &models.Backend{}
	be.Metadata = meta
	return be
}

func TestIsUnifiedGatewayManaged_Frontend(t *testing.T) {
	tests := []struct {
		name     string
		frontend *models.Frontend
		linkID   string
		want     bool
	}{
		{
			name:     "matching linkID",
			frontend: newFrontend(hugMeta("Gateway", "namespace/name", "hug")),
			linkID:   "hug",
			want:     true,
		},
		{
			name:     "wrong linkID",
			frontend: newFrontend(hugMeta("Gateway", "namespace/name", "hug")),
			linkID:   "other",
			want:     false,
		},
		{
			name:     "no hug key",
			frontend: newFrontend(map[string]any{"unrelated": "value"}),
			linkID:   "hug",
			want:     false,
		},
		{
			name:     "nil metadata",
			frontend: newFrontend(nil),
			linkID:   "hug",
			want:     false,
		},
		{
			name:     "empty metadata",
			frontend: newFrontend(map[string]any{}),
			linkID:   "hug",
			want:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isUnifiedGatewayManaged(tt.frontend, tt.linkID)
			if got != tt.want {
				t.Errorf("isUnifiedGatewayManaged() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsUnifiedGatewayManaged_Backend(t *testing.T) {
	tests := []struct {
		name    string
		backend *models.Backend
		linkID  string
		want    bool
	}{
		{
			name:    "matching linkID",
			backend: newBackend(hugMeta("HTTPRoute", "namespace/name", "hug")),
			linkID:  "hug",
			want:    true,
		},
		{
			name:    "wrong linkID",
			backend: newBackend(hugMeta("HTTPRoute", "namespace/name", "hug")),
			linkID:  "other",
			want:    false,
		},
		{
			name:    "nil metadata",
			backend: newBackend(nil),
			linkID:  "hug",
			want:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isUnifiedGatewayManaged(tt.backend, tt.linkID)
			if got != tt.want {
				t.Errorf("isUnifiedGatewayManaged() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsUnifiedGatewayManaged_MultipleObjects(t *testing.T) {
	// Two gateways under different object keys, both with the same linkID.
	meta := map[string]any{
		md.UnifiedGatewayMetaDataKey: map[string]any{
			"Gateway": map[string]any{
				"ns/gw-a": map[string]any{"LinkID": "hug", "Generation": float64(1)},
				"ns/gw-b": map[string]any{"LinkID": "hug", "Generation": float64(2)},
			},
		},
	}
	fe := newFrontend(meta)

	if !isUnifiedGatewayManaged(fe, "hug") {
		t.Error("expected true for frontend with two gateways and matching linkID")
	}
	if isUnifiedGatewayManaged(fe, "other") {
		t.Error("expected false for frontend with two gateways and non-matching linkID")
	}
}

func TestIsUnifiedGatewayManaged_MixedLinkIDs(t *testing.T) {
	// Two objects under different kinds, each with a different linkID.
	meta := map[string]any{
		md.UnifiedGatewayMetaDataKey: map[string]any{
			"Gateway": map[string]any{
				"ns/gw": map[string]any{"LinkID": "hug", "Generation": float64(1)},
			},
			"HTTPRoute": map[string]any{
				"ns/route": map[string]any{"LinkID": "other", "Generation": float64(1)},
			},
		},
	}
	fe := newFrontend(meta)

	if !isUnifiedGatewayManaged(fe, "hug") {
		t.Error("expected true: Gateway entry has linkID 'hug'")
	}
	if !isUnifiedGatewayManaged(fe, "other") {
		t.Error("expected true: HTTPRoute entry has linkID 'other'")
	}
	if isUnifiedGatewayManaged(fe, "unknown") {
		t.Error("expected false: no entry has linkID 'unknown'")
	}
}
