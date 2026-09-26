package audit

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) List(ctx context.Context, limit int, cursor string) (Page, error) {
	if limit < 1 || limit > 200 {
		return Page{}, errors.New("audit page limit must be between 1 and 200")
	}
	var (
		query string
		args  []any
	)
	if cursor == "" {
		query = `SELECT a.id::text,a.actor_user_id::text,u.email,a.action,a.object_type,a.object_id::text,
a.request_id,a.created_at
FROM audit_logs a JOIN users u ON u.id=a.actor_user_id
ORDER BY a.created_at DESC,a.id DESC LIMIT $1`
		args = []any{limit + 1}
	} else {
		position, err := ParseCursor(cursor)
		if err != nil {
			return Page{}, err
		}
		query = `SELECT a.id::text,a.actor_user_id::text,u.email,a.action,a.object_type,a.object_id::text,
a.request_id,a.created_at
FROM audit_logs a JOIN users u ON u.id=a.actor_user_id
WHERE (a.created_at,a.id) < ($1,$2::uuid)
ORDER BY a.created_at DESC,a.id DESC LIMIT $3`
		args = []any{position.CreatedAt, position.ID, limit + 1}
	}
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return Page{}, fmt.Errorf("list audit logs: %w", err)
	}
	defer rows.Close()
	items := make([]Record, 0, limit)
	for rows.Next() {
		var item Record
		if err := rows.Scan(&item.ID, &item.ActorUserID, &item.ActorEmail, &item.Action, &item.ObjectType, &item.ObjectID,
			&item.RequestID, &item.CreatedAt); err != nil {
			return Page{}, fmt.Errorf("scan audit log: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("iterate audit logs: %w", err)
	}
	var next *string
	if len(items) > limit {
		last := items[limit-1]
		value := CursorFor(last)
		next = &value
		items = items[:limit]
	}
	if len(items) == 0 {
		items = []Record{}
	}
	return Page{Items: items, NextCursor: next}, nil
}

var _ interface {
	List(context.Context, int, string) (Page, error)
} = (*PostgresRepository)(nil)
