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
package hugservice

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func port(name string, port, nodePort int32) corev1.ServicePort {
	return corev1.ServicePort{Name: name, Port: port, NodePort: nodePort, TargetPort: intstr.FromInt32(port)}
}

// vlPort builds a ServicePort as emitted by buildVirtualListenerServicePorts
// (TCP, TargetPort==Port). nodePort=0 means "unset".
func vlPort(name string, p, nodePort int32) corev1.ServicePort {
	return corev1.ServicePort{
		Name:       name,
		Protocol:   corev1.ProtocolTCP,
		Port:       p,
		NodePort:   nodePort,
		TargetPort: intstr.FromInt32(p),
	}
}

func TestPortsEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b []corev1.ServicePort
		want bool
	}{
		{
			name: "both nil",
			want: true,
		},
		{
			name: "both empty",
			a:    []corev1.ServicePort{},
			b:    []corev1.ServicePort{},
			want: true,
		},
		{
			name: "identical single port",
			a:    []corev1.ServicePort{port("http", 80, 31080)},
			b:    []corev1.ServicePort{port("http", 80, 31080)},
			want: true,
		},
		{
			name: "identical multiple ports same order",
			a:    []corev1.ServicePort{port("http", 80, 31080), port("https", 443, 31443)},
			b:    []corev1.ServicePort{port("http", 80, 31080), port("https", 443, 31443)},
			want: true,
		},
		{
			name: "identical multiple ports different order",
			a:    []corev1.ServicePort{port("http", 80, 31080), port("https", 443, 31443)},
			b:    []corev1.ServicePort{port("https", 443, 31443), port("http", 80, 31080)},
			want: true,
		},
		{
			name: "different lengths",
			a:    []corev1.ServicePort{port("http", 80, 31080)},
			b:    []corev1.ServicePort{port("http", 80, 31080), port("https", 443, 31443)},
			want: false,
		},
		{
			name: "same name and port, different nodePort",
			a:    []corev1.ServicePort{port("http", 80, 31080)},
			b:    []corev1.ServicePort{port("http", 80, 31081)},
			want: true, // nodePort is Kubernetes-assigned, not compared
		},
		{
			name: "same name and nodePort, different port",
			a:    []corev1.ServicePort{port("http", 80, 31080)},
			b:    []corev1.ServicePort{port("http", 81, 31080)},
			want: false,
		},
		{
			name: "same port numbers, different name",
			a:    []corev1.ServicePort{port("http", 80, 31080)},
			b:    []corev1.ServicePort{port("http2", 80, 31080)},
			want: false,
		},
		{
			name: "one empty one not",
			a:    []corev1.ServicePort{},
			b:    []corev1.ServicePort{port("http", 80, 31080)},
			want: false,
		},
		{
			name: "same name, port, nodePort, different targetPort",
			a:    []corev1.ServicePort{{Name: "http", Port: 80, NodePort: 31080, TargetPort: intstr.FromInt32(80)}},
			b:    []corev1.ServicePort{{Name: "http", Port: 80, NodePort: 31080, TargetPort: intstr.FromInt32(8080)}},
			want: false,
		},
		{
			name: "same name, port, nodePort, targetPort int vs string same value",
			a:    []corev1.ServicePort{{Name: "http", Port: 80, NodePort: 31080, TargetPort: intstr.FromInt32(80)}},
			b:    []corev1.ServicePort{{Name: "http", Port: 80, NodePort: 31080, TargetPort: intstr.FromString("80")}},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := portsEqual(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("portsEqual() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestBuildDesiredPortsPreservesNodePort covers the two NodePort-preservation:
// - a VL port matching an existing VL port by name/number.
// - a VL port matching a pre-seeded entry only by port number.
// Additionally, a brand-new listener must come out with NodePort=0 so
// Kubernetes allocates.
func TestBuildDesiredPortsPreservesNodePort(t *testing.T) {
	existing := []corev1.ServicePort{
		port("stat", 31024, 31721),         // reserved, kept verbatim
		vlPort("tls-31443", 31443, 31364),  // matches desired by name
		port("kube-example", 32132, 32132), // matches desired by port number
	}
	desiredVL := []corev1.ServicePort{
		vlPort("tls-31443", 31443, 0),
		vlPort("tls-32132", 32132, 0),
		vlPort("tls-40000", 40000, 0), // new listener, no existing match
	}
	want := []corev1.ServicePort{
		port("stat", 31024, 31721),
		vlPort("tls-31443", 31443, 31364),
		vlPort("tls-32132", 32132, 32132),
		vlPort("tls-40000", 40000, 0),
	}

	got := buildDesiredPorts(existing, desiredVL)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildDesiredPorts() mismatch\n  got  = %#v\n  want = %#v", got, want)
	}
}

// TestBuildDesiredPortsIsStable checks, after the first reconcile with
// Kubernetes filling any unset NodePorts, that a second reconcile is a
// no-op. This is what prevents the NodePort flip on each event batch.
func TestBuildDesiredPortsIsStable(t *testing.T) {
	desiredVL := []corev1.ServicePort{vlPort("tls-31443", 31443, 0)}
	first := buildDesiredPorts([]corev1.ServicePort{port("stat", 31024, 31024)}, desiredVL)
	for i := range first {
		if first[i].NodePort == 0 {
			first[i].NodePort = 30000 // simulate Kubernetes allocation
		}
	}
	if second := buildDesiredPorts(first, desiredVL); !reflect.DeepEqual(first, second) {
		t.Fatalf("reconcile is not stable\n  first  = %#v\n  second = %#v", first, second)
	}
}
