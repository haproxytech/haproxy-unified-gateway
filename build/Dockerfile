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

FROM golang:1.27-alpine AS builder-go

RUN apk --no-cache add git openssh

COPY /go.mod /src/go.mod
COPY /go.sum /src/go.sum
RUN cd /src && go mod download

WORKDIR /src
COPY / /src
# stage gopherd into the fs overlay tree
COPY --from=haproxytech/gopherd:latest /usr/local/sbin/gopherd /src/fs/usr/local/sbin/gopherd

RUN CGO_ENABLED=0 go build -buildvcs=true \
        -trimpath \
        -ldflags="-s" \
        -tags="$BUILD_TAGS" \
        -o fs/usr/local/sbin/hug ./cmd/controller

FROM haproxytech/haproxy-alpine:3.2 AS builder-c
RUN apk add --no-cache build-base gcc musl-dev
WORKDIR /src

COPY hug/protection/block_secrets.c .
COPY hug/protection/haproxy_wrapper.c .
RUN gcc -O3 -std=c11 -pipe \
    -Wall -Wextra -Wformat=2 -Wformat-security \
    -Wnull-dereference -Wshadow -Wstrict-prototypes \
    -D_FORTIFY_SOURCE=2 \
    -fstack-protector-strong -fstack-clash-protection \
    -fno-omit-frame-pointer \
    -flto -fPIC -shared -s \
    -Wl,-z,relro -Wl,-z,now -Wl,-z,noexecstack -Wl,--as-needed -Wl,-z,defs \
    -o libblock_secrets.so block_secrets.c -ldl
RUN gcc -O3 -std=c11 -pipe \
    -Wall -Wextra -Wformat=2 -Wformat-security \
    -Wnull-dereference -Wshadow -Wstrict-prototypes \
    -D_FORTIFY_SOURCE=2 \
    -fstack-protector-strong -fstack-clash-protection \
    -fno-omit-frame-pointer \
    -fPIE -pie -s \
    -Wl,-z,relro -Wl,-z,now -Wl,-z,noexecstack -Wl,--as-needed \
    -o haproxy_wrapper haproxy_wrapper.c

# Prune files the gateway never uses; the scratch copy below removes them for real.
FROM haproxytech/haproxy-alpine:3.2 AS haproxy-base
RUN rm -f /usr/local/bin/dataplaneapi \
           /usr/local/etc/haproxy/dataplaneapi.yml \
           /usr/local/etc/haproxy/haproxy.cfg \
           /docker-entrypoint.sh && \
    rm -rf /usr/local/var/lib/dataplaneapi /var/lib/dataplaneapi

# stable pieces only: the scratch copy flattens them into one shared layer
RUN apk --no-cache add socat openssl util-linux htop tzdata curl libcap && \
    mkdir -p /var/run/haproxy && \
    chown -R "haproxy:haproxy" /usr/local/etc/haproxy /run /var && \
    chmod -R ug+rwx /usr/local/etc/haproxy /run /var
COPY --from=builder-c /src/libblock_secrets.so /usr/local/lib/libblock_secrets.so
COPY --from=builder-c /src/haproxy_wrapper /usr/local/sbin/haproxy_wrapper

FROM scratch
COPY --from=haproxy-base / /
ENV PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

# frequently-rebuilt artifacts keep their own layers so pushes stay incremental
COPY --from=builder-go /src/fs/. /
RUN rm /usr/local/hug/defaults.go && \
    mkdir -p /usr/local/hug/aux && \
    chgrp -R haproxy /usr/local/hug && \
    chmod -R ug+rwx /usr/local/hug && \
    chmod 644 /etc/gopherd/gopherd.yml && \
    chmod u+rx /usr/local/sbin/haproxy_wrapper && \
    setcap 'cap_net_bind_service=+ep' /usr/local/sbin/haproxy_wrapper

STOPSIGNAL SIGTERM

ENTRYPOINT ["/usr/local/sbin/gopherd"]
