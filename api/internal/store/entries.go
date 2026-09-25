package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/civil"
	"github.com/JuanKsPty/corebank/api/internal/money"
)

// Entry kinds. Reports read the effective kind: UserKind when set, Kind
// otherwise.
const (
	KindIncome   = "income"
	KindExpense  = "expense"
	KindRefund   = "refund"
	KindFee      = "fee"
	KindInterest = "interest"
	KindTransfer = "transfer"
	KindTrade    = "trade"
)

// SpendKinds are what counts as spending. A refund is positive and so reduces
// the total; transfers and trades never count.
var SpendKinds = []string{KindExpense, KindFee, KindInterest, KindRefund}

// IncomeKinds are what counts as income.
var IncomeKinds = []string{KindIncome}

// Entry is one movement a statement or IBKR printed.
type Entry struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	AccountID   uuid.UUID
	StatementID *uuid.UUID
	RunID       *uuid.UUID
	BookedOn    civil.Date
	BookedAt    *time.Time
	PostedOn    *civil.Date
	BalanceOn   civil.Date
	Seq         int
	// Amount is signed from the holder's view: positive makes them richer.
	Amount         money.Cents
	Kind           string
	UserKind       *string
	Description    string
	BankRef        string
	BankCategory   string
	BankBalance    *money.Cents
	CategoryID     *uuid.UUID
	CategorySource *string
	Note           string
	DedupKey       string
	Raw            map[string]string
	CreatedAt      time.Time
}

// EffectiveKind is what reports treat the entry as.
func (e Entry) EffectiveKind() string {
	if e.UserKind != nil {
		return *e.UserKind
	}
	return e.Kind
}

const entryColumns = `id, user_id, account_id, statement_id, run_id, booked_on, booked_at, posted_on,
	balance_on, seq, amount_cents, kind, user_kind, description, bank_ref, bank_category,
	bank_balance_cents, category_id, category_source, note, dedup_key, raw, created_at`

// InsertEntryIfNew stores e unless the account already holds a line with its
// dedup key, in which case nothing changes and inserted is false.
func (q *Queries) InsertEntryIfNew(ctx context.Context, e Entry) (inserted bool, err error) {
	raw, err := json.Marshal(e.Raw)
	if err != nil {
		return false, fmt.Errorf("store.InsertEntryIfNew: %w", err)
	}
	var bankBalance *int64
	if e.BankBalance != nil {
		v := int64(*e.BankBalance)
		bankBalance = &v
	}
	const query = `
		INSERT INTO entries (id, user_id, account_id, statement_id, run_id, booked_on, booked_at, posted_on,
		                     seq, amount_cents, kind, description, bank_ref, bank_category,
		                     bank_balance_cents, category_id, category_source, dedup_key, raw, user_kind)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
		ON CONFLICT (account_id, dedup_key) DO NOTHING`
	tag, err := q.q.Exec(ctx, query, e.ID, e.UserID, e.AccountID, e.StatementID, e.RunID, e.BookedOn,
		e.BookedAt, e.PostedOn, e.Seq, int64(e.Amount), e.Kind, e.Description, e.BankRef, e.BankCategory,
		bankBalance, e.CategoryID, e.CategorySource, e.DedupKey, raw, e.UserKind)
	if err != nil {
		return false, wrap("store.InsertEntryIfNew", err)
	}
	return tag.RowsAffected() == 1, nil
}

// EntryIDByDedupKey finds the entry an account holds for a dedup key.
func (q *Queries) EntryIDByDedupKey(ctx context.Context, accountID uuid.UUID, key string) (uuid.UUID, error) {
	var id uuid.UUID
	err := q.q.QueryRow(ctx, `SELECT id FROM entries WHERE account_id = $1 AND dedup_key = $2`, accountID, key).Scan(&id)
	return id, wrap("store.EntryIDByDedupKey", err)
}

// EntryFilter selects entries for history, exports and reports. UserID is
// always required.
type EntryFilter struct {
	UserID     uuid.UUID
	AccountIDs []uuid.UUID
	// From and To bound booked_on, both inclusive; zero means unbounded.
	From, To civil.Date
	// Kinds restricts to these effective kinds.
	Kinds         []string
	CategoryIDs   []uuid.UUID
	Uncategorized bool
	Search        string
	// Before continues a newest-first listing after this cursor.
	Before *EntryCursor
	Limit  int
}

// EntryCursor is a position in the newest-first order.
type EntryCursor struct {
	BookedOn civil.Date
	ID       uuid.UUID
}

func (f EntryFilter) where() (string, []any, error) {
	if f.UserID == uuid.Nil {
		return "", nil, fmt.Errorf("store: an entry query needs a user")
	}
	conds := []string{"e.user_id = $1"}
	args := []any{f.UserID}
	add := func(cond string, v any) {
		args = append(args, v)
		conds = append(conds, strings.ReplaceAll(cond, "?", fmt.Sprintf("$%d", len(args))))
	}
	if len(f.AccountIDs) > 0 {
		add("e.account_id = ANY(?)", f.AccountIDs)
	}
	if !f.From.IsZero() {
		add("e.booked_on >= ?", f.From)
	}
	if !f.To.IsZero() {
		add("e.booked_on <= ?", f.To)
	}
	if len(f.Kinds) > 0 {
		add("COALESCE(e.user_kind, e.kind)::text = ANY(?)", f.Kinds)
	}
	switch {
	case len(f.CategoryIDs) > 0 && f.Uncategorized:
		add("(e.category_id = ANY(?) OR e.category_id IS NULL)", f.CategoryIDs)
	case len(f.CategoryIDs) > 0:
		add("e.category_id = ANY(?)", f.CategoryIDs)
	case f.Uncategorized:
		conds = append(conds, "e.category_id IS NULL")
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		args = append(args, "%"+escapeLike(s)+"%")
		conds = append(conds, fmt.Sprintf(
			"(e.description ILIKE $%[1]d OR e.note ILIKE $%[1]d OR e.bank_category ILIKE $%[1]d)", len(args)))
	}
	if f.Before != nil {
		args = append(args, f.Before.BookedOn, f.Before.ID)
		conds = append(conds, fmt.Sprintf("(e.booked_on, e.id) < ($%d, $%d)", len(args)-1, len(args)))
	}
	return strings.Join(conds, " AND "), args, nil
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Entries lists entries newest first.
func (q *Queries) Entries(ctx context.Context, f EntryFilter) ([]Entry, error) {
	where, args, err := f.where()
	if err != nil {
		return nil, err
	}
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 50
	}
	query := `SELECT ` + prefixed("e.", entryColumns) + ` FROM entries e WHERE ` + where +
		fmt.Sprintf(` ORDER BY e.booked_on DESC, e.id DESC LIMIT %d`, limit)
	return q.queryEntries(ctx, "store.Entries", query, args...)
}

// EachEntry streams every entry matching f, oldest first, for an export that
// must not load the whole history into memory at once.
func (q *Queries) EachEntry(ctx context.Context, f EntryFilter, fn func(Entry) error) error {
	where, args, err := f.where()
	if err != nil {
		return err
	}
	query := `SELECT ` + prefixed("e.", entryColumns) + ` FROM entries e WHERE ` + where +
		` ORDER BY e.booked_on, e.booked_at NULLS FIRST, e.seq, e.id`
	rows, err := q.q.Query(ctx, query, args...)
	if err != nil {
		return wrap("store.EachEntry", err)
	}
	defer rows.Close()
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return wrap("store.EachEntry", err)
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return wrap("store.EachEntry", rows.Err())
}

// AccountEntriesInOrder lists one account's entries in balance order, oldest
// first: the order a running balance is computed in.
func (q *Queries) AccountEntriesInOrder(ctx context.Context, userID, accountID uuid.UUID) ([]Entry, error) {
	query := `SELECT ` + entryColumns + ` FROM entries WHERE user_id = $1 AND account_id = $2
		ORDER BY ` + entryOrder
	return q.queryEntries(ctx, "store.AccountEntriesInOrder", query, userID, accountID)
}

// entryOrder is the order movements reach an account's balance. booked_at
// orders lines of the same day when the source gives a time; seq keeps the
// source's own order otherwise.
const entryOrder = `balance_on, booked_at NULLS FIRST, seq, id`

// EntryByID returns one of the user's entries.
func (q *Queries) EntryByID(ctx context.Context, userID, id uuid.UUID) (Entry, error) {
	rows, err := q.queryEntries(ctx, "store.EntryByID",
		`SELECT `+entryColumns+` FROM entries WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return Entry{}, err
	}
	if len(rows) == 0 {
		return Entry{}, wrap("store.EntryByID", ErrNotFound)
	}
	return rows[0], nil
}

// SetEntryCategory files an entry under a category, or clears it with nil.
func (q *Queries) SetEntryCategory(ctx context.Context, userID, id uuid.UUID, categoryID *uuid.UUID, source string) error {
	var src *string
	if categoryID != nil {
		src = &source
	}
	return q.execOne(ctx, "store.SetEntryCategory",
		`UPDATE entries SET category_id = $3, category_source = $4 WHERE id = $1 AND user_id = $2`,
		id, userID, categoryID, src)
}

// SetEntryNote sets an entry's note.
func (q *Queries) SetEntryNote(ctx context.Context, userID, id uuid.UUID, note string) error {
	return q.execOne(ctx, "store.SetEntryNote",
		`UPDATE entries SET note = $3 WHERE id = $1 AND user_id = $2`, id, userID, note)
}

// SetEntryUserKind overrides what an entry counts as, or restores the
// importer's classification with nil.
func (q *Queries) SetEntryUserKind(ctx context.Context, userID, id uuid.UUID, kind *string) error {
	return q.execOne(ctx, "store.SetEntryUserKind",
		`UPDATE entries SET user_kind = $3::entry_kind WHERE id = $1 AND user_id = $2`, id, userID, kind)
}

// AccountSums is one account's total and the date span of its movements.
type AccountSums struct {
	Total       money.Cents
	Count       int
	FirstDay    civil.Date
	LastDay     civil.Date
	LastBooking civil.Date
}

// SumsByAccount totals every entry of each of the user's accounts.
func (q *Queries) SumsByAccount(ctx context.Context, userID uuid.UUID) (map[uuid.UUID]AccountSums, error) {
	rows, err := q.q.Query(ctx, `
		SELECT account_id, COALESCE(SUM(amount_cents), 0), count(*), MIN(balance_on), MAX(balance_on), MAX(booked_on)
		FROM entries WHERE user_id = $1 GROUP BY account_id`, userID)
	if err != nil {
		return nil, wrap("store.SumsByAccount", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]AccountSums{}
	for rows.Next() {
		var id uuid.UUID
		var s AccountSums
		var total int64
		if err := rows.Scan(&id, &total, &s.Count, &s.FirstDay, &s.LastDay, &s.LastBooking); err != nil {
			return nil, wrap("store.SumsByAccount", err)
		}
		s.Total = money.Cents(total)
		out[id] = s
	}
	return out, wrap("store.SumsByAccount", rows.Err())
}

// SumThroughDay totals an account's entries that reached the balance on or
// before day.
func (q *Queries) SumThroughDay(ctx context.Context, accountID uuid.UUID, day civil.Date) (money.Cents, error) {
	var total int64
	err := q.q.QueryRow(ctx, `SELECT COALESCE(SUM(amount_cents), 0) FROM entries
		WHERE account_id = $1 AND balance_on <= $2`, accountID, day).Scan(&total)
	return money.Cents(total), wrap("store.SumThroughDay", err)
}

// SumBefore totals an account's entries that come strictly before the given
// entry in balance order.
func (q *Queries) SumBefore(ctx context.Context, accountID, entryID uuid.UUID) (money.Cents, error) {
	var total int64
	err := q.q.QueryRow(ctx, `
		SELECT COALESCE(SUM(e.amount_cents), 0) FROM entries e, entries x
		WHERE x.id = $2 AND e.account_id = $1
		  AND (e.balance_on, COALESCE(e.booked_at, '-infinity'::timestamptz), e.seq, e.id)
		    < (x.balance_on, COALESCE(x.booked_at, '-infinity'::timestamptz), x.seq, x.id)`,
		accountID, entryID).Scan(&total)
	return money.Cents(total), wrap("store.SumBefore", err)
}

func (q *Queries) queryEntries(ctx context.Context, op, query string, args ...any) ([]Entry, error) {
	rows, err := q.q.Query(ctx, query, args...)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, wrap(op, err)
		}
		out = append(out, e)
	}
	return out, wrap(op, rows.Err())
}

func scanEntry(s scanner) (Entry, error) {
	var (
		e           Entry
		amount      int64
		bankBalance *int64
		raw         []byte
	)
	err := s.Scan(&e.ID, &e.UserID, &e.AccountID, &e.StatementID, &e.RunID, &e.BookedOn, &e.BookedAt,
		&e.PostedOn, &e.BalanceOn, &e.Seq, &amount, &e.Kind, &e.UserKind, &e.Description, &e.BankRef,
		&e.BankCategory, &bankBalance, &e.CategoryID, &e.CategorySource, &e.Note, &e.DedupKey, &raw,
		&e.CreatedAt)
	if err != nil {
		return Entry{}, err
	}
	finish(&e, amount, bankBalance, raw)
	return e, nil
}

// finish converts the scanned raw columns into an Entry's typed fields.
func finish(e *Entry, amount int64, bankBalance *int64, raw []byte) {
	e.Amount = money.Cents(amount)
	if bankBalance != nil {
		b := money.Cents(*bankBalance)
		e.BankBalance = &b
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &e.Raw)
	}
}

// prefixed qualifies a comma-separated column list with a table alias.
func prefixed(alias, columns string) string {
	parts := strings.Split(columns, ",")
	for i, p := range parts {
		parts[i] = alias + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}
