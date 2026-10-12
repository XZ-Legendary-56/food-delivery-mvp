package order_test

import (
	"context"
	"food-delivery-mvp/internal/domain/errs"
	"food-delivery-mvp/internal/usecase/order"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	domainorder "food-delivery-mvp/internal/domain/order"
)

func TestGetOrder_Found(t *testing.T) {
	ctrl := gomock.NewController(t)
	orders := NewMockRepository(ctrl)
	outboxRepo := NewMockOutboxRepository(ctrl)
	tx := NewMockTxManager(ctrl)

	clientID, orderID := uuid.New(), uuid.New()
	o := &domainorder.Order{ID: orderID, ClientID: clientID}
	orders.EXPECT().Get(gomock.Any(), orderID).Return(o, nil)

	svc := order.NewOrderService(orders, outboxRepo, tx)
	got, err := svc.GetOrder(context.Background(), clientID, orderID)

	require.NoError(t, err)
	assert.Same(t, o, got)
}

func TestGetOrder_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	orders := NewMockRepository(ctrl)
	outboxRepo := NewMockOutboxRepository(ctrl)
	tx := NewMockTxManager(ctrl)

	orderID := uuid.New()
	orders.EXPECT().Get(gomock.Any(), orderID).Return(nil, nil)

	svc := order.NewOrderService(orders, outboxRepo, tx)
	_, err := svc.GetOrder(context.Background(), uuid.New(), orderID)

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeNotFound, code)
}

func TestGetOrder_BelongsToDifferentClient(t *testing.T) {
	ctrl := gomock.NewController(t)
	orders := NewMockRepository(ctrl)
	outboxRepo := NewMockOutboxRepository(ctrl)
	tx := NewMockTxManager(ctrl)

	orderID := uuid.New()
	o := &domainorder.Order{ID: orderID, ClientID: uuid.New()}
	orders.EXPECT().Get(gomock.Any(), orderID).Return(o, nil)

	svc := order.NewOrderService(orders, outboxRepo, tx)
	_, err := svc.GetOrder(context.Background(), uuid.New(), orderID)

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeNotFound, code, "another client's order must read as not found, not as forbidden")
}

func TestCancelOrder_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	orders := NewMockRepository(ctrl)
	outboxRepo := NewMockOutboxRepository(ctrl)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, orderID := uuid.New(), uuid.New()
	o := &domainorder.Order{ID: orderID, ClientID: clientID, Status: domainorder.StatusCreated}
	orders.EXPECT().Get(gomock.Any(), orderID).Return(o, nil)
	orders.EXPECT().AppendStatusChange(gomock.Any(), orderID, gomock.Any()).DoAndReturn(
		func(_ context.Context, _ uuid.UUID, change domainorder.StatusChange) error {
			assert.Equal(t, domainorder.StatusCancelled, change.To)
			assert.Equal(t, domainorder.ActorCustomer, change.Actor)
			return nil
		})
	outboxRepo.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(nil)

	svc := order.NewOrderService(orders, outboxRepo, tx)
	got, err := svc.CancelOrder(context.Background(), clientID, orderID)

	require.NoError(t, err)
	assert.Equal(t, domainorder.StatusCancelled, got.Status)
}

func TestCancelOrder_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	orders := NewMockRepository(ctrl)
	outboxRepo := NewMockOutboxRepository(ctrl)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	orderID := uuid.New()
	orders.EXPECT().Get(gomock.Any(), orderID).Return(nil, nil)

	svc := order.NewOrderService(orders, outboxRepo, tx)
	_, err := svc.CancelOrder(context.Background(), uuid.New(), orderID)

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeNotFound, code)
}

func TestCancelOrder_RejectedOnceCooking(t *testing.T) {
	ctrl := gomock.NewController(t)
	orders := NewMockRepository(ctrl)
	outboxRepo := NewMockOutboxRepository(ctrl)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, orderID := uuid.New(), uuid.New()
	o := &domainorder.Order{ID: orderID, ClientID: clientID, Status: domainorder.StatusCooking}
	orders.EXPECT().Get(gomock.Any(), orderID).Return(o, nil)
	// AppendStatusChange must not be called: the state machine rejects the
	// transition before there is anything to persist.

	svc := order.NewOrderService(orders, outboxRepo, tx)
	_, err := svc.CancelOrder(context.Background(), clientID, orderID)

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeOrderInvalidStateTransition, code)
}
