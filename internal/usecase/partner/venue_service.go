package partner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"food-delivery-mvp/internal/domain/errs"
	"food-delivery-mvp/internal/usecase"

	"github.com/google/uuid"

	domaincatalog "food-delivery-mvp/internal/domain/catalog"
)

// VenueService backs GET/PATCH /venue (docs/PLAN.md 5.3 items 4-5).
type VenueService struct {
	venues    VenueRepository
	txManager usecase.TxManager
}

func NewVenueService(venues VenueRepository, txManager usecase.TxManager) *VenueService {
	return &VenueService{venues: venues, txManager: txManager}
}

func (s *VenueService) GetVenue(ctx context.Context, venueID uuid.UUID) (*domaincatalog.Venue, error) {
	v, err := s.venues.Get(ctx, venueID)
	if err != nil {
		return nil, fmt.Errorf("get venue: %w", err)
	}
	if v == nil {
		return nil, errs.New(errs.CodeNotFound, "venue not found")
	}
	return v, nil
}

// UpdateVenueRequest is PATCH /venue's parsed input — nil fields are left
// unchanged, matching the endpoint's own "every field optional" contract.
type UpdateVenueRequest struct {
	Description         *string
	AcceptingOrders     *bool
	MinOrderAmountMinor *int64
	WebhookURL          *string
	Schedule            *[]domaincatalog.ScheduleEntry
}

// UpdateVenue applies req and returns the updated venue plus the freshly
// generated webhook secret, when WebhookURL was set or changed — "" when
// it was not touched this call. The secret is never retrievable again
// after this: only its hash would need to be stored to verify future
// requests, but delivery itself (and therefore verifying anything with it)
// is stage 9's concern, not this one's.
func (s *VenueService) UpdateVenue(ctx context.Context, venueID uuid.UUID, req UpdateVenueRequest) (*domaincatalog.Venue, string, error) {
	patch := VenueProfilePatch{
		Description:         req.Description,
		AcceptingOrders:     req.AcceptingOrders,
		MinOrderAmountMinor: req.MinOrderAmountMinor,
		WebhookURL:          req.WebhookURL,
		Schedule:            req.Schedule,
	}

	var newSecret string
	if req.WebhookURL != nil {
		secret, err := generateWebhookSecret()
		if err != nil {
			return nil, "", fmt.Errorf("generate webhook secret: %w", err)
		}
		newSecret = secret
		patch.WebhookSecret = &secret
	}

	var v *domaincatalog.Venue
	err := s.txManager.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.venues.UpdateProfile(ctx, venueID, patch); err != nil {
			return fmt.Errorf("update venue profile: %w", err)
		}
		var err error
		v, err = s.venues.Get(ctx, venueID)
		if err != nil {
			return fmt.Errorf("get updated venue: %w", err)
		}
		if v == nil {
			return errs.New(errs.CodeNotFound, "venue not found")
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return v, newSecret, nil
}

// generateWebhookSecret returns a 32-byte random value, hex-encoded (64
// characters) — long enough that guessing it is not a realistic attack on
// the HMAC signature stage 9 will build on top of it (docs/PLAN.md 6.5).
func generateWebhookSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
