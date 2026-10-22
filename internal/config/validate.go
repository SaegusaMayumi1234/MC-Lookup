package config

import (
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/saegusamayumi1234/mc-lookup/internal/constant"
)

// resolveBudgetMarginSeconds is headroom on top of the worst-case resolve time
// for response serialization and handler overhead.
const resolveBudgetMarginSeconds = 2

// knownEnvValues lists the recognized app.env values. Only the exact value "dev"
// enables the Swagger docs endpoints (see api/router.go).
var knownEnvValues = []string{"dev", "prod"}

func (cfg *Config) Validate() error {
	cfg.Resolver.Strategy = strings.ToLower(strings.TrimSpace(cfg.Resolver.Strategy))
	if cfg.Resolver.Strategy == "" {
		cfg.Resolver.Strategy = constant.StrategyFallback
	}
	cfg.Redis.Addr = strings.TrimSpace(cfg.Redis.Addr)
	cfg.Cache.Prefix = strings.TrimSpace(cfg.Cache.Prefix)
	cfg.Resolver.UserAgent = strings.TrimSpace(cfg.Resolver.UserAgent)

	if !slices.Contains(constant.KnownStrategies(), cfg.Resolver.Strategy) {
		return fmt.Errorf("invalid resolver strategy '%s': must be one of %v", cfg.Resolver.Strategy, constant.KnownStrategies())
	}
	if cfg.Api.Port < 1 || cfg.Api.Port > 65535 {
		return fmt.Errorf("api.port must be between 1 and 65535")
	}
	if err := validatePositiveSeconds("api.read_timeout", cfg.Api.ReadTimeout); err != nil {
		return err
	}
	if err := validatePositiveSeconds("api.write_timeout", cfg.Api.WriteTimeout); err != nil {
		return err
	}
	if err := validatePositiveSeconds("api.idle_timeout", cfg.Api.IdleTimeout); err != nil {
		return err
	}
	if err := validatePositiveSeconds("resolver.timeout", cfg.Resolver.Timeout); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.Resolver.UserAgent) == "" {
		return fmt.Errorf("resolver.user_agent cannot be empty")
	}
	if cfg.Cache.Prefix == "" {
		return fmt.Errorf("cache.prefix cannot be empty")
	}
	if err := validateNonNegativeSeconds("cache.ttl", cfg.Cache.TTL); err != nil {
		return err
	}
	if err := validateNonNegativeSeconds("cache.negative_ttl", cfg.Cache.NegativeTTL); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.Redis.Addr) == "" {
		return fmt.Errorf("redis.addr cannot be empty")
	}
	if err := validateRedisAddress(cfg.Redis.Addr); err != nil {
		return err
	}
	if cfg.Redis.DB < 0 {
		return fmt.Errorf("redis.db cannot be negative")
	}
	if cfg.Redis.MaxRetries < -1 {
		return fmt.Errorf("redis.max_retries must be -1 or greater")
	}

	if len(cfg.Resolver.List) == 0 {
		return fmt.Errorf("resolver list cannot be empty")
	}
	if cfg.Resolver.NotFoundMinCount < 1 || cfg.Resolver.NotFoundMinCount > len(cfg.Resolver.List) {
		return fmt.Errorf("resolver not_found_min_count must be between 1 and the number of configured resolvers (%d)", len(cfg.Resolver.List))
	}

	seen := make(map[string]struct{}, len(cfg.Resolver.List))
	for _, item := range cfg.Resolver.List {
		if !slices.Contains(constant.KnownResolverNames(), item) {
			return fmt.Errorf("resolver '%s' is not a known resolver", item)
		}
		if _, duplicate := seen[item]; duplicate {
			return fmt.Errorf("resolver '%s' is duplicated", item)
		}
		seen[item] = struct{}{}
	}

	// The write timeout must be able to cover the worst-case resolve time:
	// a response that outlives it dies mid-write and the client sees a dead
	// connection instead of a JSON error.
	worstCase := cfg.worstCaseResolveSeconds()
	minWriteTimeout := worstCase + resolveBudgetMarginSeconds
	if int64(cfg.Api.WriteTimeout) < minWriteTimeout {
		return fmt.Errorf(
			"api.write_timeout (%ds) cannot cover the worst-case resolve time (%s); set api.write_timeout to at least %d or lower resolver.timeout",
			cfg.Api.WriteTimeout, cfg.resolveBudgetDetail(), minWriteTimeout,
		)
	}

	return nil
}

// Warnings returns non-fatal advisory messages about the configuration.
// They are logged once at startup by the caller.
func (cfg *Config) Warnings() []string {
	var warnings []string
	add := func(format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	}

	if cfg.Cache.TTL > 0 && cfg.Cache.NegativeTTL > cfg.Cache.TTL {
		add("cache.negative_ttl (%ds) is greater than cache.ttl (%ds); not-found verdicts will outlive positive entries",
			cfg.Cache.NegativeTTL, cfg.Cache.TTL)
	}
	if cfg.Resolver.NotFoundMinCount*2 < len(cfg.Resolver.List) {
		add("resolver.not_found_min_count (%d) is below half of the resolver count (%d); a weak quorum lets a not-found verdict be reached and cached while several resolvers timed out",
			cfg.Resolver.NotFoundMinCount, len(cfg.Resolver.List))
	}
	if !slices.Contains(knownEnvValues, cfg.App.Env) {
		add("app.env %q is not a recognized value %v; only the exact value %q enables the Swagger docs endpoints",
			cfg.App.Env, knownEnvValues, "dev")
	}
	if cfg.Resolver.Timeout > 10 {
		add("resolver.timeout (%ds) is high; with the fallback strategy every failing resolver can add up to this to the response time",
			cfg.Resolver.Timeout)
	}
	if cfg.Cache.TTL > 0 && cfg.Cache.TTL < 60 {
		add("cache.ttl (%ds) is short; entries will churn quickly and increase upstream traffic",
			cfg.Cache.TTL)
	}
	if cfg.Redis.MaxRetries > 5 {
		add("redis.max_retries (%d) is high; requests may block for a long time during a Redis outage",
			cfg.Redis.MaxRetries)
	}

	return warnings
}

// worstCaseResolveSeconds returns the worst-case wall time a resolve can take:
// the fallback strategy tries resolvers sequentially (N × timeout), while the
// race strategy waits at most one resolver timeout.
func (cfg *Config) worstCaseResolveSeconds() int64 {
	if cfg.Resolver.Strategy == constant.StrategyFallback {
		return int64(len(cfg.Resolver.List)) * int64(cfg.Resolver.Timeout)
	}
	return int64(cfg.Resolver.Timeout)
}

func (cfg *Config) resolveBudgetDetail() string {
	if cfg.Resolver.Strategy == constant.StrategyFallback {
		return fmt.Sprintf("%d resolvers × resolver.timeout (%ds) = %ds with strategy %q",
			len(cfg.Resolver.List), cfg.Resolver.Timeout, cfg.worstCaseResolveSeconds(), cfg.Resolver.Strategy)
	}
	return fmt.Sprintf("resolver.timeout (%ds) with strategy %q", cfg.Resolver.Timeout, cfg.Resolver.Strategy)
}

// validateNonNegativeSeconds allows 0 (meaning the feature is disabled) but
// rejects negative values.
func validateNonNegativeSeconds(name string, seconds int) error {
	if seconds < 0 {
		return fmt.Errorf("%s cannot be negative", name)
	}
	const maxDurationSeconds = int64((1<<63 - 1) / int64(time.Second))
	if int64(seconds) > maxDurationSeconds {
		return fmt.Errorf("%s is too large", name)
	}
	return nil
}

func validatePositiveSeconds(name string, seconds int) error {
	if seconds <= 0 {
		return fmt.Errorf("%s must be greater than zero", name)
	}
	const maxDurationSeconds = int64((1<<63 - 1) / int64(time.Second))
	if int64(seconds) > maxDurationSeconds {
		return fmt.Errorf("%s is too large", name)
	}
	return nil
}

func validateRedisAddress(addr string) error {
	host, port, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil || host == "" {
		return fmt.Errorf("redis.addr must be a host:port address")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("redis.addr port must be between 1 and 65535")
	}
	return nil
}
