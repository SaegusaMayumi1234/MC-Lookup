# syntax=docker/dockerfile:1

# ---- Build stage ----
FROM golang:1.27-alpine AS build

WORKDIR /src

# Cache module downloads between builds
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags="-s -w" -o /out/mc-lookup ./cmd/api

# ---- Runtime stage ----
FROM alpine:3.22

# CA certificates are for the OUTBOUND HTTPS calls to the resolver upstreams
RUN apk add --no-cache ca-certificates \
    && addgroup -S app \
    && adduser -S app -G app

WORKDIR /app

COPY --from=build /out/mc-lookup /app/mc-lookup
# Baked-in fallback config; overridden by the volume mount in docker-compose.yml.
# The redis.addr / api.port values are overridden via REDIS_ADDR / API_PORT env vars.
COPY config.example.yaml /app/config.yaml

USER app

EXPOSE 8080

ENTRYPOINT ["/app/mc-lookup"]
