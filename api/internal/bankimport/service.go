package bankimport

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/categories"
	"github.com/JuanKsPty/corebank/api/internal/money"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

var (
	// ErrAccountNotFound means no external account matches that id.
	ErrAccountNotFound = errors.New("bankimport: external account not found")

	// ErrAccountNotOwned means the account exists but belongs to someone
	// else, kept distinct from ErrAccountNotFound for the same reason
	// accounts.ErrNotOwned is: the HTTP layer answers "not found" for both.
	ErrAccountNotOwned = errors.New("bankimport: external account belongs to another user")

	// ErrNoAccountHint means the file parsed correctly but did not declare
	// which account it belongs to — not expected for any of the three known
	// formats, but possible for a badly truncated file.
	ErrNoAccountHint = errors.New("bankimport: file does not declare which account it belongs to")

	// ErrTransactionNotFound means no imported movement matches that id, or
	// it belongs to an account the caller does not own — folded together
	// for the same reason accounts.ErrNotOwned is at the HTTP layer: a 403
	// would confirm the id exists.
	ErrTransactionNotFound = errors.New("bankimport: transaction not found")
)

// Service imports bank statement files and serves the accounts and
// movements they produced.
type Service struct {
	db         *store.DB
	categories *categories.Service
}

func NewService(db *store.DB, cats *categories.Service) *Service {
	return &Service{db: db, categories: cats}
}

// ImportResult tallies what one import did.
type ImportResult struct {
	ImportedAccountID uuid.UUID
	Format            string
	TotalRows         int
	Imported          int
	SkippedDuplicates int
}

// Import detects a file's format, finds or creates the account it declares,
// and records every movement that is not already there.
//
// The account is never chosen by the caller: AccountHint identifies it from
// the file itself for all three known formats, which is what lets an upload
// be "drop the file" rather than "drop the file and then also tell corebank
// which bank and account it is."
func (s *Service) Import(ctx context.Context, userID uuid.UUID, filename string, data []byte) (ImportResult, error) {
	parser, err := Detect(filename, data)
	if err != nil {
		return ImportResult{}, err
	}

	hint, ok := parser.AccountHint(bytes.NewReader(data))
	if !ok {
		return ImportResult{}, ErrNoAccountHint
	}

	account, created, err := s.findOrCreateAccount(ctx, userID, hint)
	if err != nil {
		return ImportResult{}, err
	}
	if created {
		if err := s.seedDefaultRules(ctx, userID, parser); err != nil {
			return ImportResult{}, err
		}
	}

	rows, err := parser.Parse(bytes.NewReader(data))
	if err != nil {
		return ImportResult{}, err
	}

	rules, err := s.loadRules(ctx, userID)
	if err != nil {
		return ImportResult{}, err
	}

	format := formatName(parser)
	batch, err := s.db.Q().CreateImportBatch(ctx, store.ImportBatch{
		ID:                uuid.New(),
		ImportedAccountID: account.ID,
		Filename:          filename,
		Format:            format,
		RowCount:          len(rows),
	})
	if err != nil {
		return ImportResult{}, err
	}

	counter := newOccurrenceCounter()
	var imported, skipped int
	for _, row := range rows {
		occurrence := counter.next(row.OccurredAt, int64(row.Amount), normaliseForDedup(row.Description), row.ExternalRef)
		key := dedupKey(account.ID, row.OccurredAt, int64(row.Amount), row.Description, row.ExternalRef, occurrence)

		id, isNew, err := s.db.Q().InsertExternalTransactionIfNew(ctx, store.ExternalTransaction{
			ID:                uuid.New(),
			ImportedAccountID: account.ID,
			ImportBatchID:     &batch.ID,
			DedupKey:          key,
			ExternalRef:       row.ExternalRef,
			OccurredAt:        row.OccurredAt,
			AmountCents:       int64(row.Amount),
			Description:       row.Description,
			Raw:               row.Raw,
		})
		if err != nil {
			return ImportResult{}, err
		}
		if !isNew {
			skipped++
			continue
		}
		imported++

		if categoryID, ok := matchCategory(row, rules); ok {
			if err := s.db.Q().SetExternalTransactionCategory(ctx, id, &categoryID); err != nil {
				return ImportResult{}, err
			}
		}
	}

	if err := s.db.Q().UpdateImportBatchCounts(ctx, batch.ID, imported, skipped); err != nil {
		return ImportResult{}, err
	}

	return ImportResult{
		ImportedAccountID: account.ID,
		Format:            format,
		TotalRows:         len(rows),
		Imported:          imported,
		SkippedDuplicates: skipped,
	}, nil
}

// findOrCreateAccount resolves an account the way SetLink resolves an
// investment account: by an identity a trusted source (here, the file
// itself) declares, creating it on first sight.
func (s *Service) findOrCreateAccount(ctx context.Context, userID uuid.UUID, hint AccountHint) (store.ImportedAccount, bool, error) {
	existing, err := s.db.Q().ImportedAccountByHint(ctx, userID, hint.Institution, hint.AccountNumber)
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return store.ImportedAccount{}, false, err
	}

	created, err := s.db.Q().CreateImportedAccount(ctx, store.ImportedAccount{
		ID:            uuid.New(),
		UserID:        userID,
		Institution:   hint.Institution,
		AccountNumber: hint.AccountNumber,
		DisplayName:   hint.DisplayName,
		Currency:      "USD",
	})
	if err != nil {
		return store.ImportedAccount{}, false, err
	}
	return created, true, nil
}

// seedDefaultRules translates a Banco General card's own category labels
// into rules against the user's already-seeded default categories, the
// first time such a card is linked. Silently skips any label whose target
// category does not exist (a user who deleted "Salud", say) rather than
// failing the whole import over a rule that would only have been a
// convenience.
func (s *Service) seedDefaultRules(ctx context.Context, userID uuid.UUID, parser Parser) error {
	if _, ok := parser.(bgCardParser); !ok {
		return nil
	}
	for bgLabel, categoryName := range bgCardDefaultCategoryMap {
		category, found, err := s.categories.FindByName(ctx, userID, categoryName)
		if err != nil {
			return fmt.Errorf("bankimport: seeding default rules: %w", err)
		}
		if !found {
			continue
		}
		if _, err := s.db.Q().CreateCategoryRule(ctx, store.CategoryRule{
			ID: uuid.New(), UserID: userID, MatchText: bgLabel, CategoryID: category.ID,
		}); err != nil {
			return fmt.Errorf("bankimport: seeding default rules: %w", err)
		}
	}
	return nil
}

func (s *Service) loadRules(ctx context.Context, userID uuid.UUID) ([]CategoryRule, error) {
	rows, err := s.db.Q().CategoryRulesByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]CategoryRule, 0, len(rows))
	for _, r := range rows {
		out = append(out, CategoryRule{MatchText: r.MatchText, CategoryID: r.CategoryID, Priority: r.Priority})
	}
	return out, nil
}

// Account is an imported account together with its declared balance.
//
// DeclaredBalance is a sum over whatever rows happen to be imported, never a
// figure corebank verified — see the package doc on why it never joins a
// TigerBeetle-derived total. It still deserves a name distinct from a bare
// "Balance" for the same reason: nothing here should read as more certain
// than it is.
type Account struct {
	store.ImportedAccount
	DeclaredBalance money.Cents
}

// Accounts lists a user's imported accounts, each with its declared balance.
//
// One batched balance query for all of them, not one per account — the same
// shape as accounts.Service.withBalances batching its ledger lookup.
func (s *Service) Accounts(ctx context.Context, userID uuid.UUID) ([]Account, error) {
	rows, err := s.db.Q().ImportedAccountsByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}

	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}

	balances, err := s.db.Q().DeclaredBalances(ctx, ids)
	if err != nil {
		return nil, err
	}

	out := make([]Account, 0, len(rows))
	for _, r := range rows {
		// An account with nothing imported yet just has no entry — that is a
		// declared balance of zero, not an error the way a missing ledger
		// account would be.
		out = append(out, Account{ImportedAccount: r, DeclaredBalance: money.Cents(balances[r.ID])})
	}
	return out, nil
}

// Transactions lists an account's imported movements.
func (s *Service) Transactions(ctx context.Context, userID uuid.UUID, accountID uuid.UUID, limit int) ([]store.ExternalTransaction, error) {
	if _, err := s.resolve(ctx, userID, accountID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	return s.db.Q().ExternalTransactionsByAccount(ctx, accountID, limit)
}

// ImportHistory lists an account's past imports.
func (s *Service) ImportHistory(ctx context.Context, userID uuid.UUID, accountID uuid.UUID) ([]store.ImportBatch, error) {
	if _, err := s.resolve(ctx, userID, accountID); err != nil {
		return nil, err
	}
	return s.db.Q().ImportBatchesByAccount(ctx, accountID)
}

// SpendByCategory sums an account's imported movements per category.
func (s *Service) SpendByCategory(ctx context.Context, userID uuid.UUID, accountID uuid.UUID) ([]store.CategorySpend, error) {
	if _, err := s.resolve(ctx, userID, accountID); err != nil {
		return nil, err
	}
	return s.db.Q().SpendByCategory(ctx, accountID)
}

// SetTransactionCategory files an imported movement under one of the user's
// own categories, or clears it when categoryID is nil.
//
// external_transactions carries no owner column of its own — ownership runs
// through its account, exactly like a transaction's own ownership runs
// through the accounts it touches (transactions.ownsMovement) — so this
// looks the row up first specifically to check that account is the
// caller's, rather than trusting a caller to have resolved it already.
func (s *Service) SetTransactionCategory(ctx context.Context, userID uuid.UUID, transactionID uuid.UUID, categoryID *uuid.UUID) error {
	tx, err := s.db.Q().ExternalTransactionByID(ctx, transactionID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("%w: %s", ErrTransactionNotFound, transactionID)
		}
		return err
	}
	if _, err := s.resolve(ctx, userID, tx.ImportedAccountID); err != nil {
		if errors.Is(err, ErrAccountNotFound) || errors.Is(err, ErrAccountNotOwned) {
			return fmt.Errorf("%w: %s", ErrTransactionNotFound, transactionID)
		}
		return err
	}

	if categoryID != nil {
		if _, err := s.categories.Get(ctx, userID, *categoryID); err != nil {
			return err
		}
	}
	return s.db.Q().SetExternalTransactionCategory(ctx, transactionID, categoryID)
}

// resolve finds an external account by id and checks that userID owns it.
func (s *Service) resolve(ctx context.Context, userID uuid.UUID, accountID uuid.UUID) (store.ImportedAccount, error) {
	account, err := s.db.Q().ImportedAccountByID(ctx, accountID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.ImportedAccount{}, fmt.Errorf("%w: %s", ErrAccountNotFound, accountID)
		}
		return store.ImportedAccount{}, err
	}
	if account.UserID != userID {
		return store.ImportedAccount{}, fmt.Errorf("%w: %s", ErrAccountNotOwned, accountID)
	}
	return account, nil
}

func formatName(parser Parser) string {
	switch parser.(type) {
	case bgCardParser:
		return "bg_card"
	case bgAccountParser:
		return "bg_account"
	case bacAccountParser:
		return "bac_account"
	default:
		return "unknown"
	}
}
