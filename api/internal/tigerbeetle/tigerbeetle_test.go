package tigerbeetle

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/ledger"
	"github.com/JuanKsPty/corebank/api/internal/money"
)

// These tests run against a live TigerBeetle replica (`make up`). The adapter's
// job is to translate backend statuses into domain errors, and that mapping is
// only meaningful if the real backend produces the statuses we claim — a mock
// would just assert our own assumptions back at us.
//
// When no replica is reachable the tests skip rather than fail, so a checkout
// without the infrastructure running still gets a green `make test`.

func newTestAdapter(t *testing.T) *Adapter {
	t.Helper()

	addr := os.Getenv("TB_ADDRESS")
	if addr == "" {
		addr = "127.0.0.1:3001"
	}
	conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		t.Skipf("no TigerBeetle at %s (run `make up`): %v", addr, err)
	}
	_ = conn.Close()

	a, err := Connect(0, []string{addr})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// fixture creates the world account plus two funded customer accounts. Account
// ids are fresh per test so runs never interfere with each other.
func fixture(t *testing.T, a *Adapter, fund money.Cents) (src, dst ledger.AccountID) {
	t.Helper()
	ctx := context.Background()

	src = ledger.AccountID(uuid.New().ID())
	dst = ledger.AccountID(uuid.New().ID())
	owner := uuid.New()

	err := a.EnsureAccounts(ctx, []ledger.NewAccount{
		{ID: ledger.WorldAccountID, Kind: ledger.KindWorld},
		{ID: src, Kind: ledger.KindSavings, Owner: owner},
		{ID: dst, Kind: ledger.KindChecking, Owner: owner},
	})
	if err != nil {
		t.Fatalf("EnsureAccounts: %v", err)
	}

	if fund > 0 {
		err = a.Post(ctx, ledger.Movement{
			ID: uuid.New(), From: ledger.WorldAccountID, To: src,
			Amount: fund, Kind: ledger.MovementOpening, Actor: owner,
		})
		if err != nil {
			t.Fatalf("funding Post: %v", err)
		}
	}
	return src, dst
}

func available(t *testing.T, a *Adapter, id ledger.AccountID) money.Cents {
	t.Helper()
	balances, err := a.Balances(context.Background(), []ledger.AccountID{id})
	if err != nil {
		t.Fatalf("Balances: %v", err)
	}
	b, ok := balances[id]
	if !ok {
		t.Fatalf("account %d missing from balances", id)
	}
	return b.Available
}

func TestEnsureAccountsIsIdempotent(t *testing.T) {
	a := newTestAdapter(t)
	ctx := context.Background()

	id := ledger.AccountID(uuid.New().ID())
	accounts := []ledger.NewAccount{{ID: id, Kind: ledger.KindSavings, Owner: uuid.New()}}

	if err := a.EnsureAccounts(ctx, accounts); err != nil {
		t.Fatalf("first EnsureAccounts: %v", err)
	}
	// Re-running the seeder must not be an error; this is what lets the seed
	// service run on every boot.
	if err := a.EnsureAccounts(ctx, accounts); err != nil {
		t.Fatalf("second EnsureAccounts should succeed, got %v", err)
	}
}

func TestBalancesOmitsUnknownAccounts(t *testing.T) {
	a := newTestAdapter(t)
	src, _ := fixture(t, a, 1000)

	unknown := ledger.AccountID(uuid.New().ID())
	balances, err := a.Balances(context.Background(), []ledger.AccountID{src, unknown})
	if err != nil {
		t.Fatalf("Balances: %v", err)
	}
	if _, ok := balances[src]; !ok {
		t.Error("expected the known account to be present")
	}
	// Absence rather than a zero balance: callers must be able to tell "no such
	// account" from "an account holding nothing".
	if _, ok := balances[unknown]; ok {
		t.Error("expected the unknown account to be absent, not zero-valued")
	}
}

func TestPostMovesMoneyBothSides(t *testing.T) {
	a := newTestAdapter(t)
	src, dst := fixture(t, a, 50_000)

	err := a.Post(context.Background(), ledger.Movement{
		ID: uuid.New(), From: src, To: dst,
		Amount: 12_500, Kind: ledger.MovementTransfer, Actor: uuid.New(),
	})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if got := available(t, a, src); got != 37_500 {
		t.Errorf("source balance = %s, want 375.00", got)
	}
	if got := available(t, a, dst); got != 12_500 {
		t.Errorf("destination balance = %s, want 125.00", got)
	}
}

// TestPostRejectsOverdraft is the important one: insufficient funds is refused
// by the ledger's own invariant, not by a balance check in application code.
func TestPostRejectsOverdraft(t *testing.T) {
	a := newTestAdapter(t)
	src, dst := fixture(t, a, 10_000)

	err := a.Post(context.Background(), ledger.Movement{
		ID: uuid.New(), From: src, To: dst,
		Amount: 10_001, Kind: ledger.MovementTransfer, Actor: uuid.New(),
	})
	if !errors.Is(err, ledger.ErrInsufficientFunds) {
		t.Fatalf("error = %v, want ErrInsufficientFunds", err)
	}
	if got := available(t, a, src); got != 10_000 {
		t.Errorf("balance changed on a rejected movement: %s", got)
	}
	if got := available(t, a, dst); got != 0 {
		t.Errorf("destination credited on a rejected movement: %s", got)
	}
}

func TestPostReplayIsReportedAsAlreadyApplied(t *testing.T) {
	a := newTestAdapter(t)
	src, dst := fixture(t, a, 20_000)
	ctx := context.Background()

	m := ledger.Movement{
		ID: uuid.New(), From: src, To: dst,
		Amount: 5_000, Kind: ledger.MovementTransfer, Actor: uuid.New(),
	}
	if err := a.Post(ctx, m); err != nil {
		t.Fatalf("first Post: %v", err)
	}
	// A retry after a crash must not move the money twice. The deterministic
	// movement id is what makes that safe.
	if err := a.Post(ctx, m); !errors.Is(err, ledger.ErrAlreadyApplied) {
		t.Fatalf("replay error = %v, want ErrAlreadyApplied", err)
	}
	if got := available(t, a, dst); got != 5_000 {
		t.Errorf("destination = %s, want 50.00 (credited exactly once)", got)
	}
}

func TestPostRejectedMovements(t *testing.T) {
	a := newTestAdapter(t)
	src, _ := fixture(t, a, 10_000)
	ctx := context.Background()

	t.Run("destination does not exist", func(t *testing.T) {
		err := a.Post(ctx, ledger.Movement{
			ID: uuid.New(), From: src, To: ledger.AccountID(uuid.New().ID()),
			Amount: 100, Kind: ledger.MovementTransfer, Actor: uuid.New(),
		})
		if !errors.Is(err, ledger.ErrDestinationNotFound) {
			t.Errorf("error = %v, want ErrDestinationNotFound", err)
		}
	})

	t.Run("source does not exist", func(t *testing.T) {
		err := a.Post(ctx, ledger.Movement{
			ID: uuid.New(), From: ledger.AccountID(uuid.New().ID()), To: src,
			Amount: 100, Kind: ledger.MovementTransfer, Actor: uuid.New(),
		})
		if !errors.Is(err, ledger.ErrSourceNotFound) {
			t.Errorf("error = %v, want ErrSourceNotFound", err)
		}
	})

	// Caught in the domain before reaching the backend, so no round trip.
	t.Run("same account", func(t *testing.T) {
		err := a.Post(ctx, ledger.Movement{
			ID: uuid.New(), From: src, To: src,
			Amount: 100, Kind: ledger.MovementTransfer, Actor: uuid.New(),
		})
		if !errors.Is(err, ledger.ErrSameAccount) {
			t.Errorf("error = %v, want ErrSameAccount", err)
		}
	})

	t.Run("non-positive amount", func(t *testing.T) {
		for _, amount := range []money.Cents{0, -100} {
			err := a.Post(ctx, ledger.Movement{
				ID: uuid.New(), From: src, To: ledger.WorldAccountID,
				Amount: amount, Kind: ledger.MovementWithdrawal, Actor: uuid.New(),
			})
			if !errors.Is(err, ledger.ErrAmountNotPositive) {
				t.Errorf("amount %s: error = %v, want ErrAmountNotPositive", amount, err)
			}
		}
	})
}

// TestHoldSettle covers the assistant's confirmation flow end to end: reserve,
// show the user, then commit.
func TestHoldSettle(t *testing.T) {
	a := newTestAdapter(t)
	src, dst := fixture(t, a, 100_000)
	ctx := context.Background()

	holdID := uuid.New()
	err := a.Hold(ctx, ledger.Movement{
		ID: holdID, From: src, To: dst,
		Amount: 30_000, Kind: ledger.MovementTransfer, Actor: uuid.New(),
	}, 2*time.Minute)
	if err != nil {
		t.Fatalf("Hold: %v", err)
	}

	// The reservation is visible immediately, so the same funds cannot be spent
	// twice while the user is deciding.
	if got := available(t, a, src); got != 70_000 {
		t.Errorf("source available = %s, want 700.00 while held", got)
	}
	balances, _ := a.Balances(ctx, []ledger.AccountID{src})
	if got := balances[src].Held; got != 30_000 {
		t.Errorf("held = %s, want 300.00", got)
	}
	// Crucially, the destination has not been credited yet.
	if got := available(t, a, dst); got != 0 {
		t.Errorf("destination = %s, want 0.00 before settlement", got)
	}

	if err := a.Settle(ctx, holdID, uuid.New(), 30_000, ledger.MovementTransfer); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if got := available(t, a, src); got != 70_000 {
		t.Errorf("source after settle = %s, want 700.00", got)
	}
	if got := available(t, a, dst); got != 30_000 {
		t.Errorf("destination after settle = %s, want 300.00", got)
	}
	balances, _ = a.Balances(ctx, []ledger.AccountID{src})
	if got := balances[src].Held; got != 0 {
		t.Errorf("held after settle = %s, want 0.00", got)
	}
}

// TestHoldVoid covers the user tapping Cancel: the reservation is released and
// nothing moved.
func TestHoldVoid(t *testing.T) {
	a := newTestAdapter(t)
	src, dst := fixture(t, a, 100_000)
	ctx := context.Background()

	holdID := uuid.New()
	err := a.Hold(ctx, ledger.Movement{
		ID: holdID, From: src, To: dst,
		Amount: 45_000, Kind: ledger.MovementTransfer, Actor: uuid.New(),
	}, 2*time.Minute)
	if err != nil {
		t.Fatalf("Hold: %v", err)
	}
	if got := available(t, a, src); got != 55_000 {
		t.Fatalf("source available = %s, want 550.00 while held", got)
	}

	if err := a.Void(ctx, holdID, uuid.New(), 45_000, ledger.MovementTransfer); err != nil {
		t.Fatalf("Void: %v", err)
	}
	if got := available(t, a, src); got != 100_000 {
		t.Errorf("source after void = %s, want 1000.00 restored", got)
	}
	if got := available(t, a, dst); got != 0 {
		t.Errorf("destination after void = %s, want 0.00", got)
	}
}

func TestHoldCannotBeResolvedTwice(t *testing.T) {
	a := newTestAdapter(t)
	src, dst := fixture(t, a, 50_000)
	ctx := context.Background()

	holdID := uuid.New()
	if err := a.Hold(ctx, ledger.Movement{
		ID: holdID, From: src, To: dst,
		Amount: 10_000, Kind: ledger.MovementTransfer, Actor: uuid.New(),
	}, 2*time.Minute); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	if err := a.Settle(ctx, holdID, uuid.New(), 10_000, ledger.MovementTransfer); err != nil {
		t.Fatalf("Settle: %v", err)
	}

	// A double-confirm from an impatient user must not move the money again.
	err := a.Settle(ctx, holdID, uuid.New(), 10_000, ledger.MovementTransfer)
	if !errors.Is(err, ledger.ErrHoldAlreadySettled) {
		t.Errorf("second Settle error = %v, want ErrHoldAlreadySettled", err)
	}
	// Nor may a settled hold be cancelled afterwards.
	err = a.Void(ctx, holdID, uuid.New(), 10_000, ledger.MovementTransfer)
	if !errors.Is(err, ledger.ErrHoldAlreadySettled) {
		t.Errorf("Void after Settle error = %v, want ErrHoldAlreadySettled", err)
	}
	if got := available(t, a, dst); got != 10_000 {
		t.Errorf("destination = %s, want 100.00 (settled exactly once)", got)
	}
}

func TestResolvingUnknownHold(t *testing.T) {
	a := newTestAdapter(t)
	err := a.Settle(context.Background(), uuid.New(), uuid.New(), 100, ledger.MovementTransfer)
	if !errors.Is(err, ledger.ErrHoldNotFound) {
		t.Errorf("error = %v, want ErrHoldNotFound", err)
	}
}

func TestEntriesAndLookup(t *testing.T) {
	a := newTestAdapter(t)
	src, dst := fixture(t, a, 40_000)
	ctx := context.Background()

	movementID := uuid.New()
	if err := a.Post(ctx, ledger.Movement{
		ID: movementID, From: src, To: dst,
		Amount: 2_500, Kind: ledger.MovementTransfer, Actor: uuid.New(),
	}); err != nil {
		t.Fatalf("Post: %v", err)
	}

	entries, err := a.Entries(ctx, src, ledger.EntryQuery{Limit: 50, Newest: true})
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	// The opening credit plus the transfer.
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}

	found := false
	for _, e := range entries {
		if e.ID == movementID {
			found = true
			if e.From != src || e.To != dst {
				t.Errorf("entry accounts = %d -> %d, want %d -> %d", e.From, e.To, src, dst)
			}
			if e.Amount != 2_500 {
				t.Errorf("entry amount = %s, want 25.00", e.Amount)
			}
			if e.Kind != ledger.MovementTransfer {
				t.Errorf("entry kind = %s, want transfer", e.Kind)
			}
			if e.At.IsZero() {
				t.Error("entry timestamp is zero")
			}
		}
	}
	if !found {
		t.Errorf("movement %s missing from the account's entries", movementID)
	}

	// Lookup is what the reconciliation sweeper uses to find out whether a
	// movement it lost track of actually landed.
	looked, err := a.Lookup(ctx, []uuid.UUID{movementID})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(looked) != 1 || looked[0].ID != movementID {
		t.Errorf("Lookup returned %d entries, want the one movement", len(looked))
	}

	// An id that was never submitted is simply absent.
	missing, err := a.Lookup(ctx, []uuid.UUID{uuid.New()})
	if err != nil {
		t.Fatalf("Lookup unknown: %v", err)
	}
	if len(missing) != 0 {
		t.Errorf("Lookup of an unknown id returned %d entries, want 0", len(missing))
	}
}

func TestAccountIDFromNumber(t *testing.T) {
	// Domain-level, no backend needed.
	cases := []struct {
		in   string
		want ledger.AccountID
	}{
		{"4001-6588-5247-0001", 4001658852470001},
		{"4001658852470001", 4001658852470001},
		{"4001 6588 5247 0001", 4001658852470001},
	}
	for _, c := range cases {
		got, err := ledger.AccountIDFromNumber(c.in)
		if err != nil {
			t.Errorf("AccountIDFromNumber(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("AccountIDFromNumber(%q) = %d, want %d", c.in, got, c.want)
		}
	}

	for _, bad := range []string{"", "EXTERNAL", "4001-ABCD-5247-0001", "1", "99999999999999999999"} {
		if _, err := ledger.AccountIDFromNumber(bad); err == nil {
			t.Errorf("AccountIDFromNumber(%q) should have failed", bad)
		}
	}
}

// TestResolvingAHoldLeavesTheHoldRecordPending pins down the assumption the
// reconciliation sweeper is built on.
//
// Posting a hold does not modify the hold: it creates a *separate* transfer that
// points back at it. So the hold's own record keeps reporting Pending forever,
// and "was this hold resolved?" can only be answered by asking whether its
// resolving transfer exists. Were this to change, the sweeper would start
// reporting settled movements as expired.
func TestResolvingAHoldLeavesTheHoldRecordPending(t *testing.T) {
	a := newTestAdapter(t)
	src, dst := fixture(t, a, 100_00)

	holdID, entryID := uuid.New(), uuid.New()
	move := ledger.Movement{ID: holdID, From: src, To: dst, Amount: 25_00, Kind: ledger.MovementTransfer}

	if err := a.Hold(context.Background(), move, time.Minute); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	if err := a.Settle(context.Background(), holdID, entryID, 25_00, ledger.MovementTransfer); err != nil {
		t.Fatalf("Settle: %v", err)
	}

	entries, err := a.Lookup(context.Background(), []uuid.UUID{holdID, entryID})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	byID := make(map[uuid.UUID]ledger.Entry, len(entries))
	for _, e := range entries {
		byID[e.ID] = e
	}

	hold, ok := byID[holdID]
	if !ok {
		t.Fatal("the hold is not in the ledger")
	}
	t.Logf("hold  pending=%v settles=%v voids=%v", hold.Pending, hold.Settles, hold.Voids)
	if !hold.Pending {
		t.Error("the hold stopped reporting Pending once settled; the sweeper must not rely on it")
	}

	resolution, ok := byID[entryID]
	if !ok {
		t.Fatal("the resolving transfer is not in the ledger")
	}
	t.Logf("entry pending=%v settles=%v voids=%v hold=%s",
		resolution.Pending, resolution.Settles, resolution.Voids, resolution.HoldID)
	if !resolution.Settles || resolution.Voids {
		t.Error("the resolving transfer does not identify itself as a settlement")
	}
	if resolution.HoldID != holdID {
		t.Errorf("the resolving transfer points at hold %s, want %s", resolution.HoldID, holdID)
	}
}
