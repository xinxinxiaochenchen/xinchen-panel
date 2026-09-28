package identity

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SetupRepository uses a permanent marker, so disabling or removing the initial
// administrator cannot reopen public initialization.
type SetupRepository struct{ pool *pgxpool.Pool }

func NewSetupRepository(pool *pgxpool.Pool) *SetupRepository { return &SetupRepository{pool: pool} }

const adminInitializedQuery = `SELECT
EXISTS (SELECT 1 FROM installation_setup WHERE id=1 AND completed_at IS NOT NULL)
OR EXISTS (SELECT 1 FROM user_roles WHERE role_code='admin')`

func (s *SetupRepository) Required(ctx context.Context) (bool, error) {
	var complete bool
	err := s.pool.QueryRow(ctx, adminInitializedQuery).Scan(&complete)
	return !complete, err
}

func (s *SetupRepository) Create(ctx context.Context, email, password, requestID string) (PublicUser, bool, error) {
	return bootstrapAdmin(ctx, s.pool, email, password, true, requestID)
}
