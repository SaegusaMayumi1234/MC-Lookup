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
