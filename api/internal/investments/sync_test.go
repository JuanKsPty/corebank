package investments

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JuanKsPty/corebank/api/internal/ibkr"
)

func TestCheckStatementAccount(t *testing.T) {
	cases := []struct {
		name, linked, reported string
		wantErr                bool
	}{
		{"same account", "U1234567", "U1234567", false},
		{"case and spaces do not matter", "u1234567 ", "U1234567", false},
		{"link predates the field", "", "U1234567", false},
		{"report omits the attribute", "U1234567", "", false},
		{"different account", "U1234567", "U7654321", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkStatementAccount(tc.linked, tc.reported)
			if tc.wantErr != (err != nil) {
				t.Fatalf("checkStatementAccount(%q, %q) = %v, want error: %v", tc.linked, tc.reported, err, tc.wantErr)
			}
			if tc.wantErr && !errors.Is(err, ErrAccountMismatch) {
				t.Errorf("error = %v, want ErrAccountMismatch", err)
			}
		})
	}
}

func TestPreviousBusinessDay(t *testing.T) {
	cases := []struct{ now, want string }{
		{"2026-09-24T21:26:59Z", "2026-09-23"}, // Thursday -> Wednesday
		{"2026-09-28T09:00:00Z", "2026-09-25"}, // Monday -> Friday
		{"2026-09-27T09:00:00Z", "2026-09-25"}, // Sunday -> Friday
	}
	for _, tc := range cases {
		now, _ := time.Parse(time.RFC3339, tc.now)
		if got := previousBusinessDay(now).Format(time.DateOnly); got != tc.want {
			t.Errorf("previousBusinessDay(%s) = %s, want %s", tc.now, got, tc.want)
		}
	}
}

func day(s string) time.Time {
	t, _ := time.Parse(time.DateOnly, s)
	return t
}

func TestDescribeStatementFlagsAFrozenPeriod(t *testing.T) {
	// The shape of the owner's production sync: IBKR answered at once with
	// a report whose period stopped weeks ago, so nothing new arrived.
	stmt := ibkr.Statement{
		FromDate: day("2026-09-01"), ToDate: day("2026-09-08"),
		HasCashTransactions: true, HasTrades: true, HasPositions: true,
	}
	var result SyncResult
	describeStatement(&result, stmt, time.Date(2026, 9, 24, 21, 26, 59, 0, time.UTC))

	if !result.PeriodTo.Equal(day("2026-09-08")) || !result.PeriodFrom.Equal(day("2026-09-01")) {
		t.Errorf("period = %v..%v, want 2026-09-01..2026-09-08", result.PeriodFrom, result.PeriodTo)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "2026-09-08") {
		t.Fatalf("warnings = %q, want one stale-period warning naming 2026-09-08", result.Warnings)
	}
}

func TestDescribeStatementIsQuietForAnUpToDateReport(t *testing.T) {
	stmt := ibkr.Statement{
		ToDate:              day("2026-09-23"),
		HasCashTransactions: true, HasTrades: true, HasPositions: true,
	}
	var result SyncResult
	describeStatement(&result, stmt, time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC))
	if len(result.Warnings) != 0 || len(result.MissingSections) != 0 {
		t.Errorf("warnings = %q, missing = %q; want none", result.Warnings, result.MissingSections)
	}
}

func TestDescribeStatementReportsMissingSectionsAndSkippedRows(t *testing.T) {
	stmt := ibkr.Statement{HasTrades: true}
	result := SyncResult{CashMovementsFailedBefore: 2, CashMovementsOtherAccount: 1}
	describeStatement(&result, stmt, time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC))

	if want := []string{"cash_transactions", "open_positions"}; !slices.Equal(result.MissingSections, want) {
		t.Errorf("MissingSections = %q, want %q", result.MissingSections, want)
	}
	joined := strings.Join(result.Warnings, "\n")
	for _, want := range []string{"Cash Transactions, Open Positions", "Se conservaron las posiciones", "2 movimiento(s)", "1 movimiento(s)"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings %q do not mention %q", result.Warnings, want)
		}
	}
}
