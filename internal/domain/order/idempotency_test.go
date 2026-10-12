package order_test

import (
	"food-delivery-mvp/internal/domain/order"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHashCheckoutRequest_SameInputSameHash(t *testing.T) {
	a := order.HashCheckoutRequest("addr", "+70000000000", "no onions")
	b := order.HashCheckoutRequest("addr", "+70000000000", "no onions")

	assert.Equal(t, a, b)
}

func TestHashCheckoutRequest_DifferentInputDifferentHash(t *testing.T) {
	base := order.HashCheckoutRequest("addr", "+70000000000", "no onions")

	assert.NotEqual(t, base, order.HashCheckoutRequest("other addr", "+70000000000", "no onions"))
	assert.NotEqual(t, base, order.HashCheckoutRequest("addr", "+79999999999", "no onions"))
	assert.NotEqual(t, base, order.HashCheckoutRequest("addr", "+70000000000", "extra spicy"))
}

func TestHashCheckoutRequest_NoAmbiguousConcatenation(t *testing.T) {
	// Without a separator, ("ab", "c") and ("a", "bc") would hash identically.
	a := order.HashCheckoutRequest("ab", "c", "")
	b := order.HashCheckoutRequest("a", "bc", "")

	assert.NotEqual(t, a, b)
}
