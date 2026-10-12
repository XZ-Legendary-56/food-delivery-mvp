package catalog_test

import (
	"food-delivery-mvp/internal/domain/catalog"
	"food-delivery-mvp/internal/domain/errs"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRescueOffer_DiscountedPrice_RoundsDown is docs/PLAN.md 5.5's own
// mandated test: the rounding rule, including odd amounts that do not
// divide evenly.
func TestRescueOffer_DiscountedPrice_RoundsDown(t *testing.T) {
	tests := []struct {
		name       string
		priceMinor int64
		discount   int
		want       int64
	}{
		{"even split", 10000, 50, 5000},
		{"odd amount truncates, never rounds up", 999, 33, 669},       // 999*67/100 = 669.33
		{"odd amount truncates, never rounds up 2", 12345, 10, 11110}, // 12345*90/100 = 11110.5
		{"minimum discount", 10000, 1, 9900},
		{"maximum discount", 10000, 90, 1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := catalog.RescueOffer{DiscountPercent: tt.discount}
			assert.Equal(t, tt.want, o.DiscountedPrice(tt.priceMinor))
		})
	}
}

func TestRescueOffer_WindowValid(t *testing.T) {
	now := time.Now()
	base := catalog.RescueOffer{StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour)}

	assert.True(t, base.WindowValid(now), "now falls inside [starts_at, ends_at)")

	beforeStart := base
	beforeStart.StartsAt = now.Add(time.Hour)
	beforeStart.EndsAt = now.Add(2 * time.Hour)
	assert.False(t, beforeStart.WindowValid(now), "window has not started yet")

	afterEnd := base
	afterEnd.StartsAt = now.Add(-2 * time.Hour)
	afterEnd.EndsAt = now.Add(-time.Hour)
	assert.False(t, afterEnd.WindowValid(now), "window already ended")

	atEndsAt := base
	atEndsAt.EndsAt = now
	assert.False(t, atEndsAt.WindowValid(now), "ends_at is exclusive")

	canceled := base
	cancelledAt := now.Add(-time.Minute)
	canceled.CancelledAt = &cancelledAt
	assert.False(t, canceled.WindowValid(now), "a canceled offer is never window-valid, even mid-window")
}

func TestRescueOffer_IsActive_AlsoRequiresRemainingStock(t *testing.T) {
	now := time.Now()
	offer := catalog.RescueOffer{StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour), RemainingQuantity: 0}

	assert.False(t, offer.IsActive(now), "a window-valid offer with nothing left is not active")

	offer.RemainingQuantity = 1
	assert.True(t, offer.IsActive(now))
}

func TestValidateDiscountPercent(t *testing.T) {
	require.NoError(t, catalog.ValidateDiscountPercent(1))
	require.NoError(t, catalog.ValidateDiscountPercent(90))

	for _, p := range []int{0, -1, 91, 100} {
		err := catalog.ValidateDiscountPercent(p)
		require.Errorf(t, err, "percent %d must be rejected", p)
		code, ok := errs.CodeOf(err)
		require.True(t, ok)
		assert.Equal(t, errs.CodeRescueInvalidDiscount, code)
	}
}

func TestValidateRescueWindow(t *testing.T) {
	now := time.Now()

	require.NoError(t, catalog.ValidateRescueWindow(now, now.Add(time.Hour), now))

	err := catalog.ValidateRescueWindow(now, now, now)
	require.Error(t, err, "endsAt equal to startsAt must be rejected")
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeRescueInvalidWindow, code)

	err = catalog.ValidateRescueWindow(now.Add(time.Hour), now, now)
	require.Error(t, err, "endsAt before startsAt must be rejected")

	err = catalog.ValidateRescueWindow(now.Add(-2*time.Hour), now.Add(-time.Hour), now)
	require.Error(t, err, "a window entirely in the past must be rejected")
	code, ok = errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeRescueInvalidWindow, code)
}
