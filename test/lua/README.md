# Lua integration tests

Integration tests for `fs/usr/local/hug/route.lua`. Each test spins up a real
HAProxy subprocess with the production `route.lua` loaded and drives it via
HTTP and the runtime API socket.

## Requirements

- `haproxy` binary on `PATH`, version **3.1 or newer**
  (route.lua uses `core.get_patref` iteration which is reliable from 3.1)
- Tests skip cleanly when the binary is missing or too old.

## Running

```sh
# everything
go test ./test/lua/...

# one feature
go test ./test/lua/find_route/

# verbose
go test -v ./test/lua/...
```

Each test runs HAProxy on a per-test tempdir with unix sockets — no ports are
allocated, so tests can run in parallel without conflicts.

## Layout

```
test/lua/
├── internal/harness/        shared Go helper used by every package
├── reverse_host/            reverse_host converter
├── find_route/              find_route action: exact / prefix / regex
│   └── maps/fe/             per-frontend map fixtures
├── find_route_unregistered/ find_route degrades gracefully when maps aren't preloaded
├── route_action/            route action: weighted-random & failover algorithms
└── route_cli/               register_cli commands (dump cache / clear cache)
```

Per-feature directory layout:

```
<feature>/
├── haproxy.cfg              real cfg (with {{…}} placeholders)
├── <feature>_test.go        Go tests calling harness.New(t, "haproxy.cfg")
└── maps/…                   optional map fixtures consumed by the cfg
```

## How the harness works

`harness.New(t, "haproxy.cfg")`:

1. Mirrors the test package directory (skipping `.go` files) into `t.TempDir()`.
2. Substitutes placeholders in the cfg copy:
   - `{{HTTP_SOCK}}`  — unix socket the frontend should `bind` to
   - `{{ADMIN_SOCK}}` — runtime API socket path
   - `{{ROUTE_LUA}}`  — absolute path to `fs/usr/local/hug/route.lua`
   - `{{DIR}}`        — the tempdir (use it to reference map files: `{{DIR}}/maps/fe/…`)
3. Runs `haproxy -c` for an early-fail config check.
4. Starts haproxy (`-W -db`) and waits for the admin socket to appear.
5. Returns helpers: `Get`, `Socket`, `Log`. `t.Cleanup` tears HAProxy down.

The harness is the **only** place that knows about HAProxy lifecycle — tests
just write a `haproxy.cfg` and assert on responses.

## Writing a new test

1. Create `test/lua/<feature>/haproxy.cfg` using the four placeholders above.
   If you need fixtures (map files etc.), drop them into `<feature>/…` and
   reference them as `{{DIR}}/relative/path` from the cfg.
2. Add `test/lua/<feature>/<feature>_test.go`:

   ```go
   package <feature>_test

   import (
       "testing"
       "github.com/haproxytech/haproxy-unified-gateway/test/lua/internal/harness"
   )

   func TestSomething(t *testing.T) {
       h := harness.New(t, "haproxy.cfg")
       _, body := h.Get(t, "/foo", "Header", "value")
       // assert on body / status / h.Socket(...) / h.Log()
   }
   ```

## Gotchas

- **Host header**: Go's `req.Header.Set("Host", …)` is silently ignored. The
  harness's `Get` handles this — pass `"Host"` as a header key and it'll set
  `req.Host` instead.
- **Commas in header values**: HAProxy's `req.hdr()` splits on commas. Use
  `req.fhdr()` if the value can contain commas (JSON payloads, comma-separated
  listener-route lists, etc.).
- **`Map.new` at runtime**: forbidden. Maps must be loaded at config-parse
  time (via a `map_*(...)` converter or a dummy `acl … -m found` rule). Once
  loaded, `core.get_patref` reaches them at request time and lookups see
  `set map` / `add map` / `del map` updates over the runtime socket.
