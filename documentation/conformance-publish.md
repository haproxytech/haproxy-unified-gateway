# Publishing a Conformance Report Upstream

This document describes the pipeline that takes a HUG release and publishes its
Gateway API conformance report toward the upstream
[kubernetes-sigs/gateway-api](https://github.com/kubernetes-sigs/gateway-api)
repository, so HUG appears in the official conformant-implementations list.

It builds on the conformance setup in [conformance.md](conformance.md) and the CI
mechanics in [conformance-ci.md](conformance-ci.md).

---

## What the pipeline produces

Two files, laid out as upstream expects, plus an audit record:

```
publish/
├── provenance.txt                                   # version, image digest, skip list, pipeline/job
└── upstream/conformance/reports/<major.minor>/haproxy-unified-gateway/
    ├── README.md                                    # heading, table (one row), Reproduce, Contact
    └── <channel>-<tag>-<mode>-report.yaml           # the suite's report, byte-for-byte
```

The report is never rewritten by the tooling: `cmd/conformance-publish` copies it
verbatim, validating it against the rules upstream enforces and rendering the
README around it. It does not edit the report bytes.

The report itself is produced by the **conformance suite run in the same job**
(`conformance-run-job`), parameterised by the tag and the skip list — it is not
reused from a stored artifact by default. Changing `HUG_CONFORMANCE_SKIP_TESTS`
therefore changes what that run skips, and so the report it produces. Every
publish re-runs the suite, so a re-publish yields a *new* report (a new `date`,
and possibly different results) rather than the original release-time one, and the
one-version rule replaces the version's previous report with it.

---

## The job

`conformance-publish` lives in `.gitlab/conformance-tests.yml`, in the
`conformance` stage, on a `go`-tagged Docker-in-Docker runner. It runs two tasks
in one job:

1. `task conformance-publish-ci` — pulls the **release image**, installs that
   tag's CRDs, runs the suite, and renders the payload above.
2. `task conformance-publish-github` — pushes the payload as a commit to a
   `dev-v<major.minor>` branch on the `haproxyci/gateway-api` fork, derived from
   `HUG_IMPLEMENTATION_VERSION` (`v1.0.7` → `dev-v1.0`). It pushes to that staging
   branch, never the fork's `main`, which stays a plain mirror of upstream.

It does **not** build the controller: it pulls
`haproxytech/haproxy-unified-gateway:<tag-without-v>` from Docker Hub, so the bits
under test are the published release artifact. On a tag pipeline that creates an
ordering dependency on `.github/workflows/docker_auto_release.yml` having pushed
the image; `retry: 0` makes a lost race fail loudly — re-run once it is published.


---

## Triggering a run

- **Tag pipeline** — pushing a `v*` tag runs the job automatically, with
  `HUG_IMPLEMENTATION_VERSION` taken from the tag.
- **Manual run** — Run pipeline on the branch, setting
  `HUG_IMPLEMENTATION_VERSION` to the version to serve, e.g. `v1.0.7`.

The version rule is compared against a **shape**, not emptiness: an undefined
variable substitutes to the empty string, and a project variable whose value is
itself a reference expands to empty inside the job — which would include the job
and then fail its own guard. Requiring `v` + `major.minor` can do neither.

---

## Which pipeline runs

The publish job is gated by the **version**; the standard jobs are gated by the
**pipeline source**. The two are independent — setting the version *adds* the
publish job, it does not remove the standard jobs.

The publish job's rules (`.gitlab/conformance-tests.yml`):

```yaml
- if: $CI_COMMIT_TAG =~ /^v/            # tag pipeline; the version is taken from the tag
- if: $HUG_IMPLEMENTATION_VERSION =~ /^v[0-9]+\.[0-9]+/
```

The standard jobs' rules (`.gitlab/unit-tests.yml`, `lint.yml`, `diff.yml`,
`security.yml`, `bots.yml`) never look at the version:

```yaml
- if: $CI_PIPELINE_SOURCE == 'merge_request_event'
- if: "$CI_PROJECT_NAMESPACE != 'haproxy-controller/community' && $CI_PIPELINE_SOURCE == 'push'"
```

`.gitlab/build.yaml` (the image builds) runs on `merge_request_event` and
`schedule` only, so it is absent from push and manual pipelines.

| Pipeline | Standard jobs | `conformance-publish` |
|---|---|---|
| push, branch | unit-tests / lint / diff / security / bots | ✗ (no tag, version is not a `v`-shape) |
| push, tag `v*` | unit-tests / lint / diff / security / bots + conformance | ✓ (version from the tag) |
| merge_request_event | most standard jobs | ✓ only if the version is set |
| web (manual run) | none — no rule matches `web` | ✓ if the version is set |

So a **manual (web) run** with `HUG_IMPLEMENTATION_VERSION` set is what gives the
publishing pipeline *on its own* — but it is the source being `web` that
suppresses the standard jobs, not the version. On a **tag pipeline both run**:
pushing a `v*` tag has `CI_PIPELINE_SOURCE == 'push'`, so the push-rule jobs run
alongside the publish job.

Keep the name `HUG_IMPLEMENTATION_VERSION` unused at project, group and schedule
scope. Its absence is what makes the rules behave: the shape comparison rejects a
variable that is undefined (empty) or reference-like, and nothing can then shadow
the value the job sets for itself. A *literal* `v`-shape value would defeat this —
it matches the pattern, so the publish job would join every push pipeline.

---

## Parameters

Set at **pipeline-run time** (Run pipeline → Variables):

| Variable | Required | Purpose |
|---|---|---|
| `HUG_IMPLEMENTATION_VERSION` | yes | The version to publish, e.g. `v1.0.7`. Must match `^v[0-9]+\.[0-9]+`. |
| `HUG_CONFORMANCE_SKIP_TESTS` | no | Skips the named tests for this run only; the default run is unskipped. A different name from the job-level variable on purpose, so GitLab precedence cannot shadow it. |
| `HUG_GITHUB_DRY_RUN` | no | If set (e.g. `1`), everything runs up to the commit and stops before pushing. Use it to rehearse. |
| `GWAPI_FORK_REPO` | no | Overrides the push target (default `git@github.com:haproxyci/gateway-api.git`). Point it at a scratch fork to rehearse. |

Set in the job or task for other purposes:

| Variable | Default | Purpose |
|---|---|---|
| `HUG_CONTROLLER_IMAGE_TAG` | `vX.Y.Z` → `X.Y.Z` | The image tag pulled from Docker Hub. |
| `PUBLISH_DIR` | `publish` | Output root for the payload. |
| `GWAPI_UPSTREAM_REPO` | `https://github.com/kubernetes-sigs/gateway-api.git` | Upstream, fetched as the base for the pushed commit. |

---

## Credentials

- **`HAPROXY_CI_GWAPI_PRIVATE_KEY`** — a CI/CD variable of type **File**, so its
  value is the *path* to a temp file holding the private half of the SSH **deploy
  key** registered on the target fork. The task `chmod 600`s the file and
  `ssh-add`s it. The deploy key must be **write-enabled**; a read-only key fails
  at the push, after the whole run.

  The key must be scoped to the repository being pushed: a deploy key registered
  on one fork cannot push to another, and a personal SSH key pushes wherever its
  account has access. Whichever you use must match `GWAPI_FORK_REPO`.

  **The value must keep its line breaks.** A raw private key is multi-line, and a
  paste that loses the newlines (or adds CRs) produces a file `ssh-add` rejects
  with `error in libcrypto` — the same message an empty file gives. The task logs
  the file's size and line count and validates it before `ssh-add`, so a mangled
  value is reported as such instead of as an opaque crypto error. One truncation
  is expected and repaired: GitLab strips the value's trailing newline, and an
  OpenSSH key needs it, so the task appends one before validating. The key in use
  is 452 bytes over 8 lines — 451 bytes over 7 lines as the variable delivers it.

Opening the pull request upstream is **not** automated yet and needs a separate
GitHub token — see "Not yet automated" below.

---

## What the push does

- Clones the fork, then bases the commit on **`upstream/main`**, not the fork's
  branch, so the MR diff is exactly our files and carries no unrelated drift.
- Keeps **one report version** per implementation folder: it deletes the previous
  version's report and rewrites the single README row, in the same commit. If a
  report would remain alongside the new one, the run fails rather than commit two.
- Commits with a DCO sign-off (`git commit -s`) as
  `haproxyci <haproxyci@haproxy.com>`.
- Force-pushes the fork's `dev-v<major.minor>` branch with `--force-with-lease`,
  so a re-run replaces its own previous commit but refuses if someone else moved
  the branch in between. The branch name comes from `HUG_IMPLEMENTATION_VERSION`
  (`v1.0.7` → `dev-v1.0`).

The commit message follows upstream's convention:
`Update haproxy-unified-gateway <gatewayAPIVersion> conformance report`.

---

## Re-running and verifying

Re-run the job (a **new** pipeline; a pipeline is pinned to its commit) with the
same `HUG_IMPLEMENTATION_VERSION`. This is safe: the branch is bot-owned, and the
push replaces the previous commit rather than failing.

Success signal: the job's artifacts contain
`publish/upstream/conformance/reports/…`. A red job with only the report artifact
means validation rejected it — read `failedTests` in the report and add those
names to `HUG_CONFORMANCE_SKIP_TESTS`.

**Known limitation:** the README is rendered from the payload on each run; it is
not merged with the copy already upstream. The design calls for splicing the new
row into upstream's README so prose a maintainer added during review survives, but
that is not implemented — today the README is regenerated and replaces whatever is
upstream.

---

## Not yet automated

- **Opening the pull request.** The deploy key pushes commits but cannot create a
  PR — that is a GitHub API call needing a token for `haproxyci` (a fine-grained
  PAT: repository `haproxyci/gateway-api`, *Pull requests: read & write* +
  *Contents: read*). Until then the PR is opened by hand from the fork's
  `dev-v<major.minor>` branch.
- **The CLA.** The contributing identity (`haproxyci`) must satisfy the CNCF CLA,
  or upstream's `EasyCLA` check blocks the merge.

---

## References

- Upstream rules: `conformance/reports/README.md` in the gateway-api repository.
- Upstream validator: `hack/verify-reports.sh` (checks `gatewayAPIVersion` vs the
  folder, `gatewayAPIChannel`, and `mode`; it does not check the version or filenames).
