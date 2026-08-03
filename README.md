# MC-Lookup
A high-availability, race-optimized Minecraft UUID resolver leveraging multiple third-party endpoints for maximum speed.

## Resolver Strategy Config

The resolver behavior is configurable from `resolver.strategy`:

- `race`: runs resolvers in `resolver.list` concurrently and returns the first success.
- `fallback`: runs resolvers in `resolver.list` sequentially until one succeeds.

Both lists also act as resolver toggles. If a resolver name is not listed for the active strategy, it will not be used.

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
	list:
		- mojang
		- ashcon
		- playerdb
```
