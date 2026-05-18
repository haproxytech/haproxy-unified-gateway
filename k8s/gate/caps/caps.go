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

// Package caps detects whether the running process can bind TCP/UDP ports
// without root, accounting for CAP_NET_BIND_SERVICE and the per-netns
// ip_unprivileged_port_start sysctl.
package caps

// PortBinder reports whether the running process can bind to a given port
// in its current network namespace and capability set.
type PortBinder interface {
	CanBind(port uint16) bool
	// MinUnprivilegedPort is the lowest port the process can bind to without
	// CAP_NET_BIND_SERVICE. Useful for diagnostic messages.
	MinUnprivilegedPort() uint16
	// HasNetBindService reports whether CAP_NET_BIND_SERVICE is in the
	// effective capability set.
	HasNetBindService() bool
}

// Static returns a PortBinder with fixed values. Used for tests and as the
// non-Linux fallback.
func Static(minUnprivPort uint16, hasNetBind bool) PortBinder {
	return staticBinder{minUnprivPort: minUnprivPort, hasNetBind: hasNetBind}
}

type staticBinder struct {
	minUnprivPort uint16
	hasNetBind    bool
}

func (s staticBinder) CanBind(port uint16) bool {
	return port >= s.minUnprivPort || s.hasNetBind
}

func (s staticBinder) MinUnprivilegedPort() uint16 { return s.minUnprivPort }
func (s staticBinder) HasNetBindService() bool     { return s.hasNetBind }
