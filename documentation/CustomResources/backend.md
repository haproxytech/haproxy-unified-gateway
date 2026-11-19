# Backend CustomResource

A `Backend` Custom Resource lets you tune the HAProxy `backend` section that HUG
generates for a route's `backendRef`. The CR carries HAProxy backend settings
(timeouts, balance algorithm, `http-request` rules, …) that are **merged** into
the backend HUG builds from the Gateway API resources.

The CRD schema can be found here: [Backend CRD](../../api/definition/gate.v3.haproxy.org_backends.yaml)

The `spec` accepts all fields of the HAProxy `backend` section (via the embedded
`models.Backend` from client-native).

By default HUG applies only the tuning fields of a Backend CR. HUG applies the
full `spec` only for namespaces that the administrator trusts (see
[Trusted namespaces](#trusted-namespaces)).

---

## Two ways to attach a Backend CR

A `Backend` CR is never rendered on its own — it only takes effect once it is
**attached** to a backend. There are two independent mechanisms:

| Mechanism | Declared on | Applies to | Selects the backend by |
|---|---|---|---|
| **Route-level** | an `HTTPRoute` `backendRef` (`ExtensionRef` filter) | HTTPRoute **only** | the specific `backendRef` (its filter hash) |
| **Service-level** | a `Service` annotation (`gate.v3.haproxy.org/cr-backend`) | HTTPRoute, **TLSRoute and TCPRoute** | every backend built for that Service |

> **TLSRoute and TCPRoute note:** their rules have no filters, so the route-level
> mechanism is not available for them. They can only receive a Backend CR through
> the Service-level annotation.

Both mechanisms can be combined; when they collide the **Service-level CR wins**
(see [Precedence](#precedence)).

---

## The Backend CR

```yaml
apiVersion: gate.v3.haproxy.org/v3
kind: Backend
metadata:
  name: backend-tuning
  namespace: example
spec:
  server_timeout: 70000
  balance:
    algorithm: roundrobin
  http_request_rule_list:
    - cond: unless
      cond_test: '{ src 192.168.1.0/16 }'
      hdr_format: '%T'
      hdr_name: X-Haproxy-Current-Date
      type: add-header
```

`spec.name` is irrelevant for the merge: HUG blanks it before merging so the CR
cannot rename the generated backend. The backend name is always derived by HUG
(see [Backend identity](#backend-identity)).

---

## Trusted namespaces

The full backend model is powerful. A rule can make HAProxy return a file from
its disk, or rewrite a routing map shared by all routes. A user who can create
a Backend CR could use that against other namespaces.

HUG therefore sorts Backend CRs by the namespace they live in:

| Namespace of the Backend CR | What HUG applies |
|---|---|
| Not trusted (default) | The tuning fields only. All other fields are dropped. |
| Trusted | The full `spec`. |

The tuning fields are:

* Load balancing: `balance` (without `hash_expression`), `hash_type`, `hash_balance_factor`.
* Timeouts: `check_timeout`, `connect_timeout`, `queue_timeout`, `server_timeout`,
  `server_fin_timeout`, `tunnel_timeout`, `http_request_timeout`, `http_keep_alive_timeout`.
* Retries and queueing: `retries`, `retry_on`, `redispatch`, `fullconn`, `max_keep_alive_queue`.
* Connection handling: `abortonclose`, `http_connection_mode`, `http_reuse`,
  `http-buffer-request`, `http_pretend_keepalive`, `prefer_last_server`, `forwardfor`.
* Health checks: `adv_check`, `httpchk_params`, `log_health_checks`.
* Persistence: `cookie`.
* Server defaults, in `default_server`: `check`, `inter`, `fastinter`, `downinter`,
  `fall`, `rise`, `health_check_port`, `maxconn`, `maxqueue`, `minconn`, `weight`,
  `slowstart`, `backup`, `error_limit`, `observe`, `on-error`, `on-marked-down`,
  `on-marked-up`, `pool_low_conn`, `pool_max_conn`, `pool_purge_delay`, `max_reuse`,
  `ssl`, `verify`, `ssl_min_ver`, `ssl_max_ver`, `alpn`, `proto`, `check-ssl`,
  `check_alpn`, `check_proto`, `send-proxy`, `send-proxy-v2`, `check-send-proxy`.

Everything else is dropped for a namespace that is not trusted. This includes
all rule lists (`http_request_rule_list` and similar), ACLs, error files, log
targets, stick tables and external checks.

HUG ignores the whole Backend CR when a tuning field holds whitespace or a
control character. The only exception is `retry_on`, which is a space-separated
list.

### Trust a namespace

Only do this for namespaces whose users you trust with the whole HAProxy
configuration.

1. Open the `HugConf` resource that the controller uses (option `--hugconf-crd`).
2. Add the namespace to `spec.trustedBackendCRNamespaces`:

   ```yaml
   apiVersion: gate.v3.haproxy.org/v3
   kind: HugConf
   metadata:
     name: hugconf
     namespace: haproxy-unified-gateway
   spec:
     trustedBackendCRNamespaces:
       - infra
       - company-xyz-*
   ```

3. Apply the resource.

An entry is a namespace name. A `*` stands for any run of characters, so
`company-xyz-*` trusts every namespace that starts with `company-xyz-`.

HUG applies the change without a controller restart. It rebuilds the affected
backends, and reloads HAProxy when their configuration changes.

### Find dropped fields

HUG logs each reduced Backend CR. Search the controller log for these messages:

* `Backend CR fields dropped` (level Warn): lists the dropped fields.
* `Backend CR ignored` (level Error): the CR held an unsafe value.

For a cross-namespace reference, the namespace of the Backend CR decides, not
the namespace of the route or the Service.

---

## Route-level attachment (HTTPRoute)

Attach a Backend CR to a single `backendRef` with an `ExtensionRef` filter of
kind `Backend`:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: route-echo
spec:
  parentRefs:
    - name: gateway
      sectionName: http
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: http-echo
          port: 80
          filters:
            - type: ExtensionRef
              extensionRef:
                group: gate.v3.haproxy.org
                kind: Backend
                name: backend-tuning
```

The CR is looked up **in the HTTPRoute's own namespace** (the `ExtensionRef` has
no namespace field).

### Multiple CRs and merge order — `MergeType`

Several `Backend` CRs can be listed on the same `backendRef`; they are merged in
declaration order. A special `ExtensionRef` of kind **`MergeType`** (name
`Override` or `Append`) switches the merge behaviour of the CRs that follow it:

```yaml
          filters:
            - type: ExtensionRef
              extensionRef:
                group: gate.v3.haproxy.org
                kind: Backend
                name: backend-cr-1
            - type: ExtensionRef
              extensionRef:
                group: gate.v3.haproxy.org
                kind: MergeType
                name: Override        # switch the following CRs to "override"
            - type: ExtensionRef
              extensionRef:
                group: gate.v3.haproxy.org
                kind: Backend
                name: backend-cr-2
```

`MergeType` is not a real resource — it is only a directive:

| `MergeType` | Effect on list fields (e.g. `http_request_rule_list`) |
|---|---|
| `Append` *(default)* | CR list entries are **appended** to what is already there |
| `Override` | CR list entries **replace** the existing list |

Scalar fields always follow the same rule regardless of `MergeType`: a non-zero
value in the CR replaces the current one.

---

## Service-level attachment (HTTPRoute, TLSRoute and TCPRoute)

Annotate the target `Service` with `gate.v3.haproxy.org/cr-backend`. The CR is
then merged into **every** backend HUG builds for that Service, no matter which
route (HTTPRoute or TLSRoute) references it:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: http-echo
  namespace: example
  annotations:
    gate.v3.haproxy.org/cr-backend: backend-tuning
spec:
  ports:
    - name: http
      port: 80
      targetPort: http
```

The annotation value is either:

- `name` — a Backend CR in the **Service's own namespace**, or
- `namespace/name` — a Backend CR in another namespace (see
  [Cross-namespace references](#cross-namespace-references)).

The Service-level CR is merged with **override** semantics: scalar values and
**whole list fields** in the CR replace what was there. Route-level results are
never appended to by the Service-level CR.

A missing/unresolved annotation target, or a cross-namespace reference without a
`ReferenceGrant`, is **ignored** (the backend is still built) and an error is
logged — one bad annotation cannot break the backend build.

---

## Cross-namespace references

A `namespace/name` annotation that points outside the Service's namespace must be
authorised by a `ReferenceGrant` living in the **target** namespace (the Backend
CR's namespace), granting `Service` → `Backend` access. Same-namespace references
never need a grant.

```yaml
apiVersion: v1
kind: Service
metadata:
  name: http-echo
  namespace: team-a
  annotations:
    gate.v3.haproxy.org/cr-backend: infra/backend-tuning   # CR lives in "infra"
---
apiVersion: gateway.networking.k8s.io/v1beta1
kind: ReferenceGrant
metadata:
  name: allow-team-a-to-backend-tuning
  namespace: infra                                          # target namespace
spec:
  from:
    - group: ""
      kind: Service
      namespace: team-a
  to:
    - group: gate.v3.haproxy.org
      kind: Backend
      name: backend-tuning        # omit "name" to allow every Backend CR here
```

Adding, changing or removing the grant re-reconciles the affected routes.

---

## Precedence

When a `backendRef` carries a route-level Backend CR **and** its target Service
is annotated, both are applied to the same backend, in this order:

```
HUG baseline backend
  ──► route-level Backend CR(s)   (HTTPRoute ExtensionRef, Append/Override)
      ──► Service-level Backend CR (annotation, override — wins)
```

The Service-level CR is always merged **last**, so for any field it sets, the
**Service wins over the route-level CR**.

---

## Backend identity

HUG names each backend from `service + port + filter-hash`. The **route-level**
`ExtensionRef` Backend CRs are part of that filter hash, so two `backendRef`s to
the same service/port that reference different route-level CRs produce **two
distinct backends**.

The **Service-level** annotation is **not** part of the hash: it applies uniformly
to every backend of the Service and never changes a backend's name.

Consequence: two routes that resolve to the same service/port with identical
filters share **one** backend. If they also carry conflicting route-level CRs the
backend is built once from whichever `backendRef` is processed — there is no
conflict detection. Keep per-route tuning consistent, or move shared tuning to the
Service-level annotation.

---

## Re-reconciliation

Changes propagate to already-generated configuration automatically:

- editing a `Backend` CR re-reconciles every HTTPRoute referencing it via
  `ExtensionRef` and every HTTPRoute/TLSRoute whose target Service is annotated to
  it;
- editing a Service annotation re-reconciles the routes reaching that Service;
- adding/removing a `ReferenceGrant` re-reconciles the routes whose Service
  references a Backend CR across namespaces.
