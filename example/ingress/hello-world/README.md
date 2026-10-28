# Hello World Ingress Example

This example exposes a simple echo server through an `Ingress`, using both
`Prefix` and `Exact` path types to show how they differ.

## What is being deployed

* An **IngressClass** named `hug`, whose `spec.controller` is
  `haproxy.org/ingress-controller` — the controller name HUG answers to when
  `--ingress-class` is empty.
* A **Deployment** and a **Service** (`http-echo-hello`) running the echo server
  on port `80` (named `http`).
* An **Ingress** named `hello-world` for the host `hello.ingress.local` with two
  paths, both pointing at the same Service:
  * `/all` with `pathType: Prefix`, backend port given by **number**;
  * `/hostname` with `pathType: Exact`, backend port given by **name**.

The echo server answers the exact path `/hostname` with its own hostname, and any
other path with a JSON document that includes it under `os.hostname`. That is
what makes the two path types observable.

## How to deploy

```bash
kubectl apply -f .
```

Then forward the Ingress HTTP frontend port (default `8080`) from the controller
Pod — see the [examples index](../README.md):

```sh
kubectl -n haproxy-unified-gateway port-forward deploy/haproxy-unified-gateway 8080:8080
```

## How to check the result

`--resolve` makes `curl` connect to `127.0.0.1:8080` while sending the real
`Host: hello.ingress.local`, which is what the Ingress rule matches on.

A `Prefix` path matches every path under it, so `/all/sub` is routed:

```sh
curl -s --resolve hello.ingress.local:8080:127.0.0.1 http://hello.ingress.local:8080/all/sub \
  | grep -A1 '"os"'
```

```sh
  "os": {
    "hostname": "http-echo-hello-6dcf8d475c-xqw4c"
  },
```

An `Exact` path matches only that path:

```sh
curl --resolve hello.ingress.local:8080:127.0.0.1 http://hello.ingress.local:8080/hostname
```

```sh
http-echo-hello-6dcf8d475c-xqw4c
```

...but not a sub-path, which matches no rule and is answered by HUG's
`backend_not_found` (404):

```sh
curl -i --resolve hello.ingress.local:8080:127.0.0.1 http://hello.ingress.local:8080/hostname/sub
```

```sh
HTTP/1.1 404 Not Found
```

The same happens for any path outside `/all` and `/hostname`:

```sh
curl -i --resolve hello.ingress.local:8080:127.0.0.1 http://hello.ingress.local:8080/other
```

To see what HUG built, look for the frontend `hug_http_8080` and its path maps in
the generated HAProxy configuration (the `Prefix` rule lands in the
prefix-match map, the `Exact` rule in the exact-match map).

See [documentation/ingress.md](../../../documentation/ingress.md) for how the
Ingress is translated, the controller options, and the restrictions compared to
the HAProxy Ingress Controller.
