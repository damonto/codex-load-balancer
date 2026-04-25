FROM golang:1.26-alpine AS builder

WORKDIR /app

ARG BUILD_VERSION=dev

COPY . .

RUN set -eux \
	&& CGO_ENABLED=0 go build -trimpath -ldflags="-w -s -X main.BuildVersion=${BUILD_VERSION}" -o codex-load-balancer .

FROM alpine:3.20 AS runner

WORKDIR /app

COPY --from=builder /app/codex-load-balancer /app/codex-load-balancer
COPY entrypoint.sh /app/entrypoint.sh

RUN set -eux \
	&& apk add --no-cache ca-certificates \
	&& mkdir -p /app/data \
	&& chmod +x /app/entrypoint.sh

ENTRYPOINT ["/app/entrypoint.sh"]
