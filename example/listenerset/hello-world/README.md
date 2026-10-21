# Hello World ListenerSet Example

This example exposes an echo application through a listener declared by a
ListenerSet, rather than by the Gateway itself. It follows the structure of the
[HTTPRoute hello-world example](../../http-route/hello-world), with separate
infrastructure and application namespaces to demonstrate delegation.

## Prerequisites

- A running HUG controller with ListenerSet support and the corresponding RBAC
  permissions, as provided by `example/deploy/hug/rbac.yaml`.
- Gateway API CRDs serving `ListenerSet` at `gateway.networking.k8s.io/v1`, and a
  Gateway CRD exposing `spec.allowedListeners`.
- The controller must watch both `listenerset-infra` and `listenerset-app`. If it
  is restricted to a dedicated Gateway, it must serve
  `listenerset-infra/hug-gateway`.
- The `haproxytech/http-echo:latest` image must be available to the cluster, as in
  the other hello-world examples.
- The HUG controller Service must expose port `31080`. To use the advertised
  Gateway address, that address must also be reachable from the machine running
  `curl`. For local Kind with LoadBalancer Services, the load-balancer provider
  must be running; an assigned Pod IP alone is not sufficient.

Check that the ListenerSet CRD is installed:

```sh
kubectl get crd listenersets.gateway.networking.k8s.io
```

## What is being deployed

- Two namespaces: `listenerset-infra` and `listenerset-app`. The application
  namespace carries the label `listenerset-example=allowed`.
- A dedicated GatewayClass, `haproxy-listenerset`, managed by
  `gate.haproxy.org/hug`.
- A Gateway, `listenerset-infra/hug-gateway`, whose `allowedListeners` selector
  permits ListenerSets from namespaces carrying that label. Its own HTTP
  listener serves `gateway.haproxy.local` on port `31080`.
- A ListenerSet, `listenerset-app/app-listeners`, that references this Gateway
  across namespaces and contributes an HTTP listener for
  `listenerset.haproxy.local` on port `31080`.
- An HTTPRoute, `listenerset-app/route-hello-world-listenerset`, whose parentRef
  explicitly names `kind: ListenerSet`, `name: app-listeners` and
  `sectionName: http`.
- A Deployment and Service, `listenerset-app/hello-world-listenerset`, using the
  same echo application as the HTTPRoute examples.

The Gateway and ListenerSet deliberately both name their listener `http`.
Listener names are scoped to the resource declaring them. The distinct hostnames
allow both listeners to share the same port without a conflict.

The route attaches to the ListenerSet's listener, not to the Gateway's own
listener. Changing only its parent kind/name to the Gateway would not select the
contributed listener. The ListenerSet's `allowedRoutes.namespaces.from: Same`
refers to the application namespace, where both the ListenerSet and route live.

No ReferenceGrant is required here: the Gateway explicitly permits the
ListenerSet's namespace, and the route's backend Service is in the route's own
namespace. A Gateway without `allowedListeners` would reject this contribution.

## How to deploy

Run these commands from the repository root. Create the namespaces first so the
remaining namespaced resources can be applied safely:

```sh
EXAMPLE=example/listenerset/hello-world
kubectl apply -f "$EXAMPLE/namespaces.yaml"
kubectl apply -f "$EXAMPLE/"

kubectl -n listenerset-app rollout status deployment/hello-world-listenerset --timeout=120s
kubectl wait --for=condition=Accepted gatewayclass/haproxy-listenerset --timeout=60s
kubectl -n listenerset-infra wait --for=condition=Programmed gateway/hug-gateway --timeout=120s
kubectl -n listenerset-app wait --for=condition=Accepted listenerset/app-listeners --timeout=60s
kubectl -n listenerset-app wait --for=condition=Programmed listenerset/app-listeners --timeout=120s
kubectl -n listenerset-app wait \
  --for=jsonpath='{.status.parents[0].conditions[?(@.type=="Accepted")].status}'=True \
  httproute/route-hello-world-listenerset --timeout=60s
```

## How to check the result

The Gateway should report one attached ListenerSet, and the ListenerSet's `http`
entry should report one attached route:

```sh
kubectl -n listenerset-infra get gateway hug-gateway \
  -o jsonpath='{.status.attachedListenerSets}{"\n"}'
kubectl -n listenerset-app get listenerset app-listeners \
  -o jsonpath='{.status.listeners[?(@.name=="http")].attachedRoutes}{"\n"}'
```

Both commands should print `1`. The ListenerSet's per-entry conditions and the
route's parent conditions can also be inspected with:

```sh
kubectl -n listenerset-app describe listenerset app-listeners
kubectl -n listenerset-app describe httproute route-hello-world-listenerset
```

Wait for the Gateway to publish an address, then send a request with the
ListenerSet listener's hostname:

```sh
kubectl -n listenerset-infra wait \
  --for=jsonpath='{.status.addresses[0].value}' \
  gateway/hug-gateway --timeout=120s

GW_ADDRESS=$(kubectl -n listenerset-infra get gateway hug-gateway \
  -o jsonpath='{.status.addresses[0].value}')
# Bracket an IPv6 literal when constructing the URL.
case "$GW_ADDRESS" in
  *:*) GW_ADDRESS="[$GW_ADDRESS]" ;;
esac
curl -H 'Host: listenerset.haproxy.local' "http://${GW_ADDRESS}:31080/hostname"
```

The response should contain the echo Pod's hostname, for example:

```text
hello-world-listenerset-<replicaset-hash>-<pod-suffix>
```

If the Gateway address is unavailable or unreachable, inspect the HUG Service and
its load-balancer provider. As an alternative, port-forward the controller
Service in one terminal (adjust its namespace/name if your deployment differs):

```sh
kubectl -n haproxy-unified-gateway port-forward service/haproxy-unified-gateway 31080:31080
```

Then test from another terminal:

```sh
curl -H 'Host: listenerset.haproxy.local' http://127.0.0.1:31080/hostname
```

## Cleanup

The namespaces and GatewayClass are dedicated to this example. The following
command also deletes any other resources you may have added to these namespaces:

```sh
kubectl delete -f example/listenerset/hello-world/ --ignore-not-found=true
```
