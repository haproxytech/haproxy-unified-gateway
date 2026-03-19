# Global Custom Resource Example

This example is identical to [example/http-route/hello-world-https-offload](../../http-route/hello-world-https-offload) with one addition: a `Global` Custom Resource that customizes the `global` section of the generated `haproxy.cfg`.

Refer to the [Global CR documentation](../../../documentation/CustomResources/global.md) for the full reference.

## What is being deployed

The same resources as the reference example, plus:

* A **Global** CR named `global` that raises `maxconn` to `32100`.
* An updated **HugConf** with a `globalRef` pointing to that `Global` CR.

## How to deploy in a `test` namespace

```bash
kubectl create ns test
kubectl apply -n test -f .
```

## What changes compared to the base example

The only difference is in the `global` section of `haproxy.cfg`.

Without the `Global` CR (HUG built-in default):

```
global
    maxconn 32000
    ...
```

With this `Global` CR (`merge_strategy: append`, `maxconn: 32100`):

```
global
    maxconn 32100
    ...
```

All frontends, backends, maps, and routing behaviour remain identical to the base example.

## How to check if the result is correct

### haproxy.cfg — global section

```sh
cat /usr/local/hug/haproxy.cfg
```

The `global` section should show the overridden `maxconn`:

```
global
    maxconn 32100
    ...
```

### curl

```sh
GW_IP=$(kubectl get gateway hug-gateway -n test -o jsonpath='{.status.addresses[0].value}')
curl --header "Host: offload.haproxy.local" --resolve "offload.haproxy.local":31443:$GW_IP https://offload.haproxy.local:31443/hostname -k
```

You should see a response like:

```
hello-world-offload-c6b56955d-4hjld
```
