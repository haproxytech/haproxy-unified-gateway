//
// Copyright 2025 HAProxy Technologies LLC
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

package utils // revive:disable:var-naming

import (
	"encoding/json"
	"log"
	"os"

	"github.com/haproxytech/client-native/v6/models"
	"go.yaml.in/yaml/v2"
)

func marshalOmitEmpty(v any) ([]byte, error) {
	jsonData, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m any
	if err = json.Unmarshal(jsonData, &m); err != nil {
		return nil, err
	}
	return yaml.Marshal(m)
}

func ExportFrontend(fe *models.Frontend) {
	yamlData, err := marshalOmitEmpty(fe)
	if err != nil {
		log.Printf("Error marshaling to YAML: %v", err)
	}
	err = os.WriteFile(fe.Name+".yaml", yamlData, 0o644)
	if err != nil {
		log.Printf("Error writing file: %v", err)
	}
}

func ExportBackend(be *models.Backend) {
	yamlData, err := marshalOmitEmpty(be)
	if err != nil {
		log.Printf("Error marshaling to YAML: %v", err)
	}
	err = os.WriteFile(be.Name+".yaml", yamlData, 0o644)
	if err != nil {
		log.Printf("Error writing file: %v", err)
	}
}
