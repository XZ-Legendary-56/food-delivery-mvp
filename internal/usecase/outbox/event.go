// Package outbox delivers events already written to outbox_events by other
// use-cases (order.created, order.cancelled — enqueued by usecase/order in
// the same transaction as the business write that caused them). docs/PLAN.md
// 6.5 is explicit that this exists independently of any message broker: a
// broker would still need this same table in front of it, since neither
// Postgres+broker nor Postgres+HTTP can be written atomically in one
// transaction. Swapping the delivery mechanism later means writing one new
// EventPublisher, not touching how events get produced.
package outbox

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Event is docs/PLAN.md 6.5's fixed envelope. Version exists so the format can
// change later without breaking whatever already consumes it; AggregateID
// is the future partition key (see the package doc and README's own
// "События и брокер сообщений" section for why order_id was chosen).
type Event struct {
	ID            uuid.UUID
	Type          string // e.g. "order.created"
	Version       int
	OccurredAt    time.Time
	AggregateType string // "order" for every event type this project emits
	AggregateID   uuid.UUID
	Payload       json.RawMessage
	// Attempts is how many deliveries have already failed for this event —
	// the Dispatcher uses it to pick the next backoff step (or give up).
	Attempts int
}
