package order

import (
	"crypto/sha256"
	"encoding/hex"
)

// HashCheckoutRequest returns a stable digest of the fields that define
// what "the same checkout request" means for idempotency (docs/PLAN.md 5.2):
// a repeat with the same Idempotency-Key and the same digest replays the
// original order; the same key with a different digest is
// IDEMPOTENCY_KEY_CONFLICT. Deliberately scoped to the request body only
// — not the client's cart contents at the time — matching the project's
// own wording ("тот же ключ, но другое тело запроса"): the key is meant to
// protect a client retrying an identical HTTP request, not to freeze the
// cart in place.
//
// A null byte separates the fields so that, say, ("ab", "c") and ("a",
// "bc") never collide by naive concatenation.
func HashCheckoutRequest(deliveryAddress, customerPhone, comment string) string {
	h := sha256.New()
	for _, field := range []string{deliveryAddress, customerPhone, comment} {
		h.Write([]byte(field))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
