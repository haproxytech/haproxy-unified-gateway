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
package objtypes

import (
	v3 "github.com/haproxytech/haproxy-unified-gateway/api/gate/v3"
	v1 "k8s.io/api/core/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

var (
	ObjectTypeGatewayClass = &gatewayv1.GatewayClass{}
	ObjectTypeGateway      = &gatewayv1.Gateway{}
	ObjectTypeHTTPRoute    = &gatewayv1.HTTPRoute{}
	ObjectTypeService      = &v1.Service{}
	ObjectTypeSecret       = &v1.Secret{}
	ObjectTypeHugGate      = &v3.HugGate{}
	ObjectTypeBackend      = &v3.Backend{}
	ObjectTypeGlobal       = &v3.Global{}
	ObjectTypeDefaults     = &v3.Defaults{}
	ObjectTypeTLSRoute     = &gatewayv1alpha2.TLSRoute{}
	ObjectTypeRefGrant     = &gatewayv1.ReferenceGrant{}
)

var (
	KindHTTPRoute = "HTTPRoute"
	KindTLSRoute  = "TLSRoute"
	KindGRPCRoute = "GRPCRoute"
	KindTCPRoute  = "TCPRoute"
	KindUDPRoute  = "UDPRoute"
)

var (
	RouteKindHTTP = gatewayv1.RouteGroupKind{Group: new(gatewayv1.Group(gatewayv1.GroupName)), Kind: gatewayv1.Kind(KindHTTPRoute)}
	RouteKindTLS  = gatewayv1.RouteGroupKind{Group: new(gatewayv1.Group(gatewayv1.GroupName)), Kind: gatewayv1.Kind(KindTLSRoute)}
	RouteKindGRPC = gatewayv1.RouteGroupKind{Group: new(gatewayv1.Group(gatewayv1.GroupName)), Kind: gatewayv1.Kind(KindGRPCRoute)}
	RouteKindTCP  = gatewayv1.RouteGroupKind{Group: new(gatewayv1.Group(gatewayv1.GroupName)), Kind: gatewayv1.Kind(KindTCPRoute)}
	RouteKindUDP  = gatewayv1.RouteGroupKind{Group: new(gatewayv1.Group(gatewayv1.GroupName)), Kind: gatewayv1.Kind(KindUDPRoute)}
)
