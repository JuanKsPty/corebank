package store

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/JuanKsPty/corebank/api/internal/ledger"
	"github.com/JuanKsPty/corebank/api/internal/money"
)

// TxStatus is a movement's lifecycle state.
type TxStatus string

const (
	// StatusPending covers two situations, told apart by whether HoldID is set.
	//
	//   HoldID == nil  the movement was submitted to the ledger and the outcome
	//                  is not yet recorded here. This window is milliseconds
	//                  wide and only persists if the process died inside it.
	//   HoldID != nil  funds are reserved in the ledger and the movement is
	//                  waiting for the customer to confirm or cancel it.
	//
	// Both mean "not final", which is why they share a status; the reconciliation
	// sweeper handles them differently and each case is named where it is
	// treated.
	StatusPending   TxStatus = "pending"
	StatusCompleted TxStatus = "completed"
	StatusFailed    TxStatus = "failed"
	StatusVoided    TxStatus = "voided"
	StatusExpired   TxStatus = "expired"
)

// Sources and origins. Origin is what makes "show me what the assistant did"
// answerable, which matters when the assistant can move money.
const (
	SourceSeed = "seed"
	SourceLive = "live"

	OriginAPI  = "api"
	OriginChat = "chat"
	// OriginIBKRSync marks a cash movement corebank posted on its own
	// initiative because an IBKR sync reported it, not because the customer
	// asked for it in the moment.
	OriginIBKRSync = "ibkr_sync"
	// OriginBankImport marks a movement a bank-account statement import
	// posted, the same way OriginIBKRSync marks one an IBKR sync posted.
	OriginBankImport = "bank_import"
)

// ExternalAccount is the counterparty for money entering or leaving the bank.
// The provided dataset uses this sentinel, and deposits and withdrawals reuse it
// so a statement reads the same for imported and live movements.
const ExternalAccount = "EXTERNAL"

// Transaction is a movement's metadata. The amount is duplicated here and in the
// ledger on purpose: the ledger is authoritative for money, and this row is what
// makes the movement describable, searchable and attributable.
type Transaction struct {
	ID          uuid.UUID
	Kind        ledger.MovementKind
	Status      TxStatus
	Amount      money.Cents
	Currency    string
	FromAccount string
	ToAccount   string
	Description string
	Source      string
	Origin      string
	InitiatedBy uuid.UUID
	// HoldID is the pending ledger transfer reserving the funds, for a movement
	// awaiting confirmation. ID above is reserved for the transfer that settles
	// or voids it.
	HoldID        uuid.UUID
	HoldExpiresAt time.Time
	// FailureCode is the ledger's rejection reason, kept verbatim so a failure is
	// diagnosable from the row alone.
	FailureCode string
	// CategoryID is metadata a customer attaches after the fact — see the
	// categories package. It has no bearing on the movement itself, which is
	// why it is not part of any equality check the idempotency replay does.
	CategoryID *uuid.UUID
	OccurredAt time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// AwaitingConfirmation reports whether this movement is holding funds and waiting
// for the customer.
func (t Transaction) AwaitingConfirmation() bool {
	return t.Status == StatusPending && t.HoldID != uuid.Nil
}

// txColumns is the select list every read below shares, so a column added to the
// scan cannot be forgotten in one query and present in another.
//
// The leading and trailing newlines are load-bearing: this is concatenated
// directly against `SELECT` and `FROM`, and without them the last column and the
// keyword merge into one identifier — producing a query with no FROM clause at
// all.
const txColumns = `
	id, kind, status, amount_cents, currency, from_account, to_account,
	description, source, origin, initiated_by, hold_id, hold_expires_at,
	failure_code, category_id, occurred_at, created_at, updated_at
`

// CreateTransaction inserts a movement, normally with StatusPending, before it is
// submitted to the ledger.
//
// The row is written first on purpose. The alternative — ledger first, then the
// row — leaves money moved with no record of why, which is unrecoverable. This
// way a crash leaves a pending row whose id is exactly the ledger transfer id to
// look up, so the sweeper can always determine what actually happened.
func (q *Queries) CreateTransaction(ctx context.Context, t Transaction) (Transaction, error) {
	const query = `
		INSERT INTO transactions (
			id, kind, status, amount_cents, currency, from_account, to_account,
			description, source, origin, initiated_by, hold_id, hold_expires_at, occurred_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING created_at, updated_at`

	err := q.q.QueryRow(ctx, query,
		t.ID, t.Kind.String(), string(t.Status), int64(t.Amount), t.Currency,
		nullableText(t.FromAccount), nullableText(t.ToAccount),
		t.Description, t.Source, t.Origin,
		nullableUUID(t.InitiatedBy), nullableUUID(t.HoldID), nullableTime(t.HoldExpiresAt),
		t.OccurredAt,
	).Scan(&t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return Transaction{}, wrap("store.CreateTransaction", err)
	}
	return t, nil
}

// SettleTransaction records a final outcome for a movement.
//
// The WHERE clause requires the row to still be pending, which makes the update
// safe to repeat: a retry after a lost response finds nothing to change instead
// of overwriting a decided outcome. `changed` reports whether this call was the
// one that decided it.
func (q *Queries) SettleTransaction(ctx context.Context, id uuid.UUID, status TxStatus, failureCode string) (changed bool, err error) {
	const query = `
		UPDATE transactions
		SET status = $2, failure_code = $3, updated_at = now()
		WHERE id = $1 AND status = 'pending'`

	tag, err := q.q.Exec(ctx, query, id, string(status), nullableText(failureCode))
	if err != nil {
		return false, wrap("store.SettleTransaction", err)
	}
	return tag.RowsAffected() == 1, nil
}

// TransactionByID loads one movement.
func (q *Queries) TransactionByID(ctx context.Context, id uuid.UUID) (Transaction, error) {
	query := `SELECT` + txColumns + `FROM transactions WHERE id = $1`

	t, err := scanTransaction(q.q.QueryRow(ctx, query, id))
	if err != nil {
		return Transaction{}, wrap("store.TransactionByID", err)
	}
	return t, nil
}

// TransactionByHold finds the movement holding funds under a given ledger hold,
// which is how a confirmation request is resolved: the client sends the hold id
// it was shown, and this is the only thing that maps it back to an owner.
func (q *Queries) TransactionByHold(ctx context.Context, holdID uuid.UUID) (Transaction, error) {
	query := `SELECT` + txColumns + `FROM transactions WHERE hold_id = $1`

	t, err := scanTransaction(q.q.QueryRow(ctx, query, holdID))
	if err != nil {
		return Transaction{}, wrap("store.TransactionByHold", err)
	}
	return t, nil
}

// StalePendingTransactions returns unresolved movements created before cutoff,
// for the reconciliation sweeper.
func (q *Queries) StalePendingTransactions(ctx context.Context, cutoff time.Time, limit int) ([]Transaction, error) {
	query := `SELECT` + txColumns + `
		FROM transactions
		WHERE status = 'pending' AND created_at < $1
		ORDER BY created_at
		LIMIT $2`

	rows, err := q.q.Query(ctx, query, cutoff, limit)
	if err != nil {
		return nil, wrap("store.StalePendingTransactions", err)
	}
	defer rows.Close()

	list, err := scanTransactions(rows)
	if err != nil {
		return nil, wrap("store.StalePendingTransactions", err)
	}
	return list, nil
}

// HistoryFilter bounds a history query. Zero values mean "no restriction", so the
// common case needs nothing but the accounts and a limit.
type HistoryFilter struct {
	// Accounts restricts the result to movements touching any of these account
	// numbers, on either side. Required: there is no endpoint that lists every
	// movement in the bank.
	Accounts []string
	Kind     ledger.MovementKind
	Since    time.Time
	Until    time.Time
	// Search matches the description, case-insensitively.
	Search string
	Limit  int
	// Cursor continues a previous page. Empty starts from the newest movement.
	Cursor Cursor
}

// Cursor is an opaque position in a history listing.
//
// Keyset pagination rather than OFFSET: a statement is read newest-first while
// new movements keep arriving at the top, and OFFSET would silently repeat or
// skip rows as the list shifts underneath the reader. The tie-break on id makes
// the order total, so two movements in the same instant cannot be conflated.
type Cursor struct {
	OccurredAt time.Time
	ID         uuid.UUID
}

func (c Cursor) IsZero() bool { return c.ID == uuid.Nil }

// Encode renders the cursor for a client. The format is deliberately opaque —
// callers must treat it as a token and not construct one.
func (c Cursor) Encode() string {
	if c.IsZero() {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(
		[]byte(c.OccurredAt.UTC().Format(time.RFC3339Nano) + "|" + c.ID.String()))
}

// DecodeCursor parses a cursor produced by Encode.
func DecodeCursor(raw string) (Cursor, error) {
	if raw == "" {
		return Cursor{}, nil
	}

	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return Cursor{}, fmt.Errorf("store: malformed cursor: %w", err)
	}
	stamp, id, found := strings.Cut(string(decoded), "|")
	if !found {
		return Cursor{}, fmt.Errorf("store: malformed cursor")
	}

	at, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return Cursor{}, fmt.Errorf("store: malformed cursor timestamp: %w", err)
	}
	parsedID, err := uuid.Parse(id)
	if err != nil {
		return Cursor{}, fmt.Errorf("store: malformed cursor id: %w", err)
	}
	return Cursor{OccurredAt: at, ID: parsedID}, nil
}

// Page is one page of history plus the cursor for the next one.
type Page struct {
	Transactions []Transaction
	Next         Cursor
	HasMore      bool
}

// History lists movements newest first.
func (q *Queries) History(ctx context.Context, f HistoryFilter) (Page, error) {
	if len(f.Accounts) == 0 {
		// Refusing rather than returning everything: an unscoped listing would be
		// every customer's statement, and a caller that forgot to pass the
		// accounts must not get that by accident.
		return Page{}, fmt.Errorf("store.History: at least one account is required")
	}
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	// Arguments are positional and built alongside the clauses so the two cannot
	// drift apart. Everything is parameterised; no value is ever formatted into
	// the SQL.
	args := []any{f.Accounts}
	clauses := []string{`(from_account = ANY($1) OR to_account = ANY($1))`}

	add := func(clause string, value any) {
		args = append(args, value)
		clauses = append(clauses, fmt.Sprintf(clause, len(args)))
	}

	if f.Kind != 0 {
		add(`kind = $%d`, f.Kind.String())
	}
	if !f.Since.IsZero() {
		add(`occurred_at >= $%d`, f.Since)
	}
	if !f.Until.IsZero() {
		add(`occurred_at <= $%d`, f.Until)
	}
	if f.Search != "" {
		// ILIKE with the pattern as a parameter: the wildcards are added here,
		// the user's text stays data.
		add(`description ILIKE $%d`, "%"+f.Search+"%")
	}
	if !f.Cursor.IsZero() {
		// Strictly after the cursor in the descending (occurred_at, id) order.
		args = append(args, f.Cursor.OccurredAt, f.Cursor.ID)
		clauses = append(clauses, fmt.Sprintf(
			`(occurred_at, id) < ($%d, $%d)`, len(args)-1, len(args)))
	}

	// One extra row is fetched to learn whether another page exists, which avoids
	// a second COUNT query over the same predicate.
	args = append(args, limit+1)

	query := `SELECT` + txColumns + `
		FROM transactions
		WHERE ` + strings.Join(clauses, " AND ") + `
		ORDER BY occurred_at DESC, id DESC
		LIMIT $` + fmt.Sprint(len(args))

	rows, err := q.q.Query(ctx, query, args...)
	if err != nil {
		return Page{}, wrap("store.History", err)
	}
	defer rows.Close()

	list, err := scanTransactions(rows)
	if err != nil {
		return Page{}, wrap("store.History", err)
	}

	page := Page{Transactions: list}
	if len(list) > limit {
		page.Transactions = list[:limit]
		page.HasMore = true
		last := page.Transactions[limit-1]
		page.Next = Cursor{OccurredAt: last.OccurredAt, ID: last.ID}
	}
	return page, nil
}

// FlowPoint is one day's money in and out, for the dashboard chart.
type FlowPoint struct {
	Day time.Time
	In  money.Cents
	Out money.Cents
}

// Flow is a daily series together with the window it actually covers.
type Flow struct {
	Points []FlowPoint
	From   time.Time
	To     time.Time
	// Recent is false when the window had to be moved back to where the customer's
	// activity is, so the interface can label the period instead of implying the
	// last thirty days.
	Recent bool
}

// DailyFlowWindow aggregates the last `days` of activity, falling back to the last
// `days` in which there *was* any.
//
// The plain "since today minus thirty days" query is the right one for an account in
// use, and returns nothing for one whose history ends earlier — an empty chart that
// reads as broken rather than as accurate. So when the recent window is empty, the
// window moves to end at the customer's most recent movement, and the caller is told
// which window it got.
func (q *Queries) DailyFlowWindow(ctx context.Context, accounts []string, days int, now time.Time) (Flow, error) {
	if days <= 0 || days > 365 {
		days = 30
	}
	span := time.Duration(days) * 24 * time.Hour

	from := now.Add(-span).Truncate(24 * time.Hour)
	points, err := q.DailyFlow(ctx, accounts, from)
	if err != nil {
		return Flow{}, err
	}
	if len(points) > 0 {
		return Flow{Points: points, From: from, To: now, Recent: true}, nil
	}

	const latestQuery = `
		SELECT max(occurred_at) FROM transactions
		WHERE (from_account = ANY($1) OR to_account = ANY($1)) AND status = 'completed'`

	var latest *time.Time
	if err := q.q.QueryRow(ctx, latestQuery, accounts).Scan(&latest); err != nil {
		return Flow{}, wrap("store.DailyFlowWindow", err)
	}
	if latest == nil {
		// No completed movements at all. An empty series here is the truth.
		return Flow{From: from, To: now, Recent: true}, nil
	}

	from = latest.Add(-span).Truncate(24 * time.Hour)
	points, err = q.DailyFlowUntil(ctx, accounts, from, *latest)
	if err != nil {
		return Flow{}, err
	}
	return Flow{Points: points, From: from, To: *latest, Recent: false}, nil
}

// DailyFlow aggregates completed movements per day from `since` onwards.
//
// Aggregated in SQL rather than by loading rows and summing in Go: the seeded
// history is thousands of movements per account, and a chart needs a few dozen
// points.
func (q *Queries) DailyFlow(ctx context.Context, accounts []string, since time.Time) ([]FlowPoint, error) {
	const query = `
		SELECT date_trunc('day', occurred_at) AS day,
		       coalesce(sum(amount_cents) FILTER (WHERE to_account = ANY($1)), 0)   AS inflow,
		       coalesce(sum(amount_cents) FILTER (WHERE from_account = ANY($1)), 0) AS outflow
		FROM transactions
		WHERE (from_account = ANY($1) OR to_account = ANY($1))
		  AND status = 'completed'
		  AND occurred_at >= $2
		GROUP BY day
		ORDER BY day`

	rows, err := q.q.Query(ctx, query, accounts, since)
	if err != nil {
		return nil, wrap("store.DailyFlow", err)
	}
	defer rows.Close()

	return scanFlow(rows)
}

// DailyFlowUntil is DailyFlow bounded at both ends.
func (q *Queries) DailyFlowUntil(ctx context.Context, accounts []string, since, until time.Time) ([]FlowPoint, error) {
	const query = `
		SELECT date_trunc('day', occurred_at) AS day,
		       coalesce(sum(amount_cents) FILTER (WHERE to_account = ANY($1)), 0)   AS inflow,
		       coalesce(sum(amount_cents) FILTER (WHERE from_account = ANY($1)), 0) AS outflow
		FROM transactions
		WHERE (from_account = ANY($1) OR to_account = ANY($1))
		  AND status = 'completed'
		  AND occurred_at >= $2 AND occurred_at <= $3
		GROUP BY day
		ORDER BY day`

	rows, err := q.q.Query(ctx, query, accounts, since, until)
	if err != nil {
		return nil, wrap("store.DailyFlowUntil", err)
	}
	defer rows.Close()

	return scanFlow(rows)
}

func scanFlow(rows pgx.Rows) ([]FlowPoint, error) {
	var points []FlowPoint
	for rows.Next() {
		var (
			p        FlowPoint
			in, outQ int64
		)
		if err := rows.Scan(&p.Day, &in, &outQ); err != nil {
			return nil, wrap("store.DailyFlow", err)
		}
		p.In, p.Out = money.Cents(in), money.Cents(outQ)
		points = append(points, p)
	}
	return points, wrap("store.scanFlow", rows.Err())
}

// --- idempotency ------------------------------------------------------------

// FindIdempotent returns the movement a previous request with this key created.
func (q *Queries) FindIdempotent(ctx context.Context, userID uuid.UUID, key string) (uuid.UUID, error) {
	const query = `SELECT transaction_id FROM idempotency_keys WHERE user_id = $1 AND key = $2`

	var id uuid.UUID
	if err := q.q.QueryRow(ctx, query, userID, key).Scan(&id); err != nil {
		return uuid.Nil, wrap("store.FindIdempotent", err)
	}
	return id, nil
}

// SaveIdempotencyKey binds a client-supplied key to the movement it created.
//
// Recorded inside the same transaction as the movement, so a key can never point
// at a row that does not exist and a movement can never be created without its
// key being claimed.
func (q *Queries) SaveIdempotencyKey(ctx context.Context, userID uuid.UUID, key string, txID uuid.UUID) error {
	const query = `
		INSERT INTO idempotency_keys (user_id, key, transaction_id)
		VALUES ($1, $2, $3)`

	_, err := q.q.Exec(ctx, query, userID, key, txID)
	return wrap("store.SaveIdempotencyKey", err)
}

// --- scanning ---------------------------------------------------------------

func scanTransaction(s scanner) (Transaction, error) {
	var (
		t                     Transaction
		kind, status          string
		amount                int64
		from, to, failureCode *string
		initiatedBy, holdID   *uuid.UUID
		holdExpiresAt         *time.Time
		categoryID            *uuid.UUID
	)
	err := s.Scan(&t.ID, &kind, &status, &amount, &t.Currency, &from, &to,
		&t.Description, &t.Source, &t.Origin, &initiatedBy, &holdID, &holdExpiresAt,
		&failureCode, &categoryID, &t.OccurredAt, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return Transaction{}, err
	}
	t.CategoryID = categoryID

	parsedKind, err := ledger.ParseMovementKind(kind)
	if err != nil {
		return Transaction{}, err
	}
	t.Kind = parsedKind
	t.Status = TxStatus(status)
	t.Amount = money.Cents(amount)
	t.FromAccount = deref(from)
	t.ToAccount = deref(to)
	t.FailureCode = deref(failureCode)
	if initiatedBy != nil {
		t.InitiatedBy = *initiatedBy
	}
	if holdID != nil {
		t.HoldID = *holdID
	}
	if holdExpiresAt != nil {
		t.HoldExpiresAt = *holdExpiresAt
	}
	return t, nil
}

func scanTransactions(rows pgx.Rows) ([]Transaction, error) {
	var list []Transaction
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

// Helpers translating Go zero values to SQL NULL. A zero UUID or an empty string
// means "absent" throughout this package, and storing them as NULL keeps the
// database's own constraints meaningful.

func nullableText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nullableUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

func nullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
