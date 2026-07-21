# Backend CustomResource

A `Backend` Custom Resource lets you tune the HAProxy `backend` section that HUG
generates for a route's `backendRef`. The CR carries HAProxy backend settings
(timeouts, balance algorithm, `http-request` rules, …) that are **merged** into
the backend HUG builds from the Gateway API resources.

The CRD schema can be found here: [Backend CRD](../../api/definition/gate.v3.haproxy.org_backends.yaml)

The `spec` accepts all fields of the HAProxy `backend` section (via the embedded
`models.Backend` from client-native).

---

## Two ways to attach a Backend CR

A `Backend` CR is never rendered on its own — it only takes effect once it is
**attached** to a backend. There are two independent mechanisms:

| Mechanism | Declared on | Applies to | Selects the backend by |
|---|---|---|---|
| **Route-level** | an `HTTPRoute` `backendRef` (`ExtensionRef` filter) | HTTPRoute **only** | the specific `backendRef` (its filter hash) |
| **Service-level** | a `Service` annotation (`gate.v3.haproxy.org/cr-backend`) | HTTPRoute **and** TLSRoute | every backend built for that Service |

> **TLSRoute note:** TLSRoute rules have no filters, so the route-level mechanism
> is not available for TLSRoutes. A TLSRoute can only receive a Backend CR through
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

## Service-level attachment (HTTPRoute and TLSRoute)

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
