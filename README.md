# MC-Lookup
A high-availability, race-optimized Minecraft UUID resolver leveraging multiple third-party endpoints for maximum speed.

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

## Response Headers

Successful lookups return these headers:

- `Cache-Control`: standard HTTP cache directive, e.g. `public, max-age=300`
- `X-Cache-Status`: `HIT` or `MISS` — whether the result was served from cache
- `X-Resolver`: the resolver that sourced the data (present on both HIT and MISS)
- `X-Coalesced`: `"true"` if this request was **part of a coalesced group** — concurrent
  requests for the same identifier are deduplicated via singleflight, and the result is
  broadcast to the whole group. Both the request that executed the shared lookup (the
  leader) and any deduplicated followers receive this header; the leader itself hits the
  upstream, so do not treat the header as a guarantee of deduplication.
