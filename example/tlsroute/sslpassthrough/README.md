# TLSRoute SSL Passthrough Example

This example demonstrates how to expose a backend application using **TLS passthrough** with a `TLSRoute` and the **HAProxy Kubernetes Gateway**.

When using passthrough mode, the Gateway forwards the encrypted TLS stream directly to the backend service **without terminating TLS**. This allows the backend to present its own certificate.

---

## What is deployed

This example installs the following resources:

- **GatewayClass**:
  `haproxy` — defines a class of Gateways managed by the HAProxy Kubernetes Gateway controller.

- **Gateway**:
  `tls-gateway` — exposes a listener on port **31443** configured for **TLS passthrough** and accepting routes for the hostname `example.local`.

- **TLSRoute**:
  - `tlsroute` — matches:
    - SNI: `example.local`
    - Any path
    and forwards traffic to the backend service `http-echo`, which terminates TLS.

---

## Deploying the example

```bash
kubectl apply -n <your example namespace> -f .
```

## Validating passthrough functionality

To test traffic flow, open a shell inside the Gateway controller pod (or anywhere with network access to the Gateway listener).

## Using curl

Because the Gateway is in passthrough mode, the backend’s certificate should be presented directly to the client.

Run:

```sh
GW_IP=$(kubectl get gateway tls-gateway -n <namespace> -o jsonpath='{.status.addresses[0].value}')
curl -v -k -H "Host: example.local" --resolve "example.local:31444:$GW_IP"  https://example.local:31444/
```
You should observe:
* The TLS handshake showing the backend’s certificate, not the Gateway’s.
* A successful HTTP 200 response from the echo application.

Example excerpt from the curl -v output:

```
* Server certificate:
*  subject: C=FR; L=PARIS; O=Echo HTTP; CN=http-echo-5fbc86ccc-db9hh
*  start date: Dec  2 09:22:28 2025 GMT
*  expire date: Dec  2 09:22:28 2026 GMT
*  issuer: C=FR; L=PARIS; O=Echo HTTP; CN=http-echo-5fbc86ccc-db9hh
*  SSL certificate verify result: self-signed certificate (18), continuing anyway.
*   Certificate level 0: Public key type RSA (2048/112 Bits/secBits), signed using sha256WithRSAEncryption
```

This confirms that:

1. The request reached the Gateway.

2. The Gateway routed the encrypted TLS stream through to the backend.

3. TLS termination occurred on the backend, proving that passthrough mode is functioning correctly.
