# TLS Termination Ingress Example

This example terminates TLS at HAProxy for an `Ingress` that carries a
`spec.tls` section.

## What is being deployed

* An **IngressClass** named `hug` (`haproxy.org/ingress-controller`).
* A **Deployment** and a **Service** (`http-echo-tls`) running the echo server on
  port `80`.
* A **Secret** of type `kubernetes.io/tls` named `hello-tls`. It holds a
  throwaway self-signed certificate for `hello.haproxy.local`; replace it with a
  real one for anything but a local test.
* An **Ingress** named `hello-tls` for the host `hello.haproxy.local`, with
  `spec.tls[].secretName: hello-tls` and a `/` `Prefix` rule.

## How to deploy

```bash
kubectl apply -f .
```

Then forward the Ingress frontend ports (default `8080`/`8443`) from the
controller Pod — see the [examples index](../README.md):

```sh
kubectl -n haproxy-unified-gateway port-forward deploy/haproxy-unified-gateway 8080:8080 8443:8443
```

## How to check the result

The Secret in `spec.tls` makes HUG declare the HTTPS listener of the synthetic
ingress gateway, so the same Ingress is served on both frontends. `--resolve`
sends the real `Host` and SNI while connecting to the forwarded port, and `-k`
skips the verification of the self-signed certificate.

Over HTTPS (`--https-ingress-frontend-port`, `8443`):

```sh
curl -k --resolve hello.haproxy.local:8443:127.0.0.1 https://hello.haproxy.local:8443/hostname
```

```sh
http-echo-tls-6dcf8d475c-xqw4c
```

Over HTTP (`--http-ingress-frontend-port`, `8080`) the route is served too:

```sh
curl --resolve hello.haproxy.local:8080:127.0.0.1 http://hello.haproxy.local:8080/hostname
```

To inspect the certificate HAProxy serves:

```sh
curl -kv --resolve hello.haproxy.local:8443:127.0.0.1 https://hello.haproxy.local:8443/hostname 2>&1 | grep -i 'subject\|issuer'
```

Only TLS **termination** is supported: the Ingress Controller's
`ssl-passthrough` and per-host SNI annotations have no equivalent. The synthetic
HTTPS listener is catch-all, and every `spec.tls[].secretName` of every managed
Ingress contributes to the same crt-list; `spec.tls[].hosts` documents intent but
does not create a per-host listener. See
[documentation/ingress.md](../../../documentation/ingress.md) for the details.
