// Copyright 2026 HAProxy Technologies LLC
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

// Package harness launches a real haproxy subprocess with route.lua loaded
// for integration tests. Each test directory ships a real haproxy.cfg and
// any map fixtures it needs; the harness copies them into a tempdir, fills in
// runtime placeholders, and exposes HTTP / runtime-socket helpers.
//
// Placeholders substituted in the cfg:
//
//	{{HTTP_SOCK}}   unix socket the frontend should bind to
//	{{ADMIN_SOCK}}  runtime API socket path
//	{{ROUTE_LUA}}   absolute path to fs/usr/local/hug/route.lua
//	{{DIR}}         tempdir where this test's fixtures live (maps, etc.)
//
// Tests skip if the haproxy binary is missing or older than 3.1 (route.lua
// relies on core.get_patref iteration, which is reliable from 3.1+).
package harness

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

const haproxyBin = "haproxy"

// routeLuaPath returns the absolute path of fs/usr/local/hug/route.lua,
// resolved relative to this source file.
func routeLuaPath(t *testing.T) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// harness.go → test/lua/internal/harness/  →  ../../../../ is repo root.
	p, err := filepath.Abs(filepath.Join(filepath.Dir(here),
		"..", "..", "..", "..", "fs", "usr", "local", "hug", "route.lua"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("route.lua not found at %s: %v", p, err)
	}
	return p
}

var versionRE = regexp.MustCompile(`HAProxy version (\d+)\.(\d+)`)

func skipIfHaproxyMissing(t *testing.T) {
	t.Helper()
	out, err := exec.Command(haproxyBin, "-v").CombinedOutput()
	if err != nil {
		t.Skipf("haproxy not available: %v\n%s", err, out)
	}
	m := versionRE.FindSubmatch(out)
	if m == nil {
		t.Skipf("could not parse haproxy version from %q", out)
	}
	maj, _ := strconv.Atoi(string(m[1]))
	minor, _ := strconv.Atoi(string(m[2]))
	if maj < 3 || (maj == 3 && minor < 1) {
		t.Skipf("haproxy %d.%d too old; tests require >= 3.1", maj, minor)
	}
}

// Harness wraps a running haproxy instance.
type Harness struct {
	client    *http.Client
	logBuf    *bytes.Buffer
	Dir       string // tempdir where copied fixtures live
	HTTPSock  string // unix socket for HTTP requests
	AdminSock string // unix socket for runtime API
}

// New starts haproxy using the cfg file at cfgPath (relative to the test's
// working directory, which is the test file's package directory). Every
// non-.go sibling file under the cfg's directory is copied into a tempdir
// before haproxy starts, so map files etc. can live next to the cfg.
func New(t *testing.T, cfgPath string) *Harness {
	t.Helper()
	skipIfHaproxyMissing(t)

	absCfg, err := filepath.Abs(cfgPath)
	if err != nil {
		t.Fatalf("resolve cfg path: %v", err)
	}
	dir := t.TempDir()
	h := &Harness{
		Dir:       dir,
		HTTPSock:  filepath.Join(dir, "http.sock"),
		AdminSock: filepath.Join(dir, "admin.sock"),
		logBuf:    &bytes.Buffer{},
	}

	copyFixtures(t, filepath.Dir(absCfg), dir)
	cfgInTmp := filepath.Join(dir, filepath.Base(absCfg))
	rendered := renderCfg(t, cfgInTmp, h)
	startHaproxy(t, cfgInTmp, rendered, h)
	return h
}

// copyFixtures mirrors srcDir into dstDir, skipping Go sources.
func copyFixtures(t *testing.T, srcDir, dstDir string) {
	t.Helper()
	err := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dstDir, rel), 0o755)
		}
		if strings.HasSuffix(rel, ".go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dstDir, rel), b, 0o644)
	})
	if err != nil {
		t.Fatalf("copy fixtures: %v", err)
	}
}

// renderCfg reads cfgPath, substitutes placeholders, writes it back, and
// returns the rendered text for error messages.
func renderCfg(t *testing.T, cfgPath string, h *Harness) string {
	t.Helper()
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read copied cfg: %v", err)
	}
	rendered := strings.NewReplacer(
		"{{HTTP_SOCK}}", h.HTTPSock,
		"{{ADMIN_SOCK}}", h.AdminSock,
		"{{ROUTE_LUA}}", routeLuaPath(t),
		"{{DIR}}", h.Dir,
	).Replace(string(raw))
	if err := os.WriteFile(cfgPath, []byte(rendered), 0o644); err != nil {
		t.Fatalf("rewrite cfg: %v", err)
	}
	return rendered
}

// startHaproxy runs `haproxy -c` for early validation, launches haproxy,
// waits for the admin socket to appear, builds the HTTP client, and
// registers cleanup. rendered is the cfg text — only used for error reports.
func startHaproxy(t *testing.T, cfgPath, rendered string, h *Harness) {
	t.Helper()
	if out, err := exec.Command(haproxyBin, "-c", "-f", cfgPath).CombinedOutput(); err != nil {
		t.Fatalf("haproxy -c failed: %v\n%s\n--- cfg ---\n%s", err, out, rendered)
	}
	cmd := exec.Command(haproxyBin, "-W", "-db", "-f", cfgPath)
	cmd.Stdout = h.logBuf
	cmd.Stderr = h.logBuf
	if err := cmd.Start(); err != nil {
		t.Fatalf("haproxy start: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(h.AdminSock); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(h.AdminSock); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("haproxy did not create admin socket; log:\n%s", h.logBuf.String())
	}
	h.client = &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", h.HTTPSock)
			},
		},
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		if t.Failed() {
			t.Logf("haproxy log:\n%s", h.logBuf.String())
		}
	})
}

// Get sends a GET request. hdrs are alternating key/value pairs; "Host" is
// handled specially (Go's Header.Set("Host",…) is silently ignored).
func (h *Harness) Get(t *testing.T, urlPath string, hdrs ...string) (status int, body string) {
	t.Helper()
	// http (not https) is correct: the connection is a unix socket; "x" is a
	// placeholder host the server never sees. The transport's DialContext
	// ignores it and dials h.HTTPSock instead.
	//revive:disable-next-line:unsecure-url-scheme
	req, err := http.NewRequest("GET", "http://x"+urlPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(hdrs); i += 2 {
		if strings.EqualFold(hdrs[i], "Host") {
			req.Host = hdrs[i+1]
		} else {
			req.Header.Set(hdrs[i], hdrs[i+1])
		}
	}
	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(b)
}

// Socket sends a command to the runtime API socket and returns the response.
func (h *Harness) Socket(t *testing.T, cmd string) string {
	t.Helper()
	c, err := net.Dial("unix", h.AdminSock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte(cmd + "\n")); err != nil {
		t.Fatal(err)
	}
	if uc, ok := c.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}
	b, err := io.ReadAll(c)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Log returns the haproxy stdout/stderr captured so far.
func (h *Harness) Log() string { return h.logBuf.String() }
