# Copyright 2025 HAProxy Technologies LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#    http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

FROM golang:1.25-alpine AS builder

RUN apk --no-cache add git openssh

COPY /go.mod /src/go.mod
COPY /go.sum /src/go.sum
RUN cd /src && go mod download

# https://github.com/canonical/pebble/issues/719
# RUN go install github.com/canonical/pebble/cmd/pebble@v1.25.0
WORKDIR /src
RUN git clone --branch stop-signal --single-branch https://github.com/haproxytech/pebble.git
RUN cd pebble/cmd/pebble && go build -buildvcs=true -o /go/bin/pebble
COPY / /src

RUN mkdir -p /var/run/vars && \
    cd /src && \
    CGO_ENABLED=0 go build -buildvcs=true \
        -tags="$BUILD_TAGS" \
        -o fs/kubernetes-controller ./cmd/controller

FROM haproxytech/haproxy-alpine:3.2 AS builder-c
RUN apk add --no-cache build-base gcc musl-dev
WORKDIR /src

COPY hug/protection/block_secrets.c .
COPY hug/protection/haproxy_wrapper.c .
RUN gcc -O3 -Wall -flto -fPIC -shared -s -o libblock_secrets.so block_secrets.c -ldl
RUN gcc -O3 -Wall -g -s -o haproxy_wrapper haproxy_wrapper.c

FROM haproxytech/haproxy-alpine:3.2

ARG TARGETPLATFORM

ARG S6_OVERLAY_VERSION=3.1.6.2
ENV S6_OVERLAY_VERSION=$S6_OVERLAY_VERSION
ENV S6_READ_ONLY_ROOT=1
ENV S6_USER=haproxy
ENV S6_GROUP=haproxy

COPY /fs /
COPY --from=builder /go/bin/pebble /usr/local/bin
COPY --from=builder-c /src/libblock_secrets.so /usr/local/lib/libblock_secrets.so
COPY --from=builder-c /src/haproxy_wrapper /usr/local/sbin/haproxy_wrapper

RUN apk --no-cache add socat openssl util-linux htop tzdata curl libcap && \
    rm -f /usr/local/bin/dataplaneapi /usr/bin/dataplaneapi /etc/haproxy/dataplaneapi.yml && \
    rm -f /usr/local/bin/dataplaneapi-v2 /usr/bin/dataplaneapi-v2 && \
    rm -f /etc/haproxy/haproxy.cfg && \
    rm usr/local/hug/defaults.go && \
    mkdir -p /usr/local/hug/aux && \
    mkdir -p /var/run/haproxy && \
    chgrp -R haproxy /usr/local/hug && \
    chmod -R ug+rwx /usr/local/hug && \
    chown -R "haproxy:haproxy" /usr/local/etc/haproxy /run /var && \
    chmod -R ug+rwx /usr/local/etc/haproxy /run /var && \
    chmod u+rx /usr/local/sbin/haproxy_wrapper && \
    setcap 'cap_net_bind_service=+ep' /usr/local/sbin/haproxy_wrapper

COPY --from=builder /src/fs/kubernetes-controller /usr/local/sbin/hug

STOPSIGNAL SIGTERM

ENTRYPOINT ["/start.sh"]
