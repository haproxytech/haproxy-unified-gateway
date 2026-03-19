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
package logging

import v3 "github.com/haproxytech/haproxy-unified-gateway/api/gate/v3"

var (
	LogCategoryK8s           v3.Category = "k8s"
	LogCategoryGate          v3.Category = "gate"
	LogCategoryStatus        v3.Category = "status"
	LogCategoryHaproxyCfgMgr v3.Category = "haproxycfg"
	LogCategoryApp           v3.Category = "app"
	LogCategoryBatch         v3.Category = "batch"
	LogCategoryReloadMgr     v3.Category = "reloadmgr"
	LogCategoryCertsStorage  v3.Category = "certs-storage"
	LogCategoryMapsStorage   v3.Category = "maps-storage"
	LogCategoryHugService    v3.Category = "hugservice"
)

type LogHandlerType string

const (
	LogHandlerTypeJSON LogHandlerType = "json"
	LogHandlerTypeText LogHandlerType = "text"
)
