package middleware

import (
	"context"

	"github.com/google/uuid"
)

type venueIDContextKey int

const venueIDKey venueIDContextKey = iota

// WithVenueID stores the venue an authenticated partner API request
// resolved to (docs/PLAN.md 5.3 item 1: "ключ определяет заведение").
func WithVenueID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, venueIDKey, id)
}

// VenueIDFromContext returns the venue set by WithVenueID, if any.
func VenueIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(venueIDKey).(uuid.UUID)
	return id, ok
}
