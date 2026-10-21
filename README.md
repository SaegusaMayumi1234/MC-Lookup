# MC-Lookup
A high-availability, race-optimized Minecraft UUID resolver leveraging multiple third-party endpoints for maximum speed.

## Configuration

Configuration is loaded from `config.yaml` in the working directory (`config.example.yaml` is a template). Every key can also be set via environment variables: upper-case the key path and replace dots with underscores — e.g. `cache.negative_ttl` → `CACHE_NEGATIVE_TTL`. Env overrides only take effect for keys that are present in the file or have a built-in default below.

| Key | Default | Description |
|-----|---------|-------------|
| `app.name` | — | Instance name, included in startup logs. |
| `app.env` | — | Deployment environment. The exact value `dev` enables the Swagger docs endpoints (`/` and `/api/openapi.yaml`); recognized values are `dev` and `prod`, anything else logs a startup warning. |
| `api.port` | — | HTTP listen port (1–65535). |
| `api.read_timeout` | — | Seconds allowed for reading a request (must be > 0). |
| `api.write_timeout` | — | Seconds allowed for writing a response. Must cover the worst-case resolve time — see [Timeout Budget](#timeout-budget). |
| `api.idle_timeout` | `60` | Max seconds a keep-alive connection may sit idle between requests. Keep it below your load balancer's idle timeout so the server closes stale connections first. |
| `redis.addr` | — | Redis `host:port` address; the connection is verified at startup. |
| `redis.password` | `""` | Redis password (optional). |
| `redis.db` | `0` | Redis database index (>= 0). |
| `redis.max_retries` | `-1` | Retry count for Redis commands; `-1` disables retries. |
| `resolver.timeout` | — | Per-resolver HTTP timeout in seconds (must be > 0). |
| `resolver.user_agent` | — | `User-Agent` header sent to upstreams (must be non-empty). |
| `resolver.strategy` | `fallback` | `race` or `fallback` — see [Resolver Strategy Config](#resolver-strategy-config). |
| `resolver.list` | — | Resolver names — order for `fallback`, toggle set for `race`. Known names: `mojang`, `playerdb`, `ashcon`, `mowojang`; duplicates rejected. |
| `resolver.not_found_min_count` | `3` | NOT_FOUND quorum size (1 to number of resolvers) — see [Resolver Strategy Config](#resolver-strategy-config). |
| `cache.ttl` | — | Player entry TTL in seconds (must be > 0) — see [Caching](#caching). |
| `cache.prefix` | — | Redis key prefix for player entries (must be non-empty). |
| `cache.negative_enabled` | `true` | Cache conclusive not-found verdicts — see [Caching](#caching). |
| `cache.negative_ttl` | `300` | Not-found verdict TTL in seconds (must be >= 1 when negative caching is enabled). |
| `cache.negative_prefix` | `"nf:"` | Key namespace appended to `cache.prefix` for negative entries (must be non-empty). |

Unknown keys fail startup with the offending key names (typo protection), invalid values fail startup with a specific error, and advisory issues are logged once at startup as `config warning` lines: `cache.negative_ttl` greater than `cache.ttl`, a NOT_FOUND quorum below half of the resolver count, an unrecognized `app.env`, `resolver.timeout` above 10s, `cache.ttl` below 60s, or `redis.max_retries` above 5.

## Resolver Strategy Config

The resolver behavior is configurable from `resolver.strategy`:

- `race`: runs resolvers in `resolver.list` concurrently and returns the first success.
- `fallback`: runs resolvers in `resolver.list` sequentially until one succeeds.

Both lists also act as resolver toggles. If a resolver name is not listed for the active strategy, it will not be used.

`resolver.not_found_min_count` controls how many resolvers must report `NOT_FOUND` before the API returns 404. It defaults to `3`; reaching this quorum takes precedence over other resolver errors. The value must be between 1 and the number of configured resolvers. If every resolver reports `NOT_FOUND`, the API returns 404.

Supported resolver names:

- `mojang`
- `playerdb`
- `ashcon`
- `mowojang`

Example:

```yaml
resolver:
  timeout: 5
  user_agent: "mc-lookup/1.0"
  strategy: "fallback"
  not_found_min_count: 3
  list:
    - mojang
    - ashcon
    - playerdb
```

## Caching

Lookups are cached in Redis under `cache.prefix` (by username and by UUID) for `cache.ttl` seconds.

**Negative caching**: when the NOT_FOUND quorum is reached, the 404 verdict itself is cached for `cache.negative_ttl` seconds (`cache.negative_enabled: true` by default). Repeat lookups for a nonexistent player then return 404 without contacting any upstream — this protects upstream rate-limit budgets (e.g. Mojang's ~600 requests / 10 min) from floods of invalid identifiers. Two consequences to be aware of:

- A player registered *very* recently may briefly return 404 until the negative entry expires.
- With a low `resolver.not_found_min_count`, a verdict can be reached (and cached) while some resolvers only timed out.

Set `cache.negative_enabled: false` to disable.

Negative entries live under their own key namespace, `cache.negative_prefix` (default `nf:`, keys look like `mc-lookup:nf:<identifier>`), so they can be inspected or purged independently of player entries.

## Timeout Budget

`api.write_timeout` must be able to cover the worst-case resolve time — this is validated at startup:

- `fallback` strategy: `len(resolver.list) × resolver.timeout`
- `race` strategy: `resolver.timeout`

plus a small margin for response serialization. With the default 4 resolvers × 5s timeout, set `api.write_timeout` to at least 22 (the example config ships `30`). A too-small write timeout causes responses to die mid-write: clients see dead connections instead of JSON errors.

`api.idle_timeout` is independent of this budget: it bounds how long a keep-alive connection may sit idle between requests. Without it, the server silently reuses `api.read_timeout` for that purpose, so the connection lifecycle would accidentally follow the read timeout. Keep it below the idle timeout of any load balancer in front of the API so the server closes stale connections first — clients are then never handed a connection the load balancer has already dropped.

## Response Headers

Successful lookups return these headers:

- `Cache-Control`: standard HTTP cache directive, e.g. `public, max-age=300`
- `X-Cache-Status`: `HIT` or `MISS` — whether the result was served from cache. Cached not-found (404) responses also return `HIT` together with a `Cache-Control` header
- `X-Resolver`: the resolver that sourced the data (present on both HIT and MISS; absent on cached 404 responses)
- `X-Coalesced`: `"true"` if this request was **part of a coalesced group** — concurrent
  requests for the same identifier are deduplicated via singleflight, and the result is
  broadcast to the whole group. Both the request that executed the shared lookup (the
  leader) and any deduplicated followers receive this header; the leader itself hits the
  upstream, so do not treat the header as a guarantee of deduplication.
