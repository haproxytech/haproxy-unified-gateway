# Ingress Examples

These examples show how HUG serves Kubernetes `Ingress` resources.

Ingress support is **optional and disabled by default**. Read
[documentation/ingress.md](../../documentation/ingress.md) first: it explains how
an Ingress is translated into a synthetic `HTTPRoute` + synthetic `Gateway`, the
CLI options, and the restrictions compared to the HAProxy Ingress Controller —
configuration is carried by the **Backend CR** rather than by annotations.

## Prerequisites

1. A HUG controller reachable by the cluster, started with `--enable-ingress`.
   Add the flag to the controller `args` (see
   [`../deploy/hug/controller.yaml`](../deploy/hug/controller.yaml)):

   ```yaml
   args:
     - --hugconf-crd=haproxy-unified-gateway/hugconf
     - --enable-ingress
   ```

2. The `http-echo` image available, as described in
   [`../README.md`](../README.md).

3. RBAC for `ingresses`, `ingressclasses` and `ingresses/status`. The example
   RBAC in [`../deploy/hug/rbac.yaml`](../deploy/hug/rbac.yaml) already has it;
   see [documentation/ingress.md](../../documentation/ingress.md) for the details.

4. An `IngressClass` whose `spec.controller` is `haproxy.org/ingress-controller`.
   Each example ships its own (`ingressclass.yaml`); they are identical, so
   applying several examples at once is fine.

## Examples

* [Hello World](./hello-world/): an Ingress with `Prefix` and `Exact` paths, and
  how the two path types differ.
* [TLS termination](./tls/): an Ingress with `spec.tls`, serving HTTPS from a
  Secret.

## Reaching the Ingress frontends

The Ingress traffic is served by dedicated frontends whose ports come from the
controller options: **8080** for HTTP and **8443** for HTTPS, unless changed. To
try an example without publishing those ports, forward them from the controller
Pod:

```sh
kubectl -n haproxy-unified-gateway port-forward deploy/haproxy-unified-gateway 8080:8080 8443:8443
```

The example READMEs assume this `port-forward` is running and use `curl
--resolve` so the request carries the real `Host`/SNI.
