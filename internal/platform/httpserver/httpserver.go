// Package httpserver wraps net/http.Server with the graceful shutdown
// dance so cmd/api/main.go stays a thin wiring file.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Server is an http.Server that stops accepting new connections and waits
// for in-flight requests to finish when its context is canceled.
type Server struct {
	httpServer *http.Server
	logger     *slog.Logger
}

// New builds a Server listening on addr. Timeouts are set on the server
// itself (not per-handler) so a slow or hanging client cannot hold a
// connection open forever.
func New(addr string, handler http.Handler, logger *slog.Logger) *Server {
	return &Server{
		httpServer: &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		},
		logger: logger,
	}
}

// Run serves until ctx is canceled (SIGTERM/SIGINT), then shuts down and
// waits up to shutdownTimeout for in-flight requests to finish.
func (s *Server) Run(ctx context.Context, shutdownTimeout time.Duration) error {
	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("http server starting", "addr", s.httpServer.Addr)
		if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("listen and serve: %w", err)
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	s.logger.Info("http server shutting down")
	// ctx is already Done here — that is what got us to this line — so a
	// timeout derived directly from it would expire immediately, and
	// Shutdown would never get its grace period. context.WithoutCancel
	// keeps ctx's values but drops its own cancellation, which is exactly
	// what a bounded cleanup step after the parent's lifetime has ended
	// needs.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()

	if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	return nil
}
