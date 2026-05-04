# Gateway API Conformance Testing

HUG includes a conformance test suite based on the official
[Gateway API conformance framework](https://gateway-api.sigs.k8s.io/concepts/conformance/)
from `sigs.k8s.io/gateway-api`. Running these tests validates that HUG correctly
implements the Gateway API specification.

For CI, see [conformance-ci.md](conformance-ci.md).

---

## Local development

### How it works

Tester runs **outside the cluster**, on your machine. The test binary makes HTTP/HTTPS
requests directly to the Gateway's `LoadBalancer` service IP.
[cloud-provider-kind](https://github.com/kubernetes-sigs/cloud-provider-kind) assigns
real IPs to `LoadBalancer` services in the local Kind cluster, making them reachable
from your machine. `task kind-create` starts cloud-provider-kind automatically.

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
