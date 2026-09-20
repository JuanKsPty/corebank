package bankimport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/accounts"
	"github.com/JuanKsPty/corebank/api/internal/categories"
	"github.com/JuanKsPty/corebank/api/internal/ledger"
	"github.com/JuanKsPty/corebank/api/internal/money"
	"github.com/JuanKsPty/corebank/api/internal/store"
	"github.com/JuanKsPty/corebank/api/internal/transactions"
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

	// ErrTargetAccountNotFound means a caller-supplied target account for a
	// bank-account import does not exist or belongs to someone else — folded
	// together for the same reason ErrAccountNotFound/ErrAccountNotOwned are.
	ErrTargetAccountNotFound = errors.New("bankimport: target account not found")

	// ErrAccountAlreadyLinked means the caller-supplied target account is
	// already the destination of a different external account. A real
	// account's own number has nothing to do with the number printed on a
	// bank statement, so picking the wrong one here would otherwise merge
	// two unrelated real-world accounts' histories together silently.
	ErrAccountAlreadyLinked = errors.New("bankimport: account already linked to a different external account")

	// ErrStatementBelongsToAnotherAccount means a repeat import named a
	// target account that disagrees with the one this external account was
	// already linked to on its first import. The mirror image of
	// ErrAccountAlreadyLinked: that one catches "this account is already
	// someone else's file", this one catches "this file is already a
	// different account's".
	ErrStatementBelongsToAnotherAccount = errors.New("bankimport: statement already belongs to a different account")

	// ErrCardLinked means the id requested for deletion is a linked bank
	// account's identity, not a card. Deleting it here would sever the link
	// without touching the real account it belongs to; there is no path
	// through this package for that, only through accounts.Service.Delete.
	ErrCardLinked = errors.New("bankimport: not a card")
)

// Service imports bank statement files and serves the accounts and
// movements they produced.
type Service struct {
	db           *store.DB
	categories   *categories.Service
	accounts     *accounts.Service
	transactions *transactions.Service
}

func NewService(db *store.DB, cats *categories.Service, accts *accounts.Service, tx *transactions.Service) *Service {
	return &Service{db: db, categories: cats, accounts: accts, transactions: tx}
}

// ImportResult tallies what one import did.
type ImportResult struct {
	ImportedAccountID uuid.UUID
	Format            string
	TotalRows         int
	Imported          int
	SkippedDuplicates int
	// IsCard is true for a card statement, which stays Postgres-only exactly
	// as it always has. False means the movements above were posted for
	// real, to LinkedAccountNumber.
	IsCard              bool
	LinkedAccountNumber string
}

// Import detects a file's format, finds or creates the account it declares,
// and records every movement that is not already there.
//
// The account is never chosen by the caller: AccountHint identifies it from
// the file itself for all three known formats, which is what lets an upload
// be "drop the file" rather than "drop the file and then also tell corebank
// which bank and account it is." targetAccountNumber is a separate, optional
// choice — which of the customer's own real accounts a bank-account import
// (never a card) should post to the first time that external account is
// seen. Empty means auto-open a new one. It is ignored once the external
// account is already linked, and always ignored for a card.
func (s *Service) Import(ctx context.Context, userID uuid.UUID, filename string, data []byte, targetAccountNumber string) (ImportResult, error) {
	parser, err := Detect(filename, data)
	if err != nil {
		return ImportResult{}, err
	}

	hint, ok := parser.AccountHint(bytes.NewReader(data))
	if !ok {
		return ImportResult{}, ErrNoAccountHint
	}

	account, created, err := s.findOrCreateAccount(ctx, userID, parser, hint, targetAccountNumber)
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
	// Posted in the order they happened, regardless of how the bank listed
	// them (some exports run newest-first) — otherwise a withdrawal could be
	// posted before the deposit that covered it and trip the ledger's own
	// overdraft protection for no reason. Stable, so two rows sharing a
	// timestamp keep the file's own order, which is what makes their dedup
	// occurrence index (assigned below, in this same order) reproduce
	// identically on a re-import of unchanged bytes.
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].OccurredAt.Before(rows[j].OccurredAt) })

	rules, err := s.loadRules(ctx, userID)
	if err != nil {
		return ImportResult{}, err
	}

	// A linked bank account posts every new row for real, through the same
	// path a customer's own deposit or withdrawal takes — which addresses an
	// account by number, not by the id this package stores it under.
	var linkedNumber string
	if account.LinkedAccountID != nil {
		linked, err := s.db.Q().AccountByID(ctx, *account.LinkedAccountID)
		if err != nil {
			return ImportResult{}, err
		}
		linkedNumber = linked.Number

		// Before this external account's first import batch, cover whatever
		// balance it already held in reality — the statement's own rows
		// only describe money moving, never the pile it moved on top of.
		priorBatches, err := s.db.Q().ImportBatchesByAccount(ctx, account.ID)
		if err != nil {
			return ImportResult{}, err
		}
		if len(priorBatches) == 0 {
			if err := s.seedOpeningBalance(ctx, userID, linkedNumber, account.ID, rows); err != nil {
				return ImportResult{}, err
			}
		}
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

		categoryID, hasCategory := matchCategory(row, rules)

		var txID uuid.UUID
		if linkedNumber != "" && row.Amount != 0 {
			txID, err = s.postRow(ctx, userID, linkedNumber, row, key)
			if err != nil {
				return ImportResult{}, err
			}
			if err := s.db.Q().SetExternalTransactionLink(ctx, id, txID); err != nil {
				return ImportResult{}, err
			}
		}

		switch {
		case !hasCategory:
			// Nothing to file it under.
		case txID != uuid.Nil:
			// The real transaction is the source of truth for a linked
			// account, so the category belongs there.
			if _, err := s.transactions.SetCategory(ctx, userID, txID, &categoryID); err != nil {
				return ImportResult{}, err
			}
		default:
			if err := s.db.Q().SetExternalTransactionCategory(ctx, id, &categoryID); err != nil {
				return ImportResult{}, err
			}
		}
	}

	if err := s.db.Q().UpdateImportBatchCounts(ctx, batch.ID, imported, skipped); err != nil {
		return ImportResult{}, err
	}

	return ImportResult{
		ImportedAccountID:   account.ID,
		Format:              format,
		TotalRows:           len(rows),
		Imported:            imported,
		SkippedDuplicates:   skipped,
		IsCard:              account.LinkedAccountID == nil,
		LinkedAccountNumber: linkedNumber,
	}, nil
}

// postRow posts one new, non-zero movement through the ordinary
// deposit/withdraw path, the same one a customer's own movement takes — see
// internal/investments' postCashTransaction for the identical pattern
// applied to an IBKR cash sync. The dedup key already computed for
// external_transactions doubles as the idempotency key here, so retrying
// this call (a crash between posting and SetExternalTransactionLink, say)
// can never post the same movement twice.
func (s *Service) postRow(ctx context.Context, userID uuid.UUID, accountNumber string, row ParsedRow, key string) (uuid.UUID, error) {
	req := transactions.Request{
		Account:        accountNumber,
		Description:    row.Description,
		Origin:         store.OriginBankImport,
		IdempotencyKey: "bank_import:" + key,
	}

	var (
		tx  store.Transaction
		err error
	)
	if row.Amount > 0 {
		req.Amount = row.Amount
		tx, err = s.transactions.Deposit(ctx, userID, req)
	} else {
		req.Amount = -row.Amount
		tx, err = s.transactions.Withdraw(ctx, userID, req)
	}
	if err != nil {
		return uuid.Nil, err
	}
	return tx.ID, nil
}

// seedOpeningBalance covers a linked account's real-world starting balance
// before its first import batch, so a statement whose earliest rows are
// withdrawals — money that was already there before this import, not
// overdrawn — does not trip the ledger's own overdraft protection.
//
// It walks the rows in the order they are about to be posted and finds the
// lowest point the account's own available balance would reach on top of
// them; anything below zero is covered by one deposit first. Idempotent on
// externalAccountID, so retrying an interrupted import never posts it twice.
func (s *Service) seedOpeningBalance(ctx context.Context, userID uuid.UUID, accountNumber string, externalAccountID uuid.UUID, rows []ParsedRow) error {
	current, err := s.accounts.Get(ctx, userID, accountNumber)
	if err != nil {
		return err
	}

	running := current.Balance.Available
	lowest := running
	for _, row := range rows {
		running += row.Amount
		if running < lowest {
			lowest = running
		}
	}
	if lowest >= 0 {
		return nil
	}

	_, err = s.transactions.Deposit(ctx, userID, transactions.Request{
		Account:        accountNumber,
		Amount:         -lowest,
		Description:    "Saldo inicial declarado por el estado de cuenta",
		Origin:         store.OriginBankImport,
		IdempotencyKey: "bank_import:opening:" + externalAccountID.String(),
	})
	return err
}

// findOrCreateAccount resolves an account the way SetLink resolves an
// investment account: by an identity a trusted source (here, the file
// itself) declares, creating it on first sight. A card is always Postgres-
// only, as it always has been; a bank account is now linked to one of the
// customer's real corebank accounts the first time it is seen, so every
// later import of the same file is fully automatic — no picker, no manual
// entry, just drop the file again.
func (s *Service) findOrCreateAccount(ctx context.Context, userID uuid.UUID, parser Parser, hint AccountHint, targetAccountNumber string) (store.ImportedAccount, bool, error) {
	existing, err := s.db.Q().ImportedAccountByHint(ctx, userID, hint.Institution, hint.AccountNumber)
	if err == nil {
		if err := s.verifyTarget(ctx, userID, existing, targetAccountNumber); err != nil {
			return store.ImportedAccount{}, false, err
		}
		return existing, false, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return store.ImportedAccount{}, false, err
	}

	toCreate := store.ImportedAccount{
		ID:            uuid.New(),
		UserID:        userID,
		Institution:   hint.Institution,
		AccountNumber: hint.AccountNumber,
		DisplayName:   hint.DisplayName,
		Currency:      "USD",
	}

	if _, isCard := parser.(bgCardParser); !isCard {
		linkedID, err := s.linkRealAccount(ctx, userID, hint, targetAccountNumber)
		if err != nil {
			return store.ImportedAccount{}, false, err
		}
		toCreate.LinkedAccountID = &linkedID
	}

	created, err := s.db.Q().CreateImportedAccount(ctx, toCreate)
	if err != nil {
		// The race-safe fallback for the pre-check linkRealAccount already
		// did: two requests linking the same account at once can both pass
		// that check, but only one insert can win the unique index.
		if store.IsConstraint(err, store.ImportedAccountLinkedConstraint) {
			return store.ImportedAccount{}, false, fmt.Errorf("%w: %s", ErrAccountAlreadyLinked, targetAccountNumber)
		}
		return store.ImportedAccount{}, false, err
	}
	return created, true, nil
}

// verifyTarget checks a repeat import's target, if one was given, against
// where this external account was already linked on its first import.
//
// A repeat import normally carries no target at all — the point of linking
// once is that every later import is "just drop the file" — but a caller
// (the same dialog that offers the picker on first sight) may still send
// one out of habit. Silently ignoring a mismatched choice would hide
// exactly the kind of mistake the picker's guard exists to catch, just
// approached from the other direction: not "this account already belongs
// to a different file" but "this file already belongs to a different
// account".
func (s *Service) verifyTarget(ctx context.Context, userID uuid.UUID, existing store.ImportedAccount, targetAccountNumber string) error {
	if targetAccountNumber == "" || existing.LinkedAccountID == nil {
		return nil
	}

	account, err := s.accounts.Resolve(ctx, userID, targetAccountNumber)
	if err != nil {
		if errors.Is(err, accounts.ErrNotFound) || errors.Is(err, accounts.ErrNotOwned) {
			return fmt.Errorf("%w: %s", ErrTargetAccountNotFound, targetAccountNumber)
		}
		return err
	}
	if account.ID != *existing.LinkedAccountID {
		return fmt.Errorf("%w: %s", ErrStatementBelongsToAnotherAccount, targetAccountNumber)
	}
	return nil
}

// linkRealAccount resolves or opens the real corebank account a bank-account
// import (never a card) posts its movements to, the first time that
// external account is seen. Every later import of the same account reuses
// external_accounts.linked_account_id instead of calling this again — the
// "no manual process" property holds for every import after the first.
func (s *Service) linkRealAccount(ctx context.Context, userID uuid.UUID, hint AccountHint, targetAccountNumber string) (uuid.UUID, error) {
	if targetAccountNumber != "" {
		account, err := s.accounts.Resolve(ctx, userID, targetAccountNumber)
		if err != nil {
			if errors.Is(err, accounts.ErrNotFound) || errors.Is(err, accounts.ErrNotOwned) {
				return uuid.Nil, fmt.Errorf("%w: %s", ErrTargetAccountNotFound, targetAccountNumber)
			}
			return uuid.Nil, err
		}

		// A friendly pre-check ahead of the unique index that would refuse
		// this anyway (CreateImportedAccount, below): catching it here
		// means the caller never has to distinguish "not found" from
		// "already taken" by parsing a constraint name.
		if _, err := s.db.Q().ImportedAccountByLinkedAccountID(ctx, account.ID); err == nil {
			return uuid.Nil, fmt.Errorf("%w: %s", ErrAccountAlreadyLinked, targetAccountNumber)
		} else if !errors.Is(err, store.ErrNotFound) {
			return uuid.Nil, err
		}

		return account.ID, nil
	}

	// Auto-opened the same way "Abrir cuenta" does: checking is the ordinary
	// day-to-day account a BAC/Banco General movement export describes.
	opened, err := s.accounts.OpenFor(ctx, userID, ledger.KindChecking, hint.DisplayName)
	if err != nil {
		return uuid.Nil, err
	}
	return opened.ID, nil
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

// Accounts lists a user's card accounts, each with its declared balance.
//
// A linked bank account is left out on purpose: it already appears as one of
// the customer's real accounts, with a real balance, so listing it again
// here — under a declared, unverified figure — would show the same money
// twice and at odds with itself.
//
// One batched balance query for all of them, not one per account — the same
// shape as accounts.Service.withBalances batching its ledger lookup.
func (s *Service) Accounts(ctx context.Context, userID uuid.UUID) ([]Account, error) {
	all, err := s.db.Q().ImportedAccountsByUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	rows := make([]store.ImportedAccount, 0, len(all))
	for _, r := range all {
		if r.LinkedAccountID == nil {
			rows = append(rows, r)
		}
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

// DeleteAccount removes a card, and everything imported under it.
//
// Nothing here touches TigerBeetle — a card never has, that is the whole
// point of the split this package's doc describes — so unlike deleting a
// real account, there is no balance to check and no "only account" to
// protect. Refused only for an id that turns out to be a linked bank
// account's identity rather than a card: that one belongs to
// accounts.Service.Delete instead.
func (s *Service) DeleteAccount(ctx context.Context, userID uuid.UUID, accountID uuid.UUID) error {
	account, err := s.resolve(ctx, userID, accountID)
	if err != nil {
		return err
	}
	if account.LinkedAccountID != nil {
		return fmt.Errorf("%w: %s", ErrCardLinked, accountID)
	}
	return s.db.Q().DeleteImportedAccount(ctx, accountID)
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
