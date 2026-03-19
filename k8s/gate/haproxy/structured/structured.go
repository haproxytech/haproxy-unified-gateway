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
package structured

import (
	"github.com/haproxytech/client-native/v6/models"
)

// GlobalKey is the fixed map key used for the single Global entry.
const GlobalKey = "global"

type Structured struct {
	// Note that for Deleted Structured:
	// *models.Frontend will be nil
	// *models.Backend will be nil
	// certificate.CertificateData.Data will be empty
	Frontends map[string]*models.Frontend // map[frontendName] => Frontend
	Backends  map[string]*models.Backend  // map[backendName]  => Backend
	Globals   map[string]*models.Global   // map[GlobalKey]    => Global (at most one entry)
	Defaults  map[string]*models.Defaults // map[DefaultsKey]  => Defaults (at most one entry)
}

func NewStructuredConf() Structured {
	return Structured{
		Frontends: make(map[string]*models.Frontend),
		Backends:  make(map[string]*models.Backend),
		Globals:   make(map[string]*models.Global),
		Defaults:  make(map[string]*models.Defaults),
	}
}

func (c Structured) IsEmpty() bool {
	return len(c.Frontends) == 0 && len(c.Backends) == 0 && len(c.Globals) == 0 && len(c.Defaults) == 0
}

// Maps
// Runtime
