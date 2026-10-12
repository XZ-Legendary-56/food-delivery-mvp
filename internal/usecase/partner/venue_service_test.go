package partner_test

import (
	"context"
	"food-delivery-mvp/internal/domain/errs"
	"food-delivery-mvp/internal/usecase/partner"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	domaincatalog "food-delivery-mvp/internal/domain/catalog"
)

// passthroughTx makes m.WithinTx just call fn with the given ctx — these
// tests are about VenueService's own logic, not TxManager's.
func passthroughTx(m *MockTxManager) {
	m.EXPECT().
		WithinTx(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
			return fn(ctx)
		}).
		AnyTimes()
}

func TestVenueService_GetVenue_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	venues := NewMockVenueRepository(ctrl)
	venueID := uuid.New()
	venues.EXPECT().Get(gomock.Any(), venueID).Return(nil, nil)

	svc := partner.NewVenueService(venues, NewMockTxManager(ctrl))
	_, err := svc.GetVenue(context.Background(), venueID)

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeNotFound, code)
}

func TestVenueService_UpdateVenue_WithoutWebhookURL_NoSecretGenerated(t *testing.T) {
	ctrl := gomock.NewController(t)
	venues := NewMockVenueRepository(ctrl)
	venueID := uuid.New()

	venues.EXPECT().
		UpdateProfile(gomock.Any(), venueID, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, patch partner.VenueProfilePatch) error {
			assert.Nil(t, patch.WebhookSecret, "no secret must be generated when webhookUrl is not being changed")
			return nil
		})
	venues.EXPECT().Get(gomock.Any(), venueID).Return(&domaincatalog.Venue{ID: venueID}, nil)

	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)
	svc := partner.NewVenueService(venues, tx)
	accepting := true
	_, secret, err := svc.UpdateVenue(context.Background(), venueID, partner.UpdateVenueRequest{AcceptingOrders: &accepting})

	require.NoError(t, err)
	assert.Empty(t, secret)
}

func TestVenueService_UpdateVenue_WithWebhookURL_GeneratesSecret(t *testing.T) {
	ctrl := gomock.NewController(t)
	venues := NewMockVenueRepository(ctrl)
	venueID := uuid.New()
	url := "https://venue.example/webhooks/orders"

	var capturedSecret *string
	venues.EXPECT().
		UpdateProfile(gomock.Any(), venueID, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, patch partner.VenueProfilePatch) error {
			require.NotNil(t, patch.WebhookSecret)
			capturedSecret = patch.WebhookSecret
			return nil
		})
	venues.EXPECT().Get(gomock.Any(), venueID).Return(&domaincatalog.Venue{ID: venueID}, nil)

	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)
	svc := partner.NewVenueService(venues, tx)
	_, secret, err := svc.UpdateVenue(context.Background(), venueID, partner.UpdateVenueRequest{WebhookURL: &url})

	require.NoError(t, err)
	require.NotEmpty(t, secret)
	assert.Equal(t, *capturedSecret, secret, "the secret returned to the caller must be exactly what was persisted")
	assert.Len(t, secret, 64, "32 random bytes hex-encoded")
}
