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

	domainorder "food-delivery-mvp/internal/domain/order"
)

func TestOrderService_GetOrder_BelongsToDifferentVenue(t *testing.T) {
	ctrl := gomock.NewController(t)
	orders := NewMockOrderRepository(ctrl)
	orderID := uuid.New()
	orders.EXPECT().Get(gomock.Any(), orderID).Return(&domainorder.Order{ID: orderID, VenueID: uuid.New()}, nil)

	svc := partner.NewOrderService(orders)
	_, err := svc.GetOrder(context.Background(), uuid.New(), orderID)

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeNotFound, code)
}

func TestOrderService_AcceptOrder_SetsETAAndConfirms(t *testing.T) {
	ctrl := gomock.NewController(t)
	orders := NewMockOrderRepository(ctrl)
	venueID, orderID := uuid.New(), uuid.New()
	orders.EXPECT().Get(gomock.Any(), orderID).Return(&domainorder.Order{ID: orderID, VenueID: venueID, Status: domainorder.StatusCreated}, nil)
	orders.EXPECT().ApplyTransition(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, o *domainorder.Order) error {
		assert.Equal(t, domainorder.StatusConfirmed, o.Status)
		require.NotNil(t, o.ETAMinutes)
		assert.Equal(t, 30, *o.ETAMinutes)
		assert.Equal(t, "ext-123", o.ExternalOrderID)
		return nil
	})

	svc := partner.NewOrderService(orders)
	extID := "ext-123"
	o, err := svc.AcceptOrder(context.Background(), venueID, orderID, 30, &extID)

	require.NoError(t, err)
	assert.Equal(t, domainorder.StatusConfirmed, o.Status)
}

func TestOrderService_AcceptOrder_RejectedOnceConfirmed(t *testing.T) {
	ctrl := gomock.NewController(t)
	orders := NewMockOrderRepository(ctrl)
	venueID, orderID := uuid.New(), uuid.New()
	orders.EXPECT().Get(gomock.Any(), orderID).Return(&domainorder.Order{ID: orderID, VenueID: venueID, Status: domainorder.StatusConfirmed}, nil)
	// ApplyTransition must not be called: the state machine rejects
	// created->confirmed from an order that is already confirmed.

	svc := partner.NewOrderService(orders)
	_, err := svc.AcceptOrder(context.Background(), venueID, orderID, 30, nil)

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeOrderInvalidStateTransition, code)
}

func TestOrderService_RejectOrder_SetsReason(t *testing.T) {
	ctrl := gomock.NewController(t)
	orders := NewMockOrderRepository(ctrl)
	venueID, orderID := uuid.New(), uuid.New()
	orders.EXPECT().Get(gomock.Any(), orderID).Return(&domainorder.Order{ID: orderID, VenueID: venueID, Status: domainorder.StatusCreated}, nil)
	orders.EXPECT().ApplyTransition(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, o *domainorder.Order) error {
		assert.Equal(t, domainorder.StatusRejected, o.Status)
		assert.Equal(t, "out of stock", o.RejectionReason)
		return nil
	})

	svc := partner.NewOrderService(orders)
	o, err := svc.RejectOrder(context.Background(), venueID, orderID, "out of stock")

	require.NoError(t, err)
	assert.Equal(t, domainorder.StatusRejected, o.Status)
}

func TestOrderService_AdvanceStatus_InvalidJumpRejected(t *testing.T) {
	ctrl := gomock.NewController(t)
	orders := NewMockOrderRepository(ctrl)
	venueID, orderID := uuid.New(), uuid.New()
	orders.EXPECT().Get(gomock.Any(), orderID).Return(&domainorder.Order{ID: orderID, VenueID: venueID, Status: domainorder.StatusCooking}, nil)

	svc := partner.NewOrderService(orders)
	_, err := svc.AdvanceStatus(context.Background(), venueID, orderID, domainorder.StatusDelivered)

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeOrderInvalidStateTransition, code)
}

func TestOrderService_AdvanceStatus_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	orders := NewMockOrderRepository(ctrl)
	venueID, orderID := uuid.New(), uuid.New()
	orders.EXPECT().Get(gomock.Any(), orderID).Return(&domainorder.Order{ID: orderID, VenueID: venueID, Status: domainorder.StatusCooking}, nil)
	orders.EXPECT().ApplyTransition(gomock.Any(), gomock.Any()).Return(nil)

	svc := partner.NewOrderService(orders)
	o, err := svc.AdvanceStatus(context.Background(), venueID, orderID, domainorder.StatusReady)

	require.NoError(t, err)
	assert.Equal(t, domainorder.StatusReady, o.Status)
}
