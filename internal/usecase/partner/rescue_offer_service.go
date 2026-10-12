package partner

import (
	"context"
	"fmt"
	"food-delivery-mvp/internal/domain/errs"
	"time"

	"github.com/google/uuid"

	domaincatalog "food-delivery-mvp/internal/domain/catalog"
)

// NewRescueOfferRequest is POST /rescue-offers' input.
type NewRescueOfferRequest struct {
	MenuItemID      uuid.UUID
	DiscountPercent int
	Quantity        int
	StartsAt        time.Time
	EndsAt          time.Time
}

// RescueOfferService backs the partner rescue-offer endpoints (docs/PLAN.md
// 5.3 item 9): create, list, cancel. It runs the same validation the
// domain type itself exposes before ever reaching the database — the
// database's exclusion constraint is still what actually guarantees no two
// live offers overlap under concurrent requests (docs/PLAN.md 5.5), this is
// just the fast, in-process rejection for the common case of an obviously
// bad request.
type RescueOfferService struct {
	offers RescueOfferRepository
}

func NewRescueOfferService(offers RescueOfferRepository) *RescueOfferService {
	return &RescueOfferService{offers: offers}
}

// ListOffers returns venueID's rescue offers, active-only when requested.
func (s *RescueOfferService) ListOffers(ctx context.Context, venueID uuid.UUID, activeOnly bool) ([]domaincatalog.RescueOffer, error) {
	offers, err := s.offers.List(ctx, venueID, activeOnly, time.Now())
	if err != nil {
		return nil, fmt.Errorf("list rescue offers: %w", err)
	}
	return offers, nil
}

// CreateOffer validates req and, if it passes, asks the repository to
// persist it — returning errs.CodeRescueOfferOverlap if the database's
// exclusion constraint rejects an overlapping window.
func (s *RescueOfferService) CreateOffer(ctx context.Context, venueID uuid.UUID, req NewRescueOfferRequest) (*domaincatalog.RescueOffer, error) {
	now := time.Now()
	if err := domaincatalog.ValidateDiscountPercent(req.DiscountPercent); err != nil {
		return nil, err
	}
	if err := domaincatalog.ValidateRescueWindow(req.StartsAt, req.EndsAt, now); err != nil {
		return nil, err
	}
	if req.Quantity < 1 {
		return nil, errs.New(errs.CodeValidationError, "quantity must be at least 1")
	}

	offer := domaincatalog.RescueOffer{
		ID:                uuid.New(),
		VenueID:           venueID,
		MenuItemID:        req.MenuItemID,
		DiscountPercent:   req.DiscountPercent,
		InitialQuantity:   req.Quantity,
		RemainingQuantity: req.Quantity,
		StartsAt:          req.StartsAt,
		EndsAt:            req.EndsAt,
	}
	created, err := s.offers.Create(ctx, offer)
	if err != nil {
		return nil, err
	}
	return created, nil
}

// CancelOffer ends venueID's offerID early (docs/PLAN.md 5.3 item 9), by
// setting canceled_at rather than deleting the row — the row still needs
// to exist for any order that already used it (order_items.rescue_offer_id
// references it) and for the exclusion constraint to keep excluding its
// own now-closed window from a genuinely overlapping new one.
func (s *RescueOfferService) CancelOffer(ctx context.Context, venueID, offerID uuid.UUID) error {
	if err := s.offers.Cancel(ctx, venueID, offerID, time.Now()); err != nil {
		return fmt.Errorf("cancel rescue offer: %w", err)
	}
	return nil
}
