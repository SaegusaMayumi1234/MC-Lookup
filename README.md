# MC-Lookup

A high-availability, race-optimized Minecraft UUID resolver leveraging multiple third-party endpoints for maximum speed.

## Features

- Single `GET` endpoint resolving players by **username or UUID**
- Multiple upstream resolvers, run concurrently (`race`) or sequentially (`fallback`)
- Redis caching with positive and negative (not-found) TTLs
- Request coalescing via singleflight — concurrent lookups of the same identifier hit upstreams once
- Typed JSON errors with machine-readable codes; CORS enabled
- Docker deployment with bundled or standalone Redis

## API

All requests go through `/api/v1`.

### Look up a player

```
GET /api/v1/player/{identifier}
```

The identifier is either a **username** (3–16 characters, `[a-zA-Z0-9_]`) or a **UUID** (with or without dashes). Lookups are case-insensitive.

```sh
curl http://localhost:8080/api/v1/player/Notch
curl http://localhost:8080/api/v1/player/069a79f444e94726a5befca90e38aaf5
```

Success response (`200`):

```json
{
  "success": true,
  "data": {
    "uuid": "069a79f4-44e9-4726-a5be-fca90e38aaf5",
    "username": "Notch"
  }
}
```

Error responses carry an error object:

```json
{
  "success": false,
  "error": {
    "code": "NOT_FOUND",
    "message": "Player not found"
  }
}
```

### Error codes

| Code | HTTP | Meaning |
|------|------|---------|
| `INVALID_INPUT` | 400 | The identifier is neither a valid username nor a UUID. |
| `NOT_FOUND` | 404 | The configured NOT_FOUND quorum was reached (or every consulted resolver reported not found). |
| `ENDPOINT_NOT_FOUND` | 404 | Unknown path. |
| `METHOD_NOT_ALLOWED` | 405 | Anything other than `GET` on a known path. |
| `RATE_LIMITED` | 429 | Upstream rate limits occurred and the NOT_FOUND quorum was not reached. |
| `UPSTREAM_ERROR` | 502 | Resolver attempts failed in any other way. |
| `TIMEOUT` | 504 | Resolver timeouts occurred, no rate limit took precedence, and the quorum was not reached. |
| `NO_RESOLVERS_AVAILABLE` | 500 | No resolvers configured (misconfiguration). |
| `INTERNAL_SERVER_ERROR` | 500 | Unexpected failure, including recovered panics. |

Quorum precedence: a reached `NOT_FOUND` quorum wins over `RATE_LIMITED`, `TIMEOUT`, and `UPSTREAM_ERROR` — see [Caching](#caching) for the negative-caching consequences.

### API docs (dev only)

With `app.env` set to exactly `dev`, Swagger UI is served at `/` and the OpenAPI spec at `/api/openapi.yaml`.

### CORS

All origins are allowed; methods `GET` and `OPTIONS`. The custom headers `X-Cache-Status`, `X-Resolver`, and `X-Coalesced` are exposed to browsers.

## Response Headers

| Header | Description |
|--------|-------------|
| `Cache-Control` | `public, max-age=<seconds>` with the applicable TTL (positive or negative). Absent when the relevant TTL is `0`. |
| `X-Cache-Status` | `HIT` or `MISS` — whether the response came from cache. Cached not-found (404) responses also return `HIT`. |
| `X-Resolver` | On success: the resolver that sourced the data (present on both `HIT` and `MISS`). On a not-found verdict: the comma-separated resolvers that reported `NOT_FOUND` (e.g. `mojang,playerdb`); cached 404s carry the stored list. |
| `X-Coalesced` | `"true"` if this request was **part of a coalesced group** — concurrent requests for the same identifier are deduplicated via singleflight, and the result is broadcast to the whole group. Both the request that executed the shared lookup (the leader) and any deduplicated followers receive this header; the leader itself hits the upstream, so do not treat the header as a guarantee of deduplication. |

## Resolver Strategy Config

`resolver.strategy` selects the behavior; `resolver.list` is the resolver order for `fallback` and the toggle set for `race`. A resolver not listed for the active strategy is not used.

- `race`: runs all listed resolvers concurrently and returns the first success.
- `fallback`: runs the listed resolvers sequentially until one succeeds.

`resolver.not_found_min_count` controls how many resolvers must report `NOT_FOUND` before the API returns 404 (1 to the number of configured resolvers, default `1`). Reaching the quorum takes precedence over other resolver errors; if every resolver reports `NOT_FOUND`, the API also returns 404.

Supported resolver names: `mojang`, `playerdb`, `ashcon`, `mowojang`.

## Caching

Lookups are cached in Redis under `cache.prefix` (by username and by UUID) for `cache.ttl` seconds. A TTL of `0` (or unset) disables player caching entirely — cache probes are skipped, and successful resolves are not written.

**Negative caching**: when the NOT_FOUND quorum is reached, the 404 verdict itself is cached for `cache.negative_ttl` seconds. Repeat lookups for a nonexistent player then return 404 without contacting any upstream — this protects upstream rate-limit budgets (e.g. Mojang's ~600 requests / 10 min) from floods of invalid identifiers. Two consequences to be aware of:

- A player registered *very* recently may briefly return 404 until the negative entry expires. A successful resolve overwrites the negative entry at the same key immediately — only when positive caching is enabled (`cache.ttl` > 0). With `cache.ttl: 0` no entry is ever written on success, so a freshly registered player keeps returning 404 until the negative entry expires on its own.
- With a low `resolver.not_found_min_count`, a verdict can be reached (and cached) while some resolvers only timed out.

Set `cache.negative_ttl: 0` to disable negative caching. Negative entries share the same Redis keyspace as player entries (a not-found verdict is simply an entry without a player payload) and are probed with a single roundtrip, so they cannot be inspected or purged independently of player entries.

## Timeout Budget

`api.write_timeout` must be able to cover the worst-case resolve time — this is validated at startup:

- `fallback` strategy: `len(resolver.list) × resolver.timeout`
- `race` strategy: `resolver.timeout`

plus a small margin for response serialization. With the default 4 resolvers × 5s timeout, set `api.write_timeout` to at least 22 (the example config ships `30`). A too-small write timeout causes responses to die mid-write: clients see dead connections instead of JSON errors.

`api.idle_timeout` is independent of this budget: it bounds how long a keep-alive connection may sit idle between requests. Keep it below the idle timeout of any load balancer in front of the API so the server closes stale connections first — clients are then never handed a connection the load balancer has already dropped.

## Configuration

Configuration is loaded from `config.yaml` in the working directory (`config.example.yaml` is a template). Every key can also be set via environment variables: upper-case the key path and replace dots with underscores — e.g. `cache.negative_ttl` → `CACHE_NEGATIVE_TTL`. Env overrides only take effect for keys that are present in the file or have a built-in default below.

| Key | Default | Description |
|-----|---------|-------------|
| `app.name` | — | Instance name, included in startup logs. |
| `app.env` | — | Deployment environment. The exact value `dev` enables the Swagger docs endpoints (`/` and `/api/openapi.yaml`); recognized values are `dev` and `prod`, anything else logs a startup warning. |
| `api.port` | — | HTTP listen port (1–65535). |
| `api.read_timeout` | — | Seconds allowed for reading a request (must be > 0). |
| `api.write_timeout` | — | Seconds allowed for writing a response. Must cover the worst-case resolve time — see [Timeout Budget](#timeout-budget). |
| `api.idle_timeout` | `60` | Max seconds a keep-alive connection may sit idle between requests. |
| `redis.addr` | — | Redis `host:port` address; the connection is verified at startup. |
| `redis.password` | `""` | Redis password (optional). |
| `redis.db` | `0` | Redis database index (>= 0). |
| `redis.max_retries` | `-1` | Retry count for Redis commands; `-1` disables retries. |
| `redis.op_timeout` | `1` | Per-operation timeout in seconds. |
| `resolver.timeout` | — | Per-resolver HTTP timeout in seconds (must be > 0). |
| `resolver.user_agent` | — | `User-Agent` header sent to upstreams (must be non-empty). |
| `resolver.strategy` | `fallback` | `race` or `fallback` — see [Resolver Strategy Config](#resolver-strategy-config). |
| `resolver.list` | — | Resolver names — order for `fallback`, toggle set for `race`. |
| `resolver.not_found_min_count` | `1` | NOT_FOUND quorum size (1 to number of resolvers). |
| `cache.prefix` | — | Redis key prefix for player entries (must be non-empty). |
| `cache.ttl` | — | Player entry TTL in seconds; `0` (or unset) disables player caching — see [Caching](#caching). |
| `cache.negative_ttl` | `300` | Not-found verdict TTL in seconds; `0` disables negative caching. |

Unknown keys fail startup with the offending key names (typo protection), invalid values fail startup with a specific error. Advisory issues are logged once at startup as `config warning` lines: `cache.negative_ttl` greater than `cache.ttl`, a NOT_FOUND quorum below half of the resolver count, an unrecognized `app.env`, `resolver.timeout` above 10s, `cache.ttl` below 60s, `redis.max_retries` above 5, or `redis.op_timeout` above 5.

## Docker

The image is a multi-stage build (Go builder → minimal Alpine runtime, runs as a non-root user). `config.example.yaml` is baked in as a fallback; a bind-mounted `config.yaml` wins over it.

**Bundled Redis** (app + Redis with healthcheck and AOF persistence):

```sh
docker compose up --build -d
```

**Existing Redis** — point the app at it with `REDIS_ADDR` (defaults to `host.docker.internal:6379`, i.e. Redis on the Docker host under Docker Desktop):

```sh
REDIS_ADDR=host:port docker compose -f docker-compose.standalone.yml up --build -d
```

Notes:

- **Port**: the published host port is `${APP_PORT:-8080}` (set `APP_PORT=9090 docker compose up -d` to change). The container always listens on 8080 internally (`API_PORT`).
- **TLS**: terminate at your reverse proxy — the app serves plain HTTP; the CA certificates in the image are for the outbound HTTPS calls to the resolver upstreams.

## Development

Requires Go 1.27+ and a reachable Redis (via `config.yaml` or `REDIS_ADDR`).

```sh
go run ./cmd/api
go test ./...
```

On `SIGINT`/`SIGTERM` the server stops accepting connections and waits up to 30 seconds for in-flight resolves to finish before exiting.
