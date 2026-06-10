package config

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/saegusamayumi1234/mc-lookup/internal/constant"
	"github.com/spf13/viper"
)

type Config struct {
	App      AppConfig      `mapstructure:"app"`
	Api	  	 ApiConfig      `mapstructure:"api"`
	Redis    RedisConfig    `mapstructure:"redis"`
	Cache    CacheConfig    `mapstructure:"cache"`
	Resolver ResolverConfig `mapstructure:"resolver"`
}

type AppConfig struct {
	Name string `mapstructure:"name"`
	Env  string `mapstructure:"env"`
}

type ApiConfig struct {
	Port 	     int `mapstructure:"port"`
	ReadTimeout  int `mapstructure:"read_timeout"`
	WriteTimeout int `mapstructure:"write_timeout"`
}

type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

type CacheConfig struct {
	TTL    int    `mapstructure:"ttl"`
	Prefix string `mapstructure:"prefix"`
}

type ResolverConfig struct {
	Timeout               int      `mapstructure:"timeout"`
	UserAgent             string   `mapstructure:"user_agent"`
	Strategy              string   `mapstructure:"strategy"`
	FallbackResolverOrder []string `mapstructure:"fallback_resolver_order"`
	RaceResolver          []string `mapstructure:"race_resolver"`
}

// GetCacheTTL returns cache TTL as time.Duration
func (c *CacheConfig) GetCacheTTL() time.Duration {
	return time.Duration(c.TTL) * time.Second
}

// GetResolverTimeout returns resolver timeout as time.Duration
func (c *ResolverConfig) GetResolverTimeout() time.Duration {
	return time.Duration(c.Timeout) * time.Second
}

func (c *ResolverConfig) GetSelectedResolverNames() (string, []string) {
	switch c.Strategy {
	case constant.StrategyFallback:
		return "fallback_resolver_order", c.FallbackResolverOrder
	case constant.StrategyRace:
		return "race_resolver", c.RaceResolver
	default:
		return "", nil
	}
}

func (cfg *Config) Validate() error {
	if !slices.Contains(constant.KnownStrategies(), cfg.Resolver.Strategy) {
		return fmt.Errorf("invalid resolver strategy '%s': must be one of %v", cfg.Resolver.Strategy, constant.KnownStrategies())
	}

	selectedField, selectedResolvers := cfg.Resolver.GetSelectedResolverNames()
	if selectedField == "" {
		return fmt.Errorf("resolver strategy '%s' is not configured properly", cfg.Resolver.Strategy)
	}
	if len(selectedResolvers) == 0 {
		return fmt.Errorf("%s cannot be empty when strategy is '%s'", selectedField, cfg.Resolver.Strategy)
	}

	seen := make(map[string]struct{}, len(selectedResolvers))
	for _, item := range selectedResolvers {
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

func Load() (*Config, error) {
	v := viper.New()

	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")

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
