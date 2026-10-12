package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	orderusecase "food-delivery-mvp/internal/usecase/order"
)

// IdempotencyRepository implements orderusecase.IdempotencyRepository on
// idempotency_keys.
type IdempotencyRepository struct {
	pool *pgxpool.Pool
}

func NewIdempotencyRepository(pool *pgxpool.Pool) *IdempotencyRepository {
	return &IdempotencyRepository{pool: pool}
}

var _ orderusecase.IdempotencyRepository = (*IdempotencyRepository)(nil)

// Claim first tries to INSERT the key with order_id left NULL; ON CONFLICT
// DO NOTHING means that insert simply affects zero rows if (clientID, key)
// already exists, rather than erroring — that is how this tells "I'm the
// first" from "someone got here already" without a separate existence
// check up front. Only on that conflict path does it read back what is
// already there.
//
// Postgres itself resolves the concurrent-duplicate-request case: if
// another transaction has inserted the same (clientID, key) but not yet
// committed or rolled back, this INSERT blocks until it does. So by the
// time RowsAffected() is checked here, either that other transaction
// committed (and the SELECT below sees its final, fully-linked row — see
// LinkOrder's doc comment for why it is guaranteed to be linked by then),
// or it rolled back (and this INSERT itself just succeeded, reported as
// RowsAffected() > 0). No extra locking is needed on top of what the
// unique index already provides.
func (r *IdempotencyRepository) Claim(ctx context.Context, clientID uuid.UUID, key, requestHash string, expiresAt time.Time) (orderusecase.IdempotencyClaim, error) {
	q := QuerierFromContext(ctx, r.pool)

	tag, err := q.Exec(ctx, `
		INSERT INTO idempotency_keys (id, client_id, key, request_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (client_id, key) DO NOTHING
	`, uuid.New(), clientID, key, requestHash, expiresAt)
	if err != nil {
		return orderusecase.IdempotencyClaim{}, fmt.Errorf("insert idempotency key: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return orderusecase.IdempotencyClaim{Claimed: true}, nil
	}

	var existingHash string
	var existingOrderID *uuid.UUID
	err = q.QueryRow(ctx, `
		SELECT request_hash, order_id FROM idempotency_keys WHERE client_id = $1 AND key = $2
	`, clientID, key).Scan(&existingHash, &existingOrderID)
	if errors.Is(err, pgx.ErrNoRows) {
		// Vanishingly unlikely (the row we just failed to insert would have
		// to be deleted by something else in between), but report it as a
		// plain error rather than pretend we know what happened.
		return orderusecase.IdempotencyClaim{}, fmt.Errorf("idempotency key %s disappeared between insert and read", key)
	}
	if err != nil {
		return orderusecase.IdempotencyClaim{}, fmt.Errorf("read existing idempotency key: %w", err)
	}

	claim := orderusecase.IdempotencyClaim{Claimed: false, HashMatches: existingHash == requestHash}
	if existingOrderID != nil {
		claim.OrderID = *existingOrderID
	}
	return claim, nil
}

// LinkOrder sets order_id on an already-claimed key.
func (r *IdempotencyRepository) LinkOrder(ctx context.Context, clientID uuid.UUID, key string, orderID uuid.UUID) error {
	q := QuerierFromContext(ctx, r.pool)

	tag, err := q.Exec(ctx, `
		UPDATE idempotency_keys SET order_id = $1 WHERE client_id = $2 AND key = $3
	`, orderID, clientID, key)
	if err != nil {
		return fmt.Errorf("link order to idempotency key: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("idempotency key %s not found when linking order %s", key, orderID)
	}
	return nil
}
