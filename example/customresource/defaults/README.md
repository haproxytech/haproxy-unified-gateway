# Defaults Custom Resource Example

This example is identical to [example/http-route/hello-world-https-offload](../../http-route/hello-world-https-offload) with one addition: a `Defaults` Custom Resource that customizes the `defaults` section of the generated `haproxy.cfg`.

Refer to the [Defaults CR documentation](../../../documentation/CustomResources/defaults.md) for the full reference.

## What is being deployed

The same resources as the reference example, plus:

* A **Defaults** CR named `haproxytech` that raises `client_timeout` to `60000` ms and `server_timeout` to `60000` ms.
* An updated **HugConf** with a `defaultsRef` pointing to that `Defaults` CR.

## How to deploy in a `test` namespace

```bash
kubectl create ns test
kubectl apply -n test -f .
```

## What changes compared to the base example

The only difference is in the `defaults` section of `haproxy.cfg`.

Without the `Defaults` CR (HUG built-in default):

```
defaults
    client_timeout   50000
    server_timeout   50000
    ...
```

With this `Defaults` CR (`merge_strategy: append`, `client_timeout: 60000`, `server_timeout: 60000`):

```
defaults
    client_timeout   60000
    server_timeout   60000
    ...
```

All frontends, backends, maps, and routing behaviour remain identical to the base example.

## How to check if the result is correct

### haproxy.cfg — defaults section

```sh
cat /usr/local/hug/haproxy.cfg
```

The `defaults` section should show the overridden timeouts:

```
defaults
    client_timeout   60000
    server_timeout   60000
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
