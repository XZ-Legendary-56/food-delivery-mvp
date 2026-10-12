// Package middleware holds the HTTP middleware shared by every handler
// group (public, partner, and whatever comes later): request id, access
// logging, panic recovery. Anything specific to one API (X-Api-Key,
// X-Client-Id) belongs to that API's own package instead, once it exists.
package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

type contextKey int

const requestIDKey contextKey = iota

// requestIDHeader is echoed back to the caller and is what gets logged, so
// a single request can be traced across a client, this service, and its logs.
const requestIDHeader = "X-Request-Id"

// RequestID assigns a request id (reusing one supplied by the caller, e.g.
// a gateway upstream) and stores it in the request context.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if id == "" {
			id = uuid.NewString()
		}
		w.Header().Set(requestIDHeader, id)

		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDFromContext returns the current request's id, or "" outside of a
// request handled by RequestID.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}
