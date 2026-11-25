package haproxy

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/haproxytech/client-native/v6/runtime"
	"github.com/haproxytech/haproxy-unified-gateway/hug/haproxy/api"
	"github.com/haproxytech/haproxy-unified-gateway/hug/reload"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/structured"
)

// fakeRuntime records the runtime calls; unimplemented methods panic (nil embedded interface).
type fakeRuntime struct {
	runtime.Runtime
	calls     []string
	failAdd   bool
	failDel   bool
	failMaint bool
}

func (f *fakeRuntime) AddServer(be, name, _ string) error {
	f.calls = append(f.calls, "add "+name)
	if f.failAdd {
		return errors.New("add failed")
	}
	return nil
}

func (f *fakeRuntime) EnableServer(_, name string) error {
	f.calls = append(f.calls, "enable "+name)
	return nil
}

func (f *fakeRuntime) DeleteServer(_, name string) error {
	f.calls = append(f.calls, "del "+name)
	if f.failDel {
		return errors.New("del failed")
	}
	return nil
}

func (f *fakeRuntime) ExecuteRaw(cmd string) (string, error) {
	f.calls = append(f.calls, "raw "+cmd)
	if f.failMaint {
		return "", errors.New("raw failed")
	}
	return "", nil
}

type fakeClient struct {
	api.HAProxyClient
	rt *fakeRuntime
}

func (f *fakeClient) RuntimeClient() runtime.Runtime { return f.rt }
func (f *fakeClient) DefaultsSectionGet(string) (*models.Defaults, error) {
	return &models.Defaults{}, nil
}

var reloadMu sync.Mutex

func newApplyMgr(t *testing.T, old []string) (*HaproxyConfMgrImpl, *fakeRuntime) {
	t.Helper()
	reloadMu.Lock()
	t.Cleanup(func() { reload.Instance().Reset(); reloadMu.Unlock() })
	reload.Instance().Reset()

	rt := &fakeRuntime{}
	mgr := newTestServersMgr("default", "svc", nil)
	mgr.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr.params.RuntimeUpdateHaproxy = true
	mgr.haproxyClient = &fakeClient{rt: rt}
	mgr.configuration.structured = structured.Structured{Backends: map[string]*models.Backend{
		"be": {BackendBase: models.BackendBase{Name: "be"}, Servers: serversOf(old...)},
	}}
	mgr.configuration.resetDiffs()
	return mgr, rt
}

func serversOf(names ...string) map[string]models.Server {
	m := map[string]models.Server{}
	for _, n := range names {
		m[n] = models.Server{Name: n, Address: "10.0.0.1"}
	}
	return m
}

func apply(t *testing.T, mgr *HaproxyConfMgrImpl, next ...string) error {
	t.Helper()
	servers := serversOf(next...)
	diffs, err := mgr.configuration.upsertBackendWithServers(mgr.logger, "be", servers)
	if err != nil {
		t.Fatal(err)
	}
	return mgr.runtimeApplyServerDiffs("be", diffs, servers)
}

func firstIndex(calls []string, prefix string) int {
	for i, c := range calls {
		if strings.HasPrefix(c, prefix) {
			return i
		}
	}
	return -1
}

func lastIndex(calls []string, prefix string) int {
	idx := -1
	for i, c := range calls {
		if strings.HasPrefix(c, prefix) {
			idx = i
		}
	}
	return idx
}

func TestApplyFullReplacementAddsBeforeDeleting(t *testing.T) {
	mgr, rt := newApplyMgr(t, []string{"A", "B"})
	if err := apply(t, mgr, "C", "D"); err != nil {
		t.Fatal(err)
	}
	if lastIndex(rt.calls, "add ") < 0 || firstIndex(rt.calls, "raw ") < 0 || firstIndex(rt.calls, "del ") < 0 {
		t.Fatalf("missing calls: %v", rt.calls)
	}
	if lastIndex(rt.calls, "enable ") > firstIndex(rt.calls, "raw ") {
		t.Fatalf("a delete/maint started before all servers were added and enabled: %v", rt.calls)
	}
	if reload.Instance().DynamicUpdateServerStateFailed() {
		t.Fatal("unexpected failure flag")
	}
}

func TestApplyKeepsServerWithSameName(t *testing.T) {
	mgr, rt := newApplyMgr(t, []string{"A", "B"})
	if err := apply(t, mgr, "B", "C"); err != nil {
		t.Fatal(err)
	}
	for _, c := range rt.calls {
		if strings.Contains(c, "B") && !strings.HasPrefix(c, "raw") {
			t.Fatalf("B must not be touched: %v", rt.calls)
		}
		if strings.HasPrefix(c, "raw") && strings.Contains(c, "/B ") {
			t.Fatalf("B must not be put in maint: %v", rt.calls)
		}
	}
	if firstIndex(rt.calls, "add C") < 0 || firstIndex(rt.calls, "del A") < 0 {
		t.Fatalf("expected add C and del A: %v", rt.calls)
	}
}

func TestApplyPartialReplacementLeavesUnchangedAlone(t *testing.T) {
	mgr, rt := newApplyMgr(t, []string{"A", "B", "C"})
	if err := apply(t, mgr, "A", "B", "D"); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"add D": true, "enable D": true, "del C": true}
	for _, c := range rt.calls {
		if strings.HasPrefix(c, "raw") {
			if !strings.Contains(c, "/C ") || strings.Contains(c, "/A ") || strings.Contains(c, "/B ") {
				t.Fatalf("unexpected maint command %q", c)
			}
			continue
		}
		if !want[c] {
			t.Fatalf("unexpected call %q in %v", c, rt.calls)
		}
	}
}

func TestApplyAddFailureKeepsOldServersAndSetsFlag(t *testing.T) {
	mgr, rt := newApplyMgr(t, []string{"A"})
	rt.failAdd = true
	if err := apply(t, mgr, "B"); err == nil {
		t.Fatal("expected error")
	}
	if firstIndex(rt.calls, "raw ") >= 0 || firstIndex(rt.calls, "del ") >= 0 {
		t.Fatalf("nothing must be deleted after a failed add: %v", rt.calls)
	}
	if !reload.Instance().DynamicUpdateServerStateFailed() {
		t.Fatal("failure flag not set")
	}
}

func TestApplyDeleteFailureSetsFlag(t *testing.T) {
	mgr, rt := newApplyMgr(t, []string{"A"})
	rt.failDel = true
	if err := apply(t, mgr, "B"); err != nil {
		t.Fatal(err)
	}
	if !reload.Instance().DynamicUpdateServerStateFailed() {
		t.Fatal("failure flag not set after failed del server")
	}
}

// Drift: a stale server survived a failed delete, a later pod reuses its name, HAProxy refuses the
// duplicate add. The failure flag must be set so the reload fixes it.
func TestApplyDuplicateAddAfterStaleServerSetsFlag(t *testing.T) {
	mgr, rt := newApplyMgr(t, []string{"B"})
	rt.failAdd = true // HAProxy: server already exists
	if err := apply(t, mgr, "A"); err == nil {
		t.Fatal("expected error")
	}
	if !reload.Instance().DynamicUpdateServerStateFailed() {
		t.Fatal("failure flag not set")
	}
}
