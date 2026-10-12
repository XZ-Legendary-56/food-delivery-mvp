// Package errs defines the domain's typed errors. A domain error always
// carries a stable, machine-readable Code so adapter/http/httperr can map it
// to an HTTP status and response body in exactly one place (docs/PLAN.md
// 10.2) — no handler ever string-matches an error message to decide what
// to return.
//
// This package must stay dependency-free beyond the standard library
// (docs/PLAN.md 6.2: domain imports nothing but stdlib and uuid), since every
// other domain package imports it.
package errs

import (
	"errors"
	"fmt"
)

// Code identifies a specific business error. Values match the `code` enum
// in api/openapi/*.yaml exactly, since that enum is generated from this
// catalog's intent — adding a code here without a matching enum entry (or
// vice versa) is a bug.
type Code string

const (
	// Order state machine (docs/PLAN.md 5.4).
	CodeOrderInvalidStateTransition Code = "ORDER_INVALID_STATE_TRANSITION"

	// Cart invariants (docs/PLAN.md 5.1).
	CodeCartVenueMismatch Code = "CART_VENUE_MISMATCH"
	CodeCartEmpty         Code = "CART_EMPTY"

	// Venue and menu-item availability (docs/PLAN.md 5.2).
	CodeVenueClosed              Code = "VENUE_CLOSED"
	CodeVenueNotAcceptingOrders  Code = "VENUE_NOT_ACCEPTING_ORDERS"
	CodeItemUnavailable          Code = "ITEM_UNAVAILABLE"
	CodeInsufficientStock        Code = "INSUFFICIENT_STOCK"
	CodePriceChanged             Code = "PRICE_CHANGED"
	CodeMinOrderAmountNotReached Code = "MIN_ORDER_AMOUNT_NOT_REACHED"

	// Idempotent checkout (docs/PLAN.md 5.2): the same Idempotency-Key was
	// already used with a different request body. A matching body is not
	// an error at all — it replays the original order instead.
	CodeIdempotencyKeyConflict Code = "IDEMPOTENCY_KEY_CONFLICT"

	// Generic REST cases, shared by both APIs (api/openapi/*.yaml ErrorCode
	// description): not business rules of their own, but still routed
	// through the same Code -> HTTP status mapping in adapter/http/httperr
	// so there is still only one place that decides a status code.
	CodeValidationError Code = "VALIDATION_ERROR"
	CodeNotFound        Code = "NOT_FOUND"
	CodeInternal        Code = "INTERNAL_ERROR"

	// Partner API access control (docs/PLAN.md 5.3 item 1): missing, unknown
	// or revoked X-Api-Key. Only meaningful on the partner API — the
	// public API has no authentication to fail.
	CodeUnauthorized Code = "UNAUTHORIZED"

	// Rescue offers (docs/PLAN.md 5.5). The first three are partner-side
	// creation failures; the last is a public-API checkout conflict — the
	// specific offer a cart line's price was based on is entirely gone
	// (window ended or canceled) by the time the order is placed. Partial
	// depletion (some units still get the discount, the rest do not) is
	// deliberately NOT an error of its own: it succeeds as a split order
	// with two line items, per docs/PLAN.md 5.5's own description of the
	// mechanism.
	CodeRescueOfferOverlap    Code = "RESCUE_OFFER_OVERLAP"
	CodeRescueInvalidWindow   Code = "RESCUE_INVALID_WINDOW"
	CodeRescueInvalidDiscount Code = "RESCUE_INVALID_DISCOUNT"
	CodeRescueOfferExpired    Code = "RESCUE_OFFER_EXPIRED"
)

// Error is a domain error: a stable Code plus a human-readable Message, an
// optional structured Details payload, and optionally the lower-level error
// it wraps (kept reachable through Unwrap so errors.Is/errors.As still work
// across the wrap, per docs/PLAN.md 10.2).
//
// Details exists because some conflicts are about more than one thing at
// once — docs/PLAN.md 5.2's error table asks INSUFFICIENT_STOCK to list how
// much of *each* item is available, not just report the first shortage
// found. Its shape matches api/openapi/*.yaml's Error.details exactly
// ([]map[string]any is the same type as []map[string]interface{}) so
// adapter/http/httperr can hand it to the generated response type with no
// conversion step — but building it is still just constructing plain Go
// maps, not touching HTTP or JSON, so this stays inside the layering rule.
type Error struct {
	Code    Code
	Message string
	Details []map[string]any
	cause   error
}

// New creates a fresh domain error with no wrapped cause and no Details.
func New(code Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Newf is New with fmt.Sprintf-style formatting for Message.
func Newf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// NewWithDetails is New plus a structured Details payload.
func NewWithDetails(code Code, message string, details []map[string]any) *Error {
	return &Error{Code: code, Message: message, Details: details}
}

// Wrap creates a domain error that carries cause as its Unwrap target, for
// when a business rule violation was detected while handling a lower-level
// error (e.g. a repository call failed in a way that maps to a domain code).
func Wrap(code Code, message string, cause error) *Error {
	return &Error{Code: code, Message: message, cause: cause}
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap exposes the wrapped cause, if any, to errors.Is/errors.As.
func (e *Error) Unwrap() error {
	return e.cause
}

// As extracts the *Error carried by err, if err is or wraps one.
func As(err error) (*Error, bool) {
	var domainErr *Error
	if errors.As(err, &domainErr) {
		return domainErr, true
	}
	return nil, false
}

// CodeOf extracts the Code carried by err, if err is or wraps an *Error.
func CodeOf(err error) (Code, bool) {
	domainErr, ok := As(err)
	if !ok {
		return "", false
	}
	return domainErr.Code, true
}
