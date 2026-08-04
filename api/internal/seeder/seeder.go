package seeder

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/auth"
	"github.com/JuanKsPty/corebank/api/internal/ledger"
	"github.com/JuanKsPty/corebank/api/internal/money"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

// Seeder imports the dataset. It is safe to run on every boot: the first thing it
// does is check whether the import already happened.
type Seeder struct {
	db         *store.DB
	book       ledger.Ledger
	logger     *slog.Logger
	bcryptCost int
}

func New(db *store.DB, book ledger.Ledger, bcryptCost int, logger *slog.Logger) *Seeder {
	return &Seeder{db: db, book: book, bcryptCost: bcryptCost, logger: logger.With("component", "seeder")}
}

// Report summarises what an import did.
type Report struct {
	AlreadySeeded bool
	Users         int
	Accounts      int
	Movements     int
	// RewrittenEmails are the addresses that collided and had to be made unique.
	RewrittenEmails []string
	// DistinctPasswords is how many bcrypt hashes were actually computed.
	DistinctPasswords int
	Duration          time.Duration
}

// Run imports the dataset at path, doing nothing if it has been imported already.
func (s *Seeder) Run(ctx context.Context, path string) (Report, error) {
	started := time.Now()

	if state, err := s.db.Q().SeedState(ctx); err == nil {
		s.logger.Info("dataset already imported, nothing to do",
			"seeded_at", state.SeededAt, "users", state.Users,
			"accounts", state.Accounts, "movements", state.Movements)
		return Report{AlreadySeeded: true, Users: state.Users,
			Accounts: state.Accounts, Movements: state.Movements}, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return Report{}, err
	}

	s.logger.Info("importing dataset", "file", path)

	data, err := loadDataset(path)
	if err != nil {
		return Report{}, err
	}

	users, rewritten, distinctPasswords, err := s.prepareUsers(data.Users)
	if err != nil {
		return Report{}, err
	}

	accounts, openings, err := prepareAccounts(data.Accounts, users)
	if err != nil {
		return Report{}, err
	}

	movements, err := prepareMovements(data.Transactions, accounts)
	if err != nil {
		return Report{}, err
	}

	// The ledger goes first, and this ordering is the important one. A ledger
	// account or opening transfer with no PostgreSQL row is inert: nothing
	// references it, and creating it again is a documented no-op. A PostgreSQL
	// account with no ledger account is broken — its balance cannot be read. So
	// the half that is harmless when orphaned is written first, and the guard
	// marker last.
	if err := s.seedLedger(ctx, accounts, openings); err != nil {
		return Report{}, err
	}

	err = s.db.InTx(ctx, func(q *store.Queries) error {
		if err := q.InsertUsers(ctx, users); err != nil {
			return err
		}
		if err := q.InsertAccounts(ctx, accounts); err != nil {
			return err
		}
		if err := q.InsertTransactions(ctx, movements); err != nil {
			return err
		}
		return q.MarkSeeded(ctx, store.SeedState{
			Users: len(users), Accounts: len(accounts), Movements: len(movements),
		})
	})
	if err != nil {
		return Report{}, err
	}

	report := Report{
		Users:             len(users),
		Accounts:          len(accounts),
		Movements:         len(movements),
		RewrittenEmails:   rewritten,
		DistinctPasswords: distinctPasswords,
		Duration:          time.Since(started),
	}
	s.logger.Info("dataset imported",
		"users", report.Users, "accounts", report.Accounts, "movements", report.Movements,
		"rewritten_emails", len(report.RewrittenEmails),
		"bcrypt_hashes", report.DistinctPasswords,
		"took", report.Duration.Round(time.Millisecond))
	return report, nil
}

// prepareUsers hashes passwords and resolves duplicate addresses.
func (s *Seeder) prepareUsers(raw []datasetUser) (users []store.User, rewritten []string, distinct int, err error) {
	// The 1000 seed users share only 43 distinct passwords, all of the form
	// Nombre2024!. Hashing each distinct password once and reusing the result
	// takes the import from about a minute to a few seconds.
	//
	// This is sound *only* because these are fixture credentials that are
	// published in the README anyway. It is not how registration works: a real
	// sign-up calls HashPassword and gets its own salt, so two customers who pick
	// the same password never share a hash. The optimisation is confined to this
	// function for exactly that reason.
	hashes := make(map[string]string)

	seen := make(map[string]int, len(raw))
	users = make([]store.User, 0, len(raw))

	for _, u := range raw {
		hash, ok := hashes[u.Password]
		if !ok {
			hash, err = auth.HashPassword(u.Password, s.bcryptCost)
			if err != nil {
				return nil, nil, 0, fmt.Errorf("seeder: hashing the password of %s: %w", u.Email, err)
			}
			hashes[u.Password] = hash
		}

		email := auth.NormaliseEmail(u.Email)
		seen[email]++
		if n := seen[email]; n > 1 {
			// Twenty addresses appear twice, each time for a *different* person:
			// distinct id, distinct surname. Merging them would leave the second
			// person's accounts owned by the first, so both are kept and the
			// later address is made unique with a subaddress tag — a form real
			// mail systems deliver to the same inbox.
			unique := subaddress(email, n)
			s.logger.Warn("duplicate e-mail in the dataset, made unique",
				"original", email, "stored_as", unique, "user_id", u.ID)
			rewritten = append(rewritten, fmt.Sprintf("%s → %s", email, unique))
			email = unique
		}

		users = append(users, store.User{
			ID:           u.ID,
			Email:        email,
			PasswordHash: hash,
			FullName:     strings.TrimSpace(u.FullName),
			CreatedAt:    u.CreatedAt,
		})
	}
	return users, rewritten, len(hashes), nil
}

// subaddress turns ana@example.com into ana+dup2@example.com.
func subaddress(email string, n int) string {
	local, domain, found := strings.Cut(email, "@")
	if !found {
		return fmt.Sprintf("%s+dup%d", email, n)
	}
	return fmt.Sprintf("%s+dup%d@%s", local, n, domain)
}

// prepareAccounts builds the account rows and their opening movements.
func prepareAccounts(raw []datasetAccount, users []store.User) ([]store.Account, []ledger.Movement, error) {
	owners := make(map[uuid.UUID]bool, len(users))
	for _, u := range users {
		owners[u.ID] = true
	}

	accounts := make([]store.Account, 0, len(raw))
	openings := make([]ledger.Movement, 0, len(raw))

	for _, a := range raw {
		if !owners[a.UserID] {
			return nil, nil, fmt.Errorf("seeder: account %s belongs to unknown user %s", a.Number, a.UserID)
		}
		if err := money.CheckCurrency(a.Currency); err != nil {
			return nil, nil, fmt.Errorf("seeder: account %s: %w", a.Number, err)
		}

		kind, err := ledger.ParseAccountKind(a.AccountType)
		if err != nil {
			return nil, nil, fmt.Errorf("seeder: account %s: %w", a.Number, err)
		}
		ledgerID, err := ledger.AccountIDFromNumber(a.Number)
		if err != nil {
			return nil, nil, err
		}

		// Parsed from the file's own digits, never through a float.
		balance, err := money.Parse(a.InitialBalance.String())
		if err != nil {
			return nil, nil, fmt.Errorf("seeder: account %s balance %q: %w", a.Number, a.InitialBalance, err)
		}
		if balance <= 0 {
			return nil, nil, fmt.Errorf("seeder: account %s has a non-positive opening balance %s", a.Number, balance)
		}

		accounts = append(accounts, store.Account{
			ID:       accountRowID(a.Number),
			UserID:   a.UserID,
			Number:   a.Number,
			LedgerID: ledgerID,
			Kind:     kind,
			Currency: money.CurrencyUSD,
		})
		openings = append(openings, ledger.Movement{
			ID:     openingTransferID(a.Number),
			From:   ledger.WorldAccountID,
			To:     ledgerID,
			Amount: balance,
			Kind:   ledger.MovementOpening,
			Actor:  a.UserID,
		})
	}
	return accounts, openings, nil
}

// prepareMovements builds the historical movement rows.
func prepareMovements(raw []datasetTransaction, accounts []store.Account) ([]store.Transaction, error) {
	known := make(map[string]bool, len(accounts)+1)
	for _, a := range accounts {
		known[a.Number] = true
	}
	known[store.ExternalAccount] = true

	movements := make([]store.Transaction, 0, len(raw))

	for i, t := range raw {
		kind, err := ledger.ParseMovementKind(t.Type)
		if err != nil {
			return nil, fmt.Errorf("seeder: movement %d: %w", i, err)
		}
		amount, err := money.ParsePositive(t.Amount.String())
		if err != nil {
			return nil, fmt.Errorf("seeder: movement %d amount %q: %w", i, t.Amount, err)
		}
		for side, number := range map[string]string{"from_account": t.From, "to_account": t.To} {
			if !known[number] {
				return nil, fmt.Errorf("seeder: movement %d %s references unknown account %s", i, side, number)
			}
		}
		if t.From == t.To {
			return nil, fmt.Errorf("seeder: movement %d has the same account on both sides", i)
		}
		// The dataset only contains completed movements. Accepting other states
		// silently would import them as completed, which would misstate history.
		if status := store.TxStatus(t.Status); status != store.StatusCompleted {
			return nil, fmt.Errorf("seeder: movement %d has unexpected status %q", i, t.Status)
		}

		movements = append(movements, store.Transaction{
			ID:          movementID(i, t),
			Kind:        kind,
			Status:      store.StatusCompleted,
			Amount:      amount,
			Currency:    money.CurrencyUSD,
			FromAccount: t.From,
			ToAccount:   t.To,
			Description: t.Description,
			// Marked as imported so it is distinguishable from anything this
			// system did. These rows have no ledger transfer behind them, which
			// is exactly what this flag records.
			Source:     store.SourceSeed,
			Origin:     store.OriginAPI,
			OccurredAt: t.Timestamp,
		})
	}
	return movements, nil
}

// seedLedger creates the ledger accounts and funds them.
func (s *Seeder) seedLedger(ctx context.Context, accounts []store.Account, openings []ledger.Movement) error {
	// The equity counterparty must exist before anything can be credited from it.
	newAccounts := make([]ledger.NewAccount, 0, len(accounts)+1)
	newAccounts = append(newAccounts, ledger.World())
	for _, a := range accounts {
		newAccounts = append(newAccounts, ledger.NewAccount{ID: a.LedgerID, Kind: a.Kind, Owner: a.UserID})
	}

	if err := s.book.EnsureAccounts(ctx, newAccounts); err != nil {
		return fmt.Errorf("seeder: creating ledger accounts: %w", err)
	}
	s.logger.Info("ledger accounts ready", "count", len(newAccounts))

	results, err := s.book.PostMany(ctx, openings)
	if err != nil {
		return fmt.Errorf("seeder: funding accounts: %w", err)
	}

	var funded, replayed int
	for i, result := range results {
		switch {
		case result == nil:
			funded++
		case errors.Is(result, ledger.ErrAlreadyApplied):
			// A previous run got this far. The account already holds the right
			// amount, so this is not a failure.
			replayed++
		default:
			return fmt.Errorf("seeder: funding account %d (%s): %w",
				i, openings[i].ID, result)
		}
	}
	s.logger.Info("accounts funded", "created", funded, "already_present", replayed)
	return nil
}
