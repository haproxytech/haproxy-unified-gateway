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


## Running a single test

Use the `CONFORMANCE_RUN_TEST` variable to filter which conformance test(s) to execute:

```bash
# Run a single test
task conformance-run CONFORMANCE_RUN_TEST="HTTPRouteServiceTypes"
```

where `HTTPRouteServiceTypes` is the shortname of the test.

## Skipping some tests

Use the `CONFORMANCE_SKIP_TESTS` variable to filter which conformance test(s) to execute:

Where `CONFORMANCE_SKIP_TESTS` contains a list of comma separated shortnames of tests to skip:

```bash
task conformance-run CONFORMANCE_SKIP_TESTS="HTTPRouteHeaderMatching,HTTPRouteInvalidBackendRefUnknownKind"
```


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
| `CONFORMANCE_REPORT_OUTPUT` | `conformance-report.yaml` | Path for the conformance report

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

taskfile/
└── conformance.yml              # Task definitions
```
