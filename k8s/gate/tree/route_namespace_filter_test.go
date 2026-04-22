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
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func makeListener(from *gatewayv1.FromNamespaces, selector *metav1.LabelSelector) *Listener {
	l := &Listener{}
	if from != nil {
		ns := &gatewayv1.RouteNamespaces{From: from}
		if selector != nil {
			ns.Selector = selector
		}
		l.K8sResource = gatewayv1.Listener{
			AllowedRoutes: &gatewayv1.AllowedRoutes{Namespaces: ns},
		}
	}
	return l
}

func makeNamespaces(names ...string) map[types.NamespacedName]*v1.Namespace {
	m := make(map[types.NamespacedName]*v1.Namespace, len(names))
	for _, name := range names {
		m[types.NamespacedName{Name: name}] = &v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: name},
		}
	}
	return m
}

func makeNamespacesWithLabels(labels map[string]map[string]string) map[types.NamespacedName]*v1.Namespace {
	m := make(map[types.NamespacedName]*v1.Namespace, len(labels))
	for name, lbls := range labels {
		m[types.NamespacedName{Name: name}] = &v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: name, Labels: lbls},
		}
	}
	return m
}

//revive:disable:function-length
func Test_isRouteNamespaceAllowed(t *testing.T) {
	fromSame := gatewayv1.NamespacesFromSame
	fromAll := gatewayv1.NamespacesFromAll
	fromSelector := gatewayv1.NamespacesFromSelector

	tests := []struct {
		name             string
		routeNamespace   string
		gatewayNamespace string
		listener         *Listener
		namespaces       map[types.NamespacedName]*v1.Namespace
		want             bool
	}{
		{
			name:             "nil AllowedRoutes defaults to Same, same namespace",
			routeNamespace:   "ns-a",
			gatewayNamespace: "ns-a",
			listener:         &Listener{},
			namespaces:       makeNamespaces("ns-a"),
			want:             true,
		},
		{
			name:             "nil AllowedRoutes defaults to Same, different namespace",
			routeNamespace:   "ns-b",
			gatewayNamespace: "ns-a",
			listener:         &Listener{},
			namespaces:       makeNamespaces("ns-a", "ns-b"),
			want:             false,
		},
		{
			name:             "from=Same, same namespace",
			routeNamespace:   "ns-a",
			gatewayNamespace: "ns-a",
			listener:         makeListener(&fromSame, nil),
			namespaces:       makeNamespaces("ns-a"),
			want:             true,
		},
		{
			name:             "from=Same, different namespace",
			routeNamespace:   "ns-b",
			gatewayNamespace: "ns-a",
			listener:         makeListener(&fromSame, nil),
			namespaces:       makeNamespaces("ns-a", "ns-b"),
			want:             false,
		},
		{
			name:             "from=All, same namespace",
			routeNamespace:   "ns-a",
			gatewayNamespace: "ns-a",
			listener:         makeListener(&fromAll, nil),
			namespaces:       makeNamespaces("ns-a"),
			want:             true,
		},
		{
			name:             "from=All, different namespace",
			routeNamespace:   "ns-b",
			gatewayNamespace: "ns-a",
			listener:         makeListener(&fromAll, nil),
			namespaces:       makeNamespaces("ns-a", "ns-b"),
			want:             true,
		},
		{
			name:             "from=Selector, namespace labels match",
			routeNamespace:   "ns-b",
			gatewayNamespace: "ns-a",
			listener: makeListener(&fromSelector, &metav1.LabelSelector{
				MatchLabels: map[string]string{"team": "infra"},
			}),
			namespaces: makeNamespacesWithLabels(map[string]map[string]string{
				"ns-a": {"team": "gateway"},
				"ns-b": {"team": "infra"},
			}),
			want: true,
		},
		{
			name:             "from=Selector, namespace labels do not match",
			routeNamespace:   "ns-b",
			gatewayNamespace: "ns-a",
			listener: makeListener(&fromSelector, &metav1.LabelSelector{
				MatchLabels: map[string]string{"team": "infra"},
			}),
			namespaces: makeNamespacesWithLabels(map[string]map[string]string{
				"ns-a": {"team": "gateway"},
				"ns-b": {"team": "backend"},
			}),
			want: false,
		},
		{
			name:             "from=Selector, namespace not in store",
			routeNamespace:   "ns-unknown",
			gatewayNamespace: "ns-a",
			listener: makeListener(&fromSelector, &metav1.LabelSelector{
				MatchLabels: map[string]string{"team": "infra"},
			}),
			namespaces: makeNamespaces("ns-a"),
			want:       false,
		},
		{
			name:             "from=Selector, nil selector returns false",
			routeNamespace:   "ns-b",
			gatewayNamespace: "ns-a",
			listener:         makeListener(&fromSelector, nil),
			namespaces:       makeNamespaces("ns-a", "ns-b"),
			want:             false,
		},
		{
			name:             "from=Selector, empty selector matches all namespaces",
			routeNamespace:   "ns-b",
			gatewayNamespace: "ns-a",
			listener:         makeListener(&fromSelector, &metav1.LabelSelector{}),
			namespaces:       makeNamespaces("ns-a", "ns-b"),
			want:             true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isRouteNamespaceAllowed(tt.routeNamespace, tt.listener, tt.gatewayNamespace, tt.namespaces)
			assert.Equal(t, tt.want, got)
		})
	}
}
