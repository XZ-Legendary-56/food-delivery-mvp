package order_test

import (
	"food-delivery-mvp/internal/domain/errs"
	"food-delivery-mvp/internal/domain/order"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_RejectsEmptyItems(t *testing.T) {
	_, err := order.New(uuid.New(), uuid.New(), uuid.New(), nil, "addr", "+70000000000", "", time.Now())

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeCartEmpty, code)
}

func TestNew_SetsInitialState(t *testing.T) {
	now := time.Now()
	items := []order.Item{{UnitPriceMinor: 10000, Quantity: 2}}

	o, err := order.New(uuid.New(), uuid.New(), uuid.New(), items, "addr", "+70000000000", "no onions", now)

	require.NoError(t, err)
	assert.Equal(t, order.StatusCreated, o.Status)
	require.Len(t, o.History, 1, "creation itself must be recorded as the first history entry")
	assert.Nil(t, o.History[0].From, "an order's creation has no prior status to record")
	assert.Equal(t, order.StatusCreated, o.History[0].To)
	assert.Equal(t, order.ActorCustomer, o.History[0].Actor)
	assert.Equal(t, now, o.CreatedAt)
	assert.Equal(t, now, o.UpdatedAt)
}

func TestTotalMinor_SumsLines(t *testing.T) {
	o := &order.Order{Items: []order.Item{
		{UnitPriceMinor: 10000, Quantity: 2}, // 20000
		{UnitPriceMinor: 5000, Quantity: 3},  // 15000
	}}

	assert.Equal(t, int64(35000), o.TotalMinor())
}

func TestTransitionTo_AppendsHistoryAndAdvancesStatus(t *testing.T) {
	t0 := time.Now()
	o := &order.Order{Status: order.StatusCreated, CreatedAt: t0, UpdatedAt: t0}

	t1 := t0.Add(time.Minute)
	err := o.TransitionTo(order.StatusConfirmed, order.ActorVenue, t1)

	require.NoError(t, err)
	assert.Equal(t, order.StatusConfirmed, o.Status)
	assert.Equal(t, t1, o.UpdatedAt)
	require.Len(t, o.History, 1)
	require.NotNil(t, o.History[0].From)
	assert.Equal(t, order.StatusCreated, *o.History[0].From)
	assert.Equal(t, order.StatusConfirmed, o.History[0].To)
	assert.Equal(t, order.ActorVenue, o.History[0].Actor)
	assert.Equal(t, t1, o.History[0].CreatedAt)
}

func TestTransitionTo_InvalidTransitionLeavesOrderUntouched(t *testing.T) {
	t0 := time.Now()
	o := &order.Order{Status: order.StatusCreated, UpdatedAt: t0}

	err := o.TransitionTo(order.StatusDelivered, order.ActorVenue, t0.Add(time.Minute))

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeOrderInvalidStateTransition, code)
	assert.Equal(t, order.StatusCreated, o.Status, "status must not change on a rejected transition")
	assert.Equal(t, t0, o.UpdatedAt, "UpdatedAt must not change on a rejected transition")
	assert.Empty(t, o.History, "history must not grow on a rejected transition")
}

func TestReject_SetsReasonAndStatus(t *testing.T) {
	o := &order.Order{Status: order.StatusCreated}

	err := o.Reject("out of ingredients", time.Now())

	require.NoError(t, err)
	assert.Equal(t, order.StatusRejected, o.Status)
	assert.Equal(t, "out of ingredients", o.RejectionReason)
}

func TestCancel_AllowedBeforeCooking(t *testing.T) {
	o := &order.Order{Status: order.StatusConfirmed}

	assert.NoError(t, o.Cancel(time.Now()))
	assert.Equal(t, order.StatusCancelled, o.Status)
}

func TestCancel_RejectedOnceCooking(t *testing.T) {
	o := &order.Order{Status: order.StatusCooking}

	err := o.Cancel(time.Now())

	require.Error(t, err)
	assert.Equal(t, order.StatusCooking, o.Status)
}
