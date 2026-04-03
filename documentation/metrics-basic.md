# Metrics Authentication: HTTP Basic Auth

This mode secures the metrics endpoint using HTTP Basic Authentication over HTTPS.
Credentials are provided via CLI flags or environment variables.

The endpoint is served over HTTPS with an auto-generated self-signed certificate.

> **Important:** This mode serves metrics over **HTTPS** (not HTTP). If you are switching from the
> default `none` mode, you must use `https://` in your curl commands and Prometheus scrape configs.
> Using `http://` will result in: *"Client sent an HTTP request to an HTTPS server."*

## When to Use

- External Prometheus instances scraping from outside the cluster
- Simple setups where Kubernetes RBAC is not available or too complex
- Quick secure setup without Kubernetes-specific configuration

## How It Works

1. Prometheus sends a request with an `Authorization: Basic <base64(user:pass)>` header
2. The controller validates the credentials using constant-time comparison (resistant to timing attacks)
3. If credentials match, the request is forwarded to the metrics handler
4. If credentials are invalid, a `401 Unauthorized` response is returned with a `WWW-Authenticate` challenge

## Setup

### 1. Create a Secret for Credentials

Store the credentials in a Kubernetes Secret so they can be mounted as environment variables:

```bash
kubectl create secret generic hug-metrics-auth \
  -n haproxy-unified-gateway \
  --from-literal=username=prometheus \
  --from-literal=password='<your-strong-password>'
```

### 2. Controller Configuration

**Option A: Direct flags (for testing)**

```yaml
# example/deploy/hug/controller.yaml (args section)
args:
  - --hugconf-crd=haproxy-unified-gateway/hugconf
  - --metrics-auth=basic
  - --metrics-basic-auth-user=prometheus
  - --metrics-basic-auth-password=changeme
```

**Option B: Environment variables from Secret (recommended for production)**

```yaml
# example/deploy/hug/controller.yaml (container section)
args:
  - --hugconf-crd=haproxy-unified-gateway/hugconf
  - --metrics-auth=basic
env:
  - name: POD_NAME
    valueFrom:
      fieldRef:
        fieldPath: metadata.name
  - name: POD_NAMESPACE
    valueFrom:
      fieldRef:
        fieldPath: metadata.namespace
  - name: POD_IP
    valueFrom:
      fieldRef:
        fieldPath: status.podIP
  - name: METRICS_BASIC_AUTH_USER
    valueFrom:
      secretKeyRef:
        name: hug-metrics-auth
        key: username
  - name: METRICS_BASIC_AUTH_PASSWORD
    valueFrom:
      secretKeyRef:
        name: hug-metrics-auth
        key: password
```

The controller uses `ff` for flag parsing, which automatically reads environment variables.
Flag `--metrics-basic-auth-user` maps to env var `METRICS_BASIC_AUTH_USER`.

### 3. Deploy

```bash
kubectl apply -f example/deploy/hug/namespace.yaml
kubectl apply -f example/deploy/hug/rbac.yaml
kubectl apply -f example/deploy/hug/hugconf.yaml

# Create the credentials secret first
kubectl create secret generic hug-metrics-auth \
  -n haproxy-unified-gateway \
  --from-literal=username=prometheus \
  --from-literal=password='<your-strong-password>'

kubectl apply -f example/deploy/hug/controller.yaml
```

### 4. Verify

Port-forward and test with credentials:

```bash
kubectl port-forward -n haproxy-unified-gateway svc/haproxy-unified-gateway 31060:31060

# With valid credentials (skip TLS verify for self-signed cert)
curl -k -u prometheus:changeme https://localhost:31060/metrics

# Without credentials - should return 401
curl -k https://localhost:31060/metrics
# 401 Unauthorized

# With wrong credentials - should return 401
curl -k -u wrong:credentials https://localhost:31060/metrics
# 401 Unauthorized
```

### 5. Prometheus Scrape Config

```yaml
scrape_configs:
  - job_name: 'hug'
    scheme: https
    tls_config:
      # Self-signed cert, skip verification
      insecure_skip_verify: true
    basic_auth:
      username: prometheus
      password: '<your-strong-password>'
    kubernetes_sd_configs:
      - role: endpoints
        namespaces:
          names:
            - haproxy-unified-gateway
    relabel_configs:
      - source_labels: [__meta_kubernetes_service_name, __meta_kubernetes_endpoint_port_name]
        action: keep
        regex: haproxy-unified-gateway;metrics
```

If using the Prometheus Operator (kube-prometheus-stack), create a Secret for the credentials
and reference it in a `ServiceMonitor`:

```bash
# Create the secret in the namespace where Prometheus runs
kubectl create secret generic hug-metrics-basic-auth \
  -n monitoring \
  --from-literal=username=prometheus \
  --from-literal=password='<your-strong-password>'
```

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: haproxy-unified-gateway
  namespace: haproxy-unified-gateway
spec:
  selector:
    matchLabels:
      run: haproxy-unified-gateway
  endpoints:
    - port: metrics
      scheme: https
      tlsConfig:
        insecureSkipVerify: true
      basicAuth:
        username:
          name: hug-metrics-basic-auth
          key: username
        password:
          name: hug-metrics-basic-auth
          key: password
```

## Security Considerations

- Always use strong passwords. Avoid default or example passwords in production.
- Prefer environment variables from Secrets over plain-text flags to avoid credentials appearing in `kubectl describe pod` output.
- The HTTPS self-signed certificate prevents credentials from being sent in plain text over the network.
- Credentials are compared using constant-time comparison to prevent timing attacks.
- Consider rotating credentials periodically by updating the Kubernetes Secret and restarting the controller.

## Troubleshooting

### 401 Unauthorized

- Verify the username and password match what the controller was started with
- Check if the credentials were loaded from env vars:

```bash
kubectl get pod -n haproxy-unified-gateway -l run=haproxy-unified-gateway -o yaml | grep -A2 METRICS_BASIC
```

### Connection Refused

- Ensure the controller pod is running and the metrics port is configured
- Check pod logs for startup errors:

```bash
kubectl logs -n haproxy-unified-gateway deploy/haproxy-unified-gateway | grep -i metric
```

### Certificate Errors

Both `kube-rbac` and `basic` modes use a self-signed certificate. You must either:
- Set `insecure_skip_verify: true` in Prometheus TLS config
- Or extract and trust the certificate (not recommended for self-signed)
