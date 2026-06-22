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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

var (
	_                              Builder = &SyntheticGatewayBuilderImpl{}
	syntheticGatewayNamespacedName         = types.NamespacedName{
		Namespace: "",
		Name:      "ing:gateway",
	}
)

type SyntheticGatewayBuilderImpl struct {
	*ControllerStore
}

func NewSyntheticGatewayBuilder(controllerStore *ControllerStore) Builder {
	return &SyntheticGatewayBuilderImpl{
		ControllerStore: controllerStore,
	}
}

func (b *SyntheticGatewayBuilderImpl) ComputeTreeUpdates() {
	if len(b.ClusterStore.Updates.Ingresses) == 0 &&
		b.ClusterStore.Gateways[syntheticGatewayNamespacedName] != nil {
		return
	}

	previousGatewayUpdate := b.ClusterStore.Updates.Gateways[syntheticGatewayNamespacedName]
	previousGatewayUpdate.OldObject = previousGatewayUpdate.NewObject
	previousGatewayUpdate.NewObject = &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      syntheticGatewayNamespacedName.Name,
			Namespace: syntheticGatewayNamespacedName.Namespace,
		},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "ing:gatewayclass",
			Listeners: []gatewayv1.Listener{
				{Name: "http", Port: gatewayv1.PortNumber(b.ControllerStore.HTTPIngressFrontendPort), Protocol: gatewayv1.HTTPProtocolType},
				{Name: "https", Port: gatewayv1.PortNumber(b.ControllerStore.HTTPSIngressFrontendPort), Protocol: gatewayv1.HTTPSProtocolType,
					TLS: &gatewayv1.ListenerTLSConfig{ /* CertificateRefs vide */ }},
			},
			// Hostname nil to match everything.
		},
	}

	b.ClusterStore.Updates.Gateways[syntheticGatewayNamespacedName] = previousGatewayUpdate
}

func (*SyntheticGatewayBuilderImpl) CleanTreeUpdates() {}
