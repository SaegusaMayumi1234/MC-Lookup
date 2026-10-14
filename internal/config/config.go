package config

import (
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/saegusamayumi1234/mc-lookup/internal/constant"
	"github.com/spf13/viper"
)

type Config struct {
	App      AppConfig      `mapstructure:"app"`
	Api      ApiConfig      `mapstructure:"api"`
	Redis    RedisConfig    `mapstructure:"redis"`
	Cache    CacheConfig    `mapstructure:"cache"`
	Resolver ResolverConfig `mapstructure:"resolver"`
}

type AppConfig struct {
	Name string `mapstructure:"name"`
	Env  string `mapstructure:"env"`
}

type ApiConfig struct {
	Port         int `mapstructure:"port"`
	ReadTimeout  int `mapstructure:"read_timeout"`
	WriteTimeout int `mapstructure:"write_timeout"`
}

type RedisConfig struct {
	Addr       string `mapstructure:"addr"`
	Password   string `mapstructure:"password"`
	DB         int    `mapstructure:"db"`
	MaxRetries int    `mapstructure:"max_retries"`
}

type CacheConfig struct {
	TTL    int    `mapstructure:"ttl"`
	Prefix string `mapstructure:"prefix"`
}

type ResolverConfig struct {
	Timeout          int      `mapstructure:"timeout"`
	UserAgent        string   `mapstructure:"user_agent"`
	Strategy         string   `mapstructure:"strategy"`
	List             []string `mapstructure:"list"`
	NotFoundMinCount int      `mapstructure:"not_found_min_count"`
}

// GetCacheTTL returns cache TTL as time.Duration
func (c *CacheConfig) GetCacheTTL() time.Duration {
	return time.Duration(c.TTL) * time.Second
}

// GetResolverTimeout returns resolver timeout as time.Duration
func (c *ResolverConfig) GetResolverTimeout() time.Duration {
	return time.Duration(c.Timeout) * time.Second
}

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
	if err := validatePositiveSeconds("resolver.timeout", cfg.Resolver.Timeout); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.Resolver.UserAgent) == "" {
		return fmt.Errorf("resolver.user_agent cannot be empty")
	}
	if err := validatePositiveSeconds("cache.ttl", cfg.Cache.TTL); err != nil {
		return err
	}
	if cfg.Cache.Prefix == "" {
		return fmt.Errorf("cache.prefix cannot be empty")
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

func Load() (*Config, error) {
	v := viper.New()

	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.SetDefault("resolver.strategy", constant.StrategyFallback)
	v.SetDefault("resolver.not_found_min_count", 3)

	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}
