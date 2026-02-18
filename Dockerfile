FROM golang:1.26-alpine AS builder

WORKDIR /app

ARG VERSION

COPY . .

RUN set -eux \
	&& CGO_ENABLED=0 go build -trimpath -ldflags="-w -s -X main.BuildVersion=${VERSION}" -o codex-load-balancer .

FROM alpine:3.20 AS runner

WORKDIR /app

COPY --from=builder /app/codex-load-balancer /app/codex-load-balancer

RUN set -eux \
	&& apk add --no-cache libcurl

COPY entrypoint.sh /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
