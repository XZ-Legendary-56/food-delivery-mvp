package partner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/google/uuid"
)

// AuthService resolves the venue behind a partner API key (docs/PLAN.md 5.3
// item 1, 9: partner_api_keys.key_hash).
type AuthService struct {
	keys APIKeyRepository
}

func NewAuthService(keys APIKeyRepository) *AuthService {
	return &AuthService{keys: keys}
}

// Authenticate hashes rawKey the same way cmd/seed's demo keys are hashed
// (sha256, hex-encoded — see cmd/seed/seed.sql) and resolves it to a venue.
func (s *AuthService) Authenticate(ctx context.Context, rawKey string) (uuid.UUID, error) {
	return s.keys.ResolveVenueByKeyHash(ctx, HashAPIKey(rawKey))
}

// HashAPIKey is the one place this hash is computed, so authentication and
// any future key-issuing code can never drift apart on the algorithm.
func HashAPIKey(rawKey string) string {
	sum := sha256.Sum256([]byte(rawKey))
	return hex.EncodeToString(sum[:])
}
