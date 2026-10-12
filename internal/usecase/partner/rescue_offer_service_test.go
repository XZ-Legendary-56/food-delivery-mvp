package partner_test

import (
	"context"
	"food-delivery-mvp/internal/domain/errs"
	"food-delivery-mvp/internal/usecase/partner"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	domaincatalog "food-delivery-mvp/internal/domain/catalog"
)

func TestRescueOfferService_CreateOffer_ValidatesDiscountBeforeHittingRepository(t *testing.T) {
	ctrl := gomock.NewController(t)
	offers := NewMockRescueOfferRepository(ctrl)
	// No Create call expected: an invalid request must fail before ever
	// reaching the repository.

	svc := partner.NewRescueOfferService(offers)
	_, err := svc.CreateOffer(context.Background(), uuid.New(), partner.NewRescueOfferRequest{
		DiscountPercent: 95,
		Quantity:        1,
		StartsAt:        time.Now(),
		EndsAt:          time.Now().Add(time.Hour),
	})

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeRescueInvalidDiscount, code)
}

func TestRescueOfferService_CreateOffer_ValidatesWindowBeforeHittingRepository(t *testing.T) {
	ctrl := gomock.NewController(t)
	offers := NewMockRescueOfferRepository(ctrl)

	svc := partner.NewRescueOfferService(offers)
	now := time.Now()
	_, err := svc.CreateOffer(context.Background(), uuid.New(), partner.NewRescueOfferRequest{
		DiscountPercent: 40,
		Quantity:        1,
		StartsAt:        now,
		EndsAt:          now.Add(-time.Hour), // ends before it starts
	})

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeRescueInvalidWindow, code)
}

func TestRescueOfferService_CreateOffer_RejectsNonPositiveQuantity(t *testing.T) {
	ctrl := gomock.NewController(t)
	offers := NewMockRescueOfferRepository(ctrl)

	svc := partner.NewRescueOfferService(offers)
	_, err := svc.CreateOffer(context.Background(), uuid.New(), partner.NewRescueOfferRequest{
		DiscountPercent: 40,
		Quantity:        0,
		StartsAt:        time.Now(),
		EndsAt:          time.Now().Add(time.Hour),
	})

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeValidationError, code)
}

func TestRescueOfferService_CreateOffer_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	offers := NewMockRescueOfferRepository(ctrl)
	venueID, menuItemID := uuid.New(), uuid.New()
	now := time.Now()

	offers.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, o domaincatalog.RescueOffer) (*domaincatalog.RescueOffer, error) {
			assert.Equal(t, venueID, o.VenueID)
			assert.Equal(t, menuItemID, o.MenuItemID)
			assert.Equal(t, 5, o.InitialQuantity)
			assert.Equal(t, 5, o.RemainingQuantity, "a brand new offer must start fully stocked")
			return &o, nil
		})

	svc := partner.NewRescueOfferService(offers)
	created, err := svc.CreateOffer(context.Background(), venueID, partner.NewRescueOfferRequest{
		MenuItemID:      menuItemID,
		DiscountPercent: 40,
		Quantity:        5,
		StartsAt:        now,
		EndsAt:          now.Add(time.Hour),
	})

	require.NoError(t, err)
	assert.Equal(t, menuItemID, created.MenuItemID)
}

func TestRescueOfferService_CreateOffer_PropagatesOverlapError(t *testing.T) {
	ctrl := gomock.NewController(t)
	offers := NewMockRescueOfferRepository(ctrl)
	overlapErr := errs.New(errs.CodeRescueOfferOverlap, "overlaps an existing offer")
	offers.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil, overlapErr)

	svc := partner.NewRescueOfferService(offers)
	now := time.Now()
	_, err := svc.CreateOffer(context.Background(), uuid.New(), partner.NewRescueOfferRequest{
		DiscountPercent: 40,
		Quantity:        1,
		StartsAt:        now,
		EndsAt:          now.Add(time.Hour),
	})

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeRescueOfferOverlap, code)
}

func TestRescueOfferService_ListOffers_Delegates(t *testing.T) {
	ctrl := gomock.NewController(t)
	offers := NewMockRescueOfferRepository(ctrl)
	venueID := uuid.New()
	want := []domaincatalog.RescueOffer{{ID: uuid.New(), VenueID: venueID}}
	offers.EXPECT().List(gomock.Any(), venueID, true, gomock.Any()).Return(want, nil)

	svc := partner.NewRescueOfferService(offers)
	got, err := svc.ListOffers(context.Background(), venueID, true)

	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestRescueOfferService_CancelOffer_Delegates(t *testing.T) {
	ctrl := gomock.NewController(t)
	offers := NewMockRescueOfferRepository(ctrl)
	venueID, offerID := uuid.New(), uuid.New()
	offers.EXPECT().Cancel(gomock.Any(), venueID, offerID, gomock.Any()).Return(nil)

	svc := partner.NewRescueOfferService(offers)
	err := svc.CancelOffer(context.Background(), venueID, offerID)

	require.NoError(t, err)
}

func TestRescueOfferService_CancelOffer_PropagatesNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	offers := NewMockRescueOfferRepository(ctrl)
	venueID, offerID := uuid.New(), uuid.New()
	offers.EXPECT().Cancel(gomock.Any(), venueID, offerID, gomock.Any()).
		Return(errs.New(errs.CodeNotFound, "rescue offer not found"))

	svc := partner.NewRescueOfferService(offers)
	err := svc.CancelOffer(context.Background(), venueID, offerID)

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeNotFound, code)
}
