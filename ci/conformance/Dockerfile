ARG GO_VERSION=1.27
FROM golang:${GO_VERSION}-alpine AS builder
WORKDIR /workspace
COPY . .
# test2json ships only as a Go source tool, so build it here; then the runtime
# image needs no toolchain.
RUN GO111MODULE=off go build -o /test2json "$(go env GOROOT)/src/cmd/test2json"
RUN go install gotest.tools/gotestsum@v1.13.0 && \
    go test -c -tags=conformance ./test/conformance/ -o /conformance.test

FROM alpine:3
COPY --from=builder /conformance.test /conformance.test
COPY --from=builder /test2json /test2json
COPY --from=builder /go/bin/gotestsum /gotestsum
COPY ci/conformance/run.sh /run.sh
RUN chmod +x /run.sh
ENTRYPOINT ["/run.sh"]
