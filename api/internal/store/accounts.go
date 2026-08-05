package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/JuanKsPty/corebank/api/internal/ledger"
)

// Account is a row of the accounts table: the descriptive half of an account.
// The other half — how much money is in it — is only in the ledger.
type Account struct {
	ID       uuid.UUID
	UserID   uuid.UUID
	Number   string
	LedgerID ledger.AccountID
	Kind     ledger.AccountKind
	// Alias is what the customer calls this account. Empty means they have not named
	// it, and the interface falls back to the account's type — which is why this is a
	// plain string rather than a pointer: "unnamed" and "named the empty string" are
	// the same thing to a person, and a nullable column would only spread that
	// non-distinction through every layer above.
	Alias     string
	Currency  string
	CreatedAt time.Time
}

// Constraint names a caller may need to distinguish.
const (
	AccountsNumberConstraint   = "accounts_account_number_key"
	AccountsLedgerIDConstraint = "accounts_ledger_id_key"
)

// CreateAccount inserts an account.
//
// It records no balance, which is the point: with no balance column there is
// nothing to keep in sync with the ledger and no way for the two to disagree.
func (q *Queries) CreateAccount(ctx context.Context, a Account) (Account, error) {
	const query = `
		INSERT INTO accounts (id, user_id, account_number, ledger_id, account_type, alias, currency)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING created_at`

	err := q.q.QueryRow(ctx, query,
		a.ID, a.UserID, a.Number, int64(a.LedgerID), a.Kind.String(), a.Alias, a.Currency,
	).Scan(&a.CreatedAt)
	if err != nil {
		return Account{}, wrap("store.CreateAccount", err)
	}
	return a, nil
}

// AccountsByUser lists a user's accounts, oldest first so the account they
// opened with stays at the top of the dashboard.
func (q *Queries) AccountsByUser(ctx context.Context, userID uuid.UUID) ([]Account, error) {
	const query = `
		SELECT id, user_id, account_number, ledger_id, account_type, alias, currency, created_at
		FROM accounts WHERE user_id = $1 ORDER BY created_at, account_number`

	rows, err := q.q.Query(ctx, query, userID)
	if err != nil {
		return nil, wrap("store.AccountsByUser", err)
	}
	defer rows.Close()

	accounts, err := scanAccounts(rows)
	if err != nil {
		return nil, wrap("store.AccountsByUser", err)
	}
	return accounts, nil
}

// AccountByNumber looks up a single account regardless of owner.
//
// Authorisation is the caller's responsibility and is checked explicitly at the
// service layer; a lookup that silently filtered by owner would make
// "destination account does not exist" indistinguishable from "that account
// belongs to someone else", and a transfer needs to find accounts it does not
// own.
func (q *Queries) AccountByNumber(ctx context.Context, number string) (Account, error) {
	const query = `
		SELECT id, user_id, account_number, ledger_id, account_type, alias, currency, created_at
		FROM accounts WHERE account_number = $1`

	a, err := scanAccount(q.q.QueryRow(ctx, query, number))
	if err != nil {
		return Account{}, wrap("store.AccountByNumber", err)
	}
	return a, nil
}

// UpdateAccountAlias sets what the customer calls an account.
//
// Keyed by account number and not by owner: authorisation happens at the service
// layer, which resolves the account through the same ownership check every other
// operation uses. Repeating it in the WHERE clause would be a second place for that
// rule to live, and the one that drifts is always the copy.
//
// An empty alias is a normal value, not a missing one — it is how a customer removes
// a name they no longer want.
func (q *Queries) UpdateAccountAlias(ctx context.Context, number, alias string) error {
	const query = `UPDATE accounts SET alias = $2 WHERE account_number = $1`

	tag, err := q.q.Exec(ctx, query, number, alias)
	if err != nil {
		return wrap("store.UpdateAccountAlias", err)
	}
	if tag.RowsAffected() == 0 {
		return wrap("store.UpdateAccountAlias", pgx.ErrNoRows)
	}
	return nil
}

// AccountNumberTaken reports whether a generated number is already in use, used
// by the generator to retry before attempting the insert.
func (q *Queries) AccountNumberTaken(ctx context.Context, number string) (bool, error) {
	const query = `SELECT EXISTS (SELECT 1 FROM accounts WHERE account_number = $1)`

	var exists bool
	if err := q.q.QueryRow(ctx, query, number).Scan(&exists); err != nil {
		return false, wrap("store.AccountNumberTaken", err)
	}
	return exists, nil
}

// scanner is satisfied by both pgx.Row and pgx.Rows, so one scan body serves the
// single-row and multi-row cases.
type scanner interface {
	Scan(dest ...any) error
}

func scanAccount(s scanner) (Account, error) {
	var (
		a        Account
		ledgerID int64
		kind     string
	)
	if err := s.Scan(&a.ID, &a.UserID, &a.Number, &ledgerID, &kind, &a.Alias, &a.Currency, &a.CreatedAt); err != nil {
		return Account{}, err
	}
	a.LedgerID = ledger.AccountID(ledgerID)

	parsed, err := ledger.ParseAccountKind(kind)
	if err != nil {
		// The column has a CHECK constraint, so reaching here means the schema
		// and the code have drifted apart — worth reporting rather than
		// defaulting to some kind and carrying on.
		return Account{}, err
	}
	a.Kind = parsed
	return a, nil
}

func scanAccounts(rows pgx.Rows) ([]Account, error) {
	var accounts []Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}
