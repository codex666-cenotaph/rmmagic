FROM golang:1.25-alpine AS builder

WORKDIR /build

# Copy workspace and modules first for layer caching
COPY go.work go.work.sum* ./
COPY shared/ ./shared/
COPY server/ ./server/
COPY agent/ ./agent/

RUN CGO_ENABLED=0 go build -o /app/rmmserver ./server/cmd/rmmserver

# Install golang-migrate for the entrypoint
RUN apk add --no-cache curl && \
    ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/') && \
    curl -fsSL "https://github.com/golang-migrate/migrate/releases/download/v4.18.3/migrate.linux-${ARCH}.tar.gz" | tar -xz -C /usr/local/bin migrate

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 1000 rmm

COPY --from=builder /app/rmmserver /app/rmmserver
COPY --from=builder /usr/local/bin/migrate /usr/local/bin/migrate
COPY server/migrations/ /app/migrations/
COPY server/docker-entrypoint.sh /app/docker-entrypoint.sh

RUN chmod +x /app/docker-entrypoint.sh && \
    chown -R rmm:rmm /app

USER rmm
EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=3s --start-period=15s \
  CMD wget -qO- http://localhost:8080/healthz || exit 1

ENTRYPOINT ["/app/docker-entrypoint.sh"]
CMD ["--roles=api,gateway,worker"]
