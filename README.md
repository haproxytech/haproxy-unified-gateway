# ![HAProxy](assets/images/haproxy-weblogo-210x49.png "HAProxy")

## HAProxy Unified Gateway for Kubernetes

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

### Description

HUG provides a free open-source solution for cloud-native application routing, featuring unified integration with the latest Kubernetes [Gateway API](https://gateway-api.sigs.k8s.io/) and (coming next year) the existing Ingress API, all built on HAProxy’s legendary performance, reliability, and flexibility – so you can trust it to operate dependably at the largest scale.

### Usage

Docker image is available on [Docker Hub](https://hub.docker.com/r/haproxytech/haproxy-unified-gateway)

If you prefer to build it from source use

```sh
task kind-build-controller-image-build-in-docker
```

Example environment can be created with

```sh
task kind-create
```

or

```sh
task k0s-create
```

## Examples

Examples about deployment can be seen in [example](./example/README.md) folder.

## HAProxy Helm Charts

Helm Chart are available in [haproxytech/haproxy-unified-gateway](https://github.com/haproxytech/haproxy-unified-gateway/) repository

### Contributing

Thanks for your interest in the project and your willing to contribute:

- Pull requests are welcome!
- For commit messages and general style please follow the haproxy project's [CONTRIBUTING guide](https://github.com/haproxy/haproxy/blob/master/CONTRIBUTING) and use that where applicable.
- Please use `task lint` for linting code.

### Discussion

A Github issue is the right place to discuss feature requests, bug reports or any other subject that needs tracking.

To ask questions, get some help or even have a little chat, you can join our #ingress-controller channel in [HAProxy Community Slack](https://slack.haproxy.org).

## License

[Apache License 2.0](LICENSE)
