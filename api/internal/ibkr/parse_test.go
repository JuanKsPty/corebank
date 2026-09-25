package ibkr

import (
	"testing"
	"time"
)

// sampleStatement is shaped like a real IBKR Flex Query XML export, checked
// against an actual account's report. It omits levelOfDetail on purpose,
// unlike the fixtures below that specifically exercise it, to also cover a
// report row that never carries the attribute at all.
const sampleStatement = `<?xml version="1.0" encoding="UTF-8"?>
<FlexQueryResponse queryName="corebank" type="AF">
  <FlexStatements count="1">
    <FlexStatement accountId="U1234567" fromDate="20240101" toDate="20240131">
      <CashTransactions>
        <CashTransaction accountId="U1234567" currency="USD" symbol="" amount="1000" type="Deposits/Withdrawals" dateTime="20240115;103000" reportDate="20240115" transactionID="tx-1" />
        <CashTransaction accountId="U1234567" currency="USD" symbol="AAPL" amount="12.34" type="Dividends" dateTime="20240120;000000" reportDate="20240120" transactionID="tx-2" />
        <CashTransaction accountId="U1234567" currency="USD" symbol="" amount="-1.50" type="Broker Interest Paid" dateTime="20240131;000000" reportDate="20240131" transactionID="tx-3" />
      </CashTransactions>
      <Trades>
        <Trade accountId="U1234567" currency="USD" symbol="AAPL" assetCategory="STK" buySell="BUY" quantity="10" tradePrice="150.25" ibCommission="-1.00" netCash="-1503.50" tradeDate="20240116" tradeID="trade-1" />
      </Trades>
      <OpenPositions>
        <OpenPosition accountId="U1234567" currency="USD" symbol="AAPL" assetCategory="STK" position="10" markPrice="155.30" positionValue="1553.00" costBasisPrice="150.25" reportDate="20240131" />
      </OpenPositions>
    </FlexStatement>
  </FlexStatements>
</FlexQueryResponse>`

func TestParse(t *testing.T) {
	stmt, err := Parse([]byte(sampleStatement))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if stmt.AccountID != "U1234567" {
		t.Errorf("AccountID = %q, want U1234567", stmt.AccountID)
	}

	if len(stmt.CashTransactions) != 3 {
		t.Fatalf("got %d cash transactions, want 3", len(stmt.CashTransactions))
	}
	deposit := stmt.CashTransactions[0]
	if deposit.ExternalRef != "tx-1" || deposit.Amount != 100000 || deposit.Type != "Deposits/Withdrawals" {
		t.Errorf("deposit = %+v", deposit)
	}
	fee := stmt.CashTransactions[2]
	if fee.Amount != -150 {
		t.Errorf("fee.Amount = %d, want -150 (a negative amount must stay negative)", fee.Amount)
	}
	wantWhen := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	if !deposit.When.Equal(wantWhen) {
		t.Errorf("deposit.When = %v, want %v", deposit.When, wantWhen)
	}

	if len(stmt.Trades) != 1 {
		t.Fatalf("got %d trades, want 1", len(stmt.Trades))
	}
	trade := stmt.Trades[0]
	if trade.ExternalRef != "trade-1" || trade.Side != "buy" || trade.Quantity != 10 ||
		trade.Price != 15025 || trade.Commission != -100 || trade.NetCash != -150350 {
		t.Errorf("trade = %+v", trade)
	}

	if len(stmt.Positions) != 1 {
		t.Fatalf("got %d positions, want 1", len(stmt.Positions))
	}
	pos := stmt.Positions[0]
	if pos.Symbol != "AAPL" || pos.Quantity != 10 || pos.MarkPrice != 15530 || pos.MarketValue != 155300 {
		t.Errorf("position = %+v", pos)
	}
}

func TestParseRejectsMultipleStatements(t *testing.T) {
	const doc = `<FlexQueryResponse><FlexStatements count="2">
		<FlexStatement accountId="A"></FlexStatement>
		<FlexStatement accountId="B"></FlexStatement>
	</FlexStatements></FlexQueryResponse>`

	if _, err := Parse([]byte(doc)); err == nil {
		t.Fatal("Parse() with two FlexStatement blocks did not error")
	}
}

func TestParseRejectsMalformedAmount(t *testing.T) {
	const doc = `<FlexQueryResponse><FlexStatements count="1">
		<FlexStatement accountId="A">
			<CashTransactions>
				<CashTransaction currency="USD" amount="not-a-number" type="Fees" dateTime="20240115;000000" transactionID="tx-1" />
			</CashTransactions>
		</FlexStatement>
	</FlexStatements></FlexQueryResponse>`

	if _, err := Parse([]byte(doc)); err == nil {
		t.Fatal("Parse() with a malformed amount did not error")
	}
}

// The fixtures below encode findings from a real account's Flex Query
// export, not invented edge cases: IBKR reported one deposit as two
// CashTransaction rows (SUMMARY + DETAIL), one ETF position as a SUMMARY row
// plus several LOT rows, a CashTransaction dateTime with no time component,
// and trade commission/net cash with far more than two decimal places from a
// fractional-share purchase.

func TestParseKeepsOnlyDetailLevelCashTransactions(t *testing.T) {
	const doc = `<FlexQueryResponse><FlexStatements count="1">
		<FlexStatement accountId="U1234567">
			<CashTransactions>
				<CashTransaction currency="USD" amount="6.73" type="Deposits/Withdrawals" dateTime="20260909" reportDate="20260909" transactionID="" levelOfDetail="SUMMARY" />
				<CashTransaction currency="USD" amount="6.73" type="Deposits/Withdrawals" dateTime="20260909" reportDate="20260909" transactionID="42624315771" levelOfDetail="DETAIL" />
			</CashTransactions>
		</FlexStatement>
	</FlexStatements></FlexQueryResponse>`

	stmt, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(stmt.CashTransactions) != 1 {
		t.Fatalf("got %d cash transactions, want 1 (the SUMMARY duplicate must be dropped)", len(stmt.CashTransactions))
	}
	if stmt.CashTransactions[0].ExternalRef != "42624315771" {
		t.Errorf("ExternalRef = %q, want the DETAIL row's transactionID", stmt.CashTransactions[0].ExternalRef)
	}
}

func TestParseKeepsSummaryRowsWhenNoDetailRowExists(t *testing.T) {
	// A report configured without DETAIL rows at all must not lose every
	// cash transaction to the filter.
	const doc = `<FlexQueryResponse><FlexStatements count="1">
		<FlexStatement accountId="U1234567">
			<CashTransactions>
				<CashTransaction currency="USD" amount="6.73" type="Deposits/Withdrawals" dateTime="20260909" reportDate="20260909" levelOfDetail="SUMMARY" />
			</CashTransactions>
		</FlexStatement>
	</FlexStatements></FlexQueryResponse>`

	stmt, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(stmt.CashTransactions) != 1 {
		t.Fatalf("got %d cash transactions, want 1 (a SUMMARY-only report must not be filtered to nothing)", len(stmt.CashTransactions))
	}
}

func TestParseKeepsOnlySummaryLevelPositions(t *testing.T) {
	const doc = `<FlexQueryResponse><FlexStatements count="1">
		<FlexStatement accountId="U1234567">
			<OpenPositions>
				<OpenPosition currency="USD" symbol="AGG" position="1.5" markPrice="95.98" positionValue="143.97" costBasisPrice="96.00" reportDate="20260911" levelOfDetail="LOT" />
				<OpenPosition currency="USD" symbol="AGG" position="1.582" markPrice="95.98" positionValue="151.86" costBasisPrice="97.50" reportDate="20260911" levelOfDetail="LOT" />
				<OpenPosition currency="USD" symbol="AGG" position="3.082" markPrice="95.98" positionValue="295.81" costBasisPrice="99.801812135" reportDate="20260911" levelOfDetail="SUMMARY" />
			</OpenPositions>
		</FlexStatement>
	</FlexStatements></FlexQueryResponse>`

	stmt, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(stmt.Positions) != 1 {
		t.Fatalf("got %d positions, want 1 (the two LOT rows must be dropped in favour of the SUMMARY row)", len(stmt.Positions))
	}
	if stmt.Positions[0].Quantity != 3.082 {
		t.Errorf("Quantity = %v, want the SUMMARY row's aggregate 3.082, not one LOT's share of it", stmt.Positions[0].Quantity)
	}
}

func TestParseAcceptsACashTransactionDateWithNoTimeComponent(t *testing.T) {
	// Verified against a real account: unlike a Trade's dateTime
	// ("20260908;093112"), a CashTransaction's can be a bare date with no
	// time at all.
	const doc = `<FlexQueryResponse><FlexStatements count="1">
		<FlexStatement accountId="U1234567">
			<CashTransactions>
				<CashTransaction currency="USD" amount="6.73" type="Deposits/Withdrawals" dateTime="20260909" reportDate="20260909" transactionID="tx-1" />
			</CashTransactions>
		</FlexStatement>
	</FlexStatements></FlexQueryResponse>`

	stmt, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	want := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	if !stmt.CashTransactions[0].When.Equal(want) {
		t.Errorf("When = %v, want %v", stmt.CashTransactions[0].When, want)
	}
}

func TestParseRoundsSubCentTradeAndPositionAmounts(t *testing.T) {
	// A 0.0075-share fractional purchase: commission and net cash scale
	// down with the tiny quantity and land well past two decimal places.
	const doc = `<FlexQueryResponse><FlexStatements count="1">
		<FlexStatement accountId="U1234567">
			<Trades>
				<Trade currency="USD" symbol="AGG" assetCategory="STK" buySell="BUY" quantity="0.0075" tradePrice="97.04" ibCommission="-0.000729857" netCash="-0.728529857" tradeDate="20260908" tradeID="trade-1" />
			</Trades>
			<OpenPositions>
				<OpenPosition currency="USD" symbol="AGG" position="3.082" markPrice="95.98" positionValue="295.81" costBasisPrice="99.801812135" reportDate="20260911" />
			</OpenPositions>
		</FlexStatement>
	</FlexStatements></FlexQueryResponse>`

	stmt, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got := stmt.Trades[0].NetCash; got != -73 {
		t.Errorf("Trades[0].NetCash = %d, want -73 (-0.728529857 rounded to the nearest cent)", got)
	}
	if got := stmt.Trades[0].Commission; got != 0 {
		t.Errorf("Trades[0].Commission = %d, want 0 (-0.000729857 rounds to nothing)", got)
	}
	if got := stmt.Positions[0].CostBasisPrice; got != 9980 {
		t.Errorf("Positions[0].CostBasisPrice = %d, want 9980 (99.801812135 rounded to the nearest cent)", got)
	}
}

func TestLooksLikeStatement(t *testing.T) {
	if !looksLikeStatement([]byte(sampleStatement)) {
		t.Error("a real statement was not recognised as one")
	}
	envelope := []byte(`<FlexStatementResponse><Status>Success</Status></FlexStatementResponse>`)
	if looksLikeStatement(envelope) {
		t.Error("the small envelope was mistaken for a statement")
	}
}

func TestParseReadsThePeriodAndWhichSectionsArePresent(t *testing.T) {
	stmt, err := Parse([]byte(sampleStatement))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if want := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC); !stmt.FromDate.Equal(want) {
		t.Errorf("FromDate = %v, want %v", stmt.FromDate, want)
	}
	if want := time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC); !stmt.ToDate.Equal(want) {
		t.Errorf("ToDate = %v, want %v", stmt.ToDate, want)
	}
	if !stmt.GeneratedAt.IsZero() {
		t.Errorf("GeneratedAt = %v, want zero: the fixture carries no whenGenerated", stmt.GeneratedAt)
	}
	if !stmt.HasCashTransactions || !stmt.HasTrades || !stmt.HasPositions {
		t.Errorf("sections present = cash %v, trades %v, positions %v; want all true",
			stmt.HasCashTransactions, stmt.HasTrades, stmt.HasPositions)
	}
}

func TestParseTellsAMissingSectionFromAnEmptyOne(t *testing.T) {
	// Trades is present but empty; OpenPositions and CashTransactions are
	// not requested at all. Only the missing ones must report false, since a
	// missing positions section must never be read as "no holdings".
	const doc = `<FlexQueryResponse><FlexStatements count="1">
		<FlexStatement accountId="U1" fromDate="20260901" toDate="20260923" whenGenerated="20260924;061500">
			<Trades></Trades>
		</FlexStatement>
	</FlexStatements></FlexQueryResponse>`

	stmt, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if stmt.HasCashTransactions || stmt.HasPositions {
		t.Errorf("HasCashTransactions = %v, HasPositions = %v; want both false", stmt.HasCashTransactions, stmt.HasPositions)
	}
	if !stmt.HasTrades {
		t.Error("HasTrades = false, want true: an empty section is still present")
	}
	if want := time.Date(2026, 9, 24, 6, 15, 0, 0, time.UTC); !stmt.GeneratedAt.Equal(want) {
		t.Errorf("GeneratedAt = %v, want %v", stmt.GeneratedAt, want)
	}
}

func TestParseIgnoresAMalformedPeriod(t *testing.T) {
	const doc = `<FlexQueryResponse><FlexStatements count="1">
		<FlexStatement accountId="U1" fromDate="01/09/2026" toDate=""></FlexStatement>
	</FlexStatements></FlexQueryResponse>`

	stmt, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse() error = %v, want nil: the period describes the report, it must not fail the sync", err)
	}
	if !stmt.FromDate.IsZero() || !stmt.ToDate.IsZero() {
		t.Errorf("FromDate = %v, ToDate = %v; want both zero", stmt.FromDate, stmt.ToDate)
	}
}
