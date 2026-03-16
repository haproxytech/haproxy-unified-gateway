# Gateway API Conformance Testing

HUG includes a conformance test suite based on the official
[Gateway API conformance framework](https://gateway-api.sigs.k8s.io/concepts/conformance/)
from `sigs.k8s.io/gateway-api`. Running these tests validates that HUG correctly
implements the Gateway API specification.

## Quick start

```bash
# Full pipeline: create Kind cluster, deploy HUG, run tests
task conformance

# Or step by step:
task conformance-create   # create cluster + build + deploy
task conformance-run      # run conformance tests (cluster must exist)
```

The standalone script can also be used:

```bash
./test/conformance/scripts/setup.sh --all
```

## Running a single test

Use the `RUN` variable to filter which conformance test(s) to execute:

```bash
# Run a single test
task conformance-run RUN="TestConformance/HTTPRouteServiceTypes"

# Run several tests (regex)
task conformance-run RUN="TestConformance/(HTTPRouteServiceTypes|HTTPRouteSimpleSameNamespace)"
```

The value is passed to `go test -run`, which filters on Go subtests.
Note that the conformance suite still runs full setup (applying all manifests)
regardless of the filter — only the actual test execution is skipped for
non-matching tests.

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
| `HUG_HTTP_PORT` | `31080` | NodePort for HTTP traffic |
| `HUG_HTTPS_PORT` | `31443` | NodePort for HTTPS traffic |
| `HUG_TEST_TLS` | _(unset)_ | Set to `1` to enable the GATEWAY-TLS profile |
| `CONFORMANCE_REPORT_OUTPUT` | `conformance-report.yaml` | Path for the conformance report |

## Architecture

### Port remapping

HUG serves traffic on NodePort ports (31080/31443), but the conformance tests
create Gateways with standard ports (80/443). A custom `DialContext` on the
`DefaultRoundTripper` transparently remaps connections:

```
test connects to <node-ip>:80  →  DialContext rewrites to <node-ip>:31080
test connects to <node-ip>:443 →  DialContext rewrites to <node-ip>:31443
```

### Gateway address patching

HUG does not currently set `Gateway.Status.Addresses`. The conformance framework
requires an address to send traffic. A background goroutine watches for Gateways
with the conformance GatewayClass and patches their status with the Kind node's
InternalIP.

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

taskfile/
└── conformance.yml              # Task definitions
```
