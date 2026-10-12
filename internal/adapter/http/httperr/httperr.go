// Package httperr is the single place a Go error becomes an HTTP response
// for either API (docs/PLAN.md 10.2): handlers never choose a status code
// themselves, they just return the error and the generated strict server
// routes it here through the hooks wired in adapter/http/public and
// adapter/http/partner.
package httperr

import (
	"encoding/json"
	"food-delivery-mvp/internal/adapter/http/middleware"
	"food-delivery-mvp/internal/domain/errs"
	"food-delivery-mvp/internal/generated/partnerapi"
	"food-delivery-mvp/internal/generated/publicapi"
	"log/slog"
	"net/http"
)

// statusFor maps a domain error Code to its HTTP status, per the table in
// docs/PLAN.md 5.2 plus the generic REST cases from 7.1. Shared by both APIs:
// every code either API can produce is covered here in one place, so the
// two specs' identical Error schemas never end up mapped inconsistently.
func statusFor(code errs.Code) int {
	switch code {
	case errs.CodeVenueClosed,
		errs.CodeVenueNotAcceptingOrders,
		errs.CodeItemUnavailable,
		errs.CodeInsufficientStock,
		errs.CodePriceChanged,
		errs.CodeCartVenueMismatch,
		errs.CodeCartEmpty,
		errs.CodeOrderInvalidStateTransition,
		errs.CodeIdempotencyKeyConflict:
		return http.StatusConflict
	case errs.CodeMinOrderAmountNotReached:
		return http.StatusUnprocessableEntity
	case errs.CodeValidationError:
		return http.StatusBadRequest
	case errs.CodeNotFound:
		return http.StatusNotFound
	case errs.CodeUnauthorized:
		return http.StatusUnauthorized
	default:
		return http.StatusInternalServerError
	}
}

// describe turns err into what any Write variant needs: a status, a code,
// and a message safe to show a client. Anything that is not a domain error
// is unexpected — logged here in full (this is the only place that sees
// it) and reported as a generic 500 with no details, so an internal error
// message or stack-adjacent text never leaks out.
func describe(r *http.Request, requestID string, err error) (status int, code errs.Code, message string) {
	if domainErr, ok := errs.As(err); ok {
		return statusFor(domainErr.Code), domainErr.Code, domainErr.Message
	}
	slog.Error("unhandled error reached the http layer",
		"error", err, "request_id", requestID, "path", r.URL.Path)
	return http.StatusInternalServerError, errs.CodeInternal, "internal server error"
}

// Write encodes err as the public API's Error schema.
func Write(w http.ResponseWriter, r *http.Request, err error) {
	requestID := middleware.RequestIDFromContext(r.Context())
	status, code, message := describe(r, requestID, err)
	writeJSON(w, status, publicapi.Error{
		Code:      publicapi.ErrorCode(code),
		Message:   message,
		RequestId: requestID,
	})
}

// WriteRequestError handles a malformed public-API request the generated
// layer could not even bind (bad JSON body, wrong-format path/query/header
// parameter) — always the client's fault, so it is always
// VALIDATION_ERROR/400 rather than going through statusFor.
func WriteRequestError(w http.ResponseWriter, r *http.Request, err error) {
	requestID := middleware.RequestIDFromContext(r.Context())
	writeJSON(w, http.StatusBadRequest, publicapi.Error{
		Code:      publicapi.ErrorCode(errs.CodeValidationError),
		Message:   err.Error(),
		RequestId: requestID,
	})
}

// WritePartner is Write for the partner API's own generated Error type —
// same shape, same status mapping, different Go type because oapi-codegen
// generates one independently per spec.
func WritePartner(w http.ResponseWriter, r *http.Request, err error) {
	requestID := middleware.RequestIDFromContext(r.Context())
	status, code, message := describe(r, requestID, err)
	writePartnerJSON(w, status, partnerapi.Error{
		Code:      partnerapi.ErrorCode(code),
		Message:   message,
		RequestId: requestID,
	})
}

// WritePartnerRequestError is WriteRequestError for the partner API.
func WritePartnerRequestError(w http.ResponseWriter, r *http.Request, err error) {
	requestID := middleware.RequestIDFromContext(r.Context())
	writePartnerJSON(w, http.StatusBadRequest, partnerapi.Error{
		Code:      partnerapi.ErrorCode(errs.CodeValidationError),
		Message:   err.Error(),
		RequestId: requestID,
	})
}

func writeJSON(w http.ResponseWriter, status int, body publicapi.Error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writePartnerJSON(w http.ResponseWriter, status int, body partnerapi.Error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
