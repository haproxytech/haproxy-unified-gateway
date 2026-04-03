# Metrics Authentication: Kubernetes RBAC

This mode secures the metrics endpoint using Kubernetes-native authentication and authorization.
The controller validates requests via `TokenReview` and `SubjectAccessReview` API calls, ensuring
only ServiceAccounts with the correct RBAC permissions can access `/metrics`.

The endpoint is served over HTTPS with an auto-generated self-signed certificate.

> **Important:** This mode serves metrics over **HTTPS** (not HTTP). If you are switching from the
> default `none` mode, you must use `https://` in your curl commands and Prometheus scrape configs.
> Using `http://` will result in: *"Client sent an HTTP request to an HTTPS server."*

## When to Use

- Production environments
- In-cluster Prometheus with a dedicated ServiceAccount
- Environments requiring audit-trail of metrics access

## How It Works

1. Prometheus sends a request with a bearer token (ServiceAccount token)
2. The controller calls the Kubernetes API to validate the token (`TokenReview`)
3. The controller checks if the authenticated identity has permission to GET `/metrics` (`SubjectAccessReview`)
4. If both pass, the request is forwarded to the metrics handler

## Setup

### 1. Controller Configuration

Set `--metrics-auth=kube-rbac` in the controller args:

```yaml
# example/deploy/hug/controller.yaml (args section)
args:
  - --hugconf-crd=haproxy-unified-gateway/hugconf
  - --metrics-auth=kube-rbac
```

### 2. Controller RBAC

The controller's ServiceAccount needs permissions to create `TokenReview` and `SubjectAccessReview`
resources. These are already included in `example/deploy/hug/rbac.yaml`:

```yaml
# Required for kube-rbac metrics auth
- apiGroups:
    - authentication.k8s.io
  resources:
    - tokenreviews
  verbs:
    - create
- apiGroups:
    - authorization.k8s.io
  resources:
    - subjectaccessreviews
  verbs:
    - create
```

### 3. Prometheus RBAC

Prometheus needs a ClusterRole that grants access to the `/metrics` non-resource URL,
and a ClusterRoleBinding to its ServiceAccount. These are included in `example/deploy/hug/rbac.yaml`:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: haproxy-unified-gateway-metrics-reader
rules:
  - nonResourceURLs:
      - "/metrics"
    verbs:
      - get
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: haproxy-unified-gateway-metrics-reader
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: haproxy-unified-gateway-metrics-reader
subjects:
  - kind: ServiceAccount
    name: prometheus
    namespace: monitoring
```

Adjust the `subjects` section to match your Prometheus ServiceAccount name and namespace.
For example, with kube-prometheus-stack, the ServiceAccount is typically named `prometheus-kube-prometheus-prometheus`
in the `monitoring` namespace.

### 4. Deploy

```bash
kubectl apply -f example/deploy/hug/namespace.yaml
kubectl apply -f example/deploy/hug/rbac.yaml
kubectl apply -f example/deploy/hug/hugconf.yaml
kubectl apply -f example/deploy/hug/controller.yaml
```

### 5. Verify

Port-forward and test with a ServiceAccount token:

```bash
kubectl port-forward -n haproxy-unified-gateway svc/haproxy-unified-gateway 31060:31060

# Get a token for the Prometheus ServiceAccount
TOKEN=$(kubectl create token prometheus -n monitoring)

# Scrape metrics (skip TLS verify for self-signed cert)
curl -k -H "Authorization: Bearer $TOKEN" https://localhost:31060/metrics
```

Without a valid token, you should get a `401 Unauthorized` response:

```bash
curl -k https://localhost:31060/metrics
# 401 Unauthorized
```

### 6. Prometheus Scrape Config

```yaml
scrape_configs:
  - job_name: 'hug'
    scheme: https
    tls_config:
      # Self-signed cert, skip verification
      insecure_skip_verify: true
    authorization:
      type: Bearer
      credentials_file: /var/run/secrets/kubernetes.io/serviceaccount/token
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

If using the Prometheus Operator (kube-prometheus-stack), you can use a `ServiceMonitor` instead:

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
      authorization:
        type: Bearer
        credentials:
          name: prometheus-sa-token
          key: token
```

## Troubleshooting

### 403 Forbidden

The Prometheus ServiceAccount is authenticated but not authorized. Check that:
- The `haproxy-unified-gateway-metrics-reader` ClusterRole exists
- The ClusterRoleBinding references the correct ServiceAccount name and namespace

```bash
kubectl get clusterrolebinding haproxy-unified-gateway-metrics-reader -o yaml
```

### 401 Unauthorized

The token is missing or invalid. Check that:
- Prometheus is sending the `Authorization: Bearer <token>` header
- The ServiceAccount exists and its token is valid

```bash
kubectl create token prometheus -n monitoring
```

### Connection Refused

The controller may not have started the metrics server. Check:
- `--controller-port` is set (default: 31060)
- The pod logs for metrics server startup errors

```bash
kubectl logs -n haproxy-unified-gateway deploy/haproxy-unified-gateway | grep -i metric
```
