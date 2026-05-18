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

//go:build !linux

package caps

import (
	"context"
	"log/slog"
)

// Detect on non-Linux returns a permissive binder; HUG only ships on Linux,
// but unit tests / dev builds may run elsewhere.
// Developer note: behaviour will differ on Linux deploy — privileged ports
// (<1024) will be rejected there unless CAP_NET_BIND_SERVICE is granted or
// net.ipv4.ip_unprivileged_port_start is lowered.
func Detect(_ context.Context, _ *slog.Logger) PortBinder {
	return staticBinder{minUnprivPort: 0, hasNetBind: true}
}
