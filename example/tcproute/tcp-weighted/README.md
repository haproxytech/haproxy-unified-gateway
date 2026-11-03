# TCPRoute Weighted Example

This example shows a `TCPRoute` splitting connections between two TCP services by
weight. It is the TCP counterpart of the blue-green HTTPRoute example.

A `TCPRoute` attaches to a listener with `protocol: TCP`, and the weights on its
`backendRefs` decide how connections are shared: about 90% reach the `blue`
service and about 10% reach the `green` one. Weights are proportions, not
percentages, and connections are picked at random, so a sample of 100
connections lands close to the ratio rather than exactly on it.

---

## What is deployed

- **GatewayClass**: `haproxy`
- **Gateway**: `tcp-weighted-gateway`, a listener on port **31447** with `protocol: TCP`
- **TCPRoute**: `tcp-weighted-route`, with two weighted backends:
  - weight `90` to `tcp-weighted-blue`
  - weight `10` to `tcp-green`
- **Deployments and Services**: `tcp-weighted-blue` and `tcp-green`, both serving TLS on port `443`

---

## Deploying the example

```bash
kubectl apply -n <your example namespace> -f .
```

## Validating

The port's selection is visible in the generated map, next to the controller's
configuration:

```sh
cat /usr/local/hug/maps/*_tcp_31447/tcp_route.map
```

```sh
tcp_31447 {"a":"wr","l":"hug_<namespace>_tcp-green_443_:10,hug_<namespace>_tcp-weighted-blue_443_:90"}
```

Send connections through the Gateway and count which backend answered:

```sh
GW_IP=$(kubectl get gateway tcp-weighted-gateway -n <namespace> -o jsonpath='{.status.addresses[0].value}')
GW_IP=$GW_IP sh test.sh
```

The script opens 100 TLS connections through the Gateway, and you should see a
split close to the configured weights:

```sh
 91 blue
  9 green
```

A single connection is enough to prove forwarding works:

```sh
curl -sk --resolve "tcp-weighted.example.local:31447:$GW_IP" https://tcp-weighted.example.local:31447/hostname
```

Note that only one `TCPRoute` serves a port at a time: if several routes attach
to the same listener, the oldest one receives all connections.
