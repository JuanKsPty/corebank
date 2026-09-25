package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/civil"
	"github.com/JuanKsPty/corebank/api/internal/money"
)

// ImportRun is one ingestion: an uploaded file or an IBKR sync.
type ImportRun struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	AccountID      *uuid.UUID
	Source         string
	Status         string // ok | unchanged | error
	Filename       string
	ContentSHA256  string
	PeriodStart    civil.Date
	PeriodEnd      civil.Date
	LinesSeen      int
	LinesNew       int
	LinesDuplicate int
	LinesFailed    int
	Details        map[string]any
	Error          string
	CreatedAt      time.Time
}

// CreateImportRun records a run. Runs are written once, with their final
// status, so a crash never leaves one claiming to be in progress.
func (q *Queries) CreateImportRun(ctx context.Context, r ImportRun) error {
	details, err := json.Marshal(nonNilMap(r.Details))
	if err != nil {
		return err
	}
	const query = `
		INSERT INTO import_runs (id, user_id, account_id, source, status, filename, content_sha256,
		                         period_start, period_end, lines_seen, lines_new, lines_duplicate,
		                         lines_failed, details, error)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`
	_, err = q.q.Exec(ctx, query, r.ID, r.UserID, r.AccountID, r.Source, r.Status, r.Filename,
		r.ContentSHA256, r.PeriodStart, r.PeriodEnd, r.LinesSeen, r.LinesNew, r.LinesDuplicate,
		r.LinesFailed, details, r.Error)
	return wrap("store.CreateImportRun", err)
}

// SetImportRunCounts records how many of a run's lines were new.
func (q *Queries) SetImportRunCounts(ctx context.Context, runID uuid.UUID, lineNew, duplicates int) error {
	return q.execOne(ctx, "store.SetImportRunCounts",
		`UPDATE import_runs SET lines_new = $2, lines_duplicate = $3 WHERE id = $1`, runID, lineNew, duplicates)
}

// SetImportRunDetails records a run's final counts and details.
func (q *Queries) SetImportRunDetails(ctx context.Context, runID uuid.UUID, lineNew, duplicates, failed int, details map[string]any) error {
	raw, err := json.Marshal(nonNilMap(details))
	if err != nil {
		return err
	}
	return q.execOne(ctx, "store.SetImportRunDetails",
		`UPDATE import_runs SET lines_new = $2, lines_duplicate = $3, lines_failed = $4, details = $5 WHERE id = $1`,
		runID, lineNew, duplicates, failed, raw)
}

// SetImportRunAccount attaches a run to the account it turned out to be for,
// once the file's identity has been resolved.
func (q *Queries) SetImportRunAccount(ctx context.Context, runID, accountID uuid.UUID) error {
	return q.execOne(ctx, "store.SetImportRunAccount",
		`UPDATE import_runs SET account_id = $2 WHERE id = $1`, runID, accountID)
}

// ImportRunByContent finds a successful run of the same bytes by the same
// user, so a re-upload of an unchanged file is reported as such.
func (q *Queries) ImportRunByContent(ctx context.Context, userID uuid.UUID, sha string) (ImportRun, error) {
	runs, err := q.importRuns(ctx, "store.ImportRunByContent",
		`WHERE user_id = $1 AND content_sha256 = $2 AND status = 'ok' ORDER BY created_at DESC LIMIT 1`, userID, sha)
	if err != nil {
		return ImportRun{}, err
	}
	if len(runs) == 0 {
		return ImportRun{}, wrap("store.ImportRunByContent", ErrNotFound)
	}
	return runs[0], nil
}

// ImportRunsByAccount lists an account's runs, newest first.
func (q *Queries) ImportRunsByAccount(ctx context.Context, userID, accountID uuid.UUID, limit int) ([]ImportRun, error) {
	return q.importRuns(ctx, "store.ImportRunsByAccount",
		`WHERE user_id = $1 AND account_id = $2 ORDER BY created_at DESC LIMIT $3`, userID, accountID, limit)
}

func (q *Queries) importRuns(ctx context.Context, op, where string, args ...any) ([]ImportRun, error) {
	rows, err := q.q.Query(ctx, `
		SELECT id, user_id, account_id, source, status, filename, content_sha256, period_start, period_end,
		       lines_seen, lines_new, lines_duplicate, lines_failed, details, error, created_at
		FROM import_runs `+where, args...)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()
	var out []ImportRun
	for rows.Next() {
		var r ImportRun
		var details []byte
		var start, end *civil.Date
		if err := rows.Scan(&r.ID, &r.UserID, &r.AccountID, &r.Source, &r.Status, &r.Filename,
			&r.ContentSHA256, &start, &end, &r.LinesSeen, &r.LinesNew, &r.LinesDuplicate,
			&r.LinesFailed, &details, &r.Error, &r.CreatedAt); err != nil {
			return nil, wrap(op, err)
		}
		if start != nil {
			r.PeriodStart = *start
		}
		if end != nil {
			r.PeriodEnd = *end
		}
		_ = json.Unmarshal(details, &r.Details)
		out = append(out, r)
	}
	return out, wrap(op, rows.Err())
}

// Statement is what one file said about one account for one period.
type Statement struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	AccountID   uuid.UUID
	RunID       uuid.UUID
	PeriodStart civil.Date
	PeriodEnd   civil.Date
	Opening     *money.Cents
	Closing     *money.Cents
	Available   *money.Cents
	Held        *money.Cents
	LineCount   int
	Warnings    []string
	CreatedAt   time.Time
}

// CreateStatement records a statement.
func (q *Queries) CreateStatement(ctx context.Context, s Statement) error {
	warnings, err := json.Marshal(nonNilStrings(s.Warnings))
	if err != nil {
		return err
	}
	_, err = q.q.Exec(ctx, `
		INSERT INTO statements (id, user_id, account_id, run_id, period_start, period_end, opening_cents,
		                        closing_cents, available_cents, held_cents, line_count, warnings)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		s.ID, s.UserID, s.AccountID, s.RunID, s.PeriodStart, s.PeriodEnd, centsArg(s.Opening),
		centsArg(s.Closing), centsArg(s.Available), centsArg(s.Held), s.LineCount, warnings)
	return wrap("store.CreateStatement", err)
}

// StatementsByAccount lists an account's statements by period.
func (q *Queries) StatementsByAccount(ctx context.Context, userID, accountID uuid.UUID) ([]Statement, error) {
	rows, err := q.q.Query(ctx, `
		SELECT id, user_id, account_id, run_id, period_start, period_end, opening_cents, closing_cents,
		       available_cents, held_cents, line_count, warnings, created_at
		FROM statements WHERE user_id = $1 AND account_id = $2 ORDER BY period_start, created_at`,
		userID, accountID)
	if err != nil {
		return nil, wrap("store.StatementsByAccount", err)
	}
	defer rows.Close()
	var out []Statement
	for rows.Next() {
		var s Statement
		var opening, closing, available, held *int64
		var warnings []byte
		if err := rows.Scan(&s.ID, &s.UserID, &s.AccountID, &s.RunID, &s.PeriodStart, &s.PeriodEnd,
			&opening, &closing, &available, &held, &s.LineCount, &warnings, &s.CreatedAt); err != nil {
			return nil, wrap("store.StatementsByAccount", err)
		}
		s.Opening, s.Closing, s.Available, s.Held = centsPtr(opening), centsPtr(closing), centsPtr(available), centsPtr(held)
		_ = json.Unmarshal(warnings, &s.Warnings)
		out = append(out, s)
	}
	return out, wrap("store.StatementsByAccount", rows.Err())
}

// Checkpoint sources.
const (
	SourceStatementOpening = "statement_opening"
	SourceStatementClosing = "statement_closing"
	SourceBroker           = "broker"
	SourceManual           = "manual"
)

// Checkpoint is a balance somebody stated: at the end of AsOf, or — when
// BeforeEntryID is set — immediately before that entry.
type Checkpoint struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	AccountID      uuid.UUID
	AsOf           civil.Date
	BeforeEntryID  *uuid.UUID
	Balance        money.Cents
	Source         string
	StatementID    *uuid.UUID
	RunID          *uuid.UUID
	Pinned         bool
	DueOn          *civil.Date
	MinimumPayment *money.Cents
	Note           string
	CreatedAt      time.Time
}

// CreateCheckpoint records a checkpoint.
func (q *Queries) CreateCheckpoint(ctx context.Context, c Checkpoint) error {
	_, err := q.q.Exec(ctx, `
		INSERT INTO balance_checkpoints (id, user_id, account_id, as_of, before_entry_id, balance_cents,
		                                 source, statement_id, run_id, pinned, due_on, minimum_payment_cents, note)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		c.ID, c.UserID, c.AccountID, c.AsOf, c.BeforeEntryID, int64(c.Balance), c.Source, c.StatementID,
		c.RunID, c.Pinned, c.DueOn, centsArg(c.MinimumPayment), c.Note)
	return wrap("store.CreateCheckpoint", err)
}

// UpsertBrokerCheckpoint records the broker's stated cash for a day, replacing
// an earlier sync's figure for the same day.
func (q *Queries) UpsertBrokerCheckpoint(ctx context.Context, c Checkpoint) error {
	_, err := q.q.Exec(ctx, `
		INSERT INTO balance_checkpoints (id, user_id, account_id, as_of, balance_cents, source, run_id)
		VALUES ($1, $2, $3, $4, $5, 'broker', $6)
		ON CONFLICT (account_id, as_of) WHERE source = 'broker'
		DO UPDATE SET balance_cents = EXCLUDED.balance_cents, run_id = EXCLUDED.run_id`,
		c.ID, c.UserID, c.AccountID, c.AsOf, int64(c.Balance), c.RunID)
	return wrap("store.UpsertBrokerCheckpoint", err)
}

// CheckpointsByAccount lists an account's checkpoints by date.
func (q *Queries) CheckpointsByAccount(ctx context.Context, userID, accountID uuid.UUID) ([]Checkpoint, error) {
	return q.checkpoints(ctx, "store.CheckpointsByAccount",
		`WHERE user_id = $1 AND account_id = $2 ORDER BY as_of, created_at`, userID, accountID)
}

// CheckpointsByUser lists every checkpoint of every account the user holds.
func (q *Queries) CheckpointsByUser(ctx context.Context, userID uuid.UUID) ([]Checkpoint, error) {
	return q.checkpoints(ctx, "store.CheckpointsByUser",
		`WHERE user_id = $1 ORDER BY account_id, as_of, created_at`, userID)
}

func (q *Queries) checkpoints(ctx context.Context, op, where string, args ...any) ([]Checkpoint, error) {
	rows, err := q.q.Query(ctx, `
		SELECT id, user_id, account_id, as_of, before_entry_id, balance_cents, source, statement_id, run_id,
		       pinned, due_on, minimum_payment_cents, note, created_at
		FROM balance_checkpoints `+where, args...)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()
	var out []Checkpoint
	for rows.Next() {
		var c Checkpoint
		var balance int64
		var minimum *int64
		if err := rows.Scan(&c.ID, &c.UserID, &c.AccountID, &c.AsOf, &c.BeforeEntryID, &balance, &c.Source,
			&c.StatementID, &c.RunID, &c.Pinned, &c.DueOn, &minimum, &c.Note, &c.CreatedAt); err != nil {
			return nil, wrap(op, err)
		}
		c.Balance = money.Cents(balance)
		c.MinimumPayment = centsPtr(minimum)
		out = append(out, c)
	}
	return out, wrap(op, rows.Err())
}

// DeleteManualCheckpoint removes a checkpoint the owner entered. Statement and
// broker checkpoints go only with the import that produced them.
func (q *Queries) DeleteManualCheckpoint(ctx context.Context, userID, id uuid.UUID) error {
	return q.execOne(ctx, "store.DeleteManualCheckpoint",
		`DELETE FROM balance_checkpoints WHERE id = $1 AND user_id = $2 AND source = 'manual'`, id, userID)
}

// PinCheckpoint makes id the account's anchor, or clears the pin when id is
// nil. The caller holds the user lock.
func (q *Queries) PinCheckpoint(ctx context.Context, userID, accountID uuid.UUID, id *uuid.UUID) error {
	if _, err := q.q.Exec(ctx, `UPDATE balance_checkpoints SET pinned = false
		WHERE user_id = $1 AND account_id = $2 AND pinned`, userID, accountID); err != nil {
		return wrap("store.PinCheckpoint", err)
	}
	if id == nil {
		return nil
	}
	return q.execOne(ctx, "store.PinCheckpoint", `UPDATE balance_checkpoints SET pinned = true
		WHERE id = $1 AND user_id = $2 AND account_id = $3`, *id, userID, accountID)
}

func centsArg(c *money.Cents) *int64 {
	if c == nil {
		return nil
	}
	v := int64(*c)
	return &v
}

func centsPtr(v *int64) *money.Cents {
	if v == nil {
		return nil
	}
	c := money.Cents(*v)
	return &c
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nonNilMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
