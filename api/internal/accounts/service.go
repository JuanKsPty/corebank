package accounts

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/ledger"
	"github.com/JuanKsPty/corebank/api/internal/money"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

var (
	// ErrNotFound means no account has that number.
	ErrNotFound = errors.New("accounts: account not found")

	// ErrNotOwned means the account exists but belongs to someone else. It is
	// kept distinct from ErrNotFound inside the service so the reason is visible
	// in logs; what the customer is told is decided at the HTTP layer, which
	// answers "not found" for both so account numbers cannot be probed for
	// existence.
	ErrNotOwned = errors.New("accounts: account belongs to another user")

	// ErrNumberUnavailable means the generator could not find a free account
	// number. With twelve random digits this indicates a broken generator rather
	// than a full namespace.
	ErrNumberUnavailable = errors.New("accounts: could not allocate an account number")

	// ErrTooManyAccounts is returned when a customer already holds the most accounts
	// one person may open. A real bank has such a limit for anti-abuse reasons and so
	// does this one; without it a single session could open accounts until the number
	// space ran short, and every one of them would be a row the statement has to scan.
	ErrTooManyAccounts = errors.New("accounts: the customer already holds the maximum number of accounts")

	// ErrAccountNotEmpty means the account still holds funds. TigerBeetle has
	// no delete operation at all — this is the closest thing corebank has to
	// closing an account, and it never closes one that still holds money.
	ErrAccountNotEmpty = errors.New("accounts: account still holds funds")

	// ErrLastAccount means this is the only account the customer has left.
	// Deleting it would leave "the customer's only account" — the shorthand
	// a deposit or a chat request without a named account resolves to —
	// with nothing to resolve to.
	ErrLastAccount = errors.New("accounts: cannot delete a customer's only account")

	// ErrAccountLinked means an investment account still has an IBKR link.
	// Deleting it would cascade away the link, its positions and its trades
	// with no confirmation that was the point — unlinking is its own,
	// deliberate step.
	ErrAccountLinked = errors.New("accounts: account still has an external link")
)

// Account is an account with the balance the ledger reports for it.
type Account struct {
	store.Account
	Balance ledger.Balance
}

// Service reads and opens accounts.
type Service struct {
	db   *store.DB
	book ledger.Ledger
}

func NewService(db *store.DB, book ledger.Ledger) *Service {
	return &Service{db: db, book: book}
}

// numberAttempts bounds the retry loop that looks for a free account number.
const numberAttempts = 5

// maxAccountsPerCustomer caps how many accounts one person may hold. The seeded
// dataset's busiest customer has three, so this leaves room to open more without
// making the limit feel arbitrary.
const maxAccountsPerCustomer = 6

// Open creates an account for a user: a row in PostgreSQL and the matching
// account in the ledger.
//
// It takes the queries rather than opening its own transaction so registration
// can create a user and their first account atomically. The order inside is
// deliberate: the ledger account is created *before* the caller commits, because
// the two possible failures are not equally bad. A ledger account with no
// PostgreSQL row is inert — nothing references it, it holds nothing, and
// creating it again is a no-op. A PostgreSQL row with no ledger account is a
// broken account whose balance cannot be read. So the reversible half goes last.
//
// The alias is taken already normalised: this is the low-level half that registration
// shares, and validating the same string twice invites the two checks to disagree.
func (s *Service) Open(ctx context.Context, q *store.Queries, userID uuid.UUID, kind ledger.AccountKind, alias string) (store.Account, error) {
	number, err := s.allocateNumber(ctx, q)
	if err != nil {
		return store.Account{}, err
	}

	ledgerID, err := ledger.AccountIDFromNumber(number)
	if err != nil {
		return store.Account{}, fmt.Errorf("accounts: deriving the ledger id: %w", err)
	}

	account, err := q.CreateAccount(ctx, store.Account{
		ID:       uuid.New(),
		UserID:   userID,
		Number:   number,
		LedgerID: ledgerID,
		Kind:     kind,
		Alias:    alias,
		Currency: money.CurrencyUSD,
	})
	if err != nil {
		// The pre-check in allocateNumber lost a race with a concurrent
		// registration. The unique constraint is the authority, and it just
		// spoke; retrying here is not possible because the failed statement has
		// aborted the caller's transaction, so the caller retries the whole
		// registration instead.
		if store.IsConstraint(err, store.AccountsNumberConstraint) ||
			store.IsConstraint(err, store.AccountsLedgerIDConstraint) {
			return store.Account{}, fmt.Errorf("%w: %s was taken concurrently", ErrNumberUnavailable, number)
		}
		return store.Account{}, err
	}

	err = s.book.EnsureAccounts(ctx, []ledger.NewAccount{{
		ID:    ledgerID,
		Kind:  kind,
		Owner: userID,
	}})
	if err != nil {
		return store.Account{}, fmt.Errorf("accounts: creating the ledger account: %w", err)
	}
	return account, nil
}

// allocateNumber finds an unused account number.
//
// The existence check is only an optimisation that avoids burning a transaction
// on a collision; the unique constraint on the column remains the authority,
// because between this check and the insert another request could take the same
// number.
func (s *Service) allocateNumber(ctx context.Context, q *store.Queries) (string, error) {
	for range numberAttempts {
		number, err := GenerateNumber()
		if err != nil {
			return "", err
		}
		taken, err := q.AccountNumberTaken(ctx, number)
		if err != nil {
			return "", err
		}
		if !taken {
			return number, nil
		}
	}
	return "", ErrNumberUnavailable
}

// List returns a user's accounts with their balances.
func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]Account, error) {
	rows, err := s.db.Q().AccountsByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.withBalances(ctx, rows)
}

// Get returns one account belonging to userID.
func (s *Service) Get(ctx context.Context, userID uuid.UUID, number string) (Account, error) {
	row, err := s.db.Q().AccountByNumber(ctx, number)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Account{}, fmt.Errorf("%w: %s", ErrNotFound, number)
		}
		return Account{}, err
	}
	if row.UserID != userID {
		return Account{}, fmt.Errorf("%w: %s", ErrNotOwned, number)
	}

	withBalance, err := s.withBalances(ctx, []store.Account{row})
	if err != nil {
		return Account{}, err
	}
	return withBalance[0], nil
}

// Resolve finds an account by number and checks that userID owns it.
//
// This is the guard every money-moving path calls on its *source* account, and
// the one the AI's tools rely on: the assistant may name any account as a
// destination, which is what a transfer is, but a source it does not own is
// rejected here regardless of what the model asked for.
func (s *Service) Resolve(ctx context.Context, userID uuid.UUID, number string) (store.Account, error) {
	row, err := s.db.Q().AccountByNumber(ctx, number)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Account{}, fmt.Errorf("%w: %s", ErrNotFound, number)
		}
		return store.Account{}, err
	}
	if row.UserID != userID {
		return store.Account{}, fmt.Errorf("%w: %s", ErrNotOwned, number)
	}
	return row, nil
}

// Rename sets what a customer calls one of their accounts.
//
// Ownership is checked by Resolve, the same function every other operation on a
// customer's own account goes through, so a rename cannot reach an account the caller
// does not hold and the rule lives in one place.
//
// An empty alias is a valid request: it is how a name is removed. There is nothing to
// undo and nothing in the ledger to touch — this is a label on a row of metadata, and
// no amount of renaming can move money.
func (s *Service) Rename(ctx context.Context, userID uuid.UUID, number, rawAlias string) (Account, error) {
	alias, err := normaliseAlias(rawAlias)
	if err != nil {
		return Account{}, err
	}

	row, err := s.Resolve(ctx, userID, number)
	if err != nil {
		return Account{}, err
	}
	if err := s.db.Q().UpdateAccountAlias(ctx, row.Number, alias); err != nil {
		return Account{}, err
	}
	row.Alias = alias

	// The balance comes back with it, so the caller can render the account without a
	// second request and the response is the same shape every other account endpoint
	// returns.
	withBalance, err := s.withBalances(ctx, []store.Account{row})
	if err != nil {
		return Account{}, err
	}
	return withBalance[0], nil
}

// Delete removes an account the customer no longer wants.
//
// "Delete" is the word the interface uses; what actually happens is narrower.
// TigerBeetle has no operation that deletes an account, so the ledger account
// this pointed to persists forever, holding whatever balance it already had —
// which is why this refuses unless that balance is zero. Below that, deleting
// is just removing the PostgreSQL row: the app stops showing the account, and
// nothing about real transaction history changes, because transactions are
// never linked to an account by id, only by the number that keeps existing
// wherever it was already recorded.
func (s *Service) Delete(ctx context.Context, userID uuid.UUID, number string) error {
	account, err := s.Resolve(ctx, userID, number)
	if err != nil {
		return err
	}

	balances, err := s.book.Balances(ctx, []ledger.AccountID{account.LedgerID})
	if err != nil {
		return fmt.Errorf("accounts: reading balance: %w", err)
	}
	if balance := balances[account.LedgerID]; balance.Posted != 0 || balance.Held != 0 {
		return fmt.Errorf("%w: %s", ErrAccountNotEmpty, number)
	}

	held, err := s.db.Q().AccountsByUser(ctx, userID)
	if err != nil {
		return err
	}
	if len(held) <= 1 {
		return fmt.Errorf("%w: %s", ErrLastAccount, number)
	}

	if account.Kind == ledger.KindInvestment {
		if _, err := s.db.Q().IBKRLinkByAccountID(ctx, account.ID); err == nil {
			return fmt.Errorf("%w: %s", ErrAccountLinked, number)
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}

	return s.db.Q().DeleteAccount(ctx, account.ID)
}

// Lookup finds any account by number without an ownership check, for validating
// a transfer's destination.
func (s *Service) Lookup(ctx context.Context, number string) (store.Account, error) {
	row, err := s.db.Q().AccountByNumber(ctx, number)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Account{}, fmt.Errorf("%w: %s", ErrNotFound, number)
		}
		return store.Account{}, err
	}
	return row, nil
}

// Total sums the available balance across a user's accounts, for the dashboard's
// consolidated figure.
func (s *Service) Total(ctx context.Context, list []Account) money.Cents {
	var total money.Cents
	for _, a := range list {
		total = total.Add(a.Balance.Available)
	}
	return total
}

// withBalances attaches ledger balances to account rows.
//
// One batched ledger lookup for all of them, not one per account: this is what
// the dashboard calls, and a per-account round trip would make the page's cost
// grow with the number of accounts for no reason.
func (s *Service) withBalances(ctx context.Context, rows []store.Account) ([]Account, error) {
	if len(rows) == 0 {
		return nil, nil
	}

	ids := make([]ledger.AccountID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.LedgerID)
	}

	balances, err := s.book.Balances(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("accounts: reading balances: %w", err)
	}

	out := make([]Account, 0, len(rows))
	for _, r := range rows {
		balance, ok := balances[r.LedgerID]
		if !ok {
			// A row whose ledger account is missing means the two stores have
			// drifted. Reporting it as a zero balance would be a lie about
			// money, so it is an error.
			return nil, fmt.Errorf("accounts: %s has no ledger account (id %d)", r.Number, r.LedgerID)
		}
		out = append(out, Account{Account: r, Balance: balance})
	}
	return out, nil
}

// OpenFor opens an additional account for an existing customer.
//
// The difference from Open is the transaction: registration already has one open and
// needs its user and first account to commit together, so Open takes the queries.
// Everything after registration comes through here, which owns the transaction and can
// therefore retry it.
//
// That retry is the reason this cannot simply be Open with a wrapper. An account number
// is allocated by pre-checking for a free one and relying on the unique constraint as
// the authority; when the constraint speaks, the statement has already aborted the
// transaction, so the only way forward is a new one. Registration handles that by
// retrying the whole registration. Here the whole thing is just this.
func (s *Service) OpenFor(ctx context.Context, userID uuid.UUID, kind ledger.AccountKind, rawAlias string) (Account, error) {
	alias, err := normaliseAlias(rawAlias)
	if err != nil {
		return Account{}, err
	}

	held, err := s.db.Q().AccountsByUser(ctx, userID)
	if err != nil {
		return Account{}, err
	}
	if len(held) >= maxAccountsPerCustomer {
		return Account{}, fmt.Errorf("%w: %d", ErrTooManyAccounts, len(held))
	}

	var opened store.Account
	for attempt := 0; attempt < numberAttempts; attempt++ {
		err = s.db.InTx(ctx, func(q *store.Queries) error {
			var openErr error
			opened, openErr = s.Open(ctx, q, userID, kind, alias)
			return openErr
		})
		if err == nil {
			break
		}
		if !errors.Is(err, ErrNumberUnavailable) {
			return Account{}, err
		}
	}
	if err != nil {
		return Account{}, err
	}

	// Read the balance back rather than assuming zero. It is zero, but composing the
	// response the same way every other endpoint does means the new account cannot be
	// the one shape the frontend has to special-case.
	withBalance, err := s.withBalances(ctx, []store.Account{opened})
	if err != nil {
		return Account{}, err
	}
	return withBalance[0], nil
}
