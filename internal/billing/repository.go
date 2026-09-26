package billing

import (
	"errors"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func sameID(a, b string) bool { return strings.EqualFold(a, b) }

func databaseError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505", "23P01":
			return ErrConflict
		case "23503":
			return ErrNotFound
		case "22003", "23514":
			return ErrInvalidMeter
		}
	}
	return err
}

const periodColumns = `id::text,membership_id::text,user_id::text,starts_at,ends_at,quota_bytes,uploaded_bytes,downloaded_bytes,charged_bytes,status`

func scanPeriod(row pgx.Row) (Period, error) {
	var p Period
	err := row.Scan(&p.ID, &p.MembershipID, &p.UserID, &p.StartsAt, &p.EndsAt, &p.QuotaBytes, &p.UploadedBytes, &p.DownloadedBytes, &p.ChargedBytes, &p.Status)
	return p, databaseError(err)
}
