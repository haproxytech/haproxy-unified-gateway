# Hello World HTTPRoute with HTTPS offload Example

This example demonstrates how to use an HTTPRoute to expose simple "hello world" applications through the HAProxy Kubernetes Gateway, with HTTPS offload.



## What is being deployed

This example deploys the following resources:

* A **GatewayClass** named `haproxy` that defines a class of Gateways that can be provisioned by the HAProxy Kubernetes Gateway.
* A **Gateway** named `hug-gateway` that requests a listener on port `31080` for HTTP traffic.
* An **HTTPRoute** that directs traffic for `offload.haproxy.local` to the service `hello-world-offload`
* One **Deployment** and **Service** for the echo applications:
  * `hello-world-offload`: A simple echo server
* A **Secret** `offload`

## How to deploy in a `test` namepace

```bash
kubectl create ns test
kubectl apply -f -n test .
```

## How to check if the result is correct

### POD

```sh
$ cat /usr/local/hug/maps/hug_test_hug-gateway_https/path_prefix.map
https.haproxy.local/ hug_test_hello-world-offload_443__
```

### curl

### From outside the cluster

Assuming that a NodePort Service like

```yaml
apiVersion: v1
kind: Service
metadata:
  name: haproxy-unified-gateway
  namespace: haproxy-unified-gateway
spec:
  selector:
    run: haproxy-unified-gateway
  type: NodePort
  ports:
    - name: http
      port: 31080
      targetPort: 31080
      # nodePort is needed for NodePort service type
      nodePort: 31080
      protocol: TCP
    - name: https
      port: 31443
      targetPort: 31443
      # nodePort is needed for NodePort service type
      nodePort: 31443
      protocol: TCP
    - name: stat
      port: 31024
      targetPort: 31024
```

Use `curl` to send a request to the services through the Gateway.

```sh
GW_IP=$(kubectl get gateway hug-gateway -n test -o jsonpath='{.status.addresses[0].value}')
curl --header "Host: offload.haproxy.local" --resolve "offload.haproxy.local":31443:$GW_IP https://offload.haproxy.local:31443/hostname -k
```

You should see a response from the services, confirming that the traffic was routed correctly.

```sh
hello-world-offload-c6b56955d-4hjld
```

### haproxy.cfg
You can also check the generated `haproxy.cfg`.

```
cat /usr/local/hug/haproxy.cfg
```

```sh
frontend hug_test_hug-gateway_https from haproxytech # {"hug":{"Gateway":{"test/hug-gateway":{"Generation":1,"LinkID":"hug"}}}}
  mode http
  bind 0.0.0.0:31443 name v4 ssl crt-list /usr/local/hug/certlists/test_hug-gateway_https.list
  bind [::]:31443 name v6 ssl crt-list /usr/local/hug/certlists/test_hug-gateway_https.list
  acl route_is_json var(txn.route),bytes(0,1) -m str { # {"hug":"for lua routing"}
  http-request set-var(txn.base) base
  http-request set-var(txn.path) path
  http-request set-var(txn.host) req.hdr(Host),host_only
  http-request set-var(txn.route) base,map(/usr/local/hug/maps/hug_test_hug-gateway_https/path_exact.map) # {"hug":"exact domain + exact path"}
  http-request set-var(txn.route,ifnotexists) path,map(/usr/local/hug/maps/hug_test_hug-gateway_https/path_exact.map) # {"hug":"any domain + exact path"}
  http-request set-var(txn.route,ifnotexists) base,map_beg(/usr/local/hug/maps/hug_test_hug-gateway_https/path_prefix.map) # {"hug":"exact domain + path prefix"}
  http-request set-var(txn.route,ifnotexists) path,map_beg(/usr/local/hug/maps/hug_test_hug-gateway_https/path_prefix.map) # {"hug":"exact domain + path prefix"}
  http-request set-var(txn.route,ifnotexists) base,map_end(/usr/local/hug/maps/hug_test_hug-gateway_https/domain_wildcard_path_exact.map) # {"hug":"domain wildcard + exact path"}
  http-request set-var(txn.route,ifnotexists) path,map_reg(/usr/local/hug/maps/hug_test_hug-gateway_https/path_regex.map) # {"hug":"any domain + path regex"}
  http-request set-var(txn.route,ifnotexists) base,map_reg(/usr/local/hug/maps/hug_test_hug-gateway_https/path_regex.map) # {"hug":"domain wildcard + path prefix or regex, exact domain + path regex"}
  http-request lua.route if route_is_json # {"hug":"lua routing"}
  use_backend %[var(txn.backend)] if route_is_json
  use_backend %[var(txn.route)]
  default_backend backend_not_found
```
Note the `bind` doing HTTPS termination.

```sh
backend hug_test_hello-world-offload_80__ from haproxytech # {"hug":{"HTTPRoute":{"test/route-hello-world-offload":{"Generation":2,"LinkID":"hug"}}}}
  mode http
  balance roundrobin
  option forwardfor
  no option abortonclose
  timeout server 50000
  default-server check
  server SRV_8f3e9904fc22b571a9e21e8b158c09f79153cb9b 10.244.0.7:8888 enabled
```
