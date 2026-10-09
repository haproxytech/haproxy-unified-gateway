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
package diffs

import (
	"testing"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/metadata"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"
	"k8s.io/apimachinery/pkg/types"
)

func makeMetadata(gatewayEntries map[string]any) map[string]any {
	return makeMetadataKinds(map[string]map[string]any{"Gateway": gatewayEntries})
}

func makeMetadataKinds(kinds map[string]map[string]any) map[string]any {
	return map[string]any{
		metadata.UnifiedGatewayMetaDataKey: kinds,
	}
}

func gatewayEntry(generation float64, linkID string) map[string]any {
	return map[string]any{
		"Generation": generation,
		"LinkID":     linkID,
	}
}

func TestExtractGatewayGenerationsFromFrontend(t *testing.T) {
	tests := []struct {
		name     string
		frontend *models.Frontend
		want     map[types.NamespacedName]int64
	}{
		{
			name:     "nil frontend",
			frontend: nil,
			want:     map[types.NamespacedName]int64{},
		},
		{
			name:     "nil metadata",
			frontend: &models.Frontend{},
			want:     map[types.NamespacedName]int64{},
		},
		{
			name: "metadata missing hug key",
			frontend: &models.Frontend{
				Metadata: map[string]any{"other": "value"},
			},
			want: map[types.NamespacedName]int64{},
		},
		{
			name: "hug key present but no Gateway section",
			frontend: &models.Frontend{
				Metadata: map[string]any{
					metadata.UnifiedGatewayMetaDataKey: map[string]any{
						"SomethingElse": map[string]any{},
					},
				},
			},
			want: map[types.NamespacedName]int64{},
		},
		{
			name: "single gateway",
			frontend: &models.Frontend{
				Metadata: makeMetadata(map[string]any{
					"default/my-gw": gatewayEntry(7, "link-1"),
				}),
			},
			want: map[types.NamespacedName]int64{
				{Namespace: "default", Name: "my-gw"}: 7,
			},
		},
		{
			name: "multiple gateways",
			frontend: &models.Frontend{
				Metadata: makeMetadata(map[string]any{
					"default/gw-a": gatewayEntry(3, "link-1"),
					"infra/gw-b":   gatewayEntry(10, "link-2"),
					"staging/gw-c": gatewayEntry(1, "link-3"),
				}),
			},
			want: map[types.NamespacedName]int64{
				{Namespace: "default", Name: "gw-a"}: 3,
				{Namespace: "infra", Name: "gw-b"}:   10,
				{Namespace: "staging", Name: "gw-c"}: 1,
			},
		},
		{
			name: "entry with invalid namespace/name (no slash) is skipped",
			frontend: &models.Frontend{
				Metadata: makeMetadata(map[string]any{
					"no-slash":      gatewayEntry(5, "link-1"),
					"default/valid": gatewayEntry(2, "link-2"),
				}),
			},
			want: map[types.NamespacedName]int64{
				{Namespace: "default", Name: "valid"}: 2,
			},
		},
		{
			name: "entry missing Generation field is skipped",
			frontend: &models.Frontend{
				Metadata: makeMetadata(map[string]any{
					"default/no-gen": map[string]any{"LinkID": "link-1"},
					"default/valid":  gatewayEntry(4, "link-2"),
				}),
			},
			want: map[types.NamespacedName]int64{
				{Namespace: "default", Name: "valid"}: 4,
			},
		},
		{
			name: "Generation field is not a number is skipped",
			frontend: &models.Frontend{
				Metadata: makeMetadata(map[string]any{
					"default/bad-gen": map[string]any{"Generation": "not-a-number"},
					"default/valid":   gatewayEntry(9, "link-1"),
				}),
			},
			want: map[types.NamespacedName]int64{
				{Namespace: "default", Name: "valid"}: 9,
			},
		},
		{
			name: "generation zero is preserved",
			frontend: &models.Frontend{
				Metadata: makeMetadata(map[string]any{
					"default/gw": gatewayEntry(0, "link-1"),
				}),
			},
			want: map[types.NamespacedName]int64{
				{Namespace: "default", Name: "gw"}: 0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			all := make(tree.ObservedGenerations)
			extractObservedGenerationsFromFrontend(tt.frontend, all)
			// Every fixture above is Gateway-declared; the kinds are covered apart.
			got := make(map[types.NamespacedName]int64)
			for owner, gen := range all {
				if owner.Kind == tree.OwnerKindGateway {
					got[owner.Key] = gen
				}
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d entries, want %d: got=%v want=%v", len(got), len(tt.want), got, tt.want)
			}
			for k, wantGen := range tt.want {
				gotGen, ok := got[k]
				if !ok {
					t.Errorf("missing key %v in result", k)
					continue
				}
				if gotGen != wantGen {
					t.Errorf("key %v: got generation %d, want %d", k, gotGen, wantGen)
				}
			}
		})
	}
}

// A frontend records the generation of every resource its listeners answer to, under the
// kind of the resource that declares them: the Gateway for its own listeners, the
// ListenerSet for an entry it contributed. The two can share a name, so the kind is what
// keeps their generations apart.
func TestExtractObservedGenerationsKeepsEachDeclaringKind(t *testing.T) {
	frontend := &models.Frontend{
		Metadata: makeMetadataKinds(map[string]map[string]any{
			"Gateway":     {"infra/shared": gatewayEntry(3, "link-1")},
			"ListenerSet": {"infra/shared": gatewayEntry(11, "link-1")},
		}),
	}

	got := make(tree.ObservedGenerations)
	extractObservedGenerationsFromFrontend(frontend, got)

	want := tree.ObservedGenerations{
		{Kind: tree.OwnerKindGateway, Key: types.NamespacedName{Namespace: "infra", Name: "shared"}}:     3,
		{Kind: tree.OwnerKindListenerSet, Key: types.NamespacedName{Namespace: "infra", Name: "shared"}}: 11,
	}

	if len(got) != len(want) {
		t.Fatalf("got %d owners, want %d: got=%v want=%v", len(got), len(want), got, want)
	}
	for owner, wantGen := range want {
		gotGen, ok := got[owner]
		if !ok {
			t.Errorf("missing owner %v in result", owner)
			continue
		}
		if gotGen != wantGen {
			t.Errorf("owner %v: got generation %d, want %d", owner, gotGen, wantGen)
		}
	}
}
