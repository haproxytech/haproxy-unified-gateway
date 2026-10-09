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
package metadata

import (
	"encoding/json"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

type FrontendMetaData map[string]map[string]K8sObjectInfo // map[kind] -> map[objectKey]K8sObjectInfo

// FrontendMetaData records, per frontend, the resources its listeners answer to and the
// generation each is programmed from: the Gateway for its own listeners, the ListenerSet for
// the entries it contributed. The kind is part of the key because a ListenerSet may share a
// name and namespace with its parent Gateway.
func (mm *ManagerImpl) FrontendMetaData(vListener *tree.VirtualListener) MetaData {
	frontendMetadata := make(FrontendMetaData)

	for _, listener := range vListener.Listeners {
		owner := mm.cs.OwnerK8sResource(listener)
		if owner == nil {
			continue
		}
		kind := string(listener.OwnerKind)
		if frontendMetadata[kind] == nil {
			frontendMetadata[kind] = make(map[string]K8sObjectInfo)
		}
		frontendMetadata[kind][client.ObjectKeyFromObject(owner).String()] = K8sObjectInfo{
			Generation: owner.GetGeneration(),
			LinkID:     mm.linkID,
		}
	}

	md := make(MetaData)
	// Marshal/unmarshal so the metadata has the same shape as the one parsed back out of
	// the configuration.
	o := make(map[string]any)
	by, _ := json.Marshal(frontendMetadata)
	_ = json.Unmarshal(by, &o)

	md[UnifiedGatewayMetaDataKey] = o

	return md
}

// func (mm *ManagerImpl) gatewayKeysFromFrontendMetadata(frontend *models.Frontend) (map[string]struct{}, error) {
// 	frontendMetadataI, ok := frontend.Metadata[UnifiedGatewayMetatDataKey]
// 	gateways := make(map[string]struct{})
// 	if !ok {
// 		return nil, fmt.Errorf("no %s metadata found in frontend %s", UnifiedGatewayMetatDataKey, frontend.Name)
// 	}

// 	by, err := json.Marshal(frontendMetadataI)
// 	if err != nil {
// 		return nil, err
// 	}
// 	var fmd FrontendMetaData
// 	if err := json.Unmarshal(by, &fmd); err != nil {
// 		return nil, fmt.Errorf("failed to unmarshal frontend metadata: %w", err)
// 	}

// 	gatewayMetadata, ok := fmd[b.params.extractGVK(objtypes.ObjectTypeGateway).Kind]
// 	if !ok {
// 		return nil, fmt.Errorf("no %s metadata found in frontend %s", b.params.extractGVK(objtypes.ObjectTypeGateway).Kind, frontend.Name)
// 	}

// 	for key := range gatewayMetadata {
// 		gateways[key] = struct{}{}
// 	}
// 	return gateways, nil
// }
