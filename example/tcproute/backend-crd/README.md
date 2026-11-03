# TCPRoute Example With a Backend CR

This example shows how to customise the HAProxy backend generated for a
`TCPRoute`, using a `Backend` Custom Resource attached to the target Service.

A `TCPRoute` rule has no filters, so the route-level `ExtensionRef` mechanism
available to HTTPRoutes does not apply. A `TCPRoute` receives a `Backend` CR
through the Service annotation instead:

```yaml
metadata:
  annotations:
    gate.v3.haproxy.org/cr-backend: backend-tcp-tuning
```

The CR is merged into every backend HUG builds for that Service, whichever route
kind references it.

---

## What is deployed

- **GatewayClass**: `haproxy`
- **Gateway**: `tcp-backend-crd-gateway`, a listener on port **31448** with `protocol: TCP`
- **TCPRoute**: `tcp-backend-crd-route`, forwarding to `tcp-echo` port `443`
- **Deployment and Service**: `tcp-echo`, annotated with the Backend CR
- **Backend CR**: `backend-tcp-tuning`, which sets `timeout server 120000` and
  `balance leastconn` on the generated backend

---

## Deploying the example

```bash
kubectl apply -n <your example namespace> -f .
```

## Validating

Check the generated backend in the controller's configuration:

```sh
cat /usr/local/hug/haproxy.cfg
```

```sh
backend hug_<namespace>_tcp-echo_443_ from haproxytech # {"hug":{"TCPRoute":...
  mode tcp
  balance leastconn
  no option abortonclose
  timeout server 120000
  default-server check
  server SRV_... <pod ip>:8443 enabled
```

`mode tcp` and `timeout server 120000` come from the merge; without the CR the
timeout would stay at the controller default.

Then check that connections reach the backend:

```sh
GW_IP=$(kubectl get gateway tcp-backend-crd-gateway -n <namespace> -o jsonpath='{.status.addresses[0].value}')
curl -sk --resolve "tcp-backend-crd.example.local:31448:$GW_IP" https://tcp-backend-crd.example.local:31448/hostname
```

You should see the echo pod's hostname.
