//go:build conformance

// Copyright 2024 HAProxy Technologies LLC
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

package conformance_test

import (
	"context"
	"net"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
)

// newPortRemappingDialer returns a DialContext function that remaps Gateway API
// standard ports (80, 443) to the actual NodePort ports HUG listens on.
// This is needed because the conformance tests construct URLs using the
// listener port from the Gateway spec (80/443), but HUG serves traffic
// on NodePort ports (e.g. 31080/31443).
func newPortRemappingDialer(httpPort, httpsPort string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	portMap := map[string]string{
		"80":  httpPort,
		"443": httpsPort,
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if mappedPort, ok := portMap[port]; ok {
			addr = net.JoinHostPort(host, mappedPort)
		}
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}
}

// getNodeIP returns the InternalIP of the first Ready node in the cluster.
func getNodeIP(ctx context.Context, cs clientset.Interface) string {
	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil || len(nodes.Items) == 0 {
		return ""
	}
	for _, node := range nodes.Items {
		for _, addr := range node.Status.Addresses {
			if addr.Type == "InternalIP" {
				return addr.Address
			}
		}
	}
	// Fallback: use localhost for Kind clusters
	return "127.0.0.1"
}
