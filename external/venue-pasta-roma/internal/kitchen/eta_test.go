package kitchen

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEstimateETAMinutes_GrowsWithItemsAndLoad(t *testing.T) {
	small := estimateETAMinutes([]OrderItem{{Quantity: 1}}, 0)
	bigger := estimateETAMinutes([]OrderItem{{Quantity: 5}}, 0)
	busy := estimateETAMinutes([]OrderItem{{Quantity: 1}}, 10)

	assert.Greater(t, bigger, small, "more items on the order must not quote a shorter or equal ETA")
	assert.Greater(t, busy, small, "a busier kitchen must not quote a shorter or equal ETA")
}

func TestEstimateETAMinutes_ClampedToRange(t *testing.T) {
	assert.GreaterOrEqual(t, estimateETAMinutes(nil, 0), 10)
	assert.LessOrEqual(t, estimateETAMinutes([]OrderItem{{Quantity: 1000}}, 1000), 60)
}
