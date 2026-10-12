// Package usecase holds ports shared across use-case packages (catalog,
// order, partner, outbox). Each use-case's own request/response types and
// repository ports still live in its own subpackage; only what's genuinely
// cross-cutting — right now just the transaction boundary — lives here.
package usecase

import "context"

// TxManager lets a use-case run several repository calls atomically without
// importing anything about pgx. adapter/postgres implements this by opening
// a transaction and stashing it in the context; repositories pull it back
// out if it's there, and fall back to the shared pool otherwise. See
// docs/PLAN.md section 6.4 for why: it keeps the use-case layer from knowing
// the database driver exists at all.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}
