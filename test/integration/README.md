# Tools for degugging: envtest kubeconfig

While running integration tests, an envtest kubeconfig is generated in:
- `cfgDir/test folder/kubeconfig`

The exact location can be found at the beginning of the test logs:
```
=== RUN   TestGatewayTLSMultipleTestSuite
    /home/helene/go/src/gitlab.int.haproxy.com/haproxy-unified-gateway/test/integration/base/hugconfig.go:37: Haproxy config path: /tmp/hug/e2e-tests-gateway-tls-multiple
    /home/helene/go/src/gitlab.int.haproxy.com/haproxy-unified-gateway/test/integration/base/inttest.go:146: kubeconfig path: /tmp/hug/e2e-tests-gateway-tls-multiple/kubeconfig
```

You can then inspect the objects with `kubectl` or `k9s`:
```
k9s --kubeconfig=/tmp/hug/e2e-tests-gateway-tls-multiple
```

# Parallel run

The test in each folder under `test/integration` will be run in parallel in a different envtest.
Inside each folder, they run by default sequentially.

Let's take an example for `test/integration/gateway-tls-multiple`

Output of this tests will be in:
```
/tmp/hug/e2e-tests-gateway-tls-multiple
```

```
$ ls
ls
certlists  certs  haproxy.cfg  haproxy-master.sock  haproxy.pid  haproxy-runtime-api.sock  kubeconfig  maps  route.lua

```

Note also the kubeconfig file there.

# Writing a case

Each folder under `test/integration` is a suite: `suite_test.go` runs it, `<feature>_test.go`
holds one `Test_<n>_<name>` per case, and the fixtures live in `manifests/<n>-<slug>/`.
A case creates its fixtures, asserts, and lets a deferred cleanup delete them:

```go
func (s *FeatureSuite) Test_1_Something() {
	fixturePath := path.Join(utils.GetCRDFixturePath(), "1-something")
	s.CreateFixtures(fixturePath, nil)
	defer s.CleanupFixtures(fixturePath, nil)

	s.ExpectAttachedRoute(s.Test().Ctx, s.Test().Namespace, "gateway", "http", 1)
}
```

Four things the framework does not show at the call site. Each one has cost a run:

- **Ports are template numbers**, listed in `utils.TemplatePorts` (31081 HTTP, 31444
  TLS, 8080, 8443...). The harness maps each to a per-run ephemeral port and
  substitutes them in expectation contents, so fixtures and map directories are
  written with the template value (`hug_http_31081`) and work unchanged everywhere.
  Namespaces are *not* substituted: a field naming one must match a suite constant by
  hand.
- **The controller under test watches a fixed list of namespaces**: the per-run test
  namespace, `other`, and `test` (`base/hugconfig.go`). A resource in any other
  namespace is invisible to it, which shows up as that resource *not existing* rather
  than as a permission problem. `other` is watched but never created, so a suite that
  needs it creates it in `SetupSuite`.
- **`CleanupFixtures(path, nil)` deletes every manifest of the directory in the test
  namespace.** A directory holding a resource for another namespace therefore needs an
  explicit manifest list, plus `CleanupFixturesInNamespace` for the rest, or the
  deferred cleanup fails on a resource it looks for in the wrong namespace.
- **An update is an apply.** Two manifests declaring the same resource name, applied in
  two steps, express a transition (a moved `parentRef`, a changed spec). Applied
  together they share a creation timestamp, which is not something to assert
  precedence on.

To run one suite, and only it:

```
task test PKG=./test/integration/<feature>/...
```

`task test` runs `./...` with the envtest assets and `HAPROXY_BIN`, and the CI's unit-tests
job runs exactly that -- so a case that fails here fails the pipeline. The command uses
`-failfast`: the run stops at the first failing case, and the testdox summary at the top
of the output lists what ran.
