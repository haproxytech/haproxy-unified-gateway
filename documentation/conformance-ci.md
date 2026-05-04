# Gateway API Conformance Testing in CI

This document describes the CI pipeline for Gateway API conformance testing used in the
`conformance-MR-GW-API-1_5_0` job. It complements the standard conformance setup
described in [conformance.md](conformance.md).

---

## Why run tests inside the cluster?

The standard `conformance-run` task executes HTTP requests **from the CI runner
container** toward the Gateway. In the GitLab Docker-in-Docker (DinD) environment, the
Kind cluster runs inside the DinD daemon. The Kind nodes have internal IPs (e.g.
`172.19.0.x`) that are not directly reachable from the CI runner — DinD's own iptables
rules block forwarding.

The solution is to run the conformance test binary **as a Kubernetes Job inside the Kind
cluster**. From inside the cluster, the pod can reach Gateway services directly. Results
are written to a `hostPath` volume on the Kind node and retrieved with `docker cp` from
the CI runner, which has access to the DinD Docker daemon.

---

## Architecture

```
CI runner container
│
├── kubectl / docker CLI   # orchestrate the cluster from outside
│
└── Kind cluster (inside DinD)
      ├── HUG controller   # the Gateway implementation under test
      └── conformance-tests Job pod   ← tests run here
            └── /run.sh
                  ├── /conformance.test   (compiled Go test binary)
                  └── /gotestsum          (wraps test output → JUnit XML)
```

Results flow:

```
Job pod writes → /tmp/results/ (hostPath → /tmp/conformance-results/ on Kind node)
                                                      ↓
                              docker cp from CI runner → CI project dir
```

---

## Components

### 1. `ci/conformance/Dockerfile`

A two-stage Docker build:

**Stage 1 — compile:**
```dockerfile
FROM golang:${GO_VERSION}-alpine AS builder
RUN go install gotest.tools/gotestsum@v1.13.0
RUN go test -c -tags=conformance ./test/conformance/ -o /conformance.test
```

- `go test -c` compiles `test/conformance/` into a standalone binary without running it.
- `-tags=conformance` is required to include files guarded by `//go:build conformance`.
- `gotestsum` is installed to wrap test output into JUnit XML at runtime.

**Stage 2 — runtime:**
```dockerfile
FROM golang:${GO_VERSION}-alpine
COPY --from=builder /conformance.test /conformance.test
COPY --from=builder /go/bin/gotestsum /gotestsum
COPY ci/conformance/run.sh /run.sh
```

Uses `golang:alpine` (not plain `alpine`) because `go tool test2json` — needed at
runtime to convert test output to JSON — is part of the Go toolchain.

---

### 2. `ci/conformance/run.sh`

The pod entrypoint. Runs the test binary and writes results to `/tmp/results/`.

```sh
CONFORMANCE_REPORT_OUTPUT="${RESULTS_DIR}/conformance-report.yaml" \
/gotestsum \
  --raw-command \
  --junitfile "${RESULTS_DIR}/junit.xml" \
  -- go tool test2json -t /conformance.test \
    -test.v -test.timeout 60m ${EXTRA_FLAGS}
```

**Output pipeline:**

```
/conformance.test -test.v
        │  (verbose text output)
        ▼
go tool test2json -t
        │  (JSON test events)
        ▼
gotestsum --raw-command
        │
        ├── junit.xml                  (JUnit report for GitLab)
        └── conformance-report.yaml    (Gateway API conformance report)
```

Both files land in `/tmp/results/`, which is a `hostPath` volume backed by
`/tmp/conformance-results` on the Kind node.

Environment variables forwarded from the CI job (via the Job spec):

| Variable | Purpose |
|---|---|
| `CONFORMANCE_RUN_TEST` | Run only a specific test by name |
| `CONFORMANCE_SKIP_TESTS` | Comma-separated list of tests to skip |

**Exit code:** `gotestsum` propagates the exit code of `conformance.test`. With `set -e`
in `run.sh`, a test failure exits the script with a non-zero code, which marks the Job
as failed in Kubernetes.

---

### 3. `example/deploy/hug-conformance-ci/conformance-job.yaml`

Defines three resources applied together:

- **ServiceAccount** `conformance-tests` — dedicated identity for the pod.
- **ClusterRoleBinding** — binds `conformance-tests` to `cluster-admin` so the pod can
  query cluster state if needed.
- **Job** — runs the test pod with:
  - `imagePullPolicy: Never` — image is pre-loaded into Kind, no registry pull.
  - `hostPath` volume at `/tmp/results` → `/tmp/conformance-results` on the node.
  - `backoffLimit: 0` — no retries on failure.
  - `restartPolicy: Never` — no local container restart; failure goes straight to the
    Job controller.
  - `CONFORMANCE_RUN_TEST` and `CONFORMANCE_SKIP_TESTS` env vars, injected at apply
    time via `sed` substitution of `${VAR}` placeholders.

---

## CI pipeline — `conformance-MR-GW-API-1_5_0`

The job runs on every push and merge request. Its script executes three tasks:

### Step 1 — `conformance-create-ci`

Creates the Kind cluster, pulls conformance images, patches the kubeconfig to reach the
Kind API server via the DinD service IP (`DOCKER_IP:7443`), and deploys HUG and its
prerequisites.

### Step 2 — `conformance-build-test-image`

Builds the conformance test Docker image from `ci/conformance/Dockerfile` and loads it
into the Kind cluster:

```
docker build → haproxytech/hug-conformance:latest
kind load docker-image → conformance-mr-1-5-0 cluster
```

Must happen after the cluster exists (step 1) and before the Job runs (step 3).
`imagePullPolicy: Never` is set in the Job spec because the image is pre-loaded — there
is no registry to pull from.

### Step 3 — `conformance-run-job`

1. **Applies** `example/deploy/hug-conformance-ci/conformance-job.yaml` after injecting
   `CONFORMANCE_RUN_TEST` and `CONFORMANCE_SKIP_TESTS` via `sed`.
2. **Waits** for the Job to finish:
   ```
   kubectl wait --for=condition=complete --for=condition=failed --timeout=90m job/conformance-tests
   ```
   Exits on either condition (success or failure), avoiding a full 90-minute wait when
   tests fail.
3. **Checks** `status.succeeded` to determine the outcome — `status.succeeded ≥ 1`
   means all tests passed.
4. **Retrieves results** using `docker cp` from the Kind node container (accessible via
   the DinD Docker daemon):
   ```
   docker cp conformance-mr-1-5-0-control-plane:/tmp/conformance-results/junit.xml ...
   docker cp conformance-mr-1-5-0-control-plane:/tmp/conformance-results/conformance-report.yaml ...
   ```
5. **Deletes** the Job and generates an HTML report.
6. **Exits** with the Job's status — if tests failed, the CI step fails.

### After script — `kind-delete`

Deletes the Kind cluster regardless of whether the job succeeded or failed.

---

## Artifacts

| File | Content |
|---|---|
| `conformance-report-1.5.0.yaml` | Gateway API conformance report (YAML) |
| `conformance-junit-1.5.0.xml` | JUnit XML — shown as test results in GitLab MR |
| `conformance-junit-1.5.0.html` | Human-readable HTML summary |

All three are written to `${CI_PROJECT_DIR}` and declared under `artifacts.paths` so
they are always uploaded even when the job fails (`when: always`).
