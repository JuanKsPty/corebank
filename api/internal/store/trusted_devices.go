package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// TrustedDevice is a device that may unlock a session with a PIN instead of a
// password. Only the hash of the device token is stored, for the same reason
// only the hash of a refresh token is: a database dump must not be replayable
// as a live device.
type TrustedDevice struct {
	ID              uuid.UUID
	UserID          uuid.UUID
	DeviceTokenHash string
	CreatedAt       time.Time
	LastUsedAt      time.Time
	FailedAttempts  int
	ExpiresAt       time.Time
}

// CreateTrustedDevice records a newly trusted device.
func (q *Queries) CreateTrustedDevice(ctx context.Context, userID uuid.UUID, hash string, expiresAt time.Time) error {
	const query = `
		INSERT INTO trusted_devices (id, user_id, device_token_hash, expires_at)
		VALUES ($1, $2, $3, $4)`

	_, err := q.q.Exec(ctx, query, uuid.New(), userID, hash, expiresAt)
	return wrap("store.CreateTrustedDevice", err)
}

// TrustedDeviceByHash looks up a device for a PIN login attempt. An expired
// device is reported as ErrNotFound, the same as one that never existed — the
// two look identical to whoever is asking.
func (q *Queries) TrustedDeviceByHash(ctx context.Context, hash string, now time.Time) (TrustedDevice, error) {
	const query = `
		SELECT id, user_id, device_token_hash, created_at, last_used_at, failed_attempts, expires_at
		FROM trusted_devices WHERE device_token_hash = $1 AND expires_at > $2`

	var d TrustedDevice
	err := q.q.QueryRow(ctx, query, hash, now).Scan(
		&d.ID, &d.UserID, &d.DeviceTokenHash, &d.CreatedAt, &d.LastUsedAt, &d.FailedAttempts, &d.ExpiresAt)
	if err != nil {
		return TrustedDevice{}, wrap("store.TrustedDeviceByHash", err)
	}
	return d, nil
}

// TouchTrustedDevice records a successful PIN login: the failure count clears,
// so a mistyped digit weeks ago does not count against a customer who just got
// it right.
func (q *Queries) TouchTrustedDevice(ctx context.Context, id uuid.UUID, now time.Time) error {
	const query = `
		UPDATE trusted_devices SET last_used_at = $2, failed_attempts = 0 WHERE id = $1`

	_, err := q.q.Exec(ctx, query, id, now)
	return wrap("store.TouchTrustedDevice", err)
}

// IncrementTrustedDeviceFailures records one wrong PIN and returns the new
// total, so the caller can decide whether this device has had enough chances.
func (q *Queries) IncrementTrustedDeviceFailures(ctx context.Context, id uuid.UUID) (int, error) {
	const query = `
		UPDATE trusted_devices SET failed_attempts = failed_attempts + 1
		WHERE id = $1
		RETURNING failed_attempts`

	var attempts int
	err := q.q.QueryRow(ctx, query, id).Scan(&attempts)
	if err != nil {
		return 0, wrap("store.IncrementTrustedDeviceFailures", err)
	}
	return attempts, nil
}

// RevokeTrustedDevice removes one device's trust, scoped to the user it
// belongs to. Idempotent and silent about whether the device ever existed —
// the caller (disabling PIN access, or the login handler giving up on a
// device that failed too many times) never needs to know which.
func (q *Queries) RevokeTrustedDevice(ctx context.Context, hash string, userID uuid.UUID) error {
	const query = `
		DELETE FROM trusted_devices WHERE device_token_hash = $1 AND user_id = $2`

	_, err := q.q.Exec(ctx, query, hash, userID)
	return wrap("store.RevokeTrustedDevice", err)
}

// RevokeTrustedDeviceByID is what a device revokes itself with once it has
// failed too many PIN attempts in a row — the login handler has the row's id
// already, and does not have the raw token to hash again.
func (q *Queries) RevokeTrustedDeviceByID(ctx context.Context, id uuid.UUID) error {
	const query = `DELETE FROM trusted_devices WHERE id = $1`

	_, err := q.q.Exec(ctx, query, id)
	return wrap("store.RevokeTrustedDeviceByID", err)
}

// RevokeUserTrustedDevices ends every device's trust for a user. Removing a
// PIN calls this: a device trusted for a PIN that no longer exists is not a
// state worth allowing even momentarily.
func (q *Queries) RevokeUserTrustedDevices(ctx context.Context, userID uuid.UUID) error {
	const query = `DELETE FROM trusted_devices WHERE user_id = $1`

	_, err := q.q.Exec(ctx, query, userID)
	return wrap("store.RevokeUserTrustedDevices", err)
}

// DeleteExpiredTrustedDevices prunes rows that can no longer authenticate
// anything, the same housekeeping refresh_tokens gets.
func (q *Queries) DeleteExpiredTrustedDevices(ctx context.Context, before time.Time) (int64, error) {
	const query = `DELETE FROM trusted_devices WHERE expires_at < $1`

	tag, err := q.q.Exec(ctx, query, before)
	if err != nil {
		return 0, wrap("store.DeleteExpiredTrustedDevices", err)
	}
	return tag.RowsAffected(), nil
}
