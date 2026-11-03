# TCPRoute Example

This example demonstrates how to expose a plain TCP service with a `TCPRoute` and the **HAProxy Kubernetes Gateway**.

A `TCPRoute` attaches to a listener with `protocol: TCP`. Selection is by port alone: of the routes attached to one listener, only the oldest receives connections, per the Gateway API tie-break.

---

## What is deployed

- **GatewayClass**: `haproxy`
- **Gateway**: `tcp-gateway` - a listener on port **31446** with `protocol: TCP`, accepting `TCPRoute`s
- **TCPRoute**: `tcproute` - forwards every connection on the listener port to the `http-echo` service, port `443`

A port with no route - or a share that resolves to no backend - closes the connection: a TCP client is never answered with an HTTP error page.

---

## Deploying the example

```bash
kubectl apply -n <your example namespace> -f .
```

## Validating

The `http-echo` backend terminates TLS, so a plain TCP connection through the Gateway still reaches it:

```sh
GW_IP=$(kubectl get gateway tcp-gateway -n <namespace> -o jsonpath='{.status.addresses[0].value}')
curl -v -k --resolve "example.local:31446:$GW_IP" https://example.local:31446/
```

You should observe the backend's certificate and a `200` response, proving the TCP stream was forwarded end to end.
