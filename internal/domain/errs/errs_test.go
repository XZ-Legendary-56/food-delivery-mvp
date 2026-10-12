package errs_test

import (
	"errors"
	"food-delivery-mvp/internal/domain/errs"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	err := errs.New(errs.CodeCartEmpty, "cart is empty")

	assert.Equal(t, errs.CodeCartEmpty, err.Code)
	assert.Equal(t, "cart is empty", err.Message)
	assert.Nil(t, err.Unwrap())
	assert.Equal(t, `CART_EMPTY: cart is empty`, err.Error())
}

func TestNewf(t *testing.T) {
	err := errs.Newf(errs.CodeInsufficientStock, "only %d left, requested %d", 1, 3)

	assert.Equal(t, "only 1 left, requested 3", err.Message)
}

func TestWrap_UnwrapsToCause(t *testing.T) {
	cause := errors.New("connection reset")

	err := errs.Wrap(errs.CodeInsufficientStock, "could not check stock", cause)

	assert.ErrorIs(t, err, cause)
	assert.Contains(t, err.Error(), "connection reset")
}

func TestCodeOf(t *testing.T) {
	domainErr := errs.New(errs.CodeVenueClosed, "closed")
	wrapped := errors.Join(errors.New("context"), domainErr)

	code, ok := errs.CodeOf(domainErr)
	require.True(t, ok)
	assert.Equal(t, errs.CodeVenueClosed, code)

	code, ok = errs.CodeOf(wrapped)
	require.True(t, ok, "CodeOf should find the domain error through errors.Join")
	assert.Equal(t, errs.CodeVenueClosed, code)

	_, ok = errs.CodeOf(errors.New("plain error"))
	assert.False(t, ok)
}
