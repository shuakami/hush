# --- builder ----------------------------------------------------------------
FROM golang:1.23-alpine AS builder
WORKDIR /src

# Cache deps separately for fast incremental rebuilds.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=0.1.0
RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags "-s -w -X main.Version=${VERSION}" \
    -o /out/hush ./cmd/hush

# --- runtime ---------------------------------------------------------------
FROM alpine:3.20
RUN apk add --no-cache ca-certificates curl bash openssh-client \
 && adduser -D -u 10001 hush \
 && mkdir -p /var/lib/hush \
 && chown -R hush:hush /var/lib/hush

USER hush
WORKDIR /var/lib/hush

ENV HUSH_LISTEN=0.0.0.0:8443 \
    HUSH_DATA_DIR=/var/lib/hush \
    HUSH_KEK_KIND=file \
    HUSH_KEK_FILE=/var/lib/hush/kek

EXPOSE 8443
COPY --from=builder /out/hush /usr/local/bin/hush

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
  CMD wget -qO- http://127.0.0.1:8443/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/hush"]
CMD ["server"]
