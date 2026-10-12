// Package postgres holds the Postgres-backed implementations of the ports
// use-cases declare (repositories, TxManager). It depends on usecase and
// domain, never the other way round — see docs/PLAN.md section 6.2. Repository
// implementations are added alongside the use-case that declares their port,
// starting at stage 5/6; TxManager is added now because it is not owned by
// any single use-case.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"food-delivery-mvp/internal/usecase"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ctxKey int

const txKey ctxKey = iota

// Querier is the subset of pgx used by repositories, satisfied by both
// *pgxpool.Pool and pgx.Tx. A repository calls QuerierFromContext instead of
// holding a *pgxpool.Pool directly, so it transparently joins whatever
// transaction TxManager.WithinTx opened, or falls back to the pool outside
// of one.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// QuerierFromContext returns the transaction TxManager.WithinTx stashed in
// ctx, or pool if there isn't one.
func QuerierFromContext(ctx context.Context, pool *pgxpool.Pool) Querier {
	if tx, ok := ctx.Value(txKey).(pgx.Tx); ok {
		return tx
	}
	return pool
}

// TxManager implements usecase.TxManager on top of a pgxpool.Pool.
type TxManager struct {
	pool *pgxpool.Pool
}

var _ usecase.TxManager = (*TxManager)(nil)

// NewTxManager builds a TxManager over the given pool.
func NewTxManager(pool *pgxpool.Pool) *TxManager {
	return &TxManager{pool: pool}
}

// WithinTx runs fn inside a single transaction, committing if fn returns nil
// and rolling back otherwise. Nested calls (ctx already carries a
// transaction) reuse it instead of opening a second one, since pgx
// transactions cannot nest.
func (m *TxManager) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, alreadyInTx := ctx.Value(txKey).(pgx.Tx); alreadyInTx {
		return fn(ctx)
	}

	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	if err := fn(context.WithValue(ctx, txKey, tx)); err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			return fmt.Errorf("%w (rollback also failed: %w)", err, rbErr)
		}
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}
