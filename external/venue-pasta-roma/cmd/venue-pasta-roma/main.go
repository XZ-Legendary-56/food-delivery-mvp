// Command venue-pasta-roma emulates a real restaurant's own back-office
// system integrated with the food-delivery-mvp platform (docs/PLAN.md 8). It talks
// to the platform exclusively through a client generated from
// api/openapi/partner.yaml, the same spec any third-party integrator would
// be given — see this module's own go.mod: it is a separate Go module with
// zero dependency on the platform's internal/ packages, so there is no
// shortcut available even by accident.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
	"venue-pasta-roma/internal/bootstrap"
	"venue-pasta-roma/internal/config"
	"venue-pasta-roma/internal/generated/partnerclient"
	"venue-pasta-roma/internal/kitchen"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal startup error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parseLevel(cfg.LogLevel)}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("waiting for platform readiness", "url", cfg.PlatformBaseURL)
	if err := bootstrap.WaitForPlatformReady(ctx, cfg.PlatformBaseURL, cfg.ReadinessPollInterval, cfg.ReadinessTimeout); err != nil {
		return fmt.Errorf("wait for platform: %w", err)
	}

	client, err := partnerclient.NewClientWithResponses(
		strings.TrimSuffix(cfg.PlatformBaseURL, "/")+"/api/v1/partner",
		partnerclient.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
			req.Header.Set("X-Api-Key", cfg.PartnerAPIKey)
			return nil
		}),
	)
	if err != nil {
		return fmt.Errorf("build partner client: %w", err)
	}

	webhookURL := strings.TrimSuffix(cfg.SelfBaseURL, "/") + "/webhooks/orders"
	logger.Info("registering webhook", "url", webhookURL)
	secret, err := bootstrap.RegisterWebhook(ctx, client, webhookURL)
	if err != nil {
		return fmt.Errorf("register webhook: %w", err)
	}

	state := kitchen.NewState()
	menu, err := kitchen.BuildMenu(cfg.MenuJSON)
	if err != nil {
		return fmt.Errorf("build menu: %w", err)
	}
	logger.Info("loading own menu into the platform", "categories", len(menu), "overridden", cfg.MenuJSON != "")
	if err := kitchen.LoadMenu(ctx, client, state, menu); err != nil {
		return fmt.Errorf("load menu: %w", err)
	}

	handler := kitchen.NewHandler(client, state, secret, cfg.CookStepInterval, logger)
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler:           handler.NewRouter(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		kitchen.RunStatusAdvancer(ctx, client, state, cfg.CookStepInterval, logger)
	}()
	go func() {
		defer wg.Done()
		kitchen.RunStockSync(ctx, client, state, cfg.StockSyncInterval, logger)
	}()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server starting", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("listen and serve: %w", err)
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		stop()
		wg.Wait()
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	wg.Wait()

	logger.Info("stopped gracefully")
	return nil
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
