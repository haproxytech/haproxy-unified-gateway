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
package haproxy

import (
	"time"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/storage"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/templates"
	utilsk8s "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils-k8s"
)

type HaproxyConfMgrParams struct {
	certificateStorage storage.CertificateStorage
	mapsStorage        storage.MapsStorage
	extractGVK         utilsk8s.ExtractGVK
	HaproxyConfParams
}

type HaproxyConfParams struct {
	// HaproxyDirs contains all the needed dir
	// used for example in map files reference
	HaproxyDirs
	// Templates for all generated names (Frontend, Backend, Server)
	templates.Templates
	// IPv4BindAddress is the IPv4 address to bind to.
	IPv4BindAddress string
	// IPv6BindAddress is the IPv6 address to bind to.
	IPv6BindAddress string
	// DefaultsSectionName is the defaults name to use to create FE/BE/...
	DefaultsSectionName string
	// StoreCertificateStructureType contains the structure type of the certificates storage
	StoreCertificateStructureType storage.StructureType
	StoreMapsStructureType        storage.StructureType
	// LindID: an ID for the link to the cluster
	LinkID string
	// TimeoutWaitForRuntime speficied the max time to wait for HUG library to the runtime.Runtime at startup
	// This is used in combination with UpdateHaproxyThroughRuntime = true
	// If UpdateHaproxyThroughRuntime = false, the gate library does not need runtime.Runtime and does not wait for it.
	TimeoutWaitForRuntime time.Duration
	// DisableIPv4 indicates whether IPv4 is disabled.
	DisableIPv4 bool
	// DisableIPv6 indicates whether IPv6 is disabled.
	DisableIPv6 bool
	// RuntimeUpdateHaproxy is a flag that indicates to the gate library to try to perform runtime commands
	RuntimeUpdateHaproxy bool
	// StoreCertificatesOnDisk is a flag that indicates to the gate library to store certificates on disk
	StoreCertificateOnDisk bool
	// StoreMapsOnDisk is a flag that indicates to the gate library to store maps on disk
	StoreMapsOnDisk bool
}

type HaproxyDirs struct {
	CfgDir        string
	MainCfgFile   string
	RouteLuaFile  string
	HaproxyBinary string
	RuntimeDir    string
	StateDir      string
	AuxDir        string
	PIDFile       string
	RuntimeSocket string
	MasterSocket  string
	PatternDir    string
	ErrFileDir    string
	CertsDir      string
	CertListDir   string
	MapsDir       string
}

func NewHaproxyConfMgrParams(extractGVK utilsk8s.ExtractGVK, haproxyConfParams HaproxyConfParams,
	certificateStorage storage.CertificateStorage, mapsStorageEx storage.MapsStorage,
) (HaproxyConfMgrParams, error) {
	return HaproxyConfMgrParams{
		extractGVK:         extractGVK,
		HaproxyConfParams:  haproxyConfParams,
		certificateStorage: certificateStorage,
		mapsStorage:        mapsStorageEx,
	}, nil
}
