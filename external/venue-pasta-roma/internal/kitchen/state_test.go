package kitchen

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckAndReserve_EnoughStock_Decrements(t *testing.T) {
	s := NewState()
	itemID := uuid.New()
	s.SetMenu([]MenuItem{{PlatformID: itemID, ExternalID: "x", StockQty: 5}})

	ok := s.CheckAndReserve([]OrderItem{{PlatformMenuItemID: itemID, Quantity: 2}})

	require.True(t, ok)
	assert.Equal(t, 3, s.StockSnapshot()[0].StockQty)
}

func TestCheckAndReserve_InsufficientStock_LeavesStockUntouched(t *testing.T) {
	s := NewState()
	itemID := uuid.New()
	s.SetMenu([]MenuItem{{PlatformID: itemID, ExternalID: "x", StockQty: 1}})

	ok := s.CheckAndReserve([]OrderItem{{PlatformMenuItemID: itemID, Quantity: 2}})

	require.False(t, ok)
	assert.Equal(t, 1, s.StockSnapshot()[0].StockQty, "a rejected order must not touch stock at all")
}

func TestCheckAndReserve_UnknownItem_Fails(t *testing.T) {
	s := NewState()
	ok := s.CheckAndReserve([]OrderItem{{PlatformMenuItemID: uuid.New(), Quantity: 1}})
	assert.False(t, ok)
}

func TestCheckAndReserve_PartialShortage_ReservesNothing(t *testing.T) {
	s := NewState()
	a, b := uuid.New(), uuid.New()
	s.SetMenu([]MenuItem{
		{PlatformID: a, ExternalID: "a", StockQty: 5},
		{PlatformID: b, ExternalID: "b", StockQty: 1},
	})

	ok := s.CheckAndReserve([]OrderItem{
		{PlatformMenuItemID: a, Quantity: 1},
		{PlatformMenuItemID: b, Quantity: 2}, // not enough
	})

	require.False(t, ok)
	snap := stockByID(s)
	assert.Equal(t, 5, snap[a], "item a must not be decremented just because item b ran short")
	assert.Equal(t, 1, snap[b])
}

func TestReleaseStock_PutsQuantityBack(t *testing.T) {
	s := NewState()
	itemID := uuid.New()
	s.SetMenu([]MenuItem{{PlatformID: itemID, ExternalID: "x", StockQty: 5}})
	require.True(t, s.CheckAndReserve([]OrderItem{{PlatformMenuItemID: itemID, Quantity: 2}}))

	s.ReleaseStock([]OrderItem{{PlatformMenuItemID: itemID, Quantity: 2}})

	assert.Equal(t, 5, s.StockSnapshot()[0].StockQty)
}

func TestSetStockByExternalID_UnknownID_NotOK(t *testing.T) {
	s := NewState()
	_, ok := s.SetStockByExternalID("does-not-exist", 10)
	assert.False(t, ok)
}

func TestSetStockByExternalID_UpdatesTrackedItem(t *testing.T) {
	s := NewState()
	itemID := uuid.New()
	s.SetMenu([]MenuItem{{PlatformID: itemID, ExternalID: "pr-carbonara", StockQty: 5}})

	platformID, ok := s.SetStockByExternalID("pr-carbonara", 42)

	require.True(t, ok)
	assert.Equal(t, itemID, platformID)
	assert.Equal(t, 42, s.StockSnapshot()[0].StockQty)
}

func TestMarkEventSeen_FirstTimeTrueThenFalse(t *testing.T) {
	s := NewState()
	eventID := uuid.New()

	assert.True(t, s.MarkEventSeen(eventID), "the first delivery of an event must be treated as new")
	assert.False(t, s.MarkEventSeen(eventID), "a repeat delivery of the same event id must be recognized as a duplicate")
}

func TestOrderLifecycle_AddGetListCancel(t *testing.T) {
	s := NewState()
	orderID := uuid.New()
	itemID := uuid.New()
	s.AddOrder(Order{ID: orderID, Status: "confirmed", Items: []OrderItem{{PlatformMenuItemID: itemID, Quantity: 3}}})

	got := s.GetOrder(orderID)
	require.NotNil(t, got)
	assert.Equal(t, "confirmed", got.Status)
	assert.Len(t, s.ListOrders(), 1)

	items, ok := s.CancelOrder(orderID)
	require.True(t, ok)
	require.Len(t, items, 1)
	assert.Equal(t, itemID, items[0].PlatformMenuItemID)
	assert.Equal(t, "canceled", s.GetOrder(orderID).Status)
}

func TestCancelOrder_UntrackedOrder_NotOK(t *testing.T) {
	s := NewState()
	_, ok := s.CancelOrder(uuid.New())
	assert.False(t, ok)
}

func TestDueOrders_OnlyReturnsOrdersWhoseTimeHasArrived(t *testing.T) {
	s := NewState()
	now := time.Now()

	dueID, notDueID, terminalID := uuid.New(), uuid.New(), uuid.New()
	s.AddOrder(Order{ID: dueID, Status: "confirmed", NextAdvanceAt: now.Add(-time.Second)})
	s.AddOrder(Order{ID: notDueID, Status: "confirmed", NextAdvanceAt: now.Add(time.Hour)})
	s.AddOrder(Order{ID: terminalID, Status: "delivered"}) // zero NextAdvanceAt: never due

	due := s.DueOrders(now)

	require.Len(t, due, 1)
	assert.Equal(t, dueID, due[0].ID)
}

func TestAdvanceOrder_UpdatesStatusAndSchedule(t *testing.T) {
	s := NewState()
	orderID := uuid.New()
	s.AddOrder(Order{ID: orderID, Status: "confirmed"})

	next := time.Now().Add(time.Minute)
	s.AdvanceOrder(orderID, "cooking", next)

	got := s.GetOrder(orderID)
	assert.Equal(t, "cooking", got.Status)
	assert.WithinDuration(t, next, got.NextAdvanceAt, time.Millisecond)
}

func TestCancelOrder_ClearsPendingRetry(t *testing.T) {
	s := NewState()
	orderID := uuid.New()
	s.AddOrder(Order{ID: orderID, Status: "pending_accept", Pending: PendingAccept, NextRetryAt: time.Now().Add(time.Minute)})

	_, ok := s.CancelOrder(orderID)

	require.True(t, ok)
	got := s.GetOrder(orderID)
	assert.Equal(t, PendingNone, got.Pending, "a canceled order must not still be retried")
	assert.True(t, got.NextRetryAt.IsZero())
}

func TestDuePendingActions_OnlyReturnsWhatIsDueAndPending(t *testing.T) {
	s := NewState()
	dueID, notDueID, notPendingID := uuid.New(), uuid.New(), uuid.New()
	s.AddOrder(Order{ID: dueID, Pending: PendingAccept, NextRetryAt: time.Now().Add(-time.Second)})
	s.AddOrder(Order{ID: notDueID, Pending: PendingReject, NextRetryAt: time.Now().Add(time.Hour)})
	s.AddOrder(Order{ID: notPendingID, Status: "confirmed"})

	due := s.DuePendingActions(time.Now())

	require.Len(t, due, 1)
	assert.Equal(t, dueID, due[0].ID)
}

func TestResolvePending_ClearsPendingAndSetsStatus(t *testing.T) {
	s := NewState()
	orderID := uuid.New()
	s.AddOrder(Order{ID: orderID, Status: "pending_accept", Pending: PendingAccept, NextRetryAt: time.Now().Add(time.Minute)})

	next := time.Now().Add(time.Minute)
	s.ResolvePending(orderID, "confirmed", next)

	got := s.GetOrder(orderID)
	assert.Equal(t, "confirmed", got.Status)
	assert.Equal(t, PendingNone, got.Pending)
	assert.WithinDuration(t, next, got.NextAdvanceAt, time.Millisecond)
	assert.True(t, got.NextRetryAt.IsZero())
}

func TestScheduleRetry_UpdatesNextRetryAtOnly(t *testing.T) {
	s := NewState()
	orderID := uuid.New()
	s.AddOrder(Order{ID: orderID, Status: "pending_accept", Pending: PendingAccept})

	next := time.Now().Add(time.Minute)
	s.ScheduleRetry(orderID, next)

	got := s.GetOrder(orderID)
	assert.Equal(t, "pending_accept", got.Status)
	assert.Equal(t, PendingAccept, got.Pending)
	assert.WithinDuration(t, next, got.NextRetryAt, time.Millisecond)
}

func TestActiveOrderCount_ExcludesTerminalStatuses(t *testing.T) {
	s := NewState()
	s.AddOrder(Order{ID: uuid.New(), Status: "confirmed"})
	s.AddOrder(Order{ID: uuid.New(), Status: "cooking"})
	s.AddOrder(Order{ID: uuid.New(), Status: "delivered"})
	s.AddOrder(Order{ID: uuid.New(), Status: "rejected"})
	s.AddOrder(Order{ID: uuid.New(), Status: "canceled"})

	assert.Equal(t, 2, s.ActiveOrderCount())
}

func TestMarkEventSeen_EvictsOldestPastCapacity(t *testing.T) {
	s := newStateWithCap(2)
	a, b, c := uuid.New(), uuid.New(), uuid.New()

	assert.True(t, s.MarkEventSeen(a))
	assert.True(t, s.MarkEventSeen(b))
	assert.True(t, s.MarkEventSeen(c)) // pushes a out once the cap of 2 is exceeded

	// b and c checked first: MarkEventSeen on an already-seen id never
	// mutates seenOrder, so these two checks cannot themselves evict
	// anything. a is checked last on purpose — re-marking it as seen is
	// itself a mutation (it evicts b in turn), so it must not run before
	// the assertions that still depend on b being present.
	assert.False(t, s.MarkEventSeen(b), "b is still within the capacity window and must still dedupe")
	assert.False(t, s.MarkEventSeen(c), "c is still within the capacity window and must still dedupe")
	assert.True(t, s.MarkEventSeen(a), "a must have been evicted and so reads as new again")
}

// stockByID is a small test helper turning StockSnapshot's slice into a map
// keyed by platform id, since assertions here care about specific items,
// not the slice's (unspecified) order.
func stockByID(s *State) map[uuid.UUID]int {
	out := make(map[uuid.UUID]int)
	for _, mi := range s.StockSnapshot() {
		out[mi.PlatformID] = mi.StockQty
	}
	return out
}
