package ibkr

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/JuanKsPty/corebank/api/internal/money"
)

// Statement is one Flex Query report, translated out of IBKR's XML into
// corebank-native types. Nothing downstream of this package ever sees IBKR's
// own attribute strings.
type Statement struct {
	AccountID string

	// FromDate and ToDate are the period the report covers, and GeneratedAt
	// is when IBKR produced it. Each is the zero time when the report does
	// not carry it, and a malformed value is treated the same way: these
	// describe the report, they are not data to import, so an unexpected
	// format must not fail a sync that is otherwise fine.
	FromDate    time.Time
	ToDate      time.Time
	GeneratedAt time.Time

	// HasCashTransactions, HasTrades and HasPositions report whether the
	// query includes each section at all. A missing section is not the same
	// as an empty one: replacing the positions snapshot with a section that
	// was never requested would wipe every holding.
	HasCashTransactions bool
	HasTrades           bool
	HasPositions        bool
	HasCashReport       bool

	// CashBalances is the Cash Report: the account's cash per currency at the
	// start and end of the period, as IBKR computes it. It is the figure a
	// computed cash balance has to agree with.
	CashBalances []CashBalance

	CashTransactions []CashTransaction
	Trades           []Trade
	Positions        []Position
}

// CashTransaction is money entering or leaving the brokerage account itself —
// a contribution, a withdrawal, a dividend, interest, a fee, or a trade's net
// settlement. This is the half of a statement that becomes a real ledger
// posting; see internal/investments.
type CashTransaction struct {
	// ExternalRef is IBKR's transactionID: the key SetCategory-style dedup
	// relies on so re-running the same query twice does not double-post.
	ExternalRef string
	// Type is IBKR's own label ("Dividends", "Deposits/Withdrawals", "Broker
	// Interest Paid", ...), kept verbatim for the movement's description
	// rather than mapped onto a closed set corebank would have to keep in
	// sync with IBKR's.
	Type   string
	Symbol string
	// Amount is signed: positive is money arriving, negative is money
	// leaving.
	Amount   money.Cents
	Currency string
	When     time.Time
	// ReportDate is the day IBKR booked the movement.
	ReportDate  time.Time
	Description string
	// Key identifies the movement across syncs: IBKR's transactionID when
	// there is one, otherwise a hash of what the row says plus its position
	// among identical rows. Never a counter over the whole window, which
	// shifts as the window rolls forward.
	Key string
}

// CashBalance is one currency's line of the Cash Report.
type CashBalance struct {
	Currency      string
	Starting      money.Cents
	Ending        money.Cents
	EndingSettled money.Cents
	// AsOf is the day Ending refers to; the report's ToDate when the line
	// does not carry its own.
	AsOf time.Time
}

// Trade is a single execution. Only its net cash effect reaches the ledger
// (as a CashTransaction with the same transaction id, on statements that
// report one); the trade itself — the position it changed — is stored in
// Postgres only. See internal/investments' package doc for why.
type Trade struct {
	ExternalRef string // IBKR's tradeID
	Symbol      string
	AssetClass  string
	// Side is "buy" or "sell", lower-cased from IBKR's BUY/SELL.
	Side     string
	Quantity float64
	Price    money.Cents
	// Commission is reported by IBKR as a negative number (a cost); kept as
	// IBKR reports it rather than negated, so summing it with NetCash and
	// Price * Quantity reconciles the way IBKR's own statement does.
	Commission money.Cents
	NetCash    money.Cents
	Currency   string
	TradeDate  time.Time
}

// Position is a holding as of the moment the statement was generated — a
// snapshot, not a live quote. Never posted to the ledger; see
// internal/investments.
type Position struct {
	Symbol         string
	AssetClass     string
	Quantity       float64
	MarkPrice      money.Cents
	MarketValue    money.Cents
	CostBasisPrice money.Cents
	Currency       string
	AsOf           time.Time
}

// Parse translates a raw Flex Query statement (as returned by
// Client.Fetch/GetStatement) into a Statement.
//
// A statement with more than one <FlexStatement> block is refused rather
// than merged: a Flex Query configured for one account produces exactly one,
// and corebank's investments.Service.Sync takes a single account number to
// apply the result to. Seeing more than one here means the query is
// configured differently than expected, which is worth failing loudly on
// rather than guessing which block corresponds to the linked account.
func Parse(body []byte) (Statement, error) {
	var doc statementDocument
	if err := decodeXML(body, &doc); err != nil {
		return Statement{}, fmt.Errorf("ibkr: decoding statement: %w", err)
	}
	if len(doc.Statements) != 1 {
		return Statement{}, fmt.Errorf(
			"ibkr: expected exactly one FlexStatement, got %d", len(doc.Statements))
	}
	raw := doc.Statements[0]

	stmt := Statement{
		AccountID:           raw.AccountID,
		FromDate:            parseOptionalDate(raw.FromDate),
		ToDate:              parseOptionalDate(raw.ToDate),
		GeneratedAt:         parseOptionalDateTime(raw.WhenGenerated),
		HasCashTransactions: raw.CashTransactions != nil,
		HasTrades:           raw.Trades != nil,
		HasPositions:        raw.OpenPositions != nil,
		HasCashReport:       raw.CashReport != nil,
	}

	var (
		cashItems     []cashTransactionXML
		tradeItems    []tradeXML
		positionItems []openPositionXML
	)
	if raw.CashTransactions != nil {
		cashItems = raw.CashTransactions.Items
	}
	if raw.Trades != nil {
		tradeItems = raw.Trades.Items
	}
	if raw.OpenPositions != nil {
		positionItems = raw.OpenPositions.Items
	}

	for _, ct := range filterCashTransactions(cashItems) {
		amount, err := money.Parse(ct.Amount)
		if err != nil {
			return Statement{}, fmt.Errorf("ibkr: cash transaction %s: amount %q: %w",
				ct.TransactionID, ct.Amount, err)
		}
		when, err := parseIBKRDateTime(ct.DateTime, ct.ReportDate)
		if err != nil {
			return Statement{}, fmt.Errorf("ibkr: cash transaction %s: %w", ct.TransactionID, err)
		}
		reportDate, err := parseIBKRDate(ct.ReportDate)
		if err != nil {
			reportDate = when
		}
		stmt.CashTransactions = append(stmt.CashTransactions, CashTransaction{
			ExternalRef: ct.TransactionID,
			Type:        ct.Type,
			Symbol:      ct.Symbol,
			Amount:      amount,
			Currency:    ct.Currency,
			When:        when,
			ReportDate:  reportDate,
			Description: ct.Description,
		})
	}
	assignCashKeys(stmt.CashTransactions)

	if raw.CashReport != nil {
		for _, c := range raw.CashReport.Items {
			// BASE_SUMMARY restates the other lines converted to the base
			// currency; counting it would double every balance.
			if c.Currency == "" || c.Currency == "BASE_SUMMARY" {
				continue
			}
			starting, err1 := parseApproxCents(zeroIfEmpty(c.StartingCash))
			ending, err2 := parseApproxCents(zeroIfEmpty(c.EndingCash))
			settled, err3 := parseApproxCents(zeroIfEmpty(c.EndingSettledCash))
			if err := errors.Join(err1, err2, err3); err != nil {
				return Statement{}, fmt.Errorf("ibkr: cash report %s: %w", c.Currency, err)
			}
			asOf := stmt.ToDate
			if d, err := parseIBKRDate(c.ToDate); err == nil {
				asOf = d
			}
			stmt.CashBalances = append(stmt.CashBalances, CashBalance{
				Currency: c.Currency, Starting: starting, Ending: ending, EndingSettled: settled, AsOf: asOf,
			})
		}
	}

	for _, tr := range filterTrades(tradeItems) {
		price, err := parseApproxCents(tr.TradePrice)
		if err != nil {
			return Statement{}, fmt.Errorf("ibkr: trade %s: price %q: %w", tr.TradeID, tr.TradePrice, err)
		}
		commission, err := parseApproxCents(zeroIfEmpty(tr.IBCommission))
		if err != nil {
			return Statement{}, fmt.Errorf("ibkr: trade %s: commission %q: %w", tr.TradeID, tr.IBCommission, err)
		}
		netCash, err := parseApproxCents(zeroIfEmpty(tr.NetCash))
		if err != nil {
			return Statement{}, fmt.Errorf("ibkr: trade %s: net cash %q: %w", tr.TradeID, tr.NetCash, err)
		}
		quantity, err := strconv.ParseFloat(tr.Quantity, 64)
		if err != nil {
			return Statement{}, fmt.Errorf("ibkr: trade %s: quantity %q: %w", tr.TradeID, tr.Quantity, err)
		}
		tradeDate, err := parseIBKRDate(tr.TradeDate)
		if err != nil {
			return Statement{}, fmt.Errorf("ibkr: trade %s: %w", tr.TradeID, err)
		}
		stmt.Trades = append(stmt.Trades, Trade{
			ExternalRef: tr.TradeID,
			Symbol:      tr.Symbol,
			AssetClass:  tr.AssetCategory,
			Side:        strings.ToLower(tr.BuySell),
			Quantity:    quantity,
			Price:       price,
			Commission:  commission,
			NetCash:     netCash,
			Currency:    tr.Currency,
			TradeDate:   tradeDate,
		})
	}

	for _, p := range filterOpenPositions(positionItems) {
		markPrice, err := parseApproxCents(p.MarkPrice)
		if err != nil {
			return Statement{}, fmt.Errorf("ibkr: position %s: mark price %q: %w", p.Symbol, p.MarkPrice, err)
		}
		marketValue, err := parseApproxCents(p.PositionValue)
		if err != nil {
			return Statement{}, fmt.Errorf("ibkr: position %s: value %q: %w", p.Symbol, p.PositionValue, err)
		}
		costBasis, err := parseApproxCents(zeroIfEmpty(p.CostBasisPrice))
		if err != nil {
			return Statement{}, fmt.Errorf("ibkr: position %s: cost basis %q: %w", p.Symbol, p.CostBasisPrice, err)
		}
		quantity, err := strconv.ParseFloat(p.Position, 64)
		if err != nil {
			return Statement{}, fmt.Errorf("ibkr: position %s: quantity %q: %w", p.Symbol, p.Position, err)
		}
		asOf, err := parseIBKRDate(p.ReportDate)
		if err != nil {
			return Statement{}, fmt.Errorf("ibkr: position %s: %w", p.Symbol, err)
		}
		stmt.Positions = append(stmt.Positions, Position{
			Symbol:         p.Symbol,
			AssetClass:     p.AssetCategory,
			Quantity:       quantity,
			MarkPrice:      markPrice,
			MarketValue:    marketValue,
			CostBasisPrice: costBasis,
			Currency:       p.Currency,
			AsOf:           asOf,
		})
	}

	return stmt, nil
}

func zeroIfEmpty(s string) string {
	if s == "" {
		return "0"
	}
	return s
}

// parseApproxCents parses a decimal amount that may carry more precision
// than a whole cent, rounding to the nearest one.
//
// Verified against a real account with fractional-share trades: a
// commission of "-0.000729857" and a cost basis price of "99.801812135"
// both came back from a 0.0075-share trade, where the per-share commission
// rate scales down with a tiny fractional quantity. money.Parse's own
// two-decimal rule exists because a ledger amount must never be silently
// rounded — but none of Trade's or Position's fields are ledger amounts
// (see internal/investments' package doc: only a CashTransaction's own
// Amount is), so rounding here trades a false sense of sub-cent precision
// for a value corebank stores and displays in cents everywhere else.
func parseApproxCents(s string) (money.Cents, error) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("malformed amount %q: %w", s, err)
	}
	return money.Cents(math.Round(f * 100)), nil
}

// filterCashTransactions drops the "SUMMARY"-level duplicate of every cash
// event that also has a "DETAIL"-level row.
//
// Verified against a real account: a single deposit came back as two
// CashTransaction elements with identical date, amount and description — one
// levelOfDetail="SUMMARY" with an empty transactionID, one
// levelOfDetail="DETAIL" with a real one. Posting both would double the
// money; posting the SUMMARY one would leave nothing stable to dedupe a
// re-sync against. DETAIL is kept whenever it is present at all; if a report
// were ever configured to omit DETAIL entirely, every row here would already
// be SUMMARY and this becomes a no-op rather than silently discarding
// everything.
func filterCashTransactions(items []cashTransactionXML) []cashTransactionXML {
	hasDetail := false
	for _, it := range items {
		if it.LevelOfDetail == "DETAIL" {
			hasDetail = true
			break
		}
	}
	if !hasDetail {
		return items
	}
	out := make([]cashTransactionXML, 0, len(items))
	for _, it := range items {
		if it.LevelOfDetail == "DETAIL" {
			out = append(out, it)
		}
	}
	return out
}

// filterOpenPositions drops per-lot rows, keeping the one "SUMMARY" row per
// symbol.
//
// Verified against a real account: several symbols each appeared twice, once
// levelOfDetail="SUMMARY" (the whole position) and once "LOT" (one tax lot).
// corebank tracks aggregate holdings, not individual lots, so the LOT rows
// would otherwise violate investment_positions' one-row-per-instrument
// constraint. Falls back to keeping everything if no row is SUMMARY, on the
// same reasoning as filterCashTransactions.
func filterOpenPositions(items []openPositionXML) []openPositionXML {
	hasSummary := false
	for _, it := range items {
		if it.LevelOfDetail == "SUMMARY" {
			hasSummary = true
			break
		}
	}
	if !hasSummary {
		return items
	}
	out := make([]openPositionXML, 0, len(items))
	for _, it := range items {
		if it.LevelOfDetail == "SUMMARY" {
			out = append(out, it)
		}
	}
	return out
}

// parseIBKRDateTime parses IBKR's dateTime attribute.
//
// Verified against a real account: a Trade's dateTime carries a time
// ("20260908;093112"), but a CashTransaction's does not — it is bare
// "20260909", identical in shape to reportDate. Both forms are tried before
// falling back to reportDate for the rare row that omits dateTime entirely.
func parseIBKRDateTime(dateTime, reportDate string) (time.Time, error) {
	if dateTime == "" {
		return parseIBKRDate(reportDate)
	}
	if t, err := time.Parse("20060102;150405", dateTime); err == nil {
		return t, nil
	}
	if t, err := time.Parse("20060102", dateTime); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("malformed dateTime %q", dateTime)
}

// parseIBKRDate parses IBKR's plain "YYYYMMdd" date attributes.
func parseIBKRDate(date string) (time.Time, error) {
	t, err := time.Parse("20060102", date)
	if err != nil {
		return time.Time{}, fmt.Errorf("malformed date %q: %w", date, err)
	}
	return t, nil
}

// parseOptionalDate parses a statement-level date attribute, returning the
// zero time when it is absent or not in the expected "YYYYMMdd" form.
func parseOptionalDate(date string) time.Time {
	t, err := parseIBKRDate(date)
	if err != nil {
		return time.Time{}
	}
	return t
}

// parseOptionalDateTime parses whenGenerated ("YYYYMMdd;HHmmss"), returning
// the zero time when it is absent or malformed.
func parseOptionalDateTime(dateTime string) time.Time {
	if dateTime == "" {
		return time.Time{}
	}
	t, err := parseIBKRDateTime(dateTime, "")
	if err != nil {
		return time.Time{}
	}
	return t
}

// assignCashKeys gives every cash row its stable Key.
func assignCashKeys(rows []CashTransaction) {
	seen := map[string]int{}
	for i := range rows {
		r := &rows[i]
		if r.ExternalRef != "" {
			r.Key = "id:" + r.ExternalRef
			continue
		}
		tuple := fmt.Sprintf("%s|%s|%s|%d|%s|%s", r.ReportDate.Format("20060102"), r.Type, r.Symbol,
			r.Amount, r.Currency, r.Description)
		sum := sha256.Sum256([]byte(tuple))
		r.Key = fmt.Sprintf("noref:%s:%d", hex.EncodeToString(sum[:12]), seen[tuple])
		seen[tuple]++
	}
}

// filterTrades keeps execution-level rows when the report has any. A query
// that also asks for order or closed-lot detail restates each fill at those
// levels, often without a tradeID, and counting them would repeat the trade.
func filterTrades(items []tradeXML) []tradeXML {
	hasExecution := false
	for _, it := range items {
		if it.LevelOfDetail == "EXECUTION" {
			hasExecution = true
			break
		}
	}
	if !hasExecution {
		return items
	}
	out := make([]tradeXML, 0, len(items))
	for _, it := range items {
		if it.LevelOfDetail == "EXECUTION" {
			out = append(out, it)
		}
	}
	return out
}
