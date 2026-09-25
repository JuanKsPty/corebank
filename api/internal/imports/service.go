// Package imports stores what a bank statement file says.
//
// One file is one transaction: every line, the statement, its balances and the
// run that records what happened are written together or not at all, so a
// failure never leaves half a file behind — and a line is only "already
// imported" when it really is, because its dedup key is claimed in the same
// transaction that stores it.
package imports

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/categories"
	"github.com/JuanKsPty/corebank/api/internal/civil"
	"github.com/JuanKsPty/corebank/api/internal/logging"
	"github.com/JuanKsPty/corebank/api/internal/money"
	"github.com/JuanKsPty/corebank/api/internal/statement"
	"github.com/JuanKsPty/corebank/api/internal/store"
	"github.com/JuanKsPty/corebank/api/internal/transfers"
)

// Service imports statement files.
type Service struct {
	db         *store.DB
	categories *categories.Service
}

func NewService(db *store.DB, cats *categories.Service) *Service {
	return &Service{db: db, categories: cats}
}

// Result is what one file did, per account it held.
type Result struct {
	Source    statement.Source
	Unchanged bool
	Accounts  []AccountResult
}

// AccountResult is what one statement in the file did to its account.
type AccountResult struct {
	AccountID    uuid.UUID
	DisplayName  string
	Created      bool
	PeriodStart  civil.Date
	PeriodEnd    civil.Date
	Lines        int
	New          int
	Duplicates   int
	Opening      *money.Cents
	Closing      *money.Cents
	ChainBreak   *statement.ChainBreak
	Warnings     []string
	NeedsOpening bool
	RunID        uuid.UUID
}

// ErrAccountMismatch means the caller imported from one account's page a file
// that belongs to a different account.
var ErrAccountMismatch = errors.New("imports: the file belongs to a different account")

// ErrUnreadable wraps a structural problem in the file, such as an unreadable
// date or a missing column. Unwrap it for the parser's own message.
type unreadableError struct{ err error }

func (e unreadableError) Error() string        { return "imports: unreadable statement: " + e.err.Error() }
func (e unreadableError) Unwrap() error        { return e.err }
func (e unreadableError) Is(target error) bool { return target == ErrUnreadable }

// ErrUnreadable matches any unreadableError.
var ErrUnreadable = errors.New("imports: unreadable statement")

// Import parses a file and stores what it says. When expect is set — the
// import started from an account's own page — a file for any other account is
// refused rather than silently filed elsewhere.
func (s *Service) Import(ctx context.Context, userID uuid.UUID, filename string, data []byte, expect *uuid.UUID) (Result, error) {
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])

	parsed, err := statement.Parse(filename, data)
	if err != nil {
		if errors.Is(err, statement.ErrUnrecognised) {
			return Result{}, err
		}
		return Result{}, unreadableError{err}
	}
	result := Result{Source: parsed.Source}

	if prior, err := s.db.Q().ImportRunByContent(ctx, userID, sha); err == nil {
		result.Unchanged = true
		if prior.AccountID != nil {
			result.Accounts = []AccountResult{{AccountID: *prior.AccountID, RunID: prior.ID}}
		}
		return result, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return Result{}, err
	}

	err = s.db.InTx(ctx, func(q *store.Queries) error {
		if err := q.LockUser(ctx, userID); err != nil {
			return err
		}
		if err := s.seedDefaultRules(ctx, q, userID); err != nil {
			return err
		}
		rules, err := q.CategoryRulesByUser(ctx, userID)
		if err != nil {
			return err
		}
		var from, to civil.Date
		for _, st := range parsed.Statements {
			r, err := s.importStatement(ctx, q, userID, parsed.Source, filename, sha, st, rules, expect)
			if err != nil {
				return err
			}
			result.Accounts = append(result.Accounts, r)
			if from.IsZero() || st.PeriodStart.Before(from) {
				from = st.PeriodStart
			}
			if st.PeriodEnd.After(to) {
				to = st.PeriodEnd
			}
		}
		// A card payment and its bank debit are one transfer; matched now,
		// neither counts as spending.
		_, err = transfers.AutoMatch(ctx, q, userID, from, to)
		return err
	})
	if err != nil {
		return Result{}, err
	}

	for _, a := range result.Accounts {
		logging.FromContext(ctx).Info("statement imported", "account_id", a.AccountID,
			"source", parsed.Source, "period_start", a.PeriodStart.String(), "period_end", a.PeriodEnd.String(),
			"new", a.New, "duplicates", a.Duplicates, "warnings", len(a.Warnings))
	}
	return result, nil
}

func (s *Service) importStatement(ctx context.Context, q *store.Queries, userID uuid.UUID, source statement.Source,
	filename, sha string, st statement.Statement, rules []store.CategoryRule, expect *uuid.UUID) (AccountResult, error) {
	id := st.Identity
	if st.PeriodStart.IsZero() || st.PeriodEnd.IsZero() {
		// Only possible for a file with no movements and no stated date,
		// which says nothing that can be stored.
		return AccountResult{}, unreadableError{fmt.Errorf("the %s statement for %s has no movements and no date",
			source, id.ExternalNumber)}
	}
	account, created, err := q.EnsureAccount(ctx, store.Account{
		ID: uuid.New(), UserID: userID, Class: string(id.Class), Type: id.Type,
		Institution: id.Institution, ExternalNumber: id.ExternalNumber, Currency: id.Currency,
		DisplayName: id.DisplayName,
	})
	if err != nil {
		return AccountResult{}, err
	}
	if expect != nil && account.ID != *expect {
		return AccountResult{}, fmt.Errorf("%w: %s %s", ErrAccountMismatch, id.Institution, id.ExternalNumber)
	}

	run := store.ImportRun{
		ID: uuid.New(), UserID: userID, AccountID: &account.ID, Source: string(source), Status: "ok",
		Filename: filename, ContentSHA256: sha, PeriodStart: st.PeriodStart, PeriodEnd: st.PeriodEnd,
		LinesSeen: len(st.Lines),
	}
	stmtID := uuid.New()
	res := AccountResult{
		AccountID: account.ID, DisplayName: account.DisplayName, Created: created, RunID: run.ID,
		PeriodStart: st.PeriodStart, PeriodEnd: st.PeriodEnd, Lines: len(st.Lines),
		Opening: st.Opening, Closing: st.Closing, ChainBreak: st.ChainBreak, Warnings: st.Warnings,
	}

	var firstEntry uuid.UUID
	entries := make([]store.Entry, 0, len(st.Lines))
	for i, l := range st.Lines {
		e := store.Entry{
			ID: uuid.New(), UserID: userID, AccountID: account.ID, StatementID: &stmtID, RunID: &run.ID,
			BookedOn: l.BookedOn, BookedAt: l.BookedAt, PostedOn: l.PostedOn, Seq: i,
			Amount: l.Amount, Kind: string(l.Kind), Description: l.Description, BankRef: l.BankRef,
			BankCategory: l.BankCategory, BankBalance: l.RunningBalance, DedupKey: statement.DedupKey(id, l),
			Raw: l.Raw,
		}
		m := matchRules(l, rules)
		if m.categoryID != nil {
			src := "rule"
			e.CategoryID, e.CategorySource = m.categoryID, &src
		}
		if m.setKind != nil && *m.setKind != e.Kind {
			e.UserKind = m.setKind
		}
		entries = append(entries, e)
	}

	// The run and the statement go first so the entries can reference them.
	if err := q.CreateImportRun(ctx, run); err != nil {
		return AccountResult{}, err
	}
	if err := q.CreateStatement(ctx, store.Statement{
		ID: stmtID, UserID: userID, AccountID: account.ID, RunID: run.ID,
		PeriodStart: st.PeriodStart, PeriodEnd: st.PeriodEnd, Opening: st.Opening, Closing: st.Closing,
		Available: st.Available, Held: st.Held, LineCount: len(st.Lines), Warnings: st.Warnings,
	}); err != nil {
		return AccountResult{}, err
	}

	for i, e := range entries {
		inserted, err := q.InsertEntryIfNew(ctx, e)
		if err != nil {
			return AccountResult{}, fmt.Errorf("imports: line %d: %w", st.Lines[i].LineNo, err)
		}
		if inserted {
			res.New++
		} else {
			res.Duplicates++
		}
		if i == 0 {
			if inserted {
				firstEntry = e.ID
			} else if firstEntry, err = q.EntryIDByDedupKey(ctx, account.ID, e.DedupKey); err != nil {
				return AccountResult{}, err
			}
		}
	}
	run.LinesNew, run.LinesDuplicate = res.New, res.Duplicates

	// What the bank printed becomes checkpoints: the opening just before the
	// statement's first line, exact even when the file starts mid-day, and
	// the closing at the end of the period.
	if st.Opening != nil && firstEntry != uuid.Nil {
		if err := q.CreateCheckpoint(ctx, store.Checkpoint{
			ID: uuid.New(), UserID: userID, AccountID: account.ID, AsOf: st.PeriodStart.AddDays(-1),
			BeforeEntryID: &firstEntry, Balance: *st.Opening, Source: store.SourceStatementOpening,
			StatementID: &stmtID, RunID: &run.ID,
		}); err != nil {
			return AccountResult{}, err
		}
	}
	if st.Closing != nil {
		if err := q.CreateCheckpoint(ctx, store.Checkpoint{
			ID: uuid.New(), UserID: userID, AccountID: account.ID, AsOf: st.PeriodEnd,
			Balance: *st.Closing, Source: store.SourceStatementClosing, StatementID: &stmtID, RunID: &run.ID,
		}); err != nil {
			return AccountResult{}, err
		}
	}
	if err := q.SetImportRunCounts(ctx, run.ID, res.New, res.Duplicates); err != nil {
		return AccountResult{}, err
	}

	if st.Opening == nil {
		cps, err := q.CheckpointsByAccount(ctx, userID, account.ID)
		if err != nil {
			return AccountResult{}, err
		}
		res.NeedsOpening = len(cps) == 0
	}
	return res, nil
}

// seedDefaultRules gives the user the default card rules the first time they
// import anything. Rules already present — including ones the user edited —
// are left alone.
func (s *Service) seedDefaultRules(ctx context.Context, q *store.Queries, userID uuid.UUID) error {
	for bankLabel, categoryName := range defaultCardRules {
		category, found, err := s.categories.FindByName(ctx, userID, categoryName)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		id := category.ID
		if _, err := q.CreateCategoryRuleIfNew(ctx, store.CategoryRule{
			ID: uuid.New(), UserID: userID, MatchText: bankLabel, CategoryID: &id,
		}); err != nil {
			return err
		}
	}
	return nil
}
