// Command seed inserts demo data (partners, venues, menus, API keys) used for
// manually exercising the API with curl. It is separate from cmd/migrate:
// schema changes must be reproducible on every environment including
// production, demo data must not be, so the two are never allowed to mix in
// one file.
package main

import (
	"context"
	_ "embed"
	"fmt"
	"food-delivery-mvp/internal/config"
	"food-delivery-mvp/internal/platform/logger"
	"food-delivery-mvp/internal/platform/pgpool"
	"log/slog"
	"os"
)

//go:embed seed.sql
var seedSQL string

func main() {
	if err := run(); err != nil {
		slog.Error("seed failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log := logger.New(cfg.LogLevel)

	ctx := context.Background()
	pool, err := pgpool.New(ctx, cfg.PostgresDSN())
	if err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, seedSQL); err != nil {
		return fmt.Errorf("exec seed script: %w", err)
	}

	log.Info("seed data applied",
		"demo_api_key_pizza_mania", "demo_pizza_mania_2026",
		"demo_api_key_tokyo_wok", "demo_tokyo_wok_2026",
		"demo_api_key_burger_house", "demo_burger_house_2026",
		"demo_api_key_pasta_roma", "demo_pasta_roma_2026",
	)
	return nil
}
