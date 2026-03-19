# Hello World HTTPRoute Example With Backend CRD

This example demonstrates how to use an HTTPRoute to expose simple "hello world" applications through the HAProxy Kubernetes Gateway, using a Backend CRD to customize the generated Backend in haproy.cfg configuration.

### Feature

Adding a Filter of type `ExtensionRef` with:
- `Group=gate.v3.haproxy.org`
- `Kind=Backend`
to the Route `.BackendRef` allow to customize how the backend is generated in `haproxy.cfg` configuration and to benefit from haproxy full flexibility.
```filters:
      - type: ExtensionRef
        extensionRef:
          group: gate.v3.haproxy.org
          kind: Backend
          name: backend-cr-1
```

Note that it's possible to define several filters of type `ExtensionRef` with kind `Backend`

The will merge successfully all the Backend CR into the Backend with the following behavior:
- **Append**

**Append** means that
- any keyword already existing in the backend will be overriden by the new value
- slices will be appended to existing slice


## What is being deployed

This example deploys the following resources:

* A **GatewayClass** named `haproxy` that defines a class of Gateways that can be provisioned by the HAProxy Kubernetes Gateway.
* A **Gateway** named `hug-gateway` that requests a listener on port `31080` for HTTP traffic.
* An **HTTPRoute** that directs traffic for `backendcrd.haproxy.local` to the service `hello-world-backendcrd-1`
    Allowing to use the Backend CR `backend-cr-1` to configure the generated haproxy backend in `haproxy.cfg`

    This Backend CR adds:
    - 2 `http-request` rules
    - Overrides `timeout server`
* One **Deployment** and **Service** for the echo applications:
  * `hello-world-backendcrd-1`: A simple echo server
* A **Backend** Custom CR `backend-cr-1`


## How to deploy in a `test` namepace

```bash
kubectl create ns test
kubectl apply -f -n test .
```

## How to check if the result is correct

### POD

```sh
$ cat /usr/local/hug/maps/hug_test_hug-gateway_http/path_prefix.map
backendcrd.haproxy.local/ hug_test_hello-world-backendcrd-1_80_ef84fba452b3f62faf235c47b46c1e54
```

### curl

Use `curl` to send a request to the services through the Gateway.

```sh
GW_IP=$(kubectl get gateway hug-gateway -n test -o jsonpath='{.status.addresses[0].value}')
curl http://$GW_IP:31080/hostname -H "Host: backendcrd.haproxy.local"
```

You should see a response from the services, confirming that the traffic was routed correctly.

```sh
hello-world-backendcrd-1-55fb8db986-z8t7g
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
      "X-Forwarded-For": "127.0.0.1",
      "X-Haproxy-Current-Date": "04/Nov/2025:15:26:54 +0000",
      "X_is_secure": "0"
    },
    "host": "backendcrd.haproxy.local",
    "method": "GET",
    "path": "/",
    "protocol": "HTTP/1.1",
    "query": "",
    "raw": "GET / HTTP/1.1\r\nHost: backendcrd.haproxy.local\r\nUser-Agent: curl/8.14.1\r\nAccept: */*\r\nX-Forwarded-For: 127.0.0.1\r\nX-Haproxy-Current-Date: 04/Nov/2025:15:26:54 +0000\r\nX_is_secure: 0\r\n\r\n"
  },
  "os": {
    "hostname": "hello-world-backendcrd-1-55fb8db986-z8t7g"
  },
  "tcp": {
    "ip": "10.244.0.22",
    "port": "55398"
  }
}
```


### haproxy.cfg
You can also check the generated `haproxy.cfg`.

```
cat /usr/local/hug/haproxy.cfg
```

```sh
backend hug_test_hello-world-backendcrd-1_80_ef84fba452b3f62faf235c47b46c1e54 from haproxytech # {"hug":{"HTTPRoute":{"test/route-backend-crd":{"Generation":2,"LinkID":"hug"}}}}
  mode http
  balance roundrobin
  option forwardfor
  no option abortonclose
  timeout server 70000   ###### Value overriden
  default-server check
  http-request add-header X-Haproxy-Current-Date %T unless { src 192.168.1.0/16 }  ### slices appended
  http-request add-header X_Is_Secure %[ssl_fc] unless { src 192.168.1.0/16 }   #### slices appended
  server SRV_8d9323b858b14d6c3610ceadfc874a4af22fa007 10.244.0.15:8888 enabled
```

In this file, you can check:
- the 2 `http-request` rules are dded
- the overriden value `timeout server 70000` (instead of the default value: 50000)
