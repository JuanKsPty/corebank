package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// RefreshToken is a session record. Only the hash of the token is stored, so a
// database dump cannot be replayed as a set of live sessions.
type RefreshToken struct {
	TokenHash string
	UserID    uuid.UUID
	IssuedAt  time.Time
	ExpiresAt time.Time
	RevokedAt *time.Time
}

// StoreRefreshToken records a newly issued session.
func (q *Queries) StoreRefreshToken(ctx context.Context, hash string, userID uuid.UUID, expiresAt time.Time) error {
	const query = `
		INSERT INTO refresh_tokens (token_hash, user_id, expires_at)
		VALUES ($1, $2, $3)`

	_, err := q.q.Exec(ctx, query, hash, userID, expiresAt)
	return wrap("store.StoreRefreshToken", err)
}

// ConsumeRefreshToken atomically revokes a token and returns whose it was.
//
// Revoke-and-read in one statement is what makes refresh-token rotation safe. If
// the check and the revocation were separate statements, two concurrent refreshes
// with the same token could both pass the check and both mint a session — which
// is precisely the replay a stolen token would attempt. Here the second one
// finds no unrevoked row and gets ErrNotFound.
//
// The WHERE clause also covers expiry and prior revocation, so an expired or
// reused token is indistinguishable from an unknown one to the caller.
func (q *Queries) ConsumeRefreshToken(ctx context.Context, hash string, now time.Time) (uuid.UUID, error) {
	const query = `
		UPDATE refresh_tokens
		SET revoked_at = $2
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > $2
		RETURNING user_id`

	var userID uuid.UUID
	if err := q.q.QueryRow(ctx, query, hash, now).Scan(&userID); err != nil {
		return uuid.Nil, wrap("store.ConsumeRefreshToken", err)
	}
	return userID, nil
}

// RevokeRefreshToken ends one session, for logout.
//
// A token that is already revoked, expired or unknown reports no error: logout
// is idempotent, and telling a caller which of those it was would leak whether
// the token was ever real.
func (q *Queries) RevokeRefreshToken(ctx context.Context, hash string, now time.Time) error {
	const query = `
		UPDATE refresh_tokens SET revoked_at = $2
		WHERE token_hash = $1 AND revoked_at IS NULL`

	_, err := q.q.Exec(ctx, query, hash, now)
	return wrap("store.RevokeRefreshToken", err)
}

// RevokeUserRefreshTokens ends every session a user has. Not wired to an
// endpoint yet; it is what a password change must call.
func (q *Queries) RevokeUserRefreshTokens(ctx context.Context, userID uuid.UUID, now time.Time) error {
	const query = `
		UPDATE refresh_tokens SET revoked_at = $2
		WHERE user_id = $1 AND revoked_at IS NULL`

	_, err := q.q.Exec(ctx, query, userID, now)
	return wrap("store.RevokeUserRefreshTokens", err)
}

// DeleteExpiredRefreshTokens prunes rows that can no longer authenticate
// anything. Called periodically so the table does not grow without bound.
func (q *Queries) DeleteExpiredRefreshTokens(ctx context.Context, before time.Time) (int64, error) {
	const query = `DELETE FROM refresh_tokens WHERE expires_at < $1`

	tag, err := q.q.Exec(ctx, query, before)
	if err != nil {
		return 0, wrap("store.DeleteExpiredRefreshTokens", err)
	}
	return tag.RowsAffected(), nil
}
