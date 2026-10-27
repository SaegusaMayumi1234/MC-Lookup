package config

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"

	"github.com/saegusamayumi1234/mc-lookup/internal/constant"
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
	IdleTimeout  int `mapstructure:"idle_timeout"`
}

type RedisConfig struct {
	Addr             string `mapstructure:"addr"`
	Password         string `mapstructure:"password"`
	DB               int    `mapstructure:"db"`
	MaxRetries       int    `mapstructure:"max_retries"`
	OperationTimeout int    `mapstructure:"op_timeout"`
}

type CacheConfig struct {
	Prefix      string `mapstructure:"prefix"`
	TTL         int    `mapstructure:"ttl"`
	NegativeTTL int    `mapstructure:"negative_ttl"`
}

type ResolverConfig struct {
	Timeout          int      `mapstructure:"timeout"`
	UserAgent        string   `mapstructure:"user_agent"`
	Strategy         string   `mapstructure:"strategy"`
	List             []string `mapstructure:"list"`
	NotFoundMinCount int      `mapstructure:"not_found_min_count"`
}

func (c *CacheConfig) GetCacheTTL() time.Duration {
	return time.Duration(c.TTL) * time.Second
}

func (c *CacheConfig) GetNegativeTTL() time.Duration {
	return time.Duration(c.NegativeTTL) * time.Second
}

func (c *ResolverConfig) GetResolverTimeout() time.Duration {
	return time.Duration(c.Timeout) * time.Second
}

func (c *RedisConfig) GetOperationTimeout() time.Duration {
	return time.Duration(c.OperationTimeout) * time.Second
}

func Load() (*Config, error) {
	v := viper.New()

	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.SetDefault("api.idle_timeout", 60)
	v.SetDefault("redis.op_timeout", 1)
	v.SetDefault("resolver.strategy", constant.StrategyFallback)
	v.SetDefault("resolver.not_found_min_count", 1)
	v.SetDefault("cache.negative_ttl", 300)

	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}

	var cfg Config
	if err := decodeSettings(v, &cfg); err != nil {
		return nil, err
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// decodeSettings decodes viper settings into cfg, mirroring viper.Unmarshal's
// decoder configuration (weak typing for env overrides, list splitting) while
// additionally rejecting keys that do not map to any config field. A typo'd key
// such as `resolver.stratgy` would otherwise silently fall back to defaults.
func decodeSettings(v *viper.Viper, cfg *Config) error {
	var meta mapstructure.Metadata
	dec, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Metadata:         &meta,
		Result:           cfg,
		WeaklyTypedInput: true,
		DecodeHook: mapstructure.ComposeDecodeHookFunc(
			mapstructure.StringToTimeDurationHookFunc(),
			mapstructure.StringToSliceHookFunc(","),
		),
		TagName: "mapstructure",
	})
	if err != nil {
		return fmt.Errorf("config decoder: %w", err)
	}

	if err := dec.Decode(v.AllSettings()); err != nil {
		return err
	}

	unused := meta.Unused
	if len(unused) > 0 {
		slices.Sort(unused)
		return fmt.Errorf("unknown config keys: %s (check for typos)", strings.Join(unused, ", "))
	}

	return nil
}
