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
package storage

import "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/storage/maps"

type StructureType string

const (
	// StructureTypeCertDefault handles a default storage algorithm
	// Default algorithm for Certificate Storage
	// namespace/take two first characters of a secret name as folder
	// For example for secrets: namespace/secret-name-1 , namespace/secret-name-2, namespace/my-secret-name-1
	// - /etc/unified.../certs/<namespace>/se/
	// - /etc/unified.../certs/<namespace>/se/
	// - /etc/unified.../certs/<namespace>/my/
	StructureTypeCertDefault = "default"
	// StructureTypeMapsDefault handles a default storage algorithm
	// Default algorithm for Maps Storage
	// TODO
	StructureTypeMapsDefault = "default"
)

type CertificateStorage interface {
	CertStorage
	CrtListStorage
}

type MapsStorage interface {
	DeleteMapsDirectoryForFrontend(frontendName string) error
	DeleteMapsDirectory() error
	GetPathExactMapFile(frontendName string) *maps.MapFileState
	GetPathPrefixMapFile(frontendName string) *maps.MapFileState
	GetPathRegexMapFile(frontendName string) *maps.MapFileState
	GetSniMapFile(frontendName string) *maps.MapFileState
	GetListenerExactMatchMapFile(frontendName string) *maps.MapFileState
	GetListenerWildcardMatchMapFile(frontendName string) *maps.MapFileState
	GetListenerRouteExactMatchMapFile(frontendName string) *maps.MapFileState
	GetListenerRouteWildcardMatchMapFile(frontendName string) *maps.MapFileState
	GetMaps() map[string]map[string]*maps.MapFileState
	ProcessMapFiles()
}
