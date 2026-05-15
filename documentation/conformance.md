# Gateway API Conformance Testing

HUG includes a conformance test suite based on the official
[Gateway API conformance framework](https://gateway-api.sigs.k8s.io/concepts/conformance/)
from `sigs.k8s.io/gateway-api`. Running these tests validates that HUG correctly
implements the Gateway API specification.

For CI, see [conformance-ci.md](conformance-ci.md).

---

## Architecture — per-Gateway deployer

`task kind-create` deploys a **default HUG controller** (from
`example/deploy/hug-dev/controller.yaml`) that watches all namespaces. Before the
conformance suite starts, `conformance_test.go` starts a lightweight deployer (from
`test/conformance/deployer/`) that runs a controller-runtime reconciler embedded
inside the test binary itself. The deployer **scales the default controller to 0
replicas** so it does not conflict with the per-Gateway controllers it is about to
create. When the test binary exits (whether the tests pass or fail), the deployer
restores the default controller to 1 replica.

The deployer reconciler watches `Gateway` objects and, for each one matching the
configured `GatewayClass`, creates:

- one HUG controller **Deployment** scoped to that Gateway (`--gateway` flag),
- one **Service** labeled so the controller can report its address in the Gateway
  status.

The Service type used for the per-Gateway Services depends on the environment:

| Environment | `HUG_SERVICE_TYPE` | Service type |
|---|---|---|
| Local (`task conformance-run`) | _(unset, defaults to `LoadBalancer`)_ | `LoadBalancer` |
| CI (inside Kind cluster) | `ClusterIP` | `ClusterIP` |

Locally, [cloud-provider-kind](https://github.com/kubernetes-sigs/cloud-provider-kind)
assigns real IPs to `LoadBalancer` services so the test binary — running on your
machine outside the cluster — can reach them. In CI, the test binary runs as a Pod
inside the cluster and reaches `ClusterIP` services directly.

---

## Local development

### How it works

The test binary runs **outside the cluster**, on your machine. It makes HTTP/HTTPS
requests directly to the per-Gateway `LoadBalancer` service IPs assigned by
[cloud-provider-kind](https://github.com/kubernetes-sigs/cloud-provider-kind).
`task kind-create` starts cloud-provider-kind automatically.

### Quick start

```bash
task kind-create
task conformance-run
```

That's it. No extra setup needed.

### Running a single test

```bash
task conformance-run CONFORMANCE_RUN_TEST="HTTPRouteServiceTypes"
```

### Skipping specific tests

```bash
task conformance-run CONFORMANCE_SKIP_TESTS="HTTPRouteHeaderMatching,HTTPRouteInvalidBackendRefUnknownKind"
```

---

## Output

| File | Description |
|---|---|
| `conformance-report.yaml` | Official Gateway API conformance report. Lists profiles, passed/failed/skipped tests, supported and unsupported features. |
| `conformance-junit.xml` | JUnit XML from gotestsum. |
| `conformance-junit.html` | Human-readable summary generated from the JUnit XML. |

The conformance report is the artifact implementations submit to the upstream
`gateway-api` repository to prove spec compliance.

---

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `CONFORMANCE_RUN_TEST` | _(unset)_ | Run only the named test (shortname) |
| `CONFORMANCE_SKIP_TESTS` | _(unset)_ | Comma-separated list of test shortnames to skip |
| `CONFORMANCE_REPORT_OUTPUT` | `conformance-report.yaml` | Path for the conformance report |
| `HUG_GATEWAY_CLASS` | `haproxy` | GatewayClass name used in tests |
| `HUG_TEST_TLS` | _(unset)_ | Set to `1` to enable the GATEWAY-TLS profile |

---

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

---

## Pre-pulled images

The conformance framework deploys its own echo backends. `task kind-create` pre-pulls
and loads these images into Kind to avoid in-cluster pull timeouts:

- `gcr.io/k8s-staging-gateway-api/echo-basic:v20260204-monthly-2026.01-60-g28382302`
- `registry.k8s.io/coredns/coredns:v1.12.2`

---

## File layout

```
test/conformance/
├── conformance_test.go          # Test entry point
├── helpers_test.go              # Port remapper + Gateway address patcher
└── manifests/
    └── gatewayclass.yaml        # GatewayClass for conformance

taskfile/
└── conformance.yml              # Task definitions

cmd/junit-report/
└── main.go                      # HTML report generator
```
