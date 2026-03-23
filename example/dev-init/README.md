# Echo Deployment

This example contains a mix of [hello-world](../hello-world/README.md) and [http-world-https-offlad](../http-world-https-offlad/README.md) examples.
It show how to have 2 HTTPRoutes `route-echo-http` and `route-echo-https` to the same backend service and using both listeners `http` and `https` of the Gateway.



## What is being deployed

This example deploys the following resources:

* **Deployment** named `http-echo` that runs the echo server.
* **Service** named `http-echo` to expose the echo server's HTTP and HTTPS ports.
* **Gateway** named `hug-gateway` with 2 listeners `http` and `https` (TLS teminate = offloading)
* **Secret** named `offlad` referenced by the `https` listener
* **GatewayClass** named `haproxy`
* **HTTPRoute** named `route-echo-http` that directs traffic for `offload.haproxy` to the service `http-echo` referencing the `http` listener
* **HTTPRoute** named `route-echo-https` that directs traffic for `offload.haproxy` to the service `http-echo` referencing the `https` listener


## How to deploy

```sh
kubectl apply -f .
```

## How to test e2e curls from your laptop

### HTTP: For the route route-echo-http

```sh
GW_IP=$(kubectl get gateway hug-gateway -n example -o jsonpath='{.status.addresses[0].value}')
curl --header "Host: offload.haproxy" http://$GW_IP:31080/all
{
  "http": {
    "cookies": null,
    "headers": {
      "Accept": "*/*",
      "User-Agent": "curl/8.5.0",
      "X-Forwarded-For": "10.244.0.1"
    },
    "host": "offload.haproxy",
    "method": "GET",
    "path": "/all",
    "protocol": "HTTP/1.1",
    "query": "",
    "raw": "GET /all HTTP/1.1\r\nHost: offload.haproxy\r\nUser-Agent: curl/8.5.0\r\nAccept: */*\r\nX-Forwarded-For: 10.244.0.1\r\n\r\n"
  },
  "os": {
    "hostname": "http-echo-5fbc86ccc-p5gd5"
  },
  "tcp": {
    "ip": "10.244.0.10",
    "port": "51104"
  }
}
```

### HTTPS: For the route route-echo-https

```sh
curl --header "Host: offload.haproxy"  https://$GW_IP:31443/all -k
{
  "http": {
    "cookies": null,
    "headers": {
      "Accept": "*/*",
      "User-Agent": "curl/8.5.0",
      "X-Forwarded-For": "10.244.0.1"
    },
    "host": "offload.haproxy",
    "method": "GET",
    "path": "/all",
    "protocol": "HTTP/1.1",
    "query": "",
    "raw": "GET /all HTTP/1.1\r\nHost: offload.haproxy\r\nUser-Agent: curl/8.5.0\r\nAccept: */*\r\nX-Forwarded-For: 10.244.0.1\r\n\r\n"
  },
  "os": {
    "hostname": "http-echo-5fbc86ccc-p5gd5"
  },
  "tcp": {
    "ip": "10.244.0.10",
    "port": "54940"
  }
}
```
