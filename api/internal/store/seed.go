package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// SeedState records that the dataset has been imported.
type SeedState struct {
	SeededAt  time.Time
	Users     int
	Accounts  int
	Movements int
}

// SeedState returns the import marker, or ErrNotFound if nothing has been seeded.
func (q *Queries) SeedState(ctx context.Context) (SeedState, error) {
	const query = `SELECT seeded_at, users, accounts, movements FROM seed_state WHERE id = 1`

	var s SeedState
	err := q.q.QueryRow(ctx, query).Scan(&s.SeededAt, &s.Users, &s.Accounts, &s.Movements)
	if err != nil {
		return SeedState{}, wrap("store.SeedState", err)
	}
	return s, nil
}

// MarkSeeded writes the import marker.
//
// The insert is unconditional rather than an upsert: a second seed reaching this
// point means the guard at the start did not hold, and a unique-violation is a
// better outcome than silently overwriting the record of the first import.
func (q *Queries) MarkSeeded(ctx context.Context, s SeedState) error {
	const query = `
		INSERT INTO seed_state (id, users, accounts, movements)
		VALUES (1, $1, $2, $3)`

	_, err := q.q.Exec(ctx, query, s.Users, s.Accounts, s.Movements)
	return wrap("store.MarkSeeded", err)
}

// seedBatchSize is how many rows go into one pipelined batch. Large enough that
// the round trips disappear, small enough that a failure names a narrow range.
const seedBatchSize = 500

// InsertUsers bulk-inserts seed users, preserving their original creation dates.
func (q *Queries) InsertUsers(ctx context.Context, users []User) error {
	const query = `
		INSERT INTO users (id, email, password_hash, full_name, created_at)
		VALUES ($1, $2, $3, $4, $5)`

	return q.inBatches(ctx, "store.InsertUsers", len(users), func(b *pgx.Batch, i int) {
		u := users[i]
		b.Queue(query, u.ID, u.Email, u.PasswordHash, u.FullName, u.CreatedAt)
	})
}

// InsertAccounts bulk-inserts seed accounts.
func (q *Queries) InsertAccounts(ctx context.Context, list []Account) error {
	const query = `
		INSERT INTO accounts (id, user_id, account_number, ledger_id, account_type, currency)
		VALUES ($1, $2, $3, $4, $5, $6)`

	return q.inBatches(ctx, "store.InsertAccounts", len(list), func(b *pgx.Batch, i int) {
		a := list[i]
		b.Queue(query, a.ID, a.UserID, a.Number, int64(a.LedgerID), a.Kind.String(), a.Currency)
	})
}

// InsertTransactions bulk-inserts the imported movement history.
func (q *Queries) InsertTransactions(ctx context.Context, list []Transaction) error {
	const query = `
		INSERT INTO transactions (
			id, kind, status, amount_cents, currency, from_account, to_account,
			description, source, origin, occurred_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`

	return q.inBatches(ctx, "store.InsertTransactions", len(list), func(b *pgx.Batch, i int) {
		t := list[i]
		b.Queue(query,
			t.ID, t.Kind.String(), string(t.Status), int64(t.Amount), t.Currency,
			nullableText(t.FromAccount), nullableText(t.ToAccount),
			t.Description, t.Source, t.Origin, t.OccurredAt)
	})
}

// inBatches queues n rows in chunks and reports the first failure with the row
// that caused it.
func (q *Queries) inBatches(ctx context.Context, op string, n int, queue func(*pgx.Batch, int)) error {
	for start := 0; start < n; start += seedBatchSize {
		end := min(start+seedBatchSize, n)

		batch := &pgx.Batch{}
		for i := start; i < end; i++ {
			queue(batch, i)
		}

		results := q.q.SendBatch(ctx, batch)
		for i := start; i < end; i++ {
			if _, err := results.Exec(); err != nil {
				// Close before returning, or the connection is left mid-batch and
				// every later query on it fails with a confusing error.
				return errors.Join(
					fmt.Errorf("%s: row %d: %w", op, i, wrap(op, err)),
					results.Close(),
				)
			}
		}
		if err := results.Close(); err != nil {
			return wrap(op, err)
		}
	}
	return nil
}
