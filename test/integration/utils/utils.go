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
	"bytes"
	"context"
	"hash/fnv"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// TemplatePorts lists every port number used in test gateway manifests.
// Sorted descending so substitution always replaces longer ports first
// (e.g. 31081 before 8081) and avoids partial-match corruption.
var TemplatePorts = []int{31445, 31444, 31081, 9090, 8443, 8088, 8081, 8080}

// DerivePortMap deterministically maps each template port to a unique unprivileged
// port derived from the test namespace name. Using a hash avoids any TOCTOU race:
// no listener is opened and immediately closed, so no other process can steal the
// port between allocation and the eventual HAProxy bind.
//
// Port range: 20000–29999. This sits below Linux's default ephemeral range
// (32768–60999), above the IANA registered range, and is unlikely to be used by
// system services. With 5000 available base slots (one per 2 ports) and the small
// number of test packages, hash collisions between packages are astronomically
// unlikely.
func DerivePortMap(namespace string, templatePorts []int) map[int]int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(namespace))
	// 2 ports per slot keeps the arithmetic simple; adjust if more template ports
	// are added in the future (len(templatePorts) per slot is the safe bound).
	const (
		basePort  = 20000
		rangeSize = 10000 // 20000–29999
	)
	nSlots := rangeSize / len(templatePorts)
	base := basePort + int(h.Sum32()%uint32(nSlots))*len(templatePorts)

	// Process template ports largest-first so that the substitution order matches
	// SubstitutePortsInContent (both must agree on which index maps to which slot).
	sorted := make([]int, len(templatePorts))
	copy(sorted, templatePorts)
	slices.SortFunc(sorted, func(a, b int) int { return b - a })

	pm := make(map[int]int, len(templatePorts))
	for i, tp := range sorted {
		pm[tp] = base + i
	}
	return pm
}

// SubstitutePortsInContent replaces every template port number that appears in
// content with its actual counterpart from portMap. Ports are processed in
// descending order so that a longer port (e.g. 31081) is replaced before a
// shorter one it contains (e.g. 8081), preventing partial-match corruption.
func SubstitutePortsInContent(content []byte, portMap map[int]int) []byte {
	if len(portMap) == 0 {
		return content
	}
	tps := make([]int, 0, len(portMap))
	for tp := range portMap {
		tps = append(tps, tp)
	}
	slices.SortFunc(tps, func(a, b int) int { return b - a })
	for _, tp := range tps {
		content = bytes.ReplaceAll(content,
			[]byte(strconv.Itoa(tp)),
			[]byte(strconv.Itoa(portMap[tp])),
		)
	}
	return content
}

// SubstitutePortsInString is the string-valued variant of SubstitutePortsInContent.
func SubstitutePortsInString(s string, portMap map[int]int) string {
	return string(SubstitutePortsInContent([]byte(s), portMap))
}

func GetIntTestNamespace(levelsUp int) (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for range levelsUp {
		dir = filepath.Dir(dir)
	}
	dir = filepath.Base(dir)
	dir = strings.Map(func(r rune) rune {
		if (r < 'a' || r > 'z') && r != '-' {
			return '-'
		}
		return r
	}, strings.ToLower(dir))
	return "e2e-tests-" + dir, nil
}

func GetCRDFixturePath() string {
	path := "manifests/"

	return path
}

// WaitFor is a convenience wrapper that makes simple, "brute force"
// waiting loops easier to write.
func WaitFor(ctx context.Context, interval time.Duration, timeout time.Duration, callback func() bool) bool {
	//revive:disable
	err := wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (bool, error) {
		return callback(), nil
	})
	//revive:enable
	return err == nil
}

func SortListenerStatus(listeners []gatewayv1.ListenerStatus) {
	slices.SortFunc(listeners, func(a, b gatewayv1.ListenerStatus) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	for _, l := range listeners {
		slices.SortFunc(l.Conditions, func(a, b v1.Condition) int {
			if a.Type < b.Type {
				return -1
			}
			if a.Type > b.Type {
				return 1
			}
			return 0
		})
	}
}
