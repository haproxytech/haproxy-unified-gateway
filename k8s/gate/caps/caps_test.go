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

package caps

import "testing"

func TestStatic_CanBind(t *testing.T) {
	tests := []struct {
		name          string
		minUnprivPort uint16
		hasNetBind    bool
		port          uint16
		want          bool
	}{
		{"default min, port 80, no cap", 1024, false, 80, false},
		{"default min, port 80, with cap", 1024, true, 80, true},
		{"default min, port 1024, no cap", 1024, false, 1024, true},
		{"default min, port 8080, no cap", 1024, false, 8080, true},
		{"sysctl=0, port 80, no cap", 0, false, 80, true},
		{"sysctl=0, port 1, no cap", 0, false, 1, true},
		{"sysctl=80, port 79, no cap", 80, false, 79, false},
		{"sysctl=80, port 80, no cap", 80, false, 80, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := Static(tc.minUnprivPort, tc.hasNetBind)
			if got := b.CanBind(tc.port); got != tc.want {
				t.Errorf("CanBind(%d) = %v, want %v", tc.port, got, tc.want)
			}
			if got := b.MinUnprivilegedPort(); got != tc.minUnprivPort {
				t.Errorf("MinUnprivilegedPort() = %d, want %d", got, tc.minUnprivPort)
			}
			if got := b.HasNetBindService(); got != tc.hasNetBind {
				t.Errorf("HasNetBindService() = %v, want %v", got, tc.hasNetBind)
			}
		})
	}
}
