package catalog_test

import (
	"food-delivery-mvp/internal/domain/catalog"
	"food-delivery-mvp/internal/domain/errs"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func intPtr(v int) *int { return &v }

func TestMenuItem_EnsureAvailable_Unavailable(t *testing.T) {
	item := catalog.MenuItem{Name: "Margherita", IsAvailable: false}

	err := item.EnsureAvailable(1)

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeItemUnavailable, code)
}

func TestMenuItem_EnsureAvailable_InsufficientStock(t *testing.T) {
	item := catalog.MenuItem{Name: "Margherita", IsAvailable: true, StockQty: intPtr(2)}

	err := item.EnsureAvailable(3)

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeInsufficientStock, code)
}

func TestMenuItem_EnsureAvailable_ExactStockIsEnough(t *testing.T) {
	item := catalog.MenuItem{Name: "Margherita", IsAvailable: true, StockQty: intPtr(2)}

	assert.NoError(t, item.EnsureAvailable(2))
}

func TestMenuItem_EnsureAvailable_UnlimitedStock(t *testing.T) {
	item := catalog.MenuItem{Name: "Margherita", IsAvailable: true, StockQty: nil}

	assert.NoError(t, item.EnsureAvailable(1000))
}

func TestMenuItem_EnsurePriceUnchanged(t *testing.T) {
	item := catalog.MenuItem{Name: "Margherita", PriceMinor: 45900}

	assert.NoError(t, item.EnsurePriceUnchanged(45900))

	err := item.EnsurePriceUnchanged(39900)
	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodePriceChanged, code)
}
