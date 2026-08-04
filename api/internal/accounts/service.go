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
func (s *Service) Open(ctx context.Context, q *store.Queries, userID uuid.UUID, kind ledger.AccountKind) (store.Account, error) {
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
