// Copyright 2026 HAProxy Technologies LLC
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

package httproutenobackendrefs

import (
	"fmt"
	"io"
	"net/http"
	"path"
	"time"

	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"

	corev1 "k8s.io/api/core/v1"
	discoveryV1 "k8s.io/api/discovery/v1"
)

const (
	routeHost         = "nobe.haproxy"
	gatewayHTTPPort   = 31081 // template port, remapped through the suite PortMap
	requestTimeout    = 15 * time.Second
	requestInterval   = 1 * time.Second
	echoEndpointSlice = "http-echo"
)

// httpClient never follows redirects: redirect filters are asserted on the
// redirect response itself, not on the redirected target.
var httpClient = &http.Client{
	Timeout: 5 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

//go:fix inline
func toPtr[T any](v T) *T {
	return new(v)
}

// Test_NoBackendRefsRules drives real HTTP requests through HAProxy to prove
// that rules without backendRefs terminate on the controller's status-code
// backend (404 by default, 500 in Gateway API compatibility mode) instead of
// falling through to a valid catchall rule, while redirect filters and the
// catchall itself keep working.
func (s *NoBackendRefsSuite) Test_NoBackendRefsRules() {
	fixtureDirPath := utils.GetCRDFixturePath()

	manifests := []string{
		"echo-service.yaml",
		"gateway.yaml",
		"gatewayclass.yaml",
		"route-no-backendrefs.yaml",
	}
	s.CreateFixtures(fixtureDirPath, manifests)
	defer s.CleanupFixtures(fixtureDirPath, manifests)

	s.createEchoEndpoints()
	defer s.deleteEchoEndpoints()

	// The route programs fully: backendless rules do not degrade its status.
	expectationsPath := path.Join(fixtureDirPath, "expectations")
	s.ExpectRouteConditionsUpdated(s.IntTest().Ctx, s.IntTest().Namespace, "route-no-backendrefs",
		s.YamlToRouteConditions(path.Join(expectationsPath, "route-conditions.yaml")))
	s.ExpectAttachedRoute(s.IntTest().Ctx, s.IntTest().Namespace, "hug-gateway", "http", 1)

	pseudoStatus := http.StatusNotFound
	if s.gwapiCompat {
		pseudoStatus = http.StatusInternalServerError
	}

	// Omitted and explicit empty backendRefs win over the valid catchall.
	s.expectResponse(routeHost, "/nil-refs", nil, response{status: pseudoStatus})
	s.expectResponse(routeHost, "/empty-refs", nil, response{status: pseudoStatus})

	// A backendless prefix rule terminates its whole subtree, no fallthrough.
	s.expectResponse(routeHost, "/blocked-refs", nil, response{status: pseudoStatus})
	s.expectResponse(routeHost, "/blocked-refs/deeper", nil, response{status: pseudoStatus})

	// A redirect filter on a backendless rule still redirects.
	s.expectResponse(routeHost, "/redirect-refs", nil, response{
		status:   http.StatusFound,
		location: "https://example.com/redirect-refs",
	})

	// The conditional backendless candidate shares the catchall entry: the
	// header selects the status-code backend, its absence falls back to echo.
	backendlessHeader := map[string]string{"x-backend": "none"}
	s.expectResponse(routeHost, "/cond", backendlessHeader, response{status: pseudoStatus})
	s.expectResponse(routeHost, "/cond", nil, response{status: http.StatusOK, body: echoResponseBody})

	// The valid catchall still serves everything else.
	s.expectResponse(routeHost, "/catchall", nil, response{status: http.StatusOK, body: echoResponseBody})

	// A host with no route at all stays 404, in compatibility mode too.
	s.expectResponse("unknown.haproxy", "/nil-refs", nil, response{status: http.StatusNotFound})
}

// createEchoEndpoints wires the local echo server into the http-echo Service
// as a single ready endpoint, so catchall requests are proxied for real.
func (s *NoBackendRefsSuite) createEchoEndpoints() {
	endpoints := &discoveryV1.EndpointSlice{
		Name:        echoEndpointSlice,
		Labels:      map[string]string{"kubernetes.io/service-name": echoEndpointSlice},
		AddressType: discoveryV1.AddressTypeIPv4,
		Endpoints: []discoveryV1.Endpoint{
			{
				Addresses: []string{s.echoServer.address},
				Conditions: discoveryV1.EndpointConditions{
					Ready:   new(true),
					Serving: new(true),
				},
			},
		},
		Ports: []discoveryV1.EndpointPort{
			{
				Name:     new("http"),
				Port:     new(int32(s.echoServer.port)),
				Protocol: toPtr(corev1.ProtocolTCP),
			},
		},
	}
	endpoints.Namespace = s.IntTest().Namespace
	err := utils.CreateRuntimeObject(s.IntTest().Ctx, s.IntTest().Client, endpoints, true)
	s.Require().NoError(err)
}

func (s *NoBackendRefsSuite) deleteEchoEndpoints() {
	endpoints := &discoveryV1.EndpointSlice{
		Name: echoEndpointSlice,
	}
	endpoints.Namespace = s.IntTest().Namespace
	err := utils.DeleteRuntimeObject(s.IntTest().Ctx, s.IntTest().Client, endpoints, true)
	s.Require().NoError(err)
}

type response struct {
	status   int
	location string
	body     string
}

func (s *NoBackendRefsSuite) expectResponse(host, reqPath string, headers map[string]string, expect response) {
	var got response
	if !utils.WaitFor(s.IntTest().Ctx, requestInterval, requestTimeout, func() bool {
		got = s.doRequest(host, reqPath, headers)
		return got.status == expect.status &&
			(expect.location == "" || got.location == expect.location) &&
			(expect.body == "" || got.body == expect.body)
	}) {
		s.T().Fatalf("unexpected response for %s%s: got status=%d location=%q body=%q, want status=%d location=%q body=%q",
			host, reqPath, got.status, got.location, got.body, expect.status, expect.location, expect.body)
	}
}

func (s *NoBackendRefsSuite) doRequest(host, reqPath string, headers map[string]string) response {
	target := fmt.Sprintf("http://127.0.0.1:%d%s", s.IntTest().PortMap[gatewayHTTPPort], reqPath)
	req, err := http.NewRequest(http.MethodGet, target, nil)
	s.Require().NoError(err)
	req.Host = host
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return response{status: 0}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	s.Require().NoError(err)
	return response{
		status:   resp.StatusCode,
		location: resp.Header.Get("Location"),
		body:     string(body),
	}
}
