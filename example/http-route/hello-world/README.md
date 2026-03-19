# Hello World HTTPRoute Example

This example demonstrates how to use an HTTPRoute to expose simple "hello world" applications through the HAProxy Kubernetes Gateway, using both prefix and exact path matching.

## What is being deployed

This example deploys the following resources:

* A **GatewayClass** named `haproxy` that defines a class of Gateways that can be provisioned by the HAProxy Kubernetes Gateway.
* A **Gateway** named `hug-gateway` that requests a listener on port `31080` for HTTP traffic. It is configured to accept routes for any hostname ending in `.haproxy.local`.
* Two **HTTPRoutes**:
  * `route-hello-world-prefix`: A route that matches traffic for `prefix.haproxy.local` with any path and forwards it to the `hello-world-prefix` service.
  * `route-hello-world-exact`: A route that matches traffic for `exact.haproxy.local` with the exact path `/hostname` and forwards it to the `hello-world-exact` service.
* Two **Deployments** and **Services** for the echo applications:
  * `hello-world-prefix`: A simple echo server.
  * `hello-world-exact`: Another simple echo server.

## How to deploy

```bash
kubectl apply -f .
```

## How to check if the result is correct

Use `curl` to send a request to the `http-echo` service through the Gateway. You will need to use the `--resolve` option to tell `curl` where to send the request, as the `.local` domain will not be resolvable through DNS.


## prefix

```sh
GW_IP=$(kubectl get gateway hug-gateway -n default -o jsonpath='{.status.addresses[0].value}')
curl http://$GW_IP:31080/hostname -H "Host: prefix.haproxy.local"
```

You should see a response from the service, confirming that the traffic was routed correctly.

```sh
hello-world-prefix-74cbd7d5f9-jvsg4
```

## exact

```sh
curl http://$GW_IP:31080/hostname -H "Host: exact.haproxy.local"
```

You should see a response from the service, confirming that the traffic was routed correctly.

```sh
hello-world-exact-6dcf8d475c-xqw4c
```

```sh
curl http://$GW_IP:31080/all -H "Host: exact.haproxy.local"
```

you will get 404 Not Found
