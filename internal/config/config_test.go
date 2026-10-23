package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validConfig() *Config {
	return &Config{
		App:   AppConfig{Name: "mc-lookup", Env: "dev"},
		Api:   ApiConfig{Port: 8080, ReadTimeout: 5, WriteTimeout: 30, IdleTimeout: 60},
		Redis: RedisConfig{Addr: "localhost:6379", Password: "", DB: 0, MaxRetries: -1, OperationTimeout: 1},
		Cache: CacheConfig{TTL: 3600, Prefix: "mc-lookup:", NegativeTTL: 300},
		Resolver: ResolverConfig{
			Timeout:          5,
			UserAgent:        "mc-lookup/1.0",
			Strategy:         "fallback",
			List:             []string{"mojang", "playerdb", "ashcon", "mowojang"},
			NotFoundMinCount: 3,
		},
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string // empty means expect success
	}{
		{name: "valid", mutate: func(*Config) {}},
		{
			name:    "invalid strategy",
			mutate:  func(c *Config) { c.Resolver.Strategy = "bogus" },
			wantErr: "invalid resolver strategy",
		},
		{
			name:    "port too low",
			mutate:  func(c *Config) { c.Api.Port = 0 },
			wantErr: "api.port must be between 1 and 65535",
		},
		{
			name:    "port too high",
			mutate:  func(c *Config) { c.Api.Port = 65536 },
			wantErr: "api.port must be between 1 and 65535",
		},
		{
			name:    "read_timeout zero",
			mutate:  func(c *Config) { c.Api.ReadTimeout = 0 },
			wantErr: "api.read_timeout must be greater than zero",
		},
		{
			name:    "write_timeout zero",
			mutate:  func(c *Config) { c.Api.WriteTimeout = 0 },
			wantErr: "api.write_timeout must be greater than zero",
		},
		{
			name:    "idle_timeout zero",
			mutate:  func(c *Config) { c.Api.IdleTimeout = 0 },
			wantErr: "api.idle_timeout must be greater than zero",
		},
		{
			name:    "resolver timeout zero",
			mutate:  func(c *Config) { c.Resolver.Timeout = 0 },
			wantErr: "resolver.timeout must be greater than zero",
		},
		{
			name:    "empty user agent",
			mutate:  func(c *Config) { c.Resolver.UserAgent = "  " },
			wantErr: "resolver.user_agent cannot be empty",
		},
		{
			name:    "empty cache prefix",
			mutate:  func(c *Config) { c.Cache.Prefix = "" },
			wantErr: "cache.prefix cannot be empty",
		},
		{
			name:    "empty redis addr",
			mutate:  func(c *Config) { c.Redis.Addr = "" },
			wantErr: "redis.addr cannot be empty",
		},
		{
			name:    "redis addr without port",
			mutate:  func(c *Config) { c.Redis.Addr = "localhost" },
			wantErr: "redis.addr must be a host:port address",
		},
		{
			name:    "redis addr invalid port",
			mutate:  func(c *Config) { c.Redis.Addr = "localhost:99999" },
			wantErr: "redis.addr port must be between 1 and 65535",
		},
		{
			name:    "negative redis db",
			mutate:  func(c *Config) { c.Redis.DB = -1 },
			wantErr: "redis.db cannot be negative",
		},
		{
			name:    "max_retries below -1",
			mutate:  func(c *Config) { c.Redis.MaxRetries = -2 },
			wantErr: "redis.max_retries must be -1 or greater",
		},
		{
			name:    "negative redis op_timeout",
			mutate:  func(c *Config) { c.Redis.OperationTimeout = -1 },
			wantErr: "redis.op_timeout must be greater than zero",
		},
		{
			name:    "zero redis op_timeout",
			mutate:  func(c *Config) { c.Redis.OperationTimeout = 0 },
			wantErr: "redis.op_timeout must be greater than zero",
		},
		{
			name:    "empty resolver list",
			mutate:  func(c *Config) { c.Resolver.List = nil },
			wantErr: "resolver list cannot be empty",
		},
		{
			name: "unknown resolver name",
			mutate: func(c *Config) {
				c.Resolver.List = []string{"mojang", "bogus"}
				c.Resolver.NotFoundMinCount = 1
			},
			wantErr: "is not a known resolver",
		},
		{
			name: "duplicate resolver",
			mutate: func(c *Config) {
				c.Resolver.List = []string{"mojang", "mojang"}
				c.Resolver.NotFoundMinCount = 1
			},
			wantErr: "is duplicated",
		},
		{
			name:    "not_found_min_count below 1",
			mutate:  func(c *Config) { c.Resolver.NotFoundMinCount = 0 },
			wantErr: "resolver not_found_min_count must be between 1",
		},
		{
			name:    "not_found_min_count above resolver count",
			mutate:  func(c *Config) { c.Resolver.NotFoundMinCount = 5 },
			wantErr: "resolver not_found_min_count must be between 1",
		},
		{
			name: "write_timeout below fallback budget",
			mutate: func(c *Config) {
				// 4 resolvers × 5s = 20s worst case, +2s margin → needs >= 22
				c.Api.WriteTimeout = 5
			},
			wantErr: "api.write_timeout (5s) cannot cover the worst-case resolve time (4 resolvers × resolver.timeout (5s) = 20s with strategy \"fallback\")",
		},
		{
			name: "write_timeout below race budget",
			mutate: func(c *Config) {
				c.Resolver.Strategy = "race"
				// 1 × 5s worst case, +2s margin → needs >= 7
				c.Api.WriteTimeout = 5
			},
			wantErr: "api.write_timeout (5s) cannot cover the worst-case resolve time (resolver.timeout (5s) with strategy \"race\")",
		},
		{
			name: "cache ttl zero disables player caching",
			mutate: func(c *Config) {
				c.Cache.TTL = 0
			},
		},
		{
			name: "negative cache ttl",
			mutate: func(c *Config) {
				c.Cache.TTL = -1
			},
			wantErr: "cache.ttl cannot be negative",
		},
		{
			name: "negative_ttl zero disables negative caching",
			mutate: func(c *Config) {
				c.Cache.NegativeTTL = 0
			},
		},
		{
			name: "negative negative_ttl",
			mutate: func(c *Config) {
				c.Cache.NegativeTTL = -5
			},
			wantErr: "cache.negative_ttl cannot be negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(cfg)

			err := cfg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got none", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got: %v", tt.wantErr, err)
			}
		})
	}
}

func TestWarnings(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(*Config)
		wantWarning string
	}{
		{
			name: "negative_ttl outlives positive ttl",
			mutate: func(c *Config) {
				c.Cache.NegativeTTL = 4000
			},
			wantWarning: "cache.negative_ttl (4000s) is greater than cache.ttl (3600s)",
		},
		{
			name: "weak not_found quorum",
			mutate: func(c *Config) {
				c.Resolver.NotFoundMinCount = 1
			},
			wantWarning: "resolver.not_found_min_count (1) is below half of the resolver count (4)",
		},
		{
			name: "unrecognized env value",
			mutate: func(c *Config) {
				c.App.Env = "Dev"
			},
			wantWarning: `app.env "Dev" is not a recognized value`,
		},
		{
			name: "high resolver timeout",
			mutate: func(c *Config) {
				c.Resolver.Timeout = 15
				c.Api.WriteTimeout = 60
			},
			wantWarning: "resolver.timeout (15s) is high",
		},
		{
			name: "short cache ttl",
			mutate: func(c *Config) {
				c.Cache.TTL = 30
			},
			wantWarning: "cache.ttl (30s) is short",
		},
		{
			name: "high redis max_retries",
			mutate: func(c *Config) {
				c.Redis.MaxRetries = 10
			},
			wantWarning: "redis.max_retries (10) is high",
		},
		{
			name: "high redis op_timeout",
			mutate: func(c *Config) {
				c.Redis.OperationTimeout = 10
			},
			wantWarning: "redis.op_timeout (10s) is high",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(cfg)

			var found bool
			for _, w := range cfg.Warnings() {
				if strings.Contains(w, tt.wantWarning) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected warning containing %q, got: %v", tt.wantWarning, cfg.Warnings())
			}
		})
	}
}

func TestWarningsNoneOnValidConfig(t *testing.T) {
	cfg := validConfig()
	if warnings := cfg.Warnings(); len(warnings) != 0 {
		t.Fatalf("expected no warnings for valid config, got: %v", warnings)
	}
}

// writeConfigFile writes the given YAML into a temp dir and returns a chdir
// callback, since Load reads "config.yaml" from the working directory.
func writeConfigFile(t *testing.T, content string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
}

const loadFixtureYAML = `
app:
  name: mc-lookup
  env: dev
api:
  port: 8080
  read_timeout: 5
  write_timeout: 30
redis:
  addr: localhost:6379
  max_retries: -1
resolver:
  timeout: 5
  user_agent: "test-agent"
  strategy: "fallback"
  list:
    - mojang
  not_found_min_count: 1
cache:
  ttl: 3600
  prefix: "test:"
`

func TestLoadDefaults(t *testing.T) {
	writeConfigFile(t, loadFixtureYAML)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected config to load, got: %v", err)
	}

	if cfg.Cache.NegativeTTL != 300 {
		t.Errorf("expected cache.negative_ttl to default to 300, got %d", cfg.Cache.NegativeTTL)
	}
	if cfg.Api.IdleTimeout != 60 {
		t.Errorf("expected api.idle_timeout to default to 60, got %d", cfg.Api.IdleTimeout)
	}
	if cfg.Resolver.Strategy != "fallback" {
		t.Errorf("expected resolver.strategy to default to fallback, got %q", cfg.Resolver.Strategy)
	}
}

func TestLoadUnknownKey(t *testing.T) {
	writeConfigFile(t, loadFixtureYAML+`
  prefx: "typo"
`)

	_, err := Load()
	if err == nil {
		t.Fatal("expected unknown key to fail config load")
	}
	if !strings.Contains(err.Error(), "cache.prefx") {
		t.Fatalf("expected error to name the unknown key, got: %v", err)
	}
}

// The removed cache.negative_prefix key is rejected by typo protection:
// deployed configs that still carry it fail startup with the key named.
func TestLoadLegacyNegativePrefixKeyRejected(t *testing.T) {
	writeConfigFile(t, loadFixtureYAML+`
  negative_prefix: "nf:"
`)

	_, err := Load()
	if err == nil {
		t.Fatal("expected the removed cache.negative_prefix key to fail config load")
	}
	if !strings.Contains(err.Error(), "cache.negative_prefix") {
		t.Fatalf("expected error to name the unknown key, got: %v", err)
	}
}

func TestLoadEnvOverride(t *testing.T) {
	writeConfigFile(t, loadFixtureYAML)
	t.Setenv("CACHE_NEGATIVE_TTL", "120")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected config to load, got: %v", err)
	}

	if cfg.Cache.NegativeTTL != 120 {
		t.Errorf("expected cache.negative_ttl overridden to 120 via env, got %d", cfg.Cache.NegativeTTL)
	}
}

func TestLoadEnvOverrideNegativeTTLZero(t *testing.T) {
	writeConfigFile(t, loadFixtureYAML)
	t.Setenv("CACHE_NEGATIVE_TTL", "0")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected config to load, got: %v", err)
	}

	if cfg.Cache.NegativeTTL != 0 {
		t.Errorf("expected cache.negative_ttl overridden to 0 (disabled) via env, got %d", cfg.Cache.NegativeTTL)
	}
}
