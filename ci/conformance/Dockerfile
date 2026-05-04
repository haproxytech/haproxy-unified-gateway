ARG GO_VERSION=1.23
FROM golang:${GO_VERSION}-alpine AS builder
WORKDIR /workspace
COPY . .
RUN go install gotest.tools/gotestsum@v1.13.0 && \
    go test -c -tags=conformance ./test/conformance/ -o /conformance.test

FROM golang:${GO_VERSION}-alpine
COPY --from=builder /conformance.test /conformance.test
COPY --from=builder /go/bin/gotestsum /gotestsum
COPY ci/conformance/run.sh /run.sh
RUN chmod +x /run.sh
ENTRYPOINT ["/run.sh"]
