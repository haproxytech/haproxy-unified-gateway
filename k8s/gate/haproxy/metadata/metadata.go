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
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"
	utilsk8s "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils-k8s"
)

const (
	UnifiedGatewayMetaDataKey string = "hug"
	LinkIDMetaDataKey         string = "LinkID"
)

type (
	MetaData map[string]any
)

type K8sObjectInfo struct {
	LinkID     string
	Generation int64
}

type Manager interface {
	FrontendMetaData(vListener *tree.VirtualListener) MetaData
	BackendMetaData(routesInfo map[string]RouteMetadaInfo) MetaData
}

type ManagerImpl struct {
	extractGVK utilsk8s.ExtractGVK
	cs         *tree.ControllerStore
	linkID     string
}

var _ Manager = &ManagerImpl{}

func NewManager(extractGVK utilsk8s.ExtractGVK, cs *tree.ControllerStore, linkID string) Manager {
	return &ManagerImpl{
		extractGVK: extractGVK,
		linkID:     linkID,
		cs:         cs,
	}
}
