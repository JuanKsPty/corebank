package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ImportedAccount is a bank account corebank has never verified — see the
// package doc on why its "balance" is never mixed into a TigerBeetle-derived
// figure.
type ImportedAccount struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	Institution   string
	AccountNumber string
	DisplayName   string
	Currency      string
	CreatedAt     time.Time
}

// Constraint names a caller may need to distinguish.
const (
	ImportedAccountsConstraint          = "external_accounts_user_id_institution_account_number_key"
	ExternalTransactionsDedupConstraint = "external_transactions_external_account_id_dedup_key_key"
)

// CreateImportedAccount registers a bank account a file's own AccountHint
// named, so the next import of the same account is recognised rather than
// creating a duplicate.
func (q *Queries) CreateImportedAccount(ctx context.Context, a ImportedAccount) (ImportedAccount, error) {
	const query = `
		INSERT INTO external_accounts (id, user_id, institution, account_number, display_name, currency)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at`

	err := q.q.QueryRow(ctx, query, a.ID, a.UserID, a.Institution, a.AccountNumber, a.DisplayName, a.Currency).
		Scan(&a.CreatedAt)
	if err != nil {
		return ImportedAccount{}, wrap("store.CreateImportedAccount", err)
	}
	return a, nil
}

// ImportedAccountByHint finds the account a previous import (or an explicit
// creation) already registered for this institution and account number.
func (q *Queries) ImportedAccountByHint(ctx context.Context, userID uuid.UUID, institution, accountNumber string) (ImportedAccount, error) {
	const query = `
		SELECT id, user_id, institution, account_number, display_name, currency, created_at
		FROM external_accounts WHERE user_id = $1 AND institution = $2 AND account_number = $3`

	a, err := scanImportedAccount(q.q.QueryRow(ctx, query, userID, institution, accountNumber))
	if err != nil {
		return ImportedAccount{}, wrap("store.ImportedAccountByHint", err)
	}
	return a, nil
}

// ImportedAccountsByUser lists every imported account a user has registered.
func (q *Queries) ImportedAccountsByUser(ctx context.Context, userID uuid.UUID) ([]ImportedAccount, error) {
	const query = `
		SELECT id, user_id, institution, account_number, display_name, currency, created_at
		FROM external_accounts WHERE user_id = $1 ORDER BY created_at`

	rows, err := q.q.Query(ctx, query, userID)
	if err != nil {
		return nil, wrap("store.ImportedAccountsByUser", err)
	}
	defer rows.Close()

	var out []ImportedAccount
	for rows.Next() {
		a, err := scanImportedAccount(rows)
		if err != nil {
			return nil, wrap("store.ImportedAccountsByUser", err)
		}
		out = append(out, a)
	}
	return out, wrap("store.ImportedAccountsByUser", rows.Err())
}

// ImportedAccountByID looks up an account regardless of owner; the service
// layer checks ownership, the same split accounts.Resolve makes.
func (q *Queries) ImportedAccountByID(ctx context.Context, id uuid.UUID) (ImportedAccount, error) {
	const query = `
		SELECT id, user_id, institution, account_number, display_name, currency, created_at
		FROM external_accounts WHERE id = $1`

	a, err := scanImportedAccount(q.q.QueryRow(ctx, query, id))
	if err != nil {
		return ImportedAccount{}, wrap("store.ImportedAccountByID", err)
	}
	return a, nil
}

func scanImportedAccount(s scanner) (ImportedAccount, error) {
	var a ImportedAccount
	if err := s.Scan(&a.ID, &a.UserID, &a.Institution, &a.AccountNumber, &a.DisplayName, &a.Currency, &a.CreatedAt); err != nil {
		return ImportedAccount{}, err
	}
	return a, nil
}

// --- import batches -----------------------------------------------------------

// ImportBatch records one file import: what it was, and what happened.
type ImportBatch struct {
	ID                uuid.UUID
	ImportedAccountID uuid.UUID
	Filename          string
	Format            string
	RowCount          int
	ImportedCount     int
	DuplicateCount    int
	CreatedAt         time.Time
}

// CreateImportBatch records a completed import. Written after the import
// finishes, not before, so a batch row always describes something that
// actually happened rather than an attempt that may have failed partway.
func (q *Queries) CreateImportBatch(ctx context.Context, b ImportBatch) (ImportBatch, error) {
	const query = `
		INSERT INTO import_batches (id, external_account_id, filename, format, row_count, imported_count, duplicate_count)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING created_at`

	err := q.q.QueryRow(ctx, query, b.ID, b.ImportedAccountID, b.Filename, b.Format,
		b.RowCount, b.ImportedCount, b.DuplicateCount).Scan(&b.CreatedAt)
	if err != nil {
		return ImportBatch{}, wrap("store.CreateImportBatch", err)
	}
	return b, nil
}

// UpdateImportBatchCounts records the outcome of an import after the rows
// have actually been written, so a batch row is created up front (to tag
// every row it produces with an import_batch_id) but only reports final
// counts once they are known.
func (q *Queries) UpdateImportBatchCounts(ctx context.Context, id uuid.UUID, imported, duplicates int) error {
	const query = `UPDATE import_batches SET imported_count = $2, duplicate_count = $3 WHERE id = $1`

	tag, err := q.q.Exec(ctx, query, id, imported, duplicates)
	if err != nil {
		return wrap("store.UpdateImportBatchCounts", err)
	}
	if tag.RowsAffected() == 0 {
		return wrap("store.UpdateImportBatchCounts", pgx.ErrNoRows)
	}
	return nil
}

// ImportBatchesByAccount lists an account's import history, most recent
// first.
func (q *Queries) ImportBatchesByAccount(ctx context.Context, accountID uuid.UUID) ([]ImportBatch, error) {
	const query = `
		SELECT id, external_account_id, filename, format, row_count, imported_count, duplicate_count, created_at
		FROM import_batches WHERE external_account_id = $1 ORDER BY created_at DESC`

	rows, err := q.q.Query(ctx, query, accountID)
	if err != nil {
		return nil, wrap("store.ImportBatchesByAccount", err)
	}
	defer rows.Close()

	var out []ImportBatch
	for rows.Next() {
		var b ImportBatch
		if err := rows.Scan(&b.ID, &b.ImportedAccountID, &b.Filename, &b.Format,
			&b.RowCount, &b.ImportedCount, &b.DuplicateCount, &b.CreatedAt); err != nil {
			return nil, wrap("store.ImportBatchesByAccount", err)
		}
		out = append(out, b)
	}
	return out, wrap("store.ImportBatchesByAccount", rows.Err())
}

// --- external transactions -----------------------------------------------------

// ExternalTransaction is one imported movement.
type ExternalTransaction struct {
	ID                uuid.UUID
	ImportedAccountID uuid.UUID
	ImportBatchID     *uuid.UUID
	DedupKey          string
	ExternalRef       string
	OccurredAt        time.Time
	AmountCents       int64
	Description       string
	CategoryID        *uuid.UUID
	Raw               map[string]string
}

// InsertExternalTransactionIfNew records an imported movement, reporting
// false rather than an error when this exact dedup key was already recorded
// — the normal case when an import's date range overlaps a previous one.
func (q *Queries) InsertExternalTransactionIfNew(ctx context.Context, t ExternalTransaction) (uuid.UUID, bool, error) {
	raw, err := json.Marshal(t.Raw)
	if err != nil {
		return uuid.Nil, false, wrap("store.InsertExternalTransactionIfNew", err)
	}

	const query = `
		INSERT INTO external_transactions
			(id, external_account_id, import_batch_id, dedup_key, external_ref, occurred_at, amount_cents, description, raw)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (external_account_id, dedup_key) DO NOTHING
		RETURNING id`

	var id uuid.UUID
	err = q.q.QueryRow(ctx, query, t.ID, t.ImportedAccountID, t.ImportBatchID, t.DedupKey,
		t.ExternalRef, t.OccurredAt, t.AmountCents, t.Description, raw).Scan(&id)
	if err != nil {
		if err == pgx.ErrNoRows {
			return uuid.Nil, false, nil
		}
		return uuid.Nil, false, wrap("store.InsertExternalTransactionIfNew", err)
	}
	return id, true, nil
}

// ExternalTransactionByID looks up one imported movement regardless of
// owner; the service layer checks ownership via the account it belongs to.
func (q *Queries) ExternalTransactionByID(ctx context.Context, id uuid.UUID) (ExternalTransaction, error) {
	const query = `
		SELECT id, external_account_id, import_batch_id, dedup_key, external_ref,
		       occurred_at, amount_cents, description, category_id, raw
		FROM external_transactions WHERE id = $1`

	t, err := scanExternalTransaction(q.q.QueryRow(ctx, query, id))
	if err != nil {
		return ExternalTransaction{}, wrap("store.ExternalTransactionByID", err)
	}
	return t, nil
}

// SetExternalTransactionCategory files an imported movement under one of the
// user's own categories, or clears it when categoryID is nil.
func (q *Queries) SetExternalTransactionCategory(ctx context.Context, id uuid.UUID, categoryID *uuid.UUID) error {
	const query = `UPDATE external_transactions SET category_id = $2 WHERE id = $1`

	tag, err := q.q.Exec(ctx, query, id, categoryID)
	if err != nil {
		return wrap("store.SetExternalTransactionCategory", err)
	}
	if tag.RowsAffected() == 0 {
		return wrap("store.SetExternalTransactionCategory", pgx.ErrNoRows)
	}
	return nil
}

// ExternalTransactionsByAccount lists an account's imported movements,
// most recent first, capped at limit — the same "a personal account's
// history is small enough" call investments.Trades makes.
func (q *Queries) ExternalTransactionsByAccount(ctx context.Context, accountID uuid.UUID, limit int) ([]ExternalTransaction, error) {
	const query = `
		SELECT id, external_account_id, import_batch_id, dedup_key, external_ref,
		       occurred_at, amount_cents, description, category_id, raw
		FROM external_transactions
		WHERE external_account_id = $1
		ORDER BY occurred_at DESC
		LIMIT $2`

	rows, err := q.q.Query(ctx, query, accountID, limit)
	if err != nil {
		return nil, wrap("store.ExternalTransactionsByAccount", err)
	}
	defer rows.Close()

	var out []ExternalTransaction
	for rows.Next() {
		t, err := scanExternalTransaction(rows)
		if err != nil {
			return nil, wrap("store.ExternalTransactionsByAccount", err)
		}
		out = append(out, t)
	}
	return out, wrap("store.ExternalTransactionsByAccount", rows.Err())
}

func scanExternalTransaction(s scanner) (ExternalTransaction, error) {
	var (
		t           ExternalTransaction
		importBatch *uuid.UUID
		categoryID  *uuid.UUID
		raw         []byte
	)
	if err := s.Scan(&t.ID, &t.ImportedAccountID, &importBatch, &t.DedupKey, &t.ExternalRef,
		&t.OccurredAt, &t.AmountCents, &t.Description, &categoryID, &raw); err != nil {
		return ExternalTransaction{}, err
	}
	t.ImportBatchID = importBatch
	t.CategoryID = categoryID
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &t.Raw)
	}
	return t, nil
}

// CategorySpend is one category's total over an external account's imported
// history, for a spend-by-category view.
type CategorySpend struct {
	CategoryID  *uuid.UUID
	AmountCents int64
}

// SpendByCategory sums signed amounts per category for an external account —
// negative totals are spend, positive are inflow, exactly like AmountCents'
// own sign convention.
func (q *Queries) SpendByCategory(ctx context.Context, accountID uuid.UUID) ([]CategorySpend, error) {
	const query = `
		SELECT category_id, sum(amount_cents)
		FROM external_transactions
		WHERE external_account_id = $1
		GROUP BY category_id`

	rows, err := q.q.Query(ctx, query, accountID)
	if err != nil {
		return nil, wrap("store.SpendByCategory", err)
	}
	defer rows.Close()

	var out []CategorySpend
	for rows.Next() {
		var s CategorySpend
		if err := rows.Scan(&s.CategoryID, &s.AmountCents); err != nil {
			return nil, wrap("store.SpendByCategory", err)
		}
		out = append(out, s)
	}
	return out, wrap("store.SpendByCategory", rows.Err())
}

// --- category rules -----------------------------------------------------------

// CategoryRule is a user's own "if this text appears, use this category"
// rule for auto-categorising imported movements.
type CategoryRule struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	MatchText  string
	CategoryID uuid.UUID
	Priority   int
}

// CreateCategoryRule adds a rule.
func (q *Queries) CreateCategoryRule(ctx context.Context, r CategoryRule) (CategoryRule, error) {
	const query = `
		INSERT INTO category_rules (id, user_id, match_text, category_id, priority)
		VALUES ($1, $2, $3, $4, $5)`

	if _, err := q.q.Exec(ctx, query, r.ID, r.UserID, r.MatchText, r.CategoryID, r.Priority); err != nil {
		return CategoryRule{}, wrap("store.CreateCategoryRule", err)
	}
	return r, nil
}

// CategoryRulesByUser lists a user's rules, highest priority first — the
// order matchCategory relies on to pick the first rule that matches.
func (q *Queries) CategoryRulesByUser(ctx context.Context, userID uuid.UUID) ([]CategoryRule, error) {
	const query = `
		SELECT id, user_id, match_text, category_id, priority
		FROM category_rules WHERE user_id = $1 ORDER BY priority DESC, id`

	rows, err := q.q.Query(ctx, query, userID)
	if err != nil {
		return nil, wrap("store.CategoryRulesByUser", err)
	}
	defer rows.Close()

	var out []CategoryRule
	for rows.Next() {
		var r CategoryRule
		if err := rows.Scan(&r.ID, &r.UserID, &r.MatchText, &r.CategoryID, &r.Priority); err != nil {
			return nil, wrap("store.CategoryRulesByUser", err)
		}
		out = append(out, r)
	}
	return out, wrap("store.CategoryRulesByUser", rows.Err())
}
