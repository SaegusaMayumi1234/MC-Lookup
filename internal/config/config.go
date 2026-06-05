package config

import (
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	App struct {
		Name string
		Env  string
	}

	Redis struct {
		Addr     string
		Password string
		DB       int
	}

	Cache struct {
		TTL    int
		Prefix string
	}

	Resolver struct {
		Timeout   int
		UserAgent string `mapstructure:"user_agent"`
	}
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

	return &cfg, nil
}
