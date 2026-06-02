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

// Detect reads /proc to determine the effective bind capabilities of the
// current process. On read errors it falls back to the conservative defaults
// (minUnprivPort=1024, hasNetBind=false) and logs the failure.
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
				"caps: could not read CapEff, assuming no NET_BIND_SERVICE",
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

func readNetBindService(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		rest, ok := strings.CutPrefix(line, "CapEff:")
		if !ok {
			continue
		}
		bits, err := strconv.ParseUint(strings.TrimSpace(rest), 16, 64)
		if err != nil {
			return false, fmt.Errorf("parse CapEff: %w", err)
		}
		return bits&(1<<unix.CAP_NET_BIND_SERVICE) != 0, nil
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	return false, errors.New("CapEff line not found")
}
