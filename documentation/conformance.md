# Gateway API Conformance Testing

HUG includes a conformance test suite based on the official
[Gateway API conformance framework](https://gateway-api.sigs.k8s.io/concepts/conformance/)
from `sigs.k8s.io/gateway-api`. Running these tests validates that HUG correctly
implements the Gateway API specification.

## Two ways to run conformance

Conformance tests can be run in two contexts:

- **Local development** — on your own machine against a local Kind cluster.
- **CI** — automatically in GitLab CI using Docker-in-Docker (DinD), with additional
  network setup to route traffic to the Kind cluster.

Both contexts use the same `conformance-run` task to execute the tests.

---

## Local development

### 1. Create a cluster

Use the standard Kind cluster tasks — no special conformance-specific cluster is needed:

```bash
task kind-create          # standard cluster with HUG and an example app
task kind-create-debug    # cluster without host port maps (HUG started locally)
```

### 2. Run the tests

```bash
task conformance-run
```

### Running a single test

Set `CONFORMANCE_RUN_TEST` to the shortname of the test you want to run:

```bash
task conformance-run CONFORMANCE_RUN_TEST="HTTPRouteServiceTypes"
```

This can also be set as an environment variable (e.g. in `.env`):

```bash
CONFORMANCE_RUN_TEST=HTTPRouteServiceTypes task conformance-run
```

### Skipping specific tests

Set `CONFORMANCE_SKIP_TESTS` to a comma-separated list of test shortnames to skip:

```bash
task conformance-run CONFORMANCE_SKIP_TESTS="HTTPRouteHeaderMatching,HTTPRouteInvalidBackendRefUnknownKind"
```

This can also be set as an environment variable:

```bash
CONFORMANCE_SKIP_TESTS="HTTPRouteHeaderMatching,HTTPRouteInvalidBackendRefUnknownKind" task conformance-run
```

---

## CI

### Setup

CI runs conformance in a GitLab Docker-in-Docker (DinD) environment. The setup task
`conformance-create-ci` handles the full cluster bootstrap:

1. Creates a Kind cluster using `kind-config-conformance.yaml`.
2. Pre-pulls the conformance echo and CoreDNS images.
3. Patches the kubeconfig to point to `https://docker:7443` — in DinD the Kind API
   server is not reachable via `127.0.0.1` from the CI runner, so the `docker` hostname
   (the DinD service) is used instead.
4. Deploys HUG.
5. Runs `conformance-setup-routing-ci` (the routing hack — see below).

### The routing hack: `conformance-setup-routing-ci`

#### Why it is needed

In GitLab DinD the CI runner and the DinD daemon are two separate containers that share a
network. Kind runs *inside* the DinD daemon. Each Kind node gets a Docker-internal IP
(e.g. `172.18.0.2`) that is only reachable from inside that Docker network — not from the
CI runner.

The HUG controller writes the Kind node's `InternalIP` into
`Gateway.status.addresses`. The conformance test framework reads that address and
sends HTTP/HTTPS traffic straight to it. Without intervention, every request would fail
with "connection refused" because `172.18.0.x` is unreachable from the runner.

#### What the hack does

`conformance-setup-routing-ci` installs `iptables` rules on the CI runner that
transparently redirect outgoing traffic destined for the Kind node IP toward the DinD
host (reachable via the `docker` service hostname), which in turn forwards it into the
Kind cluster via pre-configured NodePort mappings.

#### Full traffic path

```
GitLab CI runner  (image: docker:XX-goXX)
┌──────────────────────────────────────────────────────────────────────┐
│                                                                       │
│  Go conformance test                                                  │
│  reads Gateway.status.addresses → KIND_NODE_IP (e.g. 172.18.0.2)    │
│                                                                       │
│  GET http://172.18.0.2:80/...                                        │
│                    │                                                  │
│  iptables OUTPUT chain  (installed by conformance-setup-routing-ci)  │
│  ┌─────────────────────────────────────────────────────────────────┐ │
│  │ DNAT  dst=KIND_NODE_IP:80  → DOCKER_HOST_IP:8080               │ │
│  │ DNAT  dst=KIND_NODE_IP:443 → DOCKER_HOST_IP:8443               │ │
│  └─────────────────────────────────────────────────────────────────┘ │
│  iptables POSTROUTING MASQUERADE  (fixes the return path in DinD)    │
│                    │                                                  │
└────────────────────┼─────────────────────────────────────────────────┘
                     │  to DOCKER_HOST_IP:8080 / :8443
                     ▼
DinD service  (hostname: "docker", IP: DOCKER_HOST_IP)
┌──────────────────────────────────────────────────────────────────────┐
│                                                                       │
│  kind-config-conformance.yaml portMappings                           │
│  ┌─────────────────────────────────────────────────────────────────┐ │
│  │ hostPort 8080  →  containerPort 31080  (Kind node)             │ │
│  │ hostPort 8443  →  containerPort 31443  (Kind node)             │ │
│  └─────────────────────────────────────────────────────────────────┘ │
│                    │                                                  │
│  ┌─────────────────┼────────────────────────────────────────────┐   │
│  │ Kind node  (InternalIP: KIND_NODE_IP)                         │   │
│  │                 │                                             │   │
│  │  NodePort 31080 ◄── HTTP                                     │   │
│  │  NodePort 31443 ◄── HTTPS                                    │   │
│  │  (haproxy-unified-gateway-ci Service — fixed nodePort)       │   │
│  │                 │                                             │   │
│  │  kube-proxy routes to HUG pod                                │   │
│  │                 │                                             │   │
│  │  ┌──────────────┴──────────────────────────────────────┐    │   │
│  │  │ HUG pod  (HAProxy listening on :80 / :443)          │    │   │
│  │  └─────────────────────────────────────────────────────┘    │   │
│  └─────────────────────────────────────────────────────────────┘   │
└──────────────────────────────────────────────────────────────────────┘
```

#### Why fixed NodePorts are required

The Kind portMapping (`hostPort 8080 → containerPort 31080`) is static — it is baked
into `kind-config-conformance.yaml` at cluster creation time and cannot change. The
NodePort assigned to a Kubernetes Service is normally random (30000–32767). To make the
two sides match, `example/deploy/hug-conformance-ci/controller.yaml` deploys a dedicated
`haproxy-unified-gateway-ci` Service with **fixed** `nodePort` values:

| Service port | nodePort | Kind hostPort | DNAT destination |
|---|---|---|---|
| 80 (HTTP)  | 31080 | 8080 | `DOCKER_HOST_IP:8080` |
| 443 (HTTPS) | 31443 | 8443 | `DOCKER_HOST_IP:8443` |

The regular `haproxy-unified-gateway` Service is left without HTTP/HTTPS port entries to
avoid port conflicts — only the CI-specific Service carries those fixed NodePorts.

#### Why MASQUERADE is needed

Without the `POSTROUTING MASQUERADE` rule, reply packets from the HUG pod travel back
through the Kind network to the original source IP of the CI runner. Inside DinD that
path does not exist: the Kind network is isolated inside the Docker daemon and the runner
cannot receive packets routed via the Docker-internal network directly. MASQUERADE
rewrites the source IP of the DNAT'd packets to the DinD host's address so that replies
traverse a path that is fully within reach.

### Filtering tests in CI

The `CONFORMANCE_RUN_TEST` and `CONFORMANCE_SKIP_TESTS` variables work the same way in CI:
they are passed through to `conformance-run` and can be set as CI/CD environment variables
in the GitLab pipeline configuration.

---

## What the tests produce

| File | Description |
|---|---|
| `conformance-report.yaml` | Official Gateway API conformance report (YAML). Lists profiles, passed/failed/skipped tests, supported and unsupported features. Always written, even on partial failure. |
| `conformance-junit.xml` | JUnit XML from gotestsum. Useful for CI dashboards (GitLab, Jenkins). |

The conformance report is the artifact other implementations commit to the
upstream `gateway-api` repository to prove spec compliance.

## Conformance profiles

| Profile | Status |
|---|---|
| `GATEWAY-HTTP` | Enabled by default |
| `GATEWAY-TLS` | Opt-in via `HUG_TEST_TLS=1` |

## Supported features declared

The test currently declares core features only:

- `SupportGateway`
- `SupportHTTPRoute`
- `SupportReferenceGrant`

Extended features (e.g. `HTTPRouteQueryParamMatching`, `HTTPRoutePathRewrite`)
can be added to `test/conformance/conformance_test.go` as HUG gains support.

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `HUG_GATEWAY_CLASS` | `haproxy` | GatewayClass name used in tests |
| `HUG_TEST_TLS` | _(unset)_ | Set to `1` to enable the GATEWAY-TLS profile |
| `CONFORMANCE_REPORT_OUTPUT` | `conformance-report.yaml` | Path for the conformance report |
| `CONFORMANCE_RUN_TEST` | _(unset)_ | Run only the named test (shortname) |
| `CONFORMANCE_SKIP_TESTS` | _(unset)_ | Comma-separated list of test shortnames to skip |

## Architecture

### Report always written

The test calls `suite.NewConformanceTestSuite` directly (instead of the
higher-level `RunConformanceWithOptions`) and registers a `t.Cleanup` that
writes the conformance report. This ensures the report is produced even when
`Setup()` or `Run()` fail via `require`/`t.FailNow()`.

## Pre-pulled images

The conformance framework deploys its own echo backends. The task
`conformance-pull-images` pre-pulls these images and loads them into Kind to
avoid in-cluster pull timeouts:

- `gcr.io/k8s-staging-gateway-api/echo-basic:v20240412-v1.0.0-394-g40c666fd`
- `registry.k8s.io/coredns/coredns:v1.12.2`

## File layout

```
test/conformance/
├── conformance_test.go          # Test entry point
├── helpers_test.go              # Port remapper + Gateway address patcher
├── manifests/
│   └── gatewayclass.yaml        # GatewayClass for conformance
└── scripts/
    └── setup.sh                 # Standalone setup + run script

ci/kind/
└── kind-config-conformance.yaml # Kind cluster config

example/deploy/hug-conformance-ci/
└── controller.yaml              # CI-specific Deployment + NodePort Services

taskfile/
└── conformance.yml              # Task definitions
```
