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
package protocols

import (
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type ProtocolCategory string

const (
	ProtocolCategorySecure   ProtocolCategory = "secure"
	ProtocolCategoryInsecure ProtocolCategory = "insecure"
	// ProtocolCategoryTLS is temporary.
	// When we can support TLS passthrough and HTTPS termination on the same port, then we should merge this into ProtocolCategorySecure.
	ProtocolCategoryTLS ProtocolCategory = "tls"
	ProcotolCategoryTCP ProtocolCategory = "tcp"
	ProcotolCategoryUDP ProtocolCategory = "udp"
)

var ProtocolCategories = map[gatewayv1.ProtocolType]ProtocolCategory{
	gatewayv1.HTTPProtocolType:  ProtocolCategoryInsecure,
	gatewayv1.HTTPSProtocolType: ProtocolCategorySecure,
	gatewayv1.TLSProtocolType:   ProtocolCategoryTLS,
	gatewayv1.TCPProtocolType:   ProcotolCategoryTCP,
	gatewayv1.UDPProtocolType:   ProcotolCategoryUDP,
}

// protocolsCarryingHostname are the protocols whose listeners may claim a hostname.
// The API admits the field on these only: on TCP and UDP it must be empty, so there
// is nothing there for a hostname conflict to be about.
var protocolsCarryingHostname = map[gatewayv1.ProtocolType]struct{}{
	gatewayv1.HTTPProtocolType:  {},
	gatewayv1.HTTPSProtocolType: {},
	gatewayv1.TLSProtocolType:   {},
}

// CarriesHostname reports whether a listener's hostname means anything on this
// protocol. A predicate rather than the set, because a reader asking the question
// should not have to know that absence from the set means "no".
func CarriesHostname(protocol gatewayv1.ProtocolType) bool {
	_, ok := protocolsCarryingHostname[protocol]
	return ok
}
