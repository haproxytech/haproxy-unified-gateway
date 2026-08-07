# Filesystem Overlay

## gopherd — Init System

[gopherd](https://github.com/haproxytech/gopherd) is the PID 1 init process that manages all services inside the container. It handles process lifecycle, dependency ordering, health checks, signal forwarding, zombie reaping, and graceful shutdown.

### Config: `/etc/gopherd/gopherd.yml`

The gopherd config.

#### Processes

**haproxy** — The HAProxy load balancer. This is the primary service.

```yaml
- name: haproxy
  command: /usr/local/sbin/haproxy_wrapper
  args: ["-W", "-db", "-m", "{{mem 66%}}", ...]
  stop-signal: SIGUSR1
  kill-delay: 30s
  on-success: restart
  on-failure: restart
  environment:
    LD_PRELOAD: /usr/local/lib/libblock_secrets.so
  on-check-failure:
    haproxy-alive: restart
```

Key options explained:

| Option | Value | Purpose |
|--------|-------|---------|
| `stop-signal: SIGUSR1` | | HAProxy uses USR1 for graceful shutdown (not SIGTERM) |
| `kill-delay: 30s` | | Gives HAProxy 30s to drain connections before SIGKILL |
| `on-success: restart` | | Restart if HAProxy exits cleanly (master-worker mode exits 0 on reload) |
| `on-failure: restart` | | Restart on crash |
| `{{mem 66%}}` | | HAProxy gets 66% of available memory (auto-detects system RAM and cgroup limits) |
| `LD_PRELOAD` | | Loads the secrets-blocking library before HAProxy starts |
| `on-check-failure: haproxy-alive: restart` | | Restart HAProxy if the liveness check fails 3 times |

**hug** — The HAProxy Unified Gateway controller. Manages HAProxy configuration.

```yaml
- name: hug
  command: /usr/local/sbin/hug
  args: ["--with-gopherd"]
  use-entrypoint-args: true
  pass-env: true
  after: [haproxy]
  ready-check: haproxy-ready
  ready-timeout: 30s
  on-success: shutdown
  on-failure: restart
  environment:
    GOMEMLIMIT: "{{mem 33%}}MiB"
```

| Option | Value | Purpose |
|--------|-------|---------|
| `after: [haproxy]` | | Starts only after HAProxy has been spawned |
| `ready-check: haproxy-ready` | | Blocks HUG from starting until HAProxy accepts connections |
| `ready-timeout: 30s` | | Fail startup if HAProxy doesn't become healthy within 30s |
| `use-entrypoint-args: true` | | Docker CMD / Kubernetes args are appended to HUG's args |
| `pass-env: true` | | HUG inherits the container environment (POD_IP, KUBERNETES_*, …) |
| `on-success: shutdown` | | If HUG exits cleanly, shut down the entire container |
| `on-failure: restart` | | Restart on crash |
| `{{mem 33%}}MiB` | | Go runtime memory limit set to 33% of available memory |

#### Memory Allocation

Memory is split between the two main services using `{{mem}}` templates:

- **HAProxy**: `{{mem 66%}}` — 66% of container memory (passed via `-m` flag, value in MiB)
- **HUG**: `{{mem 33%}}MiB` — 33% of container memory (passed via `GOMEMLIMIT` env var)

gopherd auto-detects available memory from `/proc/meminfo` and cgroup limits (v1 and v2), taking the lower of the two. This replaces the previous `setup-env` shell script approach.

#### Health Checks

Two checks with distinct roles: liveness (restart trigger) and readiness (startup gate).

```yaml
checks:
  haproxy-alive:
    exec:
      command: /bin/sh
      args: ["-c", "echo 'show info' | socat stdio /var/run/haproxy-runtime-api.sock | grep -q Uptime"]
    period: 5s
    timeout: 2s
    threshold: 3
    initial-delay: 10s
```

- Probes the worker's runtime CLI; proves the event loop is responsive
- CLI listeners are exempt from global `maxconn`, so overload cannot trigger a restart —
  only a genuinely wedged worker can (a restart at peak load would drop every connection)
- Triggers HAProxy restart after 3 consecutive failures (`on-check-failure: haproxy-alive`)

```yaml
  haproxy-ready:
    http:
      url: http://localhost/healthz
      socket: /var/run/haproxy/health.sock
    period: 5s
    timeout: 2s
    threshold: 3
    initial-delay: 10s
```

- HTTP check over a Unix socket; proves the worker accepts and serves requests
- Used only as a readiness gate for HUG (`ready-check: haproxy-ready`)
- Subject to `maxconn`, which is why it must not drive restarts

Both wait 10s before the first check and mark unhealthy after 3 consecutive failures.

#### Control Socket

```yaml
control:
  socket: /run/gopherd.sock
```

Enables runtime control via the gopherd CLI:

```bash
gopherd list                     # list services and status
gopherd haproxy status           # check haproxy status
gopherd haproxy restart          # restart haproxy
gopherd signal haproxy SIGUSR2   # send signal (e.g. reload)
gopherd reload                   # hot-reload config
gopherd stats                    # show metrics
```

### Shell Access / Running Ad-hoc Commands

gopherd is the image entrypoint, but any first argument that does not start with `-`
(and is not one of the CLI commands above) is exec'd directly, replacing gopherd:

```bash
docker run -ti <image> sh          # drops into a shell, daemon never starts
docker run --rm <image> haproxy -v # run any binary from the image
```

Arguments starting with `-` (or anything after a `--` separator) are treated as
entrypoint args and forwarded to HUG (`use-entrypoint-args`), which is how the
Kubernetes deployment passes flags like `--hugconf-crd=...`.

In a running container, `kubectl exec` / `docker exec` work as usual:

```bash
kubectl exec -it <pod> -- sh             # shell alongside the running services
kubectl exec <pod> -- gopherd list       # control CLI against the live daemon
```

### Service Startup Order

```
haproxy → hug (gated by the haproxy-ready check)
```

### Shutdown Order

gopherd stops services in reverse dependency order:

```
hug (stopped first) → haproxy (max 30s drain)
```

### Other Files

| Path | Purpose |
|------|---------|
| `usr/local/hug/haproxy.cfg` | Base HAProxy configuration |
| `usr/local/hug/route.lua` | HAProxy Lua routing script |
