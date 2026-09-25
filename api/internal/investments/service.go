// Package investments syncs an IBKR account through the Flex Web Service.
//
// A sync writes three things, in one transaction: IBKR's cash movements and
// the cash side of each trade become the brokerage account's movements; the
// open positions replace the holdings snapshot; and the Cash Report's ending
// cash becomes a checkpoint the computed cash has to agree with. Cash may be
// negative — a margin loan is a real balance — so nothing IBKR reports is ever
// refused for being an overdraft.
package investments

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/civil"
	"github.com/JuanKsPty/corebank/api/internal/ibkr"
	"github.com/JuanKsPty/corebank/api/internal/logging"
	"github.com/JuanKsPty/corebank/api/internal/money"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

var (
	// ErrNotBrokerage means the account exists and is the caller's own, but
	// is not a brokerage account.
	ErrNotBrokerage = errors.New("investments: account is not a brokerage account")
	// ErrNotLinked means the account has no IBKR Flex Query configured.
	ErrNotLinked = errors.New("investments: account has no IBKR link configured")
	// ErrAccountNotFound means no such account belongs to the caller.
	ErrAccountNotFound = errors.New("investments: account not found")
	// ErrAccountMismatch means the Flex Query returned a report for a
	// different IBKR account than the linked one.
	ErrAccountMismatch = errors.New("investments: statement belongs to a different IBKR account")
)

// Fetcher downloads a Flex report. The real one is an *ibkr.Client; tests pass
// a canned statement.
type Fetcher interface {
	Fetch(ctx context.Context) ([]byte, error)
}

// Service manages IBKR links and syncs.
type Service struct {
	db            *store.DB
	encryptionKey []byte
	now           func() time.Time
	// newClient builds the Fetcher for a link's token and query.
	newClient func(token, queryID string) Fetcher
}

func NewService(db *store.DB, encryptionKey []byte) *Service {
	return &Service{db: db, encryptionKey: encryptionKey, now: time.Now,
		newClient: func(token, queryID string) Fetcher { return ibkr.New(token, queryID) }}
}

// Link connects an IBKR account for the first time, creating its brokerage
// account, or replaces the token and query of an existing link.
func (s *Service) Link(ctx context.Context, userID uuid.UUID, ibkrAccountID, flexQueryID, flexToken string) (store.Account, error) {
	ciphertext, err := encryptToken(s.encryptionKey, flexToken)
	if err != nil {
		return store.Account{}, err
	}
	ibkrAccountID = strings.ToUpper(strings.TrimSpace(ibkrAccountID))
	var account store.Account
	err = s.db.InTx(ctx, func(q *store.Queries) error {
		if err := q.LockUser(ctx, userID); err != nil {
			return err
		}
		a, _, err := q.EnsureAccount(ctx, store.Account{
			ID: uuid.New(), UserID: userID, Class: "asset", Type: "brokerage", Institution: "ibkr",
			ExternalNumber: ibkrAccountID, Currency: "USD", DisplayName: "IBKR " + ibkrAccountID,
		})
		if err != nil {
			return err
		}
		account = a
		return q.UpsertIBKRLink(ctx, store.IBKRLink{
			ID: uuid.New(), UserID: userID, AccountID: a.ID, IBKRAccountID: ibkrAccountID,
			FlexQueryID: strings.TrimSpace(flexQueryID), FlexTokenCiphertext: ciphertext,
		})
	})
	return account, err
}

// LinkStatus reports a link's sync history — never the token.
func (s *Service) LinkStatus(ctx context.Context, userID, accountID uuid.UUID) (store.IBKRLink, error) {
	if _, err := s.brokerage(ctx, userID, accountID); err != nil {
		return store.IBKRLink{}, err
	}
	link, err := s.db.Q().IBKRLinkByAccount(ctx, userID, accountID)
	if errors.Is(err, store.ErrNotFound) {
		return store.IBKRLink{}, ErrNotLinked
	}
	return link, err
}

// SyncResult is what one sync saw and did.
type SyncResult struct {
	RunID           uuid.UUID
	CashNew         int
	CashDuplicate   int
	CashFailed      int
	TradesNew       int
	TradesDuplicate int
	Positions       int
	PeriodFrom      time.Time
	PeriodTo        time.Time
	GeneratedAt     time.Time
	// ReportedCash is the Cash Report's ending USD cash, when the query
	// includes the section.
	ReportedCash    *money.Cents
	MissingSections []string
	Warnings        []string
}

// Sync fetches the account's Flex report and applies it.
func (s *Service) Sync(ctx context.Context, userID, accountID uuid.UUID) (SyncResult, error) {
	account, err := s.brokerage(ctx, userID, accountID)
	if err != nil {
		return SyncResult{}, err
	}
	link, err := s.db.Q().IBKRLinkByAccount(ctx, userID, accountID)
	if errors.Is(err, store.ErrNotFound) {
		return SyncResult{}, ErrNotLinked
	} else if err != nil {
		return SyncResult{}, err
	}
	token, err := decryptToken(s.encryptionKey, link.FlexTokenCiphertext)
	if err != nil {
		return SyncResult{}, err
	}

	result, err := s.fetchAndApply(ctx, account, link, token)
	if err != nil {
		s.recordOutcome(ctx, accountID, err)
		s.recordFailedRun(ctx, userID, accountID, err)
		return SyncResult{}, err
	}
	s.recordOutcome(ctx, accountID, nil)
	logging.FromContext(ctx).Info("ibkr sync completed", "account_id", accountID,
		"period_from", civilDate(result.PeriodFrom), "period_to", civilDate(result.PeriodTo),
		"cash_new", result.CashNew, "cash_duplicate", result.CashDuplicate, "cash_failed", result.CashFailed,
		"trades_new", result.TradesNew, "positions", result.Positions, "warnings", len(result.Warnings))
	return result, nil
}

func (s *Service) fetchAndApply(ctx context.Context, account store.Account, link store.IBKRLink, token string) (SyncResult, error) {
	raw, err := s.newClient(token, link.FlexQueryID).Fetch(ctx)
	if err != nil {
		return SyncResult{}, err
	}
	stmt, err := ibkr.Parse(raw)
	if err != nil {
		return SyncResult{}, err
	}
	if err := checkStatementAccount(link.IBKRAccountID, stmt.AccountID); err != nil {
		return SyncResult{}, err
	}
	return s.apply(ctx, account, stmt)
}

func (s *Service) apply(ctx context.Context, account store.Account, stmt ibkr.Statement) (SyncResult, error) {
	var result SyncResult
	userID, accountID := account.UserID, account.ID
	runID := uuid.New()
	result.RunID = runID

	err := s.db.InTx(ctx, func(q *store.Queries) error {
		if err := q.LockUser(ctx, userID); err != nil {
			return err
		}
		// The run goes first so movements can reference it; its counts are
		// written once they are known.
		if err := q.CreateImportRun(ctx, store.ImportRun{ID: runID, UserID: userID, AccountID: &accountID,
			Source: "ibkr_flex", Status: "ok", PeriodStart: day(stmt.FromDate), PeriodEnd: day(stmt.ToDate),
			LinesSeen: len(stmt.CashTransactions) + len(stmt.Trades)}); err != nil {
			return err
		}

		for i, ct := range stmt.CashTransactions {
			if ct.Amount == 0 {
				continue
			}
			if !strings.EqualFold(ct.Currency, "USD") && ct.Currency != "" {
				// Kept out rather than posted as dollars. It stays out of
				// every sync until currencies are supported, and is counted
				// so the owner knows.
				result.CashFailed++
				continue
			}
			inserted, err := q.InsertEntryIfNew(ctx, store.Entry{
				ID: uuid.New(), UserID: userID, AccountID: accountID, RunID: &runID,
				BookedOn: civil.Of(ct.ReportDate), Seq: i, Amount: ct.Amount, Kind: cashKind(ct),
				Description: cashDescription(ct), BankRef: ct.ExternalRef, BankCategory: ct.Type,
				DedupKey: "ibkr_cash:" + ct.Key,
				Raw:      map[string]string{"type": ct.Type, "symbol": ct.Symbol, "currency": ct.Currency},
			})
			if err != nil {
				return err
			}
			count(inserted, &result.CashNew, &result.CashDuplicate)
		}

		for i, tr := range stmt.Trades {
			if tr.ExternalRef == "" {
				result.Warnings = append(result.Warnings,
					"Una operación de IBKR no trae identificador y no se registró; revisa que la sección Trades sea a nivel Execution.")
				continue
			}
			inserted, err := q.InsertTradeIfNew(ctx, userID, accountID, store.Trade{
				ExternalRef: tr.ExternalRef, Symbol: tr.Symbol, Currency: tr.Currency, AssetClass: tr.AssetClass,
				Side: tr.Side, Quantity: tr.Quantity, Price: tr.Price, Commission: tr.Commission,
				NetCash: tr.NetCash, TradeDate: civil.Of(tr.TradeDate),
			})
			if err != nil {
				return err
			}
			count(inserted, &result.TradesNew, &result.TradesDuplicate)
			// The cash a trade moved, split so the commission counts as the
			// fee it is: netCash already includes it.
			for _, leg := range tradeLegs(tr) {
				if _, err := q.InsertEntryIfNew(ctx, store.Entry{
					ID: uuid.New(), UserID: userID, AccountID: accountID, RunID: &runID,
					BookedOn: civil.Of(tr.TradeDate), Seq: 10000 + i, Amount: leg.amount, Kind: leg.kind,
					Description: leg.description, BankRef: tr.ExternalRef, BankCategory: "Trade",
					DedupKey: leg.key, Raw: map[string]string{"symbol": tr.Symbol, "side": tr.Side},
				}); err != nil {
					return err
				}
			}
		}

		if stmt.HasPositions {
			positions := make([]store.Position, 0, len(stmt.Positions))
			for _, p := range stmt.Positions {
				mark, value, cost := p.MarkPrice, p.MarketValue, p.CostBasisPrice
				positions = append(positions, store.Position{Symbol: p.Symbol, Currency: p.Currency,
					AssetClass: p.AssetClass, Quantity: p.Quantity, MarkPrice: &mark, MarketValue: &value,
					CostBasis: &cost, AsOf: civil.Of(p.AsOf)})
			}
			if err := q.ReplacePositions(ctx, userID, accountID, positions); err != nil {
				return err
			}
			result.Positions = len(positions)
		}

		for _, c := range stmt.CashBalances {
			if !strings.EqualFold(c.Currency, "USD") || c.AsOf.IsZero() {
				continue
			}
			ending := c.Ending
			result.ReportedCash = &ending
			if err := q.UpsertBrokerCheckpoint(ctx, store.Checkpoint{ID: uuid.New(), UserID: userID,
				AccountID: accountID, AsOf: civil.Of(c.AsOf), Balance: ending, RunID: &runID}); err != nil {
				return err
			}
		}

		describeStatement(&result, stmt, s.now())
		if result.CashFailed > 0 {
			result.Warnings = append(result.Warnings, fmt.Sprintf(
				"%d movimiento(s) de efectivo están en una moneda distinta al dólar y no se registraron.", result.CashFailed))
		}
		return q.SetImportRunDetails(ctx, runID, result.CashNew+result.TradesNew,
			result.CashDuplicate+result.TradesDuplicate, result.CashFailed, map[string]any{
				"missing_sections": nonNil(result.MissingSections), "warnings": nonNil(result.Warnings),
				"positions": result.Positions, "trades_new": result.TradesNew,
			})
	})
	return result, err
}

type leg struct {
	amount      money.Cents
	kind        string
	description string
	key         string
}

// tradeLegs splits a trade's net cash into the trade itself and its
// commission. netCash = -(quantity × price) + commission, with the
// commission negative.
func tradeLegs(tr ibkr.Trade) []leg {
	principal := tr.NetCash - tr.Commission
	side := "Compra"
	if tr.Side == "sell" {
		side = "Venta"
	}
	legs := []leg{}
	if principal != 0 {
		legs = append(legs, leg{amount: principal, kind: store.KindTrade,
			description: fmt.Sprintf("%s %s %s", side, trimFloat(tr.Quantity), tr.Symbol),
			key:         "ibkr_trade:" + tr.ExternalRef})
	}
	if tr.Commission != 0 {
		legs = append(legs, leg{amount: tr.Commission, kind: store.KindFee,
			description: "Comisión " + tr.Symbol, key: "ibkr_commission:" + tr.ExternalRef})
	}
	return legs
}

func trimFloat(f float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", f), "0"), ".")
}

// cashKind classifies an IBKR cash row by its type.
func cashKind(ct ibkr.CashTransaction) string {
	switch ct.Type {
	case "Deposits/Withdrawals":
		return store.KindTransfer
	case "Dividends", "Payment In Lieu Of Dividends", "Broker Interest Received":
		return store.KindIncome
	case "Broker Interest Paid":
		return store.KindInterest
	case "Withholding Tax", "Other Fees", "Commission Adjustments":
		return store.KindFee
	}
	if ct.Amount > 0 {
		return store.KindIncome
	}
	return store.KindExpense
}

func cashDescription(ct ibkr.CashTransaction) string {
	if ct.Description != "" {
		return ct.Description
	}
	if ct.Symbol != "" {
		return ct.Type + " " + ct.Symbol
	}
	return ct.Type
}

func count(inserted bool, fresh, dup *int) {
	if inserted {
		*fresh++
	} else {
		*dup++
	}
}

// SyncAll syncs every linked account, for the scheduled job. One account's
// failure does not stop the others.
func (s *Service) SyncAll(ctx context.Context) (map[uuid.UUID]SyncResult, map[uuid.UUID]error) {
	results, failures := map[uuid.UUID]SyncResult{}, map[uuid.UUID]error{}
	links, err := s.db.Q().AllIBKRLinks(ctx)
	if err != nil {
		failures[uuid.Nil] = err
		return results, failures
	}
	for _, l := range links {
		r, err := s.Sync(ctx, l.UserID, l.AccountID)
		if err != nil {
			failures[l.AccountID] = err
			continue
		}
		results[l.AccountID] = r
	}
	return results, failures
}

// Portfolio is a brokerage account's positions snapshot.
type Portfolio struct {
	Holdings  money.Cents
	Positions []store.Position
}

// Portfolio returns the account's latest positions snapshot.
func (s *Service) Portfolio(ctx context.Context, userID, accountID uuid.UUID) (Portfolio, error) {
	if _, err := s.brokerage(ctx, userID, accountID); err != nil {
		return Portfolio{}, err
	}
	positions, err := s.db.Q().PositionsByAccount(ctx, userID, accountID)
	if err != nil {
		return Portfolio{}, err
	}
	p := Portfolio{Positions: positions}
	for _, pos := range positions {
		if pos.MarketValue != nil {
			p.Holdings += *pos.MarketValue
		}
	}
	return p, nil
}

// Trades lists the account's trades, newest first.
func (s *Service) Trades(ctx context.Context, userID, accountID uuid.UUID, limit int) ([]store.Trade, error) {
	if _, err := s.brokerage(ctx, userID, accountID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	return s.db.Q().TradesByAccount(ctx, userID, accountID, limit)
}

func (s *Service) brokerage(ctx context.Context, userID, accountID uuid.UUID) (store.Account, error) {
	a, err := s.db.Q().AccountByID(ctx, userID, accountID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Account{}, ErrAccountNotFound
	}
	if err != nil {
		return store.Account{}, err
	}
	if a.Type != "brokerage" {
		return store.Account{}, ErrNotBrokerage
	}
	return a, nil
}

func (s *Service) recordOutcome(ctx context.Context, accountID uuid.UUID, cause error) {
	status, message := "ok", ""
	if cause != nil {
		status, message = "error", cause.Error()
	}
	if err := s.db.Q().RecordSyncOutcome(ctx, accountID, status, message, s.now().UTC()); err != nil {
		logging.FromContext(ctx).Error("could not record an ibkr sync outcome", "account_id", accountID, "error", err)
	}
}

func (s *Service) recordFailedRun(ctx context.Context, userID, accountID uuid.UUID, cause error) {
	if err := s.db.Q().CreateImportRun(ctx, store.ImportRun{ID: uuid.New(), UserID: userID, AccountID: &accountID,
		Source: "ibkr_flex", Status: "error", Error: cause.Error()}); err != nil {
		logging.FromContext(ctx).Error("could not record a failed ibkr sync", "account_id", accountID, "error", err)
	}
}

// checkStatementAccount refuses a report for a different IBKR account than
// the linked one. Either side being empty is let through.
func checkStatementAccount(linked, reported string) error {
	linked, reported = strings.TrimSpace(linked), strings.TrimSpace(reported)
	if linked == "" || reported == "" || strings.EqualFold(linked, reported) {
		return nil
	}
	return fmt.Errorf("%w: the Flex Query returned %s, but this account is linked to %s",
		ErrAccountMismatch, reported, linked)
}

// describeStatement records the report's own period and sections, and turns
// the ways a sync can succeed while bringing nothing new into warnings.
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
	if !stmt.HasCashReport {
		result.MissingSections = append(result.MissingSections, "cash_report")
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
}

func ibkrSectionNames(sections []string) []string {
	names := map[string]string{
		"cash_transactions": "Cash Transactions",
		"trades":            "Trades",
		"open_positions":    "Open Positions",
		"cash_report":       "Cash Report",
	}
	out := make([]string, len(sections))
	for i, s := range sections {
		out[i] = names[s]
	}
	return out
}

// previousBusinessDay is the most recent weekday strictly before now's date:
// the latest day a report generated today can cover.
func previousBusinessDay(now time.Time) time.Time {
	now = now.UTC()
	d := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	for d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
		d = d.AddDate(0, 0, -1)
	}
	return d
}

// day is t's calendar day, or the zero Date for the zero time.
func day(t time.Time) civil.Date {
	if t.IsZero() {
		return civil.Date{}
	}
	return civil.Of(t)
}

func civilDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.DateOnly)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
