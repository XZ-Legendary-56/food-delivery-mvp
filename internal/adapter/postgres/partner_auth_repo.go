package postgres

import (
	"context"
	"errors"
	"fmt"
	"food-delivery-mvp/internal/domain/errs"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	partnerusecase "food-delivery-mvp/internal/usecase/partner"
)

// PartnerAuthRepository implements partnerusecase.APIKeyRepository on
// partner_api_keys joined to venues.
type PartnerAuthRepository struct {
	pool *pgxpool.Pool
}

func NewPartnerAuthRepository(pool *pgxpool.Pool) *PartnerAuthRepository {
	return &PartnerAuthRepository{pool: pool}
}

var _ partnerusecase.APIKeyRepository = (*PartnerAuthRepository)(nil)

// ResolveVenueByKeyHash joins through partner_id to find the one venue that
// belongs to the key's partner (this project's own assumption: one partner
// owns exactly one venue — see cmd/seed/seed.sql and the README's
// "Допущения и упрощения"). A revoked key never matches.
func (r *PartnerAuthRepository) ResolveVenueByKeyHash(ctx context.Context, keyHash string) (uuid.UUID, error) {
	q := QuerierFromContext(ctx, r.pool)

	var venueID uuid.UUID
	err := q.QueryRow(ctx, `
		SELECT v.id
		FROM partner_api_keys k
		JOIN venues v ON v.partner_id = k.partner_id
		WHERE k.key_hash = $1 AND k.revoked_at IS NULL
	`, keyHash).Scan(&venueID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, errs.New(errs.CodeUnauthorized, "invalid or revoked API key")
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("resolve venue by api key: %w", err)
	}
	return venueID, nil
}
