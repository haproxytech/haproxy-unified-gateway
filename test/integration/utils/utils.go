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
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

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
