# syntax=docker/dockerfile:1
# tund-server image. Client binaries are published separately (GitHub
# Releases); /_tund/downloads/ redirects there.
FROM golang:1.26-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags "-s -w -X tund/internal/server.Version=${VERSION}" -o /out/tund-server ./cmd/tund-server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -H -u 10001 tund \
 && mkdir -p /data/certs && chown -R tund /data
COPY --from=build /out/tund-server /usr/local/bin/tund-server
# With host networking the edge binds :80/:443 as a non-root user: allow
# exactly that (CAP_NET_BIND_SERVICE), nothing else.
RUN apk add --no-cache libcap \
 && setcap cap_net_bind_service=+ep /usr/local/bin/tund-server \
 && apk del libcap
USER tund
ENV TUND_CERT_DIR=/data/certs
EXPOSE 80 443
VOLUME /data
HEALTHCHECK --interval=30s --timeout=5s CMD wget -qO- http://127.0.0.1:4040/internal/health >/dev/null || exit 1
ENTRYPOINT ["tund-server"]
