// Package config loads this service's own settings from environment
// variables — the same "config from env only" rule the main platform
// follows, kept independently here since this module must not import the
// platform's internal/config (docs/PLAN.md 8.1: zero imports from the main
// service's internal/).
package config

import (
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
)

// Config holds everything this emulator needs to talk to the platform and
// to run its own tiny HTTP API.
type Config struct {
	HTTPPort        int           `env:"HTTP_PORT" envDefault:"8090"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"10s"`
	LogLevel        string        `env:"LOG_LEVEL" envDefault:"info"`

	// PlatformBaseURL is where the platform's public and partner APIs live
	// (e.g. http://api:8080) — this is the ONLY thing that ties this
	// service to the platform; everything else goes through the generated
	// client, built from api/openapi/partner.yaml like any third-party
	// integrator would.
	PlatformBaseURL string `env:"PLATFORM_BASE_URL,required"`
	// PartnerAPIKey is this venue's static X-Api-Key, issued out of band by
	// the platform's demo seed (docs/PLAN.md 5.3) — this service was never
	// meant to provision its own key.
	PartnerAPIKey string `env:"PARTNER_API_KEY,required"`
	// SelfBaseURL is where THIS service can be reached from the platform's
	// container (e.g. http://venue-pasta-roma:8090) — registered as the
	// venue's webhookUrl on startup.
	SelfBaseURL string `env:"SELF_BASE_URL,required"`

	// ReadinessPollInterval/ReadinessTimeout govern waiting for the
	// platform's /readyz before this service registers its webhook and
	// menu — Compose's own depends_on only proves the container started,
	// not that migrations+seed have finished landing inside it.
	ReadinessPollInterval time.Duration `env:"READINESS_POLL_INTERVAL" envDefault:"1s"`
	ReadinessTimeout      time.Duration `env:"READINESS_TIMEOUT" envDefault:"60s"`

	// CookStepInterval is how long each step of cooking → ready →
	// delivering → delivered takes. docs/PLAN.md 8.2 does not name a duration;
	// a few seconds per step (rather than realistic minutes) is this
	// service's own choice, made so a demo run shows a complete order
	// lifecycle without a long wait.
	CookStepInterval time.Duration `env:"COOK_STEP_INTERVAL" envDefault:"4s"`
	// StockSyncInterval is how often this service pushes its own stock
	// numbers to the platform, per docs/PLAN.md 8.2 ("раз в 30 секунд").
	StockSyncInterval time.Duration `env:"STOCK_SYNC_INTERVAL" envDefault:"30s"`

	// MenuJSON optionally overrides kitchen.ownMenu()'s built-in default —
	// the same []MenuSyncCategory JSON shape PUT /partner/menu itself
	// accepts (kitchen.BuildMenu). Empty means "use the built-in menu".
	MenuJSON string `env:"MENU_JSON"`
}

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

func (c Config) Validate() error {
	if c.HTTPPort <= 0 || c.HTTPPort > 65535 {
		return fmt.Errorf("invalid HTTP_PORT %d: want 1-65535", c.HTTPPort)
	}
	if c.ShutdownTimeout <= 0 {
		return fmt.Errorf("invalid SHUTDOWN_TIMEOUT %s: must be positive", c.ShutdownTimeout)
	}
	if c.ReadinessPollInterval <= 0 {
		return fmt.Errorf("invalid READINESS_POLL_INTERVAL %s: must be positive", c.ReadinessPollInterval)
	}
	if c.ReadinessTimeout <= 0 {
		return fmt.Errorf("invalid READINESS_TIMEOUT %s: must be positive", c.ReadinessTimeout)
	}
	if c.CookStepInterval <= 0 {
		return fmt.Errorf("invalid COOK_STEP_INTERVAL %s: must be positive", c.CookStepInterval)
	}
	if c.StockSyncInterval <= 0 {
		return fmt.Errorf("invalid STOCK_SYNC_INTERVAL %s: must be positive", c.StockSyncInterval)
	}
	return nil
}
