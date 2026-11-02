# Gateway API Conformance Testing in CI

This document describes the CI pipeline for Gateway API conformance testing used in the
`conformance-MR-GW-API-1_7_0` job. It complements the standard conformance setup
described in [conformance.md](conformance.md). For publishing a release's report
upstream, see [conformance-publish.md](conformance-publish.md).

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
      ├── haproxy-unified-gateway namespace
      │     ├── default HUG controller (scaled to 0 by deployer)
      │     └── per-Gateway: 1 HUG Deployment + 1 ClusterIP Service  ← created by deployer
      └── conformance-tests Job pod   ← tests run here
            └── /run.sh
                  ├── /conformance.test   (compiled Go test binary)
                  └── /gotestsum          (wraps test output → JUnit XML)
```

### Per-Gateway deployer

There is no static `controller.yaml` that watches all namespaces. Instead,
`conformance_test.go` starts a lightweight `deployer` (from
`test/conformance/deployer/`) that runs a controller-runtime reconciler inside
the test binary. This reconciler watches `Gateway` objects and, for each one
matching the configured `GatewayClass`, creates:

- one HUG controller **Deployment** scoped to that Gateway (`--gateway` flag),
- one **ClusterIP Service** labeled so the controller can report its address in
  the Gateway status.

In CI, `HUG_SERVICE_TYPE=ClusterIP` is set so that the per-Gateway Services are
of type `ClusterIP` — no load-balancer is needed because the conformance test
binary runs inside the cluster and reaches Services directly.

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
RUN GO111MODULE=off go build -o /test2json "$(go env GOROOT)/src/cmd/test2json"
```

- `go test -c` compiles `test/conformance/` into a standalone binary without running it.
- `-tags=conformance` is required to include files guarded by `//go:build conformance`.
- `gotestsum` is installed to wrap test output into JUnit XML at runtime.
- `test2json` ships only as Go source, so it is compiled here as well.

**Stage 2 — runtime:**
```dockerfile
FROM alpine:3
COPY --from=builder /conformance.test /conformance.test
COPY --from=builder /test2json /test2json
COPY --from=builder /go/bin/gotestsum /gotestsum
COPY ci/conformance/run.sh /run.sh
```

Plain `alpine` is enough: every binary in the image is statically linked, and the
toolchain's only runtime use, `go tool test2json`, is now `/test2json`. The
smaller image is what `kind load docker-image` copies into the cluster, which is
one of the fixed costs of every conformance job.

---

### 2. `ci/conformance/run.sh`

The pod entrypoint. Runs the test binary and writes results to `/tmp/results/`.

```sh
CONFORMANCE_REPORT_OUTPUT="${RESULTS_DIR}/conformance-report.yaml" \
/gotestsum \
  --raw-command \
  --junitfile "${RESULTS_DIR}/junit.xml" \
  -- /test2json -t /conformance.test \
    -test.v -test.timeout 60m ${EXTRA_FLAGS}
```

**Output pipeline:**

```
/conformance.test -test.v
        │  (verbose text output)
        ▼
/test2json -t
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

## Sharding the MR run

The MR job takes about 20 minutes because the suite is sequential: the per-Gateway
deployer starts one controller per Gateway and the suite may not run its tests in
parallel (see [conformance.md](conformance.md#architecture--per-gateway-deployer)).
Splitting the suite is what shortens the run, so the MR job is fanned out over the
shards declared in [`.gitlab/conformance-jobs.yml`](../.gitlab/conformance-jobs.yml):

```
conformance-jobs-generate  (go run ./cmd/gitlab-conformance-jobs)
        ↓ generated-conformance.yml (artifact)
conformance-MR-GW-API-1_7_0 (trigger, strategy: depend)
        ↓ child pipeline
  shard 1/6 → kind cluster + in-cluster Job → its tests
  shard 2/6 → kind cluster + in-cluster Job → its tests
  ...
```

The generator reads the suite's test list from the `gateway-api` conformance
package, so a test upstream adds is assigned without editing anything here. It deals
the tests round robin in upstream order, which keeps a slow family such as
`TLSRoute` from landing in one shard, and renders one child job per shard, each
`extends` the `.conformance-mr` body in
[`.gitlab/.conformance.yml`](../.gitlab/.conformance.yml).

A shard is told which tests to **skip**, not which to run:

- The suite has no "run only these" switch; it has `-run-test` (one exact name) and
  `-skip-tests` (a list).
- A skipped test stays visible in the JUnit report and in the suite's own report,
  which marks the run `partial`. Dropping tests from the list would instead make
  each shard report look like a full run over fewer tests.
- `CONFORMANCE_RUN_TEST` keeps working: the shards that do not own the named test
  skip it and pass.

Because every shard report is partial, only the nightly and the release
(`conformance-publish`) jobs run the whole suite; they are not sharded. The trigger
job keeps the unsuffixed name, so a green MR still means every shard passed.

---

## CI pipeline — `conformance-MR-GW-API-1_7_0`

The job runs on every push and merge request. Each shard job executes three tasks:


### Step 1 — `conformance-create-ci`

Creates the Kind cluster, pulls conformance images, patches the kubeconfig to reach the
Kind API server via the DinD service IP (`DOCKER_IP:7443`), and deploys HUG prerequisites
(CRDs, RBAC, namespace, HugConf). The HUG controller itself is **not** deployed here as a
static manifest — per-Gateway controller instances are created at test runtime by the
deployer (see [Per-Gateway deployer](#per-gateway-deployer) above).

### Step 2 — `conformance-build-test-image`

Builds the conformance test Docker image from `ci/conformance/Dockerfile` and loads it
into the Kind cluster:

```
docker build → haproxytech/hug-conformance:latest
kind load docker-image → conformance-mr-1-7-0 cluster
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
   docker cp conformance-mr-1-7-0-control-plane:/tmp/conformance-results/junit.xml ...
   docker cp conformance-mr-1-7-0-control-plane:/tmp/conformance-results/conformance-report.yaml ...
   ```
5. **Deletes** the Job and generates an HTML report.
6. **Exits** with the Job's status — if tests failed, the CI step fails.

### After script — `kind-delete`

Deletes the Kind cluster regardless of whether the job succeeded or failed.

---

## Artifacts

| File | Content |
|---|---|
| `conformance-report-1.7.0.yaml` | Gateway API conformance report (YAML), partial on an MR shard |
| `conformance-junit-1.7.0.xml` | JUnit XML — shown as test results in GitLab MR |
| `conformance-junit-1.7.0.html` | Human-readable HTML summary |

All three are written to `${CI_PROJECT_DIR}` and declared under `artifacts.paths` so
they are always uploaded even when the job fails (`when: always`). On an MR the files
are uploaded by each shard and cover only that shard's tests.

---

## Which pipelines run the suite

| Pipeline | Runs the suite as | Report |
|---|---|---|
| Merge request | one job per shard | partial, one per shard |
| Push to a branch | one job per shard | partial, one per shard |
| Nightly schedule | `conformance-GW-API-<version>` | one complete report |
| Tag `v*` | `conformance-publish` | one complete report |

Two rules still schedule the suite twice:

- A tag pipeline has `$CI_PIPELINE_SOURCE == 'push'`, so the sharded MR job runs
  beside `conformance-publish` on every release. Adding
  `&& $CI_COMMIT_TAG == null` to that rule drops the duplicate run.
- A push to the default branch runs `conformance-GW-API-<version>` (full suite,
  `allow_failure: true`) and the sharded MR job (full suite, fail closed). One of
  them should own the default branch; the `conformance-report` diff job in
  `.gitlab/conformance-report.yml` follows whichever one keeps the push rule.

A push that has an open merge request can also produce both a branch pipeline and
a merge request pipeline. A `workflow:rules` section removes that duplication for
the whole repository, so it belongs in a change of its own.

---

## Optimisations that were measured and not kept

A shard pays its fixed costs once, so two overlaps were tried. Both are recorded
here so the next attempt starts from the measurement instead of repeating it.

- **Running the two image builds beside the cluster setup** cut the fixed cost by
  about 90 seconds per shard, but made the run flaky: the suite then stalled
  fetching its GatewayClass (`context deadline exceeded` against the API service
  for the whole 180s setup deadline). Six green shards became two to six failed
  ones. The control plane and the compiles share the hit on the same host, which
  is the likely cause and worth proving with control-plane logs before retrying.
- **Caching the built test image archive** does nothing on this infrastructure:
  the runners have no shared cache server, so an entry written by one pipeline
  never reaches the next (the job log says the cache is extracted locally).
  Sharing the image would need the project container registry instead.

