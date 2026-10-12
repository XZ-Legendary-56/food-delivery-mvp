package kitchen

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdvanceDueOrders_MovesThroughSequenceOneStepAtATime(t *testing.T) {
	platform := newFakePlatform(t)
	state := NewState()
	orderID := uuid.New()
	state.AddOrder(Order{ID: orderID, Status: "confirmed", NextAdvanceAt: time.Now().Add(-time.Second)})

	advanceDueOrders(context.Background(), platform.client(t), state, time.Minute, discardLogger())

	require.Len(t, platform.advanceCalls, 1)
	assert.Equal(t, orderID.String(), platform.advanceCalls[0])
	got := state.GetOrder(orderID)
	assert.Equal(t, "cooking", got.Status)
	assert.False(t, got.NextAdvanceAt.IsZero(), "cooking is not terminal, so another advance must still be scheduled")
}

func TestAdvanceDueOrders_DeliveredStopsScheduling(t *testing.T) {
	platform := newFakePlatform(t)
	state := NewState()
	orderID := uuid.New()
	state.AddOrder(Order{ID: orderID, Status: "delivering", NextAdvanceAt: time.Now().Add(-time.Second)})

	advanceDueOrders(context.Background(), platform.client(t), state, time.Minute, discardLogger())

	got := state.GetOrder(orderID)
	assert.Equal(t, "delivered", got.Status)
	assert.True(t, got.NextAdvanceAt.IsZero(), "delivered is terminal: no further advance should ever be scheduled")
}

func TestAdvanceDueOrders_TerminalStatusIsNeverPickedUp(t *testing.T) {
	platform := newFakePlatform(t)
	state := NewState()
	orderID := uuid.New()
	// Not due (zero NextAdvanceAt) and also not in statusSequence — belt and
	// suspenders that a rejected/canceled order is simply skipped.
	state.AddOrder(Order{ID: orderID, Status: "rejected"})

	advanceDueOrders(context.Background(), platform.client(t), state, time.Minute, discardLogger())

	assert.Empty(t, platform.advanceCalls)
}

func TestSyncStock_PushesEveryTrackedItem(t *testing.T) {
	platform := newFakePlatform(t)
	state := NewState()
	state.SetMenu([]MenuItem{
		{PlatformID: uuid.New(), ExternalID: "a", StockQty: 3},
		{PlatformID: uuid.New(), ExternalID: "b", StockQty: 0},
	})

	syncStock(context.Background(), platform.client(t), state, discardLogger())

	assert.Equal(t, 1, platform.availUpdates)
}

func TestRetryPendingActions_AcceptSucceedsOnRetry_ResolvesToConfirmed(t *testing.T) {
	platform := newFakePlatform(t)
	state := NewState()
	orderID := uuid.New()
	state.AddOrder(Order{
		ID:          orderID,
		Status:      "pending_accept",
		Pending:     PendingAccept,
		ETAMinutes:  20,
		NextRetryAt: time.Now().Add(-time.Second),
	})

	retryPendingActions(context.Background(), platform.client(t), state, time.Minute, discardLogger())

	require.Len(t, platform.acceptCalls, 1)
	got := state.GetOrder(orderID)
	assert.Equal(t, "confirmed", got.Status)
	assert.Equal(t, PendingNone, got.Pending)
	assert.False(t, got.NextAdvanceAt.IsZero(), "a resolved accept must re-enter the normal status ticker")
}

func TestRetryPendingActions_AcceptConflict_TreatedAsAlreadyResolved(t *testing.T) {
	platform := newFakePlatform(t)
	platform.acceptResult = fakeConflict
	state := NewState()
	orderID := uuid.New()
	state.AddOrder(Order{ID: orderID, Status: "pending_accept", Pending: PendingAccept, NextRetryAt: time.Now().Add(-time.Second)})

	retryPendingActions(context.Background(), platform.client(t), state, time.Minute, discardLogger())

	assert.Equal(t, "confirmed", state.GetOrder(orderID).Status)
}

func TestRetryPendingActions_StillFailing_ReschedulesWithoutChangingStatus(t *testing.T) {
	platform := newFakePlatform(t)
	platform.acceptResult = fakeServerError
	state := NewState()
	orderID := uuid.New()
	state.AddOrder(Order{ID: orderID, Status: "pending_accept", Pending: PendingAccept, NextRetryAt: time.Now().Add(-time.Second)})

	retryPendingActions(context.Background(), platform.client(t), state, time.Minute, discardLogger())

	got := state.GetOrder(orderID)
	assert.Equal(t, "pending_accept", got.Status)
	assert.Equal(t, PendingAccept, got.Pending)
	assert.True(t, got.NextRetryAt.After(time.Now()), "a still-failing retry must be rescheduled, not abandoned")
}

func TestRetryPendingActions_RejectSucceedsOnRetry_ResolvesToRejected(t *testing.T) {
	platform := newFakePlatform(t)
	state := NewState()
	orderID := uuid.New()
	state.AddOrder(Order{ID: orderID, Status: "pending_reject", Pending: PendingReject, RejectionReason: "no stock", NextRetryAt: time.Now().Add(-time.Second)})

	retryPendingActions(context.Background(), platform.client(t), state, time.Minute, discardLogger())

	require.Len(t, platform.rejectCalls, 1)
	got := state.GetOrder(orderID)
	assert.Equal(t, "rejected", got.Status)
	assert.Equal(t, PendingNone, got.Pending)
}

func TestRetryPendingActions_NothingDue_NoOp(t *testing.T) {
	platform := newFakePlatform(t)
	state := NewState()
	state.AddOrder(Order{ID: uuid.New(), Status: "pending_accept", Pending: PendingAccept, NextRetryAt: time.Now().Add(time.Hour)})

	retryPendingActions(context.Background(), platform.client(t), state, time.Minute, discardLogger())

	assert.Empty(t, platform.acceptCalls)
}

func TestSyncStock_NoTrackedItems_SkipsTheCall(t *testing.T) {
	platform := newFakePlatform(t)
	state := NewState()

	syncStock(context.Background(), platform.client(t), state, discardLogger())

	assert.Equal(t, 0, platform.availUpdates, "nothing to sync yet must not produce an empty request")
}
