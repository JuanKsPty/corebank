// Package investments turns an IBKR Flex Query statement into a real
// portfolio.
//
// The split that runs through this whole package: settled cash is money
// corebank's ledger can hold and move, so a cash movement IBKR reports
// (a contribution, a dividend, interest, a fee, a trade's net settlement)
// becomes a real ledger posting through transactions.Service, the same path
// a customer's own deposit or withdrawal takes. A position or a trade
// describes shares of an instrument, not dollars — nothing else in corebank
// needs a TigerBeetle account for "10 shares of AAPL" — so those live only in
// Postgres, refreshed wholesale by every sync, and are never posted to the
// ledger. An investment account's true value is therefore always the sum of
// two numbers from two different sources of truth, shown as two numbers, not
// blended into one the ledger did not actually verify.
package investments

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/accounts"
	"github.com/JuanKsPty/corebank/api/internal/ibkr"
	"github.com/JuanKsPty/corebank/api/internal/ledger"
	"github.com/JuanKsPty/corebank/api/internal/logging"
	"github.com/JuanKsPty/corebank/api/internal/money"
	"github.com/JuanKsPty/corebank/api/internal/store"
	"github.com/JuanKsPty/corebank/api/internal/transactions"
)

var (
	// ErrNotInvestmentAccount means the account exists and is the caller's
	// own, but is not the "investment" kind an IBKR link attaches to.
	ErrNotInvestmentAccount = errors.New("investments: account is not an investment account")

	// ErrNotLinked means the account has no IBKR Flex Query configured yet.
	ErrNotLinked = errors.New("investments: account has no IBKR link configured")

	// ErrAccountMismatch means the Flex Query returned a report for a
	// different IBKR account than the one this corebank account is linked
	// to. Applying it would file someone else's cash and holdings here.
	ErrAccountMismatch = errors.New("investments: statement belongs to a different IBKR account")
)

// Service manages IBKR links and the portfolio data synced from them.
type Service struct {
	db            *store.DB
	accounts      *accounts.Service
	transactions  *transactions.Service
	encryptionKey []byte
	now           func() time.Time
}

func NewService(db *store.DB, accts *accounts.Service, txs *transactions.Service, encryptionKey []byte) *Service {
	return &Service{db: db, accounts: accts, transactions: txs, encryptionKey: encryptionKey, now: time.Now}
}

// SetLink stores, or replaces, the Flex Query configuration for one of the
// caller's own investment accounts.
func (s *Service) SetLink(ctx context.Context, userID uuid.UUID, accountNumber, ibkrAccountID, flexQueryID, flexToken string) error {
	account, err := s.resolveInvestmentAccount(ctx, userID, accountNumber)
	if err != nil {
		return err
	}

	ciphertext, err := encryptToken(s.encryptionKey, flexToken)
	if err != nil {
		return err
	}

	_, err = s.db.Q().UpsertIBKRLink(ctx, store.IBKRLink{
		ID:                  uuid.New(),
		AccountID:           account.ID,
		IBKRAccountID:       ibkrAccountID,
		FlexQueryID:         flexQueryID,
		FlexTokenCiphertext: ciphertext,
	})
	return err
}

// LinkStatus is what a settings page shows about a configured link — never
// the token itself.
type LinkStatus struct {
	IBKRAccountID  string
	LastSyncedAt   *time.Time
	LastSyncStatus string
	LastSyncError  string
}

// GetLinkStatus reports a linked account's sync history.
func (s *Service) GetLinkStatus(ctx context.Context, userID uuid.UUID, accountNumber string) (LinkStatus, error) {
	account, err := s.resolveInvestmentAccount(ctx, userID, accountNumber)
	if err != nil {
		return LinkStatus{}, err
	}
	link, err := s.db.Q().IBKRLinkByAccountID(ctx, account.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return LinkStatus{}, fmt.Errorf("%w: %s", ErrNotLinked, accountNumber)
		}
		return LinkStatus{}, err
	}
	return LinkStatus{
		IBKRAccountID:  link.IBKRAccountID,
		LastSyncedAt:   link.LastSyncedAt,
		LastSyncStatus: link.LastSyncStatus,
		LastSyncError:  link.LastSyncError,
	}, nil
}

// SyncResult tallies what one sync did, so a caller — a "sync now" button, a
// cron job's log line — can tell a healthy no-op run from one that actually
// moved money.
type SyncResult struct {
	CashMovementsPosted int
	// CashMovementsSkipped counts rows already recorded on this account —
	// the only kind of skip that is harmless.
	CashMovementsSkipped int
	// CashMovementsFailedBefore counts rows whose idempotency key points at
	// a movement that never completed (typically rejected for insufficient
	// funds on an earlier sync). They are skipped on every sync after that
	// first failure, so the balance silently leaves them out; they are
	// reported rather than hidden inside the skipped count.
	CashMovementsFailedBefore int
	// CashMovementsOtherAccount counts rows whose key was claimed by a
	// movement on a different corebank account — the same IBKR account
	// linked twice.
	CashMovementsOtherAccount int
	TradesRecorded            int
	TradesSkipped             int
	Positions                 int

	// PeriodFrom and PeriodTo are the window IBKR's report covers, and
	// GeneratedAt when IBKR produced it; zero when the report omits them.
	PeriodFrom  time.Time
	PeriodTo    time.Time
	GeneratedAt time.Time
	// MissingSections lists the report sections the Flex Query does not
	// include: "cash_transactions", "trades", "open_positions".
	MissingSections []string
	// Warnings are human-readable, in Spanish, for the account page.
	Warnings []string
}

// Sync fetches the linked account's Flex Query and applies it: new cash
// movements are posted to the ledger, trades and positions are recorded in
// Postgres. Safe to call repeatedly — a Flex Query returns its whole
// configured window every time, and every write here is idempotent against
// IBKR's own ids.
func (s *Service) Sync(ctx context.Context, userID uuid.UUID, accountNumber string) (SyncResult, error) {
	account, err := s.resolveInvestmentAccount(ctx, userID, accountNumber)
	if err != nil {
		return SyncResult{}, err
	}

	link, client, err := s.clientFor(ctx, account)
	if err != nil {
		return SyncResult{}, err
	}

	raw, err := client.Fetch(ctx)
	if err != nil {
		s.recordOutcome(ctx, account.ID, err)
		return SyncResult{}, err
	}

	stmt, err := ibkr.Parse(raw)
	if err != nil {
		s.recordOutcome(ctx, account.ID, err)
		return SyncResult{}, err
	}
	if err := checkStatementAccount(link.IBKRAccountID, stmt.AccountID); err != nil {
		s.recordOutcome(ctx, account.ID, err)
		return SyncResult{}, err
	}

	result, err := s.apply(ctx, userID, account, stmt)
	if err != nil {
		s.recordOutcome(ctx, account.ID, err)
		return SyncResult{}, err
	}

	describeStatement(&result, stmt, s.now())

	s.recordOutcome(ctx, account.ID, nil)
	logging.FromContext(ctx).Info("ibkr sync completed", "account_number", account.Number,
		"period_from", civilDate(result.PeriodFrom), "period_to", civilDate(result.PeriodTo),
		"cash_posted", result.CashMovementsPosted, "cash_skipped", result.CashMovementsSkipped,
		"cash_failed_before", result.CashMovementsFailedBefore,
		"cash_other_account", result.CashMovementsOtherAccount,
		"trades_recorded", result.TradesRecorded, "positions", result.Positions,
		"missing_sections", result.MissingSections, "warnings", len(result.Warnings))
	return result, nil
}

// SyncAll runs Sync for every account with an IBKR link configured — what a
// scheduled job calls, having no specific account of its own to target.
//
// One account's misconfigured token must not stop every other account's
// sync, so a failure is recorded against that account and the loop
// continues; Sync itself has already written the failure to the account's
// own last_sync_status, so this is only what the caller — a cron job's log
// line — needs to decide whether to alert on the run as a whole.
func (s *Service) SyncAll(ctx context.Context) (results map[string]SyncResult, failures map[string]error) {
	links, err := s.db.Q().AllIBKRLinks(ctx)
	if err != nil {
		return nil, map[string]error{"*": err}
	}

	results = make(map[string]SyncResult, len(links))
	failures = make(map[string]error)
	for _, link := range links {
		result, err := s.Sync(ctx, link.UserID, link.AccountNumber)
		if err != nil {
			failures[link.AccountNumber] = err
			continue
		}
		results[link.AccountNumber] = result
	}
	return results, failures
}

func (s *Service) clientFor(ctx context.Context, account store.Account) (store.IBKRLink, *ibkr.Client, error) {
	link, err := s.db.Q().IBKRLinkByAccountID(ctx, account.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.IBKRLink{}, nil, fmt.Errorf("%w: %s", ErrNotLinked, account.Number)
		}
		return store.IBKRLink{}, nil, err
	}
	token, err := decryptToken(s.encryptionKey, link.FlexTokenCiphertext)
	if err != nil {
		return store.IBKRLink{}, nil, err
	}
	return link, ibkr.New(token, link.FlexQueryID), nil
}

// checkStatementAccount refuses a report for a different IBKR account than
// the linked one. Either side being empty is let through: an older link may
// predate the field, and a report may omit the attribute.
func checkStatementAccount(linked, reported string) error {
	linked, reported = strings.TrimSpace(linked), strings.TrimSpace(reported)
	if linked == "" || reported == "" || strings.EqualFold(linked, reported) {
		return nil
	}
	return fmt.Errorf("%w: the Flex Query returned %s, but this account is linked to %s",
		ErrAccountMismatch, reported, linked)
}

// describeStatement fills in what the report itself says about its period and
// sections, and turns the ways a sync can succeed while bringing nothing new
// into warnings the account page can show.
func describeStatement(result *SyncResult, stmt ibkr.Statement, now time.Time) {
	result.PeriodFrom, result.PeriodTo, result.GeneratedAt = stmt.FromDate, stmt.ToDate, stmt.GeneratedAt

	if !stmt.HasCashTransactions {
		result.MissingSections = append(result.MissingSections, "cash_transactions")
	}
	if !stmt.HasTrades {
		result.MissingSections = append(result.MissingSections, "trades")
	}
	if !stmt.HasPositions {
		result.MissingSections = append(result.MissingSections, "open_positions")
	}
	if len(result.MissingSections) > 0 {
		result.Warnings = append(result.Warnings, "La Flex Query no incluye: "+
			strings.Join(ibkrSectionNames(result.MissingSections), ", ")+
			". Agrégalas en IBKR para que esos datos se actualicen.")
	}
	if !stmt.HasPositions {
		result.Warnings = append(result.Warnings,
			"Se conservaron las posiciones anteriores porque el reporte no trae la sección de posiciones abiertas.")
	}

	if !stmt.ToDate.IsZero() {
		if expected := previousBusinessDay(now); stmt.ToDate.Before(expected) {
			result.Warnings = append(result.Warnings, fmt.Sprintf(
				"Los datos de IBKR llegan solo hasta el %s. Si esperabas movimientos más recientes, revisa que el período de la Flex Query sea móvil (por ejemplo, \"Last 365 Calendar Days\") y no un rango fijo. IBKR publica cada día al día siguiente.",
				civilDate(stmt.ToDate)))
		}
	}

	if result.CashMovementsFailedBefore > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"%d movimiento(s) de efectivo fallaron en una sincronización anterior y no se vuelven a intentar, así que el efectivo no los incluye.",
			result.CashMovementsFailedBefore))
	}
	if result.CashMovementsOtherAccount > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"%d movimiento(s) de efectivo ya están registrados en otra cuenta de corebank vinculada a la misma cuenta de IBKR.",
			result.CashMovementsOtherAccount))
	}
}

func ibkrSectionNames(sections []string) []string {
	names := map[string]string{
		"cash_transactions": "Cash Transactions",
		"trades":            "Trades",
		"open_positions":    "Open Positions",
	}
	out := make([]string, len(sections))
	for i, s := range sections {
		out[i] = names[s]
	}
	return out
}

// previousBusinessDay is the most recent weekday strictly before now's date:
// the latest day an IBKR report generated today can cover, since IBKR
// publishes a day's activity only after its overnight processing. Market
// holidays are not modelled, so the warning it drives is worded as a hint.
func previousBusinessDay(now time.Time) time.Time {
	now = now.UTC()
	d := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	for d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
		d = d.AddDate(0, 0, -1)
	}
	return d
}

// civilDate formats a date-only value as YYYY-MM-DD, or "" for the zero time.
func civilDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.DateOnly)
}

func (s *Service) recordOutcome(ctx context.Context, accountID uuid.UUID, cause error) {
	status, message := "ok", ""
	if cause != nil {
		status, message = "error", cause.Error()
	}
	if err := s.db.Q().RecordSyncOutcome(ctx, accountID, status, message, s.now().UTC()); err != nil {
		logging.FromContext(ctx).Error("could not record an ibkr sync outcome",
			"account_id", accountID, "error", err)
	}
}

// apply writes a parsed statement's three sections. Cash goes through the
// ledger one movement at a time — each is its own idempotent, independently
// retryable operation via transactions.Service, exactly like a customer's own
// deposit — while trades and positions are Postgres-only and share one
// transaction, since neither needs the crash-safety a ledger posting does and
// replacing a whole snapshot half-written would be worse than not replacing
// it at all.
func (s *Service) apply(ctx context.Context, userID uuid.UUID, account store.Account, stmt ibkr.Statement) (SyncResult, error) {
	var result SyncResult

	refless := 0 // counts cash transactions seen so far with no ExternalRef at all
	for _, ct := range stmt.CashTransactions {
		if ct.ExternalRef == "" {
			refless++
		}
		outcome, err := s.postCashTransaction(ctx, userID, account, ct, refless)
		if err != nil {
			return SyncResult{}, fmt.Errorf("cash transaction %s: %w", ct.ExternalRef, err)
		}
		switch outcome {
		case cashPosted:
			result.CashMovementsPosted++
		case cashAlreadyRecorded:
			result.CashMovementsSkipped++
		case cashFailedBefore:
			result.CashMovementsFailedBefore++
		case cashOnOtherAccount:
			result.CashMovementsOtherAccount++
		}
	}

	err := s.db.InTx(ctx, func(q *store.Queries) error {
		for _, tr := range stmt.Trades {
			instrumentID, err := q.UpsertInstrument(ctx, tr.Symbol, tr.Currency, tr.AssetClass)
			if err != nil {
				return fmt.Errorf("trade %s: %w", tr.ExternalRef, err)
			}
			inserted, err := q.InsertTradeIfNew(ctx, store.InvestmentTrade{
				AccountID:       account.ID,
				InstrumentID:    instrumentID,
				ExternalRef:     tr.ExternalRef,
				Side:            tr.Side,
				Quantity:        tr.Quantity,
				PriceCents:      int64(tr.Price),
				CommissionCents: int64(tr.Commission),
				NetCashCents:    int64(tr.NetCash),
				TradeDate:       tr.TradeDate,
			})
			if err != nil {
				return fmt.Errorf("trade %s: %w", tr.ExternalRef, err)
			}
			if inserted {
				result.TradesRecorded++
			} else {
				result.TradesSkipped++
			}
		}

		if !stmt.HasPositions {
			// A query that never asked for positions says nothing about
			// them; replacing the snapshot with it would wipe every holding.
			return nil
		}
		if err := q.DeletePositionsByAccount(ctx, account.ID); err != nil {
			return err
		}
		for _, p := range stmt.Positions {
			instrumentID, err := q.UpsertInstrument(ctx, p.Symbol, p.Currency, p.AssetClass)
			if err != nil {
				return fmt.Errorf("position %s: %w", p.Symbol, err)
			}
			mark, value, cost := int64(p.MarkPrice), int64(p.MarketValue), int64(p.CostBasisPrice)
			if err := q.InsertPosition(ctx, store.InvestmentPosition{
				AccountID:      account.ID,
				InstrumentID:   instrumentID,
				Quantity:       p.Quantity,
				MarkPriceCents: &mark,
				MarketValue:    &value,
				CostBasis:      &cost,
				AsOf:           p.AsOf,
			}); err != nil {
				return fmt.Errorf("position %s: %w", p.Symbol, err)
			}
			result.Positions++
		}
		return nil
	})
	if err != nil {
		return SyncResult{}, err
	}
	return result, nil
}

// postCashTransaction posts one IBKR cash movement through the ordinary
// deposit/withdraw path, or reports it as already posted.
//
// The idempotency key is checked explicitly first, rather than only relying
// on transactions.Service's own replay handling, so this can report an
// accurate posted/skipped count in SyncResult — Deposit and Withdraw only
// ever report the resulting movement, not whether it was new.
//
// A DETAIL-level cash transaction reliably carries IBKR's own transactionID
// — verified against a real account — but reflowIndex exists for the report
// that does not: without it, two distinct SUMMARY-only rows with no
// transactionID at all (same type, same day) would collide onto the same
// key and the second would be wrongly skipped as a duplicate of the first.
func (s *Service) postCashTransaction(ctx context.Context, userID uuid.UUID, account store.Account, ct ibkr.CashTransaction, reflessIndex int) (outcome cashOutcome, err error) {
	if ct.Amount == 0 {
		return cashAlreadyRecorded, nil
	}

	ref := ct.ExternalRef
	if ref == "" {
		ref = fmt.Sprintf("noref-%d", reflessIndex)
	}
	idempotencyKey := "ibkr_cash:" + ref
	if txID, err := s.db.Q().FindIdempotent(ctx, userID, idempotencyKey); err == nil {
		return s.classifyExisting(ctx, account, txID)
	} else if !errors.Is(err, store.ErrNotFound) {
		return 0, err
	}

	description := ct.Type
	if ct.Symbol != "" {
		description = ct.Type + " " + ct.Symbol
	}
	req := transactions.Request{
		Account:        account.Number,
		Description:    description,
		Origin:         store.OriginIBKRSync,
		IdempotencyKey: idempotencyKey,
	}

	if ct.Amount > 0 {
		req.Amount = ct.Amount
		_, err = s.transactions.Deposit(ctx, userID, req)
	} else {
		req.Amount = -ct.Amount
		_, err = s.transactions.Withdraw(ctx, userID, req)
	}
	if err != nil {
		return 0, err
	}
	return cashPosted, nil
}

// cashOutcome is what happened to one IBKR cash row on this sync.
type cashOutcome int

const (
	cashPosted cashOutcome = iota + 1
	cashAlreadyRecorded
	cashFailedBefore
	cashOnOtherAccount
)

// classifyExisting explains why a cash row whose idempotency key is already
// claimed is being skipped, by looking at the movement the key points to.
// Only a completed movement on this same account is a genuine duplicate.
func (s *Service) classifyExisting(ctx context.Context, account store.Account, txID uuid.UUID) (cashOutcome, error) {
	tx, err := s.db.Q().TransactionByID(ctx, txID)
	if err != nil {
		return 0, err
	}
	if tx.FromAccount != account.Number && tx.ToAccount != account.Number {
		return cashOnOtherAccount, nil
	}
	if tx.Status != store.StatusCompleted {
		return cashFailedBefore, nil
	}
	return cashAlreadyRecorded, nil
}

// Position is one holding as of the last sync.
type Position struct {
	Symbol      string
	AssetClass  string
	Quantity    float64
	MarkPrice   money.Cents
	MarketValue money.Cents
	CostBasis   money.Cents
	AsOf        time.Time
}

// Portfolio is an investment account's value from both of its sources of
// truth, kept separate: Cash is what the ledger says, HoldingsValue is what
// the last sync said the positions were worth. Neither is a substitute for
// the other.
type Portfolio struct {
	Cash          money.Cents
	HoldingsValue money.Cents
	Positions     []Position
}

// Portfolio returns an account's cash balance and its position snapshot.
func (s *Service) Portfolio(ctx context.Context, userID uuid.UUID, accountNumber string) (Portfolio, error) {
	if _, err := s.resolveInvestmentAccount(ctx, userID, accountNumber); err != nil {
		return Portfolio{}, err
	}

	account, err := s.accounts.Get(ctx, userID, accountNumber)
	if err != nil {
		return Portfolio{}, err
	}

	positions, err := s.Positions(ctx, userID, accountNumber)
	if err != nil {
		return Portfolio{}, err
	}

	out := Portfolio{Cash: account.Balance.Available, Positions: positions}
	for _, p := range positions {
		out.HoldingsValue = out.HoldingsValue.Add(p.MarketValue)
	}
	return out, nil
}

// Positions lists an account's current holdings.
func (s *Service) Positions(ctx context.Context, userID uuid.UUID, accountNumber string) ([]Position, error) {
	account, err := s.resolveInvestmentAccount(ctx, userID, accountNumber)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.Q().PositionsByAccount(ctx, account.ID)
	if err != nil {
		return nil, err
	}

	out := make([]Position, 0, len(rows))
	for _, r := range rows {
		out = append(out, Position{
			Symbol:      r.Symbol,
			AssetClass:  r.AssetClass,
			Quantity:    r.Quantity,
			MarkPrice:   centsOrZero(r.MarkPriceCents),
			MarketValue: centsOrZero(r.MarketValue),
			CostBasis:   centsOrZero(r.CostBasis),
			AsOf:        r.AsOf,
		})
	}
	return out, nil
}

// Trade is one recorded execution.
type Trade struct {
	Symbol     string
	AssetClass string
	Side       string
	Quantity   float64
	Price      money.Cents
	Commission money.Cents
	NetCash    money.Cents
	TradeDate  time.Time
}

// defaultTradesLimit and maxTradesLimit bound how much history one call
// returns. A personal account's lifetime trade count is small enough that
// cursor pagination — built for a bank statement running to thousands of
// rows — is not a problem this endpoint has yet.
const (
	defaultTradesLimit = 200
	maxTradesLimit     = 1000
)

// Trades lists an account's recorded trades, most recent first.
func (s *Service) Trades(ctx context.Context, userID uuid.UUID, accountNumber string, limit int) ([]Trade, error) {
	account, err := s.resolveInvestmentAccount(ctx, userID, accountNumber)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > maxTradesLimit {
		limit = defaultTradesLimit
	}

	rows, err := s.db.Q().TradesByAccount(ctx, account.ID, limit)
	if err != nil {
		return nil, err
	}

	out := make([]Trade, 0, len(rows))
	for _, r := range rows {
		out = append(out, Trade{
			Symbol:     r.Symbol,
			AssetClass: r.AssetClass,
			Side:       r.Side,
			Quantity:   r.Quantity,
			Price:      money.Cents(r.PriceCents),
			Commission: money.Cents(r.CommissionCents),
			NetCash:    money.Cents(r.NetCashCents),
			TradeDate:  r.TradeDate,
		})
	}
	return out, nil
}

// resolveInvestmentAccount checks that accountNumber is one of userID's own
// accounts — via accounts.Service.Resolve, the same ownership check every
// other package in this application relies on — and that it is the
// "investment" kind an IBKR link makes sense to attach to.
func (s *Service) resolveInvestmentAccount(ctx context.Context, userID uuid.UUID, accountNumber string) (store.Account, error) {
	account, err := s.accounts.Resolve(ctx, userID, accountNumber)
	if err != nil {
		return store.Account{}, err
	}
	if account.Kind != ledger.KindInvestment {
		return store.Account{}, fmt.Errorf("%w: %s is a %s account", ErrNotInvestmentAccount, accountNumber, account.Kind)
	}
	return account, nil
}

func centsOrZero(c *int64) money.Cents {
	if c == nil {
		return 0
	}
	return money.Cents(*c)
}
