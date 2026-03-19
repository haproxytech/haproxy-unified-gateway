# Hello World HTTPRoute Example

This example demonstrates how to use an HTTPRoute to expose simple "hello world" applications through the HAProxy Kubernetes Gateway, using both prefix and exact path matching.

## What is being deployed

This example deploys the following resources:

* A **GatewayClass** named `haproxy` that defines a class of Gateways that can be provisioned by the HAProxy Kubernetes Gateway.
* A **Gateway** named `hug-gateway` that requests a listener on port `31080` for HTTP traffic.
* An **HTTPRoute** that directs traffic for `blue-green.haproxy.local` to two different services with weights:
  * 90% of the traffic is sent to the `blue` service.
  * 10% of the traffic is sent to the `green` service.
* Two **Deployments** and **Services** for the echo applications:
  * `blue`: A simple echo server representing the "blue" version.
  * `green`: Another simple echo server representing the "green" version.

## How to deploy

```bash
kubectl apply -f .
```

## How to check if the result is correct

### POD

```sh
$ cat /usr/local/hug/maps/hug_default_hug-gateway_http/path_prefix.map
blue-green.haproxy.local/ {"a":"wr","l":"hug_default_blue_8888__:90,hug_default_green_8888__:10"}
```

### curl

Use `curl` to send a request to the services through the Gateway.

```sh
GW_IP=$(kubectl get gateway hug-gateway -n default -o jsonpath='{.status.addresses[0].value}')
curl http://$GW_IP:31080/hostname -H "Host: blue-green.haproxy.local"
```

You should see a response from the services, confirming that the traffic was routed correctly (one of two services).

```sh
blue-77f7ddfc87-cmr94
green-665cfb454f-qs68k
```

## script

```sh
$ sh test.sh
90 blue
10 green
```

Note that due to randomness, 9/1 ratio might slightly differ
