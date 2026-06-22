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
package configuration

import (
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy"
)

func externalDefaults() External {
	return External{
		HaproxyBinary: "/usr/local/sbin/haproxy",
		CfgDir:        "/tmp/hug/etc",
		AuxDir:        "/tmp/hug/etc/aux",
		RuntimeDir:    "/tmp/hug/run",
		StateDir:      "/tmp/hug/state/",
	}
}

func HaproxyDefaults() haproxy.HaproxyDirs {
	return haproxy.HaproxyDirs{
		HaproxyBinary: "/usr/local/sbin/haproxy",
		CfgDir:        "/usr/local/hug",
		AuxDir:        "/usr/local/hug/aux",
		RuntimeDir:    "/var/run",
		StateDir:      "/var/state/haproxy",
	}
}

var (
	defaultControllerPort           = 31060
	defaultHTTPPortIngressFrontend  = 8080
	defaultHTTPSPortIngressFrontend = 8443
)
