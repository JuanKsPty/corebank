package store

import (
	"context"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/civil"
)

// TransferCandidate is a pair of movements in two of the user's accounts
// that look like one transfer: opposite amounts a few days apart.
type TransferCandidate struct {
	Out Entry
	In  Entry
}

// TransferCandidates finds unmatched pairs: equal and opposite amounts, in
// different accounts of the same user, booked within window days of each
// other, not both already transfers, and never rejected. from and to bound
// the dates considered; zero means unbounded.
func (q *Queries) TransferCandidates(ctx context.Context, userID uuid.UUID, window int, from, to civil.Date) ([]TransferCandidate, error) {
	rows, err := q.q.Query(ctx, `
		SELECT `+prefixed("o.", entryColumns)+`, `+prefixed("i.", entryColumns)+`
		FROM entries o
		JOIN entries i ON i.user_id = o.user_id AND i.account_id <> o.account_id
		              AND i.amount_cents = -o.amount_cents
		              AND abs(i.booked_on - o.booked_on) <= $2
		WHERE o.user_id = $1 AND o.amount_cents < 0
		  AND COALESCE(o.user_kind, o.kind) <> 'trade' AND COALESCE(i.user_kind, i.kind) <> 'trade'
		  AND NOT (COALESCE(o.user_kind, o.kind) = 'transfer' AND COALESCE(i.user_kind, i.kind) = 'transfer')
		  AND ($3::date IS NULL OR o.booked_on >= $3 OR i.booked_on >= $3)
		  AND ($4::date IS NULL OR o.booked_on <= $4 OR i.booked_on <= $4)
		  AND NOT EXISTS (SELECT 1 FROM transfer_decisions d
		                  WHERE d.out_entry_id = o.id AND d.in_entry_id = i.id)
		ORDER BY o.booked_on DESC, o.id`, userID, window, from, to)
	if err != nil {
		return nil, wrap("store.TransferCandidates", err)
	}
	defer rows.Close()
	var out []TransferCandidate
	for rows.Next() {
		var c TransferCandidate
		o, i, err := scanEntryPair(rows)
		if err != nil {
			return nil, wrap("store.TransferCandidates", err)
		}
		c.Out, c.In = o, i
		out = append(out, c)
	}
	return out, wrap("store.TransferCandidates", rows.Err())
}

// RecordTransferDecision stores a decision about a pair.
func (q *Queries) RecordTransferDecision(ctx context.Context, userID, outID, inID uuid.UUID, decision, by string) error {
	_, err := q.q.Exec(ctx, `
		INSERT INTO transfer_decisions (id, user_id, out_entry_id, in_entry_id, decision, decided_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (out_entry_id, in_entry_id) DO UPDATE SET decision = EXCLUDED.decision,
		  decided_by = EXCLUDED.decided_by, created_at = now()`,
		uuid.New(), userID, outID, inID, decision, by)
	return wrap("store.RecordTransferDecision", err)
}

// MarkTransfer sets an entry's effective kind to transfer, unless its
// importer already said so.
func (q *Queries) MarkTransfer(ctx context.Context, userID, id uuid.UUID) error {
	_, err := q.q.Exec(ctx, `UPDATE entries SET user_kind = CASE WHEN kind = 'transfer' THEN NULL ELSE 'transfer'::entry_kind END
		WHERE id = $1 AND user_id = $2`, id, userID)
	return wrap("store.MarkTransfer", err)
}

func scanEntryPair(s scanner) (Entry, Entry, error) {
	var a, b Entry
	var aAmount, bAmount int64
	var aBank, bBank *int64
	var aRaw, bRaw []byte
	err := s.Scan(
		&a.ID, &a.UserID, &a.AccountID, &a.StatementID, &a.RunID, &a.BookedOn, &a.BookedAt, &a.PostedOn,
		&a.BalanceOn, &a.Seq, &aAmount, &a.Kind, &a.UserKind, &a.Description, &a.BankRef, &a.BankCategory,
		&aBank, &a.CategoryID, &a.CategorySource, &a.Note, &a.DedupKey, &aRaw, &a.CreatedAt,
		&b.ID, &b.UserID, &b.AccountID, &b.StatementID, &b.RunID, &b.BookedOn, &b.BookedAt, &b.PostedOn,
		&b.BalanceOn, &b.Seq, &bAmount, &b.Kind, &b.UserKind, &b.Description, &b.BankRef, &b.BankCategory,
		&bBank, &b.CategoryID, &b.CategorySource, &b.Note, &b.DedupKey, &bRaw, &b.CreatedAt)
	if err != nil {
		return Entry{}, Entry{}, err
	}
	finish(&a, aAmount, aBank, aRaw)
	finish(&b, bAmount, bBank, bRaw)
	return a, b, nil
}
