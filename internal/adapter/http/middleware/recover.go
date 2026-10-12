package middleware

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
)

// panicResponse mirrors the shared Error schema from api/openapi. It is
// intentionally not the same Go type as the future domain-error mapper in
// adapter/http/httperr: a panic is not a domain error, there is nothing to
// map, so building the body here does not violate "errors become HTTP
// responses in one place" — that rule is about *known* business errors.
type panicResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId"`
}

// Recover turns a panic in a downstream handler into a logged 500 instead
// of a crashed process. This is the project's only tolerated brush with
// panic/recover — everything else must return an error.
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func(ctx context.Context) {
				if rec := recover(); rec != nil {
					requestID := RequestIDFromContext(ctx)
					logger.ErrorContext(ctx, "panic recovered",
						"error", rec,
						"request_id", requestID,
						"path", r.URL.Path,
					)

					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_ = json.NewEncoder(w).Encode(panicResponse{
						Code:      "INTERNAL_ERROR",
						Message:   "internal server error",
						RequestID: requestID,
					})
				}
			}(r.Context())
			next.ServeHTTP(w, r)
		})
	}
}
