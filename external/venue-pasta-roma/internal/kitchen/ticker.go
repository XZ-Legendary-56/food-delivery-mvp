package kitchen

import (
	"context"
	"log/slog"
	"time"
	"venue-pasta-roma/internal/generated/partnerclient"
)

// statusSequence is this emulator's own timer-driven half of the order
// state machine (docs/PLAN.md 8.2 item 3: "по таймеру двигает статусы cooking
// → ready → delivering → delivered"). It only needs to know the platform's
// allowed next step, not reimplement the platform's full state machine —
// the platform's own table (domain/order/status.go) is still what actually
// enforces each transition.
var statusSequence = map[string]string{
	"confirmed":  "cooking",
	"cooking":    "ready",
	"ready":      "delivering",
	"delivering": "delivered",
}

// RunStatusAdvancer polls State for orders due to move to their next status
// and advances each through the platform's own partner API, and on the same
// tick retries any accept/reject call that previously failed (see
// retryPendingActions) — one background loop for both, since they share the
// same "come back and try the platform again" shape.
func RunStatusAdvancer(ctx context.Context, client *partnerclient.ClientWithResponses, state *State, interval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			advanceDueOrders(ctx, client, state, interval, logger)
			retryPendingActions(ctx, client, state, interval, logger)
		}
	}
}

func advanceDueOrders(ctx context.Context, client *partnerclient.ClientWithResponses, state *State, cookStep time.Duration, logger *slog.Logger) {
	for _, o := range state.DueOrders(time.Now()) {
		next, ok := statusSequence[o.Status]
		if !ok {
			continue
		}

		resp, err := client.AdvanceOrderStatusWithResponse(ctx, o.ID, partnerclient.AdvanceOrderStatusJSONRequestBody{
			Status: partnerclient.AdvanceOrderStatusRequestStatus(next),
		})
		if err != nil || resp.JSON200 == nil {
			logger.Error("advance order status failed", "order_id", o.ID, "to", next, "error", err)
			continue
		}

		nextAdvanceAt := time.Now().Add(cookStep)
		if next == "delivered" {
			nextAdvanceAt = time.Time{}
		}
		state.AdvanceOrder(o.ID, next, nextAdvanceAt)
		logger.Info("order advanced", "order_id", o.ID, "status", next)
	}
}

// retryPendingActions retries every order whose accept/reject call
// previously failed (State.DuePendingActions). A response of JSON200 OR
// JSON409 both count as resolved: 409 means the platform already knows
// about this order in some state, which — since this service only ever
// sends one decision per order — can only mean an earlier attempt actually
// landed and just lost its response, not a real conflict.
func retryPendingActions(ctx context.Context, client *partnerclient.ClientWithResponses, state *State, retryInterval time.Duration, logger *slog.Logger) {
	for _, o := range state.DuePendingActions(time.Now()) {
		switch o.Pending {
		case PendingAccept:
			resp, err := client.AcceptOrderWithResponse(ctx, o.ID, partnerclient.AcceptOrderJSONRequestBody{
				EtaMinutes:      o.ETAMinutes,
				ExternalOrderId: &o.ExternalOrderID,
			})
			if err == nil && (resp.JSON200 != nil || resp.JSON409 != nil) {
				state.ResolvePending(o.ID, "confirmed", time.Now().Add(retryInterval))
				logger.Info("pending accept resolved on retry", "order_id", o.ID)
				continue
			}
			state.ScheduleRetry(o.ID, time.Now().Add(retryInterval))
			logger.Error("retry accept failed", "order_id", o.ID, "error", err)

		case PendingReject:
			resp, err := client.RejectOrderWithResponse(ctx, o.ID, partnerclient.RejectOrderJSONRequestBody{Reason: o.RejectionReason})
			if err == nil && (resp.JSON200 != nil || resp.JSON409 != nil) {
				state.ResolvePending(o.ID, "rejected", time.Time{})
				logger.Info("pending reject resolved on retry", "order_id", o.ID)
				continue
			}
			state.ScheduleRetry(o.ID, time.Now().Add(retryInterval))
			logger.Error("retry reject failed", "order_id", o.ID, "error", err)
		}
	}
}

// RunStockSync periodically pushes this venue's own stock numbers to the
// platform (docs/PLAN.md 8.2 item 4: "раз в 30 секунд синхронизирует остатки
// в платформу") until ctx is canceled.
func RunStockSync(ctx context.Context, client *partnerclient.ClientWithResponses, state *State, interval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			syncStock(ctx, client, state, logger)
		}
	}
}

func syncStock(ctx context.Context, client *partnerclient.ClientWithResponses, state *State, logger *slog.Logger) {
	items := state.StockSnapshot()
	if len(items) == 0 {
		return
	}

	updates := make([]partnerclient.AvailabilityUpdate, len(items))
	for i, mi := range items {
		isAvailable := mi.StockQty > 0
		stockQty := mi.StockQty
		updates[i] = partnerclient.AvailabilityUpdate{
			MenuItemId:  mi.PlatformID,
			IsAvailable: &isAvailable,
			StockQty:    &stockQty,
		}
	}

	resp, err := client.UpdateAvailabilityWithResponse(ctx, partnerclient.UpdateAvailabilityJSONRequestBody{Items: updates})
	if err != nil || resp.JSON200 == nil {
		logger.Error("periodic stock sync failed", "error", err)
		return
	}
	logger.Info("stock synced to platform", "items", len(updates))
}
