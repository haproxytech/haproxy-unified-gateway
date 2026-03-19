# Hello World HTTPRoute Example With Backend CRD

This example demonstrates how to use an HTTPRoute to expose simple "hello world" applications through the HAProxy Kubernetes Gateway, using a Backend CRD to customize the generated Backend in haproy.cfg configuration.

###  Feature: Merging multiple Backend CRs with Merge types Override or Append.

This example illsutrates a more extended way to use Backend CRDs, but showing how to merge several CRDs together, with 2 different Merge type available:
- *Override*
- *Append*

The corresponding filters to use in the Route `.backendRef` are:

```
- type: ExtensionRef
  extensionRef:
    group: gate.v3.haproxy.org
    kind: MergeType
    name: Override
```

```
- type: ExtensionRef
  extensionRef:
    group: gate.v3.haproxy.org
    kind: MergeType
    name: Append
```

The default Merge type is `Append` and is illustrated in the [backend-crd](../backend-crd/README.md) example.


In this example, we show how to set several merges that are applied one after the other:
```      filters:
      - type: ExtensionRef
        extensionRef:
          group: gate.v3.haproxy.org
          kind: Backend
          name: backend-cr-1
      - type: ExtensionRef
        extensionRef:
          group: gate.v3.haproxy.org
          kind: MergeType
          name: Override
      - type: ExtensionRef
        extensionRef:
          group: gate.v3.haproxy.org
          kind: Backend
          name: backend-cr-2
```

The Backend CR `backend-cr-1` will be merged with `Append` mode (no MergeType preceeding),.
The Backend CR `backend-cr-2` will be merge with `Override` mode.

The main difference is for slices where:
- in `Override` mode, the slices are replaced by the slices in the Backend CR (if present)
- **WARNING**: in `Override` mode, if a slice is empty in the applied Backend CR, then it's delete from the Backend. That meas that in our example, if `http_request_rule_list` was not present or empty in `backend-cr-2` the resulting backend in `haproxy.cfg` would have 0 http-request rules.

With this extended features, you can merge several backend CRDs and choose the type of mrge for the following Backend CRs by inserting an `ExtensionRef` `Override`/`Append` that will apply for the next Backend CRs applied.



## What is being deployed

This example deploys the following resources:

* A **GatewayClass** named `haproxy` that defines a class of Gateways that can be provisioned by the HAProxy Kubernetes Gateway.
* A **Gateway** named `hug-gateway` that requests a listener on port `31080` for HTTP traffic.
* An **HTTPRoute** that directs traffic for `backendcrdextended.haproxy.local` to the service `hello-world-backendcrd-extended-1`using 2 Backend CRs:
    -  `backend-cr-1` to configure the generated haproxy backend in `haproxy.cfg`

    This Backend CR adds: 2 `http-request` rules and overrides `timeout server`

    -  `backend-cr-2` in `Override` mode to replace the `http request` rules with a unique rule (and nothing on `timeout server`)



* One **Deployment** and **Service** for the echo applications:
  * `example/http-route/backend-crd-extended/echo-1.yaml`: A simple echo server
* Two **Backend** Custom CR `backend-cr-1` and `backend-cr-2`


## How to deploy in a `test` namepace

```bash
kubectl create ns test
kubectl apply -f -n test .
```

## How to check if the result is correct

### POD

```sh
$ cat /usr/local/hug/maps/hug_test_hug-gateway_http/path_prefix.map
backendcrdextended.haproxy.local/ hug_test_hello-world-backendcrd-extended-1_80_870306e1a7d334a1eceb23475e6075aa
```

### curl

Use `curl` to send a request to the services through the Gateway.

```sh
GW_IP=$(kubectl get gateway hug-gateway -n test -o jsonpath='{.status.addresses[0].value}')
curl http://$GW_IP:31080/hostname -H "Host: backendcrdextended.haproxy.local"
```

You should see a response from the services, confirming that the traffic was routed correctly.

```sh
hello-world-backendcrd-extended-1-6f889d47c4-crgpr
```

You can also check that by removing `/hostname` from the path that the 2 headers have been added
- "X-Haproxy-Current-Date": "04/Nov/2025:15:26:54 +0000"
- "X_is_secure": "0"


```sh
curl http://$GW_IP:31080 -H "Host: backendcrd.haproxy.local"
```

```sh
{
  "http": {
    "cookies": null,
    "headers": {
      "Accept": "*/*",
      "User-Agent": "curl/8.14.1",
      "X-Environment": "Staging",
      "X-Forwarded-For": "127.0.0.1"
    },
    "host": "backendcrdextended.haproxy.local",
    "method": "GET",
    "path": "/",
    "protocol": "HTTP/1.1",
    "query": "",
    "raw": "GET / HTTP/1.1\r\nHost: backendcrdextended.haproxy.local\r\nUser-Agent: curl/8.14.1\r\nAccept: */*\r\nX-Environment: Staging\r\nX-Forwarded-For: 127.0.0.1\r\n\r\n"
  },
  "os": {
    "hostname": "hello-world-backendcrd-extended-1-6f889d47c4-crgpr"
  },
  "tcp": {
    "ip": "10.244.0.10",
    "port": "33490"
  }
}
```


### haproxy.cfg
You can also check the generated `haproxy.cfg`.

```
cat /usr/local/hug/haproxy.cfg
```

```sh
backend hug_test_hello-world-backendcrd-extended-1_80_870306e1a7d334a1eceb23475e6075aa from haproxytech # {"hug":{"HTTPRoute":{"test/route-backend-crd-extended":{"Generation":3,"LinkID":"hug"}}}}
  mode http
  balance roundrobin
  option forwardfor
  no option abortonclose
  timeout server 70000
  default-server check
  server SRV_3d33a56ce86311b9e937dac74f9970b69281abe6 10.244.0.11:8888 enabled

```

In this file, you can check:
- the 2 `http-request` rules added from `backend-cr-1` are replaced with the one rule from `backend-cr-2`
- the overriden value `timeout server 70000` (instead of the default value: 50000) and nothing from `backend-cr-2`
