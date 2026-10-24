FROM golang:1.26-alpine3.23 AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOSUMDB=sum.golang.org
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=secret,id=go_ca \
    if [ -f /run/secrets/go_ca ]; then \
      cat /etc/ssl/certs/ca-certificates.crt /run/secrets/go_ca > /tmp/build-ca.pem; \
      SSL_CERT_FILE=/tmp/build-ca.pem go mod download; \
    else go mod download; fi
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -o /out/ ./cmd/gochainkit ./cmd/gochainkit-wallet ./cmd/gochainkit-devnet

FROM alpine:3.23
RUN addgroup -g 10001 chain && adduser -D -u 10001 -G chain chain \
    && mkdir -p /network /data/node1 /data/node2 /data/node3 \
    && chown -R chain:chain /network /data
COPY --from=build /out/ /usr/local/bin/
USER 10001:10001
WORKDIR /data
EXPOSE 8080 19001
STOPSIGNAL SIGTERM
CMD ["gochainkit", "--config", "/network/node1.yaml"]
