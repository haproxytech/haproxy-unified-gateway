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

//go:build linux

package caps

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// procStatus and procSysctl can be overridden in tests.
var (
	procStatus = "/proc/self/status"
	procSysctl = "/proc/sys/net/ipv4/ip_unprivileged_port_start"
)

// Detect reads /proc to determine the bind capabilities available to HAProxy.
// HAProxy is launched via haproxy_wrapper, which carries a file capability
// (setcap cap_net_bind_service=+ep), so it gains NET_BIND_SERVICE at exec time
// regardless of the controller's effective set. The file capability is bounded
// only by the container-wide bounding set, which the controller shares, so we
// read CapBnd (not CapEff) to predict whether HAProxy can bind privileged ports.
// On read errors it falls back to the conservative defaults (minUnprivPort=1024,
// hasNetBind=false) and logs the failure.
func Detect(ctx context.Context, logger *slog.Logger) PortBinder {
	minPort, errMin := readUnprivPortStart(procSysctl)
	if errMin != nil {
		minPort = 1024
		if logger != nil {
			logger.LogAttrs(
				ctx, slog.LevelWarn,
				"caps: could not read ip_unprivileged_port_start, defaulting to 1024",
				slog.String("error", errMin.Error()),
			)
		}
	}

	hasCap, errCap := readNetBindService(procStatus)
	if errCap != nil {
		if logger != nil {
			logger.LogAttrs(
				ctx, slog.LevelWarn,
				"caps: could not read CapBnd, assuming no NET_BIND_SERVICE",
				slog.String("error", errCap.Error()),
			)
		}
	}

	if logger != nil {
		logger.LogAttrs(
			ctx, slog.LevelInfo,
			"caps: detected port binding capabilities",
			slog.Int("min_unprivileged_port", int(minPort)),
			slog.Bool("net_bind_service", hasCap),
		)
	}

	return staticBinder{minUnprivPort: minPort, hasNetBind: hasCap}
}

func readUnprivPortStart(path string) (uint16, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 16)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", path, err)
	}
	return uint16(v), nil
}

// readNetBindService reports whether CAP_NET_BIND_SERVICE is in the bounding
// set (CapBnd). The bounding set caps what a file capability (the +ep on
// haproxy_wrapper) can raise into HAProxy's effective set, so it predicts
// HAProxy's reach better than the controller's CapEff.
func readNetBindService(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		rest, ok := strings.CutPrefix(line, "CapBnd:")
		if !ok {
			continue
		}
		bits, err := strconv.ParseUint(strings.TrimSpace(rest), 16, 64)
		if err != nil {
			return false, fmt.Errorf("parse CapBnd: %w", err)
		}
		return bits&(1<<unix.CAP_NET_BIND_SERVICE) != 0, nil
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	return false, errors.New("CapBnd line not found")
}
