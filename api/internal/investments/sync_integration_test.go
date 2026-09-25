package investments

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/accounts"
	"github.com/JuanKsPty/corebank/api/internal/store"
	"github.com/JuanKsPty/corebank/api/internal/storetest"
)

type cannedFlex []byte

func (c cannedFlex) Fetch(context.Context) ([]byte, error) { return c, nil }

// report is shaped like the parser tests' sample: a deposit, a dividend,
// interest paid, a buy of AAPL and the Cash Report IBKR computes from them.
const report = `<FlexQueryResponse><FlexStatements count="1">
  <FlexStatement accountId="U1234567" fromDate="20240101" toDate="20240131">
    <CashTransactions>
      <CashTransaction currency="USD" amount="1000" type="Deposits/Withdrawals" dateTime="20240115" reportDate="20240115" transactionID="tx-1" />
      <CashTransaction currency="USD" symbol="AAPL" amount="12.34" type="Dividends" dateTime="20240120" reportDate="20240120" transactionID="tx-2" />
      <CashTransaction currency="USD" amount="-1.50" type="Broker Interest Paid" dateTime="20240131" reportDate="20240131" transactionID="tx-3" />
      <CashTransaction currency="EUR" amount="10" type="Dividends" dateTime="20240131" reportDate="20240131" transactionID="tx-4" />
    </CashTransactions>
    <Trades>
      <Trade currency="USD" symbol="AAPL" assetCategory="STK" buySell="BUY" quantity="10" tradePrice="150.25" ibCommission="-1.00" netCash="-1503.50" tradeDate="20240116" tradeID="trade-1" levelOfDetail="EXECUTION" />
    </Trades>
    <OpenPositions>
      <OpenPosition currency="USD" symbol="AAPL" assetCategory="STK" position="10" markPrice="155.30" positionValue="1553.00" costBasisPrice="150.25" reportDate="20240131" />
    </OpenPositions>
    <CashReport>
      <CashReportCurrency currency="USD" startingCash="0" endingCash="-492.66" endingSettledCash="-492.66" />
    </CashReport>
  </FlexStatement>
</FlexStatements></FlexQueryResponse>`

func linked(t *testing.T, flex string) (*Service, *store.DB, uuid.UUID, uuid.UUID) {
	t.Helper()
	db := storetest.New(t)
	ctx := context.Background()
	u, err := db.Q().CreateUser(ctx, store.User{ID: uuid.New(), Email: "i@example.com", PasswordHash: "x", FullName: "I"})
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	rand.Read(key)
	svc := NewService(db, key)
	svc.now = func() time.Time { return time.Date(2024, 2, 1, 12, 0, 0, 0, time.UTC) }
	svc.newClient = func(string, string) Fetcher { return cannedFlex(flex) }
	a, err := svc.Link(ctx, u.ID, "U1234567", "99", "token")
	if err != nil {
		t.Fatal(err)
	}
	return svc, db, u.ID, a.ID
}

func TestSyncCashAgreesWithIBKRsOwnCashReport(t *testing.T) {
	svc, db, user, account := linked(t, report)
	ctx := context.Background()

	r, err := svc.Sync(ctx, user, account)
	if err != nil {
		t.Fatal(err)
	}
	if r.CashNew != 3 || r.CashFailed != 1 || r.TradesNew != 1 || r.Positions != 1 {
		t.Errorf("result = %+v", r)
	}
	if r.ReportedCash == nil || *r.ReportedCash != -49266 {
		t.Errorf("ReportedCash = %v", r.ReportedCash)
	}

	a, err := accounts.NewService(db).Get(ctx, user, account)
	if err != nil {
		t.Fatal(err)
	}
	// The trade's cash is posted now, so cash is -492.66 (a margin loan),
	// not the 1,010.84 the old sync showed with the purchase never deducted.
	if !a.Anchored || a.Balance != -49266 {
		t.Errorf("cash = %s (anchored %v), want -492.66", a.Balance, a.Anchored)
	}
	if a.Holdings != 155300 {
		t.Errorf("holdings = %s, want 1,553.00", a.Holdings)
	}

	// A second sync of the same report adds nothing.
	again, err := svc.Sync(ctx, user, account)
	if err != nil {
		t.Fatal(err)
	}
	if again.CashNew != 0 || again.CashDuplicate != 3 || again.TradesNew != 0 {
		t.Errorf("second sync = %+v, want everything already recorded", again)
	}
}

func TestSyncRefusesAnotherAccountsReport(t *testing.T) {
	other := `<FlexQueryResponse><FlexStatements count="1"><FlexStatement accountId="U7654321"></FlexStatement></FlexStatements></FlexQueryResponse>`
	svc, _, user, account := linked(t, other)
	if _, err := svc.Sync(context.Background(), user, account); !errors.Is(err, ErrAccountMismatch) {
		t.Errorf("err = %v, want ErrAccountMismatch", err)
	}
	link, err := svc.LinkStatus(context.Background(), user, account)
	if err != nil || link.LastSyncStatus != "error" {
		t.Errorf("link status = %q, %v; want the failure recorded", link.LastSyncStatus, err)
	}
}

func TestSyncKeepsPositionsWhenTheSectionIsMissing(t *testing.T) {
	svc, db, user, account := linked(t, report)
	ctx := context.Background()
	if _, err := svc.Sync(ctx, user, account); err != nil {
		t.Fatal(err)
	}
	svc.newClient = func(string, string) Fetcher {
		return cannedFlex(`<FlexQueryResponse><FlexStatements count="1"><FlexStatement accountId="U1234567" toDate="20240131"><Trades></Trades></FlexStatement></FlexStatements></FlexQueryResponse>`)
	}
	r, err := svc.Sync(ctx, user, account)
	if err != nil {
		t.Fatal(err)
	}
	positions, _ := db.Q().PositionsByAccount(ctx, user, account)
	if len(positions) != 1 {
		t.Errorf("positions = %d after a report without OpenPositions, want the previous 1", len(positions))
	}
	if len(r.Warnings) == 0 {
		t.Error("a report missing sections must say so")
	}
}
