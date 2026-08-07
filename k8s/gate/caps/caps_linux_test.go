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
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func TestReadUnprivPortStart(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name    string
		content string
		want    uint16
		wantErr bool
	}{
		{"default", "1024\n", 1024, false},
		{"relaxed", "0\n", 0, false},
		{"no newline", "443", 443, false},
		{"garbage", "not-a-number\n", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := writeFile(t, dir, tc.name, tc.content)
			got, err := readUnprivPortStart(p)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr=%v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}

	t.Run("missing file", func(t *testing.T) {
		if _, err := readUnprivPortStart(filepath.Join(dir, "does-not-exist")); err == nil {
			t.Fatal("expected error for missing file")
		}
	})
}

func TestReadNetBindService(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name    string
		content string
		want    bool
		wantErr bool
	}{
		{
			name: "only NET_BIND_SERVICE",
			content: `Name:	hug
CapBnd:	0000000000000400
`,
			want: true,
		},
		{
			name: "no caps",
			content: `Name:	hug
CapBnd:	0000000000000000
`,
			want: false,
		},
		{
			name: "full caps incl NET_BIND_SERVICE",
			content: `Name:	hug
CapBnd:	00000000a80425fb
`,
			want: true,
		},
		{
			name: "missing CapBnd",
			content: `Name:	hug
CapPrm:	0000000000000400
`,
			wantErr: true,
		},
		{
			name:    "malformed hex",
			content: "CapBnd:\tzzzz\n",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := writeFile(t, dir, tc.name, tc.content)
			got, err := readNetBindService(p)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr=%v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDetect_FallbacksOnReadError(t *testing.T) {
	dir := t.TempDir()
	orig1, orig2 := procStatus, procSysctl
	t.Cleanup(func() { procStatus, procSysctl = orig1, orig2 })

	procStatus = filepath.Join(dir, "missing-status")
	procSysctl = filepath.Join(dir, "missing-sysctl")

	b := Detect(context.Background(), nil)
	if got := b.MinUnprivilegedPort(); got != 1024 {
		t.Errorf("MinUnprivilegedPort fallback = %d, want 1024", got)
	}
	if b.HasNetBindService() {
		t.Error("HasNetBindService fallback = true, want false")
	}
	if b.CanBind(80) {
		t.Error("CanBind(80) = true, want false")
	}
	if !b.CanBind(8080) {
		t.Error("CanBind(8080) = false, want true")
	}
}

func TestDetect_FromFiles(t *testing.T) {
	dir := t.TempDir()
	orig1, orig2 := procStatus, procSysctl
	t.Cleanup(func() { procStatus, procSysctl = orig1, orig2 })

	procStatus = writeFile(t, dir, "status", "CapBnd:\t0000000000000400\n")
	procSysctl = writeFile(t, dir, "sysctl", "1024\n")

	b := Detect(context.Background(), nil)
	if !b.HasNetBindService() {
		t.Error("expected HasNetBindService=true")
	}
	if !b.CanBind(80) {
		t.Error("CanBind(80) with cap should be true")
	}
}
