// Package config loads and validates the API service configuration from
// environment variables only, per the project's "config from env, nothing
// else" rule.
package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

// Config holds every setting the API service needs to start.
type Config struct {
	HTTPPort        int           `env:"HTTP_PORT" envDefault:"8080"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"10s"`
	LogLevel        string        `env:"LOG_LEVEL" envDefault:"info"`

	// OutboxPollInterval is how often the background dispatcher checks
	// outbox_events for due work (docs/PLAN.md 6.5/7.4). 5s keeps webhook
	// delivery close to real-time without polling so fast it is
	// indistinguishable from a busy loop.
	OutboxPollInterval time.Duration `env:"OUTBOX_POLL_INTERVAL" envDefault:"5s"`

	DBHost     string `env:"DB_HOST,required"`
	DBPort     int    `env:"DB_PORT" envDefault:"5432"`
	DBUser     string `env:"DB_USER,required"`
	DBPassword string `env:"DB_PASSWORD,required"`
	DBName     string `env:"DB_NAME,required"`
	DBSSLMode  string `env:"DB_SSLMODE" envDefault:"disable"`
}

// Load reads Config from the environment and validates it. A returned error
// means the process must not start: startup validation is the point where we
// catch a bad deployment, not the request that happens to hit the broken path.
func Load() (Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse env: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate config: %w", err)
	}
	return cfg, nil
}

// Validate checks constraints env.Parse cannot express on its own.
func (c Config) Validate() error {
	switch strings.ToLower(c.LogLevel) {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("invalid LOG_LEVEL %q: want debug, info, warn or error", c.LogLevel)
	}
	if c.HTTPPort <= 0 || c.HTTPPort > 65535 {
		return fmt.Errorf("invalid HTTP_PORT %d: want 1-65535", c.HTTPPort)
	}
	if c.ShutdownTimeout <= 0 {
		return fmt.Errorf("invalid SHUTDOWN_TIMEOUT %s: must be positive", c.ShutdownTimeout)
	}
	if c.OutboxPollInterval <= 0 {
		return fmt.Errorf("invalid OUTBOX_POLL_INTERVAL %s: must be positive", c.OutboxPollInterval)
	}
	return nil
}

// PostgresDSN builds a libpq connection string from the individual DB_*
// settings. Kept as separate fields rather than one DATABASE_URL variable so
// Compose and local .env files stay easy to read and diff.
func (c Config) PostgresDSN() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		url.QueryEscape(c.DBUser),
		url.QueryEscape(c.DBPassword),
		c.DBHost,
		c.DBPort,
		c.DBName,
		c.DBSSLMode,
	)
}
