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
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

// ReferenceGrantCovers is the stateless half of IsAccessGranted, and the enqueue edges
// filter on it. The two have to agree on every cross-namespace decision, or a grant the
// manager would have accepted would never reach a cycle. This compares them over a
// grant's own shape, so a divergence fails here rather than silently in a cluster.
func TestReferenceGrantCoversMatchesIsAccessGranted(t *testing.T) {
	shared := gatewayv1.ObjectName("shared")

	tests := []struct {
		name string
		rg   *gatewayv1beta1.ReferenceGrant
		from GrantFrom
		to   GrantTo
	}{
		{
			name: "named To, matching From",
			rg: &gatewayv1beta1.ReferenceGrant{
				ObjectMeta: metav1.ObjectMeta{Namespace: "certs", Name: "g"},
				Spec: gatewayv1beta1.ReferenceGrantSpec{
					From: []gatewayv1beta1.ReferenceGrantFrom{{Group: gatewayv1.GroupName, Kind: "ListenerSet", Namespace: "tenant"}},
					To:   []gatewayv1beta1.ReferenceGrantTo{{Group: "", Kind: "Secret", Name: &shared}},
				},
			},
			from: GrantFrom{Group: gatewayv1.GroupName, Kind: "ListenerSet", Namespace: "tenant"},
			to:   GrantTo{Group: "", Kind: "Secret", Namespace: "certs", Name: "shared"},
		},
		{
			name: "wildcard To, matching From",
			rg: &gatewayv1beta1.ReferenceGrant{
				ObjectMeta: metav1.ObjectMeta{Namespace: "certs", Name: "g"},
				Spec: gatewayv1beta1.ReferenceGrantSpec{
					From: []gatewayv1beta1.ReferenceGrantFrom{{Group: gatewayv1.GroupName, Kind: "ListenerSet", Namespace: "tenant"}},
					To:   []gatewayv1beta1.ReferenceGrantTo{{Group: "", Kind: "Secret", Name: nil}},
				},
			},
			from: GrantFrom{Group: gatewayv1.GroupName, Kind: "ListenerSet", Namespace: "tenant"},
			to:   GrantTo{Group: "", Kind: "Secret", Namespace: "certs", Name: "anything"},
		},
		{
			name: "named To, another name",
			rg: &gatewayv1beta1.ReferenceGrant{
				ObjectMeta: metav1.ObjectMeta{Namespace: "certs", Name: "g"},
				Spec: gatewayv1beta1.ReferenceGrantSpec{
					From: []gatewayv1beta1.ReferenceGrantFrom{{Group: gatewayv1.GroupName, Kind: "ListenerSet", Namespace: "tenant"}},
					To:   []gatewayv1beta1.ReferenceGrantTo{{Group: "", Kind: "Secret", Name: &shared}},
				},
			},
			from: GrantFrom{Group: gatewayv1.GroupName, Kind: "ListenerSet", Namespace: "tenant"},
			to:   GrantTo{Group: "", Kind: "Secret", Namespace: "certs", Name: "other"},
		},
		{
			name: "From of another kind",
			rg: &gatewayv1beta1.ReferenceGrant{
				ObjectMeta: metav1.ObjectMeta{Namespace: "certs", Name: "g"},
				Spec: gatewayv1beta1.ReferenceGrantSpec{
					From: []gatewayv1beta1.ReferenceGrantFrom{{Group: gatewayv1.GroupName, Kind: "ListenerSet", Namespace: "tenant"}},
					To:   []gatewayv1beta1.ReferenceGrantTo{{Group: "", Kind: "Secret", Name: &shared}},
				},
			},
			from: GrantFrom{Group: gatewayv1.GroupName, Kind: "Gateway", Namespace: "tenant"},
			to:   GrantTo{Group: "", Kind: "Secret", Namespace: "certs", Name: "shared"},
		},
		{
			name: "To of another kind",
			rg: &gatewayv1beta1.ReferenceGrant{
				ObjectMeta: metav1.ObjectMeta{Namespace: "certs", Name: "g"},
				Spec: gatewayv1beta1.ReferenceGrantSpec{
					From: []gatewayv1beta1.ReferenceGrantFrom{{Group: gatewayv1.GroupName, Kind: "ListenerSet", Namespace: "tenant"}},
					To:   []gatewayv1beta1.ReferenceGrantTo{{Group: "", Kind: "Secret", Name: &shared}},
				},
			},
			from: GrantFrom{Group: gatewayv1.GroupName, Kind: "ListenerSet", Namespace: "tenant"},
			to:   GrantTo{Group: "", Kind: "Service", Namespace: "certs", Name: "shared"},
		},
		{
			name: "same namespace needs no grant",
			rg: &gatewayv1beta1.ReferenceGrant{
				ObjectMeta: metav1.ObjectMeta{Namespace: "tenant", Name: "g"},
				Spec: gatewayv1beta1.ReferenceGrantSpec{
					From: []gatewayv1beta1.ReferenceGrantFrom{{Group: gatewayv1.GroupName, Kind: "ListenerSet", Namespace: "tenant"}},
					To:   []gatewayv1beta1.ReferenceGrantTo{{Group: "", Kind: "Secret", Name: &shared}},
				},
			},
			from: GrantFrom{Group: gatewayv1.GroupName, Kind: "ListenerSet", Namespace: "tenant"},
			to:   GrantTo{Group: "", Kind: "Secret", Namespace: "tenant", Name: "shared"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr := NewReferenceGrantManager()
			mgr.UpsertReferenceGrant(upserted(tt.rg))
			mgr.ComputeToFrom()

			crossNamespace := tt.to.Namespace != tt.from.Namespace
			want := crossNamespace && mgr.IsAccessGranted(tt.from, tt.to)

			assert.Equal(t, want, ReferenceGrantCovers(tt.rg, tt.from, tt.to),
				"the edge's stateless check must agree with the manager on what the grant covers")
		})
	}
}
