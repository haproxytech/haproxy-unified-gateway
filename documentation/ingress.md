# Kubernetes Ingress support

HUG can serve Kubernetes `Ingress` resources in addition to Gateway API objects.
The support is **optional and disabled by default**: the controller only
watches `Ingress`/`IngressClass` when started with `--enable-ingress`.

An `Ingress` is never rendered directly. It is **translated into synthetic
`HTTPRoute` objects** which then flow through the exact same pipeline as
hand-written Gateway API routes (tree building, checks, backends, maps,
certificates, status). This is what makes the feature small: there is no second
data-plane implementation, only a translation layer in front of the Gateway API
one.

---

## How it works

1. The Ingress builder (`k8s/gate/tree/ingress_builder.go`) checks the Ingress
   eligibility, then emits one **synthetic `HTTPRoute` per Ingress rule** (an
   `HTTPRoute` carries a single hostname set, while each Ingress rule has its own
   host) plus one extra route for `spec.defaultBackend`.
2. A **synthetic `Gateway`** provides the listeners those routes attach to. It is
   built in memory by `k8s/gate/tree/gateway_synthetic_builder.go` and is **never
   applied to the API server**:
   * key `default/ing:gateway`, `gatewayClassName: ing:gatewayclass`;
   * listener `http` on `--http-ingress-frontend-port` (default **8080**);
   * listener `https` on `--https-ingress-frontend-port` (default **8443**),
     declared **only when at least one managed Ingress declares a TLS secret**
     (a Gateway API HTTPS listener without `certificateRefs` would be invalid);
   * `allowedRoutes.namespaces.from: All`, because the synthetic routes live in
     the real Ingress namespaces while the synthetic gateway lives in `default`.
3. The synthetic routes go through the normal HTTPRoute builder, so they end up
   in the usual frontends/backends/maps. With the default link ID (`hug`) the
   Ingress traffic is served by the frontends **`hug_http_8080`** and
   **`hug_https_8443`**.

```
Ingress ──► synthetic HTTPRoute(s) ──► HTTPRoute pipeline ──► backends / maps
   │                 ▲
   └── synthetic Gateway (in memory) ──┘   listeners http:8080 / https:8443
```

### Identity of the synthetic objects

| Object | Namespace | Name |
|---|---|---|
| Gateway | `default` | `ing:gateway` |
| GatewayClass (referenced) | — | `ing:gatewayclass` |
| HTTPRoute (per rule) | Ingress namespace | `ing:<ingress-name>:<ruleIndex>` |
| HTTPRoute (`defaultBackend`) | Ingress namespace | `ing:<ingress-name>:default` |

The `ing:` prefix is illegal in a Kubernetes object name, so it is a
tamper-proof origin marker: a real resource can never carry it. Nothing named
`ing:...` exists in the cluster; those names only appear in controller logs and
in the generated HAProxy configuration.

---

## Enabling Ingress support

Three things are needed:

* the Gateway API CRDs installed (HUG depends on them either way);
* the controller started with `--enable-ingress`;
* RBAC allowing the controller to read `ingresses` and `ingressclasses` and to
  update `ingresses/status`.

The example manifests grant it: `example/deploy/hug/rbac.yaml` lists
`ingresses`, `ingresses/status` and `ingressclasses` (apiGroups `extensions` and
`networking.k8s.io`, verbs `get`/`list`/`watch`) plus `update` on
`ingresses/status`.

### Controller options

| CLI flag | Default | Meaning |
|---|---|---|
| `--enable-ingress` | **false** | watch `Ingress`/`IngressClass` and translate them onto the synthetic Gateway. |
| `--ingress-class` | empty | IngressClass controller **suffix** this instance handles. Empty means the controller name `haproxy.org/ingress-controller`. |
| `--empty-ingress-class` | false | with `--ingress-class` set, also handle Ingresses without `ingressClassName`. |
| `--http-ingress-frontend-port` | 8080 | listening port of the HTTP frontend dedicated to Ingress. |
| `--https-ingress-frontend-port` | 8443 | listening port of the HTTPS frontend dedicated to Ingress. |
| `--disable-ingress-status-update` | false | do not write `status.loadBalancer.ingress` back onto managed Ingresses. |

These ports are **container** ports of the HAProxy process: they have to be
reachable (Service/LoadBalancer of your choice, or a `kubectl port-forward`) for
Ingress traffic to be served. They are distinct from the Gateway API listener
ports, which come from the Gateway listeners themselves.

### Which Ingresses are handled (eligibility)

The rule mirrors the IngressClass contract:

* the `IngressClass.spec.controller` of the class named by the Ingress (or of the
  default class, for an unclassed Ingress) must match this instance's controller
  name:
  * `haproxy.org/ingress-controller` when `--ingress-class` is empty;
  * `haproxy.org/ingress-controller/<suffix>` when `--ingress-class=<suffix>`;
* an `IngressClass` annotated `ingressclass.kubernetes.io/is-default-class: "true"`
  is the cluster default and applies to Ingresses without `ingressClassName`;
* with `--ingress-class` empty, an Ingress that has **no** class and no default
  class is also handled (`--empty-ingress-class` is then unnecessary and has no
  additional effect);
* with `--ingress-class` set, an Ingress with no class is handled only when
  `--empty-ingress-class` is passed, or when a default class resolves to the
  suffixed controller.

An Ingress handled by another controller is simply ignored: nothing is written
for it and its backends are not built.

---

## Supported Ingress features

* `rules[].host`, including wildcard hosts (`*.example.com`) and the empty host.
* `rules[].http.paths[]` with:
  * `pathType: Prefix` → Gateway API `PathPrefix`;
  * `pathType: Exact` → Gateway API `Exact`;
  * unset or `pathType: ImplementationSpecific` → falls back to `PathPrefix`
    (see the limitation below).
* `backend.service.port.number` and `backend.service.port.name` (a named port is
  resolved against the referenced Service in the Ingress namespace).
* `spec.defaultBackend`, modelled as a hostless synthetic route with a `/`
  `PathPrefix` match, i.e. the lowest routing precedence — the fallback.
* `spec.tls[].secretName`: the Secrets are collected from **every** managed
  Ingress and attached to the synthetic `https` listener as `certificateRefs`.
* Several rules/hosts in one Ingress (one synthetic route per rule).
* The Backend CR, referenced by the `cr-backend` annotation (see below).
* `status.loadBalancer.ingress`, unless `--disable-ingress-status-update`.

---

## Configuration: the Backend CR instead of annotations

The HAProxy Ingress Controller (`haproxytech/kubernetes-ingress`) configures
HAProxy through a set of annotations. In HUG, configuration is carried by
**Custom Resources**: for an Ingress, the supported path is the **Backend CR**,
which holds the HAProxy `backend` section itself. The configuration is therefore
exactly what you declare — there is no annotation vocabulary to fit into and no
annotation-level limit on what can be expressed: any `backend` field HAProxy
accepts can be written in the CR.

### The Backend CR

An Ingress carries the `cr-backend` annotation to merge a **foreign
`ingress.v3.haproxy.org` Backend CR** (the Ingress Controller's CRD, if
installed) into every backend built for that Ingress. The CR is where the
backend configuration lives: timeouts, balance, cookie, health checks,
`http-request`/`http-response` rules, and so on — the HAProxy `backend` section
verbatim, merged into the backend HUG derives from the Service.

The annotation is accepted bare or under a known prefix: `cr-backend`,
`ingress.kubernetes.io/cr-backend`, `haproxy.org/cr-backend`,
`haproxy.com/cr-backend`.

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: hello-world
  annotations:
    cr-backend: my-backend-tuning     # "name" or "namespace/name"
spec:
  # ...
---
apiVersion: ingress.v3.haproxy.org/v3
kind: Backend
metadata:
  name: my-backend-tuning
spec:
  server_timeout: 71000
  balance:
    algorithm: leastconn
  http_request_rule_list:
    - type: set-header
      hdr_name: X-Served-By
      hdr_format: hug
```

* The value is **same-namespace only**: a `namespace/name` naming another
  namespace is ignored with a warning (an `ExtensionRef` carries no namespace).
* Pointing it at a **HUG** `gate.v3.haproxy.org` Backend is a type error and is
  ignored (the backend is still built, with defaults) — an Ingress may only
  reference an `ingress.v3.haproxy.org` Backend.
* The `gate.v3.haproxy.org/cr-backend` annotation on the **target Service** also
  applies to Ingress backends and merges a HUG `gate.v3.haproxy.org` Backend CR
  (cross-namespace allowed with a ReferenceGrant); see
  [Backend CustomResource](./CustomResources/backend.md).

### Restrictions compared to the HAProxy Ingress Controller

The table lists what the Ingress-to-Gateway-API translation itself does not
cover.

| Area | HAProxy Ingress Controller | HUG Ingress support |
|---|---|---|
| Backend configuration | annotations (`timeout-*`, `check*`, `balance`, `cookie`…) | **the Backend CR** (above), carrying the HAProxy `backend` section verbatim |
| Frontend configuration | annotations (`ssl-redirect`, `rewrite-target`, `app-root`, …) | no equivalent; expressed through the Ingress and Gateway API fields |
| TLS modes | `ssl-passthrough`, SNI per host, `server-alias`, mTLS annotations | TLS **termination** from `spec.tls[].secretName` |
| Custom resources | TCP services, `ingress.v3 Backend`, etc. | the `ingress.v3 Backend` CR is supported via `cr-backend`; the other CRDs are not |
| Backends | `backend.service.port`, plus `resource:` backends | **Service backends only** — a `Resource` backend is skipped with a warning |
| Cross-namespace | annotations can reach other namespaces (with ReferenceGrant) | an Ingress is same-namespace by spec; no ReferenceGrant is involved |
| `pathType: ImplementationSpecific` | implementation-defined | treated as `Prefix` |
| Ports | the controller's own HTTP/HTTPS frontends (often 80/443) | dedicated frontends, **8080/8443** by default, changed with the CLI flags above |
| LB status | written by the Ingress Controller | written by HUG (its own service address), unless disabled |

---

## Ingress-specific behaviours worth knowing

* **One HTTPRoute per Ingress rule.** An Ingress with three hosts produces three
  synthetic routes. This only affects object counts and logs, not the resulting
  HAProxy configuration.
* **`spec.tls[].hosts` is not used.** The synthetic `https` listener is
  catch-all, and every collected certificate is written to the same crt-list;
  certificate selection is HAProxy's usual SNI handling. Declaring `hosts`
  documents intent but does not create per-host listeners.
* **The HTTPS listener only exists when a TLS secret exists.** An Ingress
  referencing a missing Secret contributes a `certificateRef` that does not
  resolve, which leaves the listener invalid — create the Secret before (or
  together with) the Ingress.
* **The Ingress frontends are shared infrastructure.** With the default ports,
  `hug_http_8080` / `hug_https_8443` are dedicated to Ingress. If you point
  `--http-ingress-frontend-port` at a port already used by a Gateway listener,
  both land in the same virtual listener and thus the same frontend and map
  files. Keep dedicated ports unless you know you want them shared.
* **The synthetic Gateway is not user-visible.** It has no address, no status and
  no object in the cluster; the Ingress `status.loadBalancer` is filled from the
  controller's own service, not from the synthetic Gateway.
* **Real routes cannot attach to the synthetic Gateway.** A hand-written
  `HTTPRoute` naming `ing:gateway` is rejected (`NotAllowedByListeners`): the
  synthetic gateway only accepts the synthetic routes built from Ingresses.

---

## Diagnostics

* Controller logs (debug level) trace the translation:
  * `Injecting synthetic ingress gateway`
  * `Translated Ingress into synthetic HTTPRoutes` (with the route count)
  * `Skipping Ingress backend` (with the reason: not a Service backend, named
    port not found, …)
  * `Ignoring cross-namespace Backend CR annotation on Ingress`
* Generated HAProxy configuration: look for the frontends `hug_http_8080` /
  `hug_https_8443` and their map files, and for backends whose name derives from
  the echoed Service (the extension-ref hash appears in the name when a
  `cr-backend` filter is attached).

---

## Known limitations

These are marked `TODO(ingress)` in the source:

* **Cross-namespace Backend CR references** from an Ingress:
  `ingress_builder.go` (`ingressBackendCRFilters`).
* **`pathType: ImplementationSpecific`**: currently treated as `Prefix`, while
  HAProxy could interpret it as a regular expression
  (`ingress_builder.go`, `ingressPathToMatch`).
* Everything listed in the restrictions table above is, as of today, *by
  design or not yet implemented*, not configurable.
