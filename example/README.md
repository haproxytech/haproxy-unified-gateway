## HUG

## HTTP Echo docker image

### Building the `http-echo` Docker Image

This example requires the **http-echo** Docker image, which is used as a simple backend application for testing TLS passthrough and HTTP routing.

The source code for the image is located in the HAProxy Unified Gateway repository:

[https://github.com/haproxytech/haproxy-unified-gateway](https://github.com/haproxytech/haproxy-unified-gateway)

#### Steps to build the image

1. Clone the repository (or navigate to the folder if you already have it):

```bash
git clone --depth 1 https://github.com/haproxytech/haproxy-unified-gateway
cd haproxy-unified-gateway/ci/http-echo
```

2. Build the Docker image

```sh
docker build -t http-echo:latest .
```

3. Make the image available to your Kubernetes cluster

* Option 1: Push to a Docker registry accessible from the cluster:

```bash
docker tag http-echo:latest <your-registry>/http-echo:latest
docker push <your-registry>/http-echo:latest
```

* Option 2: Load the image into a Kind cluster (if you are using Kind)

```sh
kind load docker-image http-echo:latest --name <kind-cluster-name>
```

After the image is available in your cluster, you can apply the example manifests, and the TLSRoute/HTTPRoute examples will use this image as the backend.

### Gateway API

* [Gateway API](./deploy/gateway-api/README.md): Gateway API resource.

### Deployment

* [HAProxy Unified Gateway](./deploy/hug/): How to deploy the HAProxy Unified Gateway controller.
* [Echo Server](./deploy/echo/): How to deploy an echo server.

### HTTPRoute Examples

* [Hello World](./http-route/hello-world/): A simple example of exposing a service with an HTTPRoute.
* [Blue-Green Deployment](./http-route/blue-green/): An example of traffic splitting between two versions of a service.

### Ingress Examples

Kubernetes `Ingress` support is optional and disabled by default: enable it by
starting the controller with `--enable-ingress`. See
[documentation/ingress.md](../documentation/ingress.md) for how an Ingress is
translated onto the Gateway API pipeline, the controller options and the
restrictions compared to the HAProxy Ingress Controller — configuration is
carried by the Backend CR rather than by annotations.

* [Hello World](./ingress/hello-world/): An Ingress with `Prefix` and `Exact` paths.
* [TLS Termination](./ingress/tls/): An Ingress with `spec.tls`, serving HTTPS from a Secret.
