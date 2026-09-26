package subscription

import (
	"context"
	"controlplane/internal/catalog"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"strings"
	"time"
)

func (r *PostgresRepository) RevealOwn(ctx context.Context, owner, subID string) (string, error) {
	owner, subID = strings.ToLower(owner), strings.ToLower(subID)
	if !catalog.ValidID(owner) || !catalog.ValidID(subID) {
		return "", ErrNotFound
	}
	var sealed string
	err := r.pool.QueryRow(ctx, `SELECT token_ciphertext FROM subscriptions WHERE id=$1 AND user_id=$2`, subID, owner).Scan(&sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return openToken(r.cipher, subID, owner, sealed)
}
func (r *PostgresRepository) ResolveToken(ctx context.Context, token string) (Subscription, error) {
	hash, err := TokenHash(token)
	if err != nil {
		return Subscription{}, ErrNotFound
	}
	sub, err := scanSubscription(r.pool.QueryRow(ctx, subscriptionSelect+` WHERE s.token_hash=$1 AND s.enabled`, hash))
	if err != nil {
		return Subscription{}, err
	}
	return sub, nil
}
func (r *PostgresRepository) RotateOwn(ctx context.Context, owner, subID, requestID string) (string, error) {
	owner, subID = strings.ToLower(owner), strings.ToLower(subID)
	if !catalog.ValidID(owner) || !catalog.ValidID(subID) {
		return "", ErrNotFound
	}
	token, err := GenerateToken()
	if err != nil {
		return "", err
	}
	hash, _ := TokenHash(token)
	sealed, err := sealToken(r.cipher, subID, owner, token)
	if err != nil {
		return "", err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if err := lockOwner(ctx, tx, owner); err != nil {
		return "", err
	}
	var exists bool
	err = tx.QueryRow(ctx, `SELECT true FROM subscriptions WHERE id=$1 AND user_id=$2 FOR UPDATE`, subID, owner).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE subscriptions SET token_hash=$2,token_ciphertext=$3,updated_at=clock_timestamp() WHERE id=$1`, subID, hash, sealed); err != nil {
		return "", databaseError(err)
	}
	sub, err := scanSubscription(tx.QueryRow(ctx, subscriptionSelect+` WHERE s.id=$1`, subID))
	if err != nil {
		return "", err
	}
	if err := audit(ctx, tx, sub, "rotate", requestID); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return token, nil
}
func (r *PostgresRepository) DeleteOwn(ctx context.Context, owner, subID, requestID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockOwner(ctx, tx, owner); err != nil {
		return err
	}
	sub, err := scanSubscription(tx.QueryRow(ctx, subscriptionSelect+` WHERE s.id=$1 AND s.user_id=$2 FOR UPDATE OF s`, subID, owner))
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM subscriptions WHERE id=$1`, subID); err != nil {
		return fmt.Errorf("delete subscription: %w", err)
	}
	if err := audit(ctx, tx, sub, "delete", requestID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *PostgresRepository) UpdateOwn(ctx context.Context, owner, subID string, patch Patch, requestID string) (Subscription, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Subscription{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockOwner(ctx, tx, owner); err != nil {
		return Subscription{}, err
	}
	before, err := scanSubscription(tx.QueryRow(ctx, subscriptionSelect+` WHERE s.id=$1 AND s.user_id=$2 FOR UPDATE OF s`, subID, owner))
	if err != nil {
		return Subscription{}, err
	}
	input := NewSubscription{Name: before.Name, NameTemplate: before.NameTemplate, ProxyAccessIDs: before.ProxyAccessIDs, RoutingProfileID: before.RoutingProfileID, Enabled: &before.Enabled}
	if patch.Name != nil {
		input.Name = *patch.Name
	}
	if patch.NameTemplate != nil {
		input.NameTemplate = *patch.NameTemplate
	}
	if patch.ProxyAccessIDs != nil {
		input.ProxyAccessIDs = *patch.ProxyAccessIDs
	}
	if patch.RoutingProfileID.Set {
		input.RoutingProfileID = patch.RoutingProfileID.Value
	}
	if patch.Enabled != nil {
		input.Enabled = patch.Enabled
	}
	normalized, err := normalize(input, patch.ProxyAccessIDs == nil && (patch.Enabled == nil || !*patch.Enabled))
	if err != nil {
		return Subscription{}, err
	}
	needsGrant := (normalized.Enabled && !before.Enabled) || patch.ProxyAccessIDs != nil
	var grantExpires time.Time
	if needsGrant {
		grant, ends, err := loadGrant(ctx, tx, owner)
		if err != nil {
			return Subscription{}, err
		}
		if err := authorizeTargets(ctx, tx, owner, normalized.ProxyAccessIDs, grant); err != nil {
			return Subscription{}, err
		}
		grantExpires = ends
	}
	if normalized.Enabled && normalized.RoutingProfileID != nil {
		if err := authorizeProfile(ctx, tx, owner, normalized.RoutingProfileID); err != nil {
			return Subscription{}, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE subscriptions SET name=$2,name_template=$3,routing_profile_id=$4,enabled=$5,updated_at=clock_timestamp() WHERE id=$1`, subID, normalized.Name, normalized.NameTemplate, normalized.RoutingProfileID, normalized.Enabled); err != nil {
		return Subscription{}, databaseError(err)
	}
	if patch.ProxyAccessIDs != nil {
		if err := replaceTargets(ctx, tx, subID, owner, normalized.ProxyAccessIDs); err != nil {
			return Subscription{}, err
		}
	}
	after, err := scanSubscription(tx.QueryRow(ctx, subscriptionSelect+` WHERE s.id=$1`, subID))
	if err != nil {
		return Subscription{}, err
	}
	if err := audit(ctx, tx, after, "update", requestID); err != nil {
		return Subscription{}, err
	}
	if !grantExpires.IsZero() {
		if err := checkExpiry(ctx, tx, grantExpires); err != nil {
			return Subscription{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Subscription{}, err
	}
	return after, nil
}
