# Prometheus Metrics

HAProxy Unified Gateway exposes Prometheus metrics to help you monitor controller health, performance, and operations.

## Overview

Metrics are served by the controller-runtime metrics server on the controller port (default: `31060`).
When enabled, the endpoint is available at `http(s)://<pod-ip>:<port>/metrics`.

## Configuration

### CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--controller-port` | `31060` | Port to expose Prometheus metrics on |
| `--metrics-auth` | `none` | Authentication mode: `none`, `kube-rbac`, `basic` |
| `--metrics-basic-auth-user` | (empty) | Basic auth username (used with `--metrics-auth=basic`) |
| `--metrics-basic-auth-password` | (empty) | Basic auth password (used with `--metrics-auth=basic`) |

All flags can also be set via environment variables (e.g., `METRICS_AUTH=basic`).

### Authentication Modes

| Mode | Protocol | Auth Method | Use Case |
|------|----------|-------------|----------|
| `none` | HTTP | No authentication | Development, trusted networks |
| `kube-rbac` | HTTPS | Kubernetes TokenReview + SubjectAccessReview | Production with in-cluster Prometheus |
| `basic` | HTTPS | HTTP Basic Authentication | External Prometheus, simple setups |

Both `kube-rbac` and `basic` modes automatically enable HTTPS with a self-signed certificate.

> **Note:** Switching from `none` to `basic` or `kube-rbac` changes the protocol from HTTP to HTTPS.
> You must update your curl commands (use `https://` and `-k` for self-signed certs) and Prometheus
> scrape configs (set `scheme: https` with `insecure_skip_verify: true`) accordingly.

For detailed setup instructions, see:
- [metrics-none.md](metrics-none.md) - No authentication (default)
- [metrics-kube-rbac.md](metrics-kube-rbac.md) - Kubernetes RBAC authentication
- [metrics-basic.md](metrics-basic.md) - HTTP Basic authentication

## HAProxy Native Metrics

In addition to the controller metrics described below, HAProxy itself exposes Prometheus metrics
via its built-in `prometheus-exporter` service. These are available on the **stats port** (default: `31024`)
at the `/metrics` path.

This is configured in the default `haproxy.cfg` stats frontend:

```haproxy
frontend stats from haproxytech
  mode http
  http-request use-service prometheus-exporter if { path /metrics }
  stats enable
  stats uri /
```

The stats frontend also serves the HAProxy stats dashboard at `/`.

These HAProxy-native metrics cover connection counts, request rates, backend health, latency percentiles,
and all standard HAProxy counters. For a full reference, see the
[HAProxy Prometheus exporter documentation](https://www.haproxy.com/documentation/haproxy-configuration-tutorials/metrics/prometheus/).

For detailed setup, see [metrics-haproxy.md](metrics-haproxy.md).

## Controller Metrics

All controller metrics use the `hug_` namespace prefix.

### Event Batch Processing

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `hug_event_batch_duration_seconds` | Histogram | - | Duration of event batch processing |
| `hug_event_batch_size` | Histogram | - | Number of events in a processed batch |
| `hug_event_batch_total` | Counter | - | Total event batches processed |
| `hug_event_batch_errors_total` | Counter | - | Total event batch processing errors |

### HAProxy Configuration

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `hug_config_generation_duration_seconds` | Histogram | - | Duration of HAProxy config diff computation |
| `hug_config_transfer_duration_seconds` | Histogram | - | Duration of HAProxy config transfer and application |
| `hug_haproxy_reload_total` | Counter | - | Total HAProxy configuration reloads |
| `hug_config_diffs_total` | Counter | `operation`, `resource` | Config diffs by operation and resource type |

### Kubernetes Events

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `hug_events_processed_total` | Counter | `event_type` | K8s resource events processed by type |

### Certificate Storage

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `hug_cert_storage_operations_total` | Counter | `operation`, `status` | Certificate disk write/delete operations |
| `hug_cert_storage_duration_seconds` | Histogram | `operation` | Certificate disk write latency |
| `hug_crtlist_storage_operations_total` | Counter | `operation`, `status` | Crt-list file write/delete operations |
| `hug_cert_runtime_operations_total` | Counter | `operation`, `status` | Runtime API certificate operations (create/update/delete/crtlist_update) |

### Map Storage

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `hug_map_storage_operations_total` | Counter | `operation`, `status` | Map file operations (write/runtime_set/runtime_delete) |

### Label Values

**`operation` labels:**
- Certificate storage: `write`, `delete`
- Certificate runtime: `create`, `update`, `delete`, `crtlist_update`
- Map storage: `write`, `runtime_set`, `runtime_delete`

**`status` labels:** `ok`, `error`

## Deployment

Both example deployments (`example/deploy/hug/` and `example/deploy/hug-dev/`) include:
- A `metrics` container port (`31060`)
- A `metrics` service port (`31060`)

The Service exposes the metrics port so Prometheus can scrape via service discovery.

## Example Prometheus Scrape Config

```yaml
scrape_configs:
  - job_name: 'hug'
    # For kube-rbac mode, use HTTPS + bearer token
    # scheme: https
    # tls_config:
    #   insecure_skip_verify: true
    # bearer_token_file: /var/run/secrets/kubernetes.io/serviceaccount/token

    # For basic auth mode
    # basic_auth:
    #   username: prometheus
    #   password: changeme

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
