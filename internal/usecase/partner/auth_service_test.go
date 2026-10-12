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
)

func TestAuthService_Authenticate_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	keys := NewMockAPIKeyRepository(ctrl)
	venueID := uuid.New()

	keys.EXPECT().
		ResolveVenueByKeyHash(gomock.Any(), partner.HashAPIKey("demo_key")).
		Return(venueID, nil)

	svc := partner.NewAuthService(keys)
	got, err := svc.Authenticate(context.Background(), "demo_key")

	require.NoError(t, err)
	assert.Equal(t, venueID, got)
}

func TestAuthService_Authenticate_Unauthorized(t *testing.T) {
	ctrl := gomock.NewController(t)
	keys := NewMockAPIKeyRepository(ctrl)

	keys.EXPECT().
		ResolveVenueByKeyHash(gomock.Any(), gomock.Any()).
		Return(uuid.Nil, errs.New(errs.CodeUnauthorized, "invalid or revoked API key"))

	svc := partner.NewAuthService(keys)
	_, err := svc.Authenticate(context.Background(), "wrong_key")

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeUnauthorized, code)
}

func TestHashAPIKey_Deterministic(t *testing.T) {
	assert.Equal(t, partner.HashAPIKey("abc"), partner.HashAPIKey("abc"))
	assert.NotEqual(t, partner.HashAPIKey("abc"), partner.HashAPIKey("abd"))
}
