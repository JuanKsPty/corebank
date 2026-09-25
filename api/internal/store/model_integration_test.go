package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/civil"
	"github.com/JuanKsPty/corebank/api/internal/money"
	"github.com/JuanKsPty/corebank/api/internal/store"
	"github.com/JuanKsPty/corebank/api/internal/storetest"
)

func TestEntriesRoundTripAndStayImmutable(t *testing.T) {
	db := storetest.New(t)
	ctx := context.Background()
	u := newUser(t, db, "e@example.com")

	acct, created, err := db.Q().EnsureAccount(ctx, store.Account{
		ID: uuid.New(), UserID: u.ID, Class: "liability", Type: "credit_card",
		Institution: "banco_general", ExternalNumber: "**** 1111", Currency: "USD", DisplayName: "Tarjeta",
	})
	if err != nil || !created {
		t.Fatalf("EnsureAccount = %v, %v", created, err)
	}
	again, created, err := db.Q().EnsureAccount(ctx, store.Account{
		ID: uuid.New(), UserID: u.ID, Class: "liability", Type: "credit_card",
		Institution: "banco_general", ExternalNumber: "**** 1111", Currency: "USD",
	})
	if err != nil || created || again.ID != acct.ID {
		t.Fatalf("second EnsureAccount = %v, %v, %v; want the same account", again.ID, created, err)
	}

	posted := civil.Date{Year: 2026, Month: time.January, Day: 19}
	bal := money.Cents(-950)
	e := store.Entry{
		ID: uuid.New(), UserID: u.ID, AccountID: acct.ID,
		BookedOn: civil.Date{Year: 2026, Month: time.January, Day: 18}, PostedOn: &posted,
		Amount: -950, Kind: store.KindExpense, Description: "CAFE", DedupKey: "k1",
		BankBalance: &bal, Raw: map[string]string{"Tarjeta": "**** 2222"},
	}
	if ok, err := db.Q().InsertEntryIfNew(ctx, e); err != nil || !ok {
		t.Fatalf("InsertEntryIfNew = %v, %v", ok, err)
	}
	if ok, err := db.Q().InsertEntryIfNew(ctx, e); err != nil || ok {
		t.Fatalf("second InsertEntryIfNew = %v, %v; want a no-op", ok, err)
	}

	got, err := db.Q().EntryByID(ctx, u.ID, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.BalanceOn != posted || got.PostedOn == nil || *got.PostedOn != posted || got.BookedAt != nil {
		t.Errorf("dates = booked %v posted %v balance %v at %v", got.BookedOn, got.PostedOn, got.BalanceOn, got.BookedAt)
	}
	if got.Amount != -950 || got.BankBalance == nil || *got.BankBalance != -950 || got.Raw["Tarjeta"] != "**** 2222" {
		t.Errorf("entry = %+v", got)
	}

	transfer := store.KindTransfer
	if err := db.Q().SetEntryUserKind(ctx, u.ID, e.ID, &transfer); err != nil {
		t.Fatalf("SetEntryUserKind: %v", err)
	}
	list, err := db.Q().Entries(ctx, store.EntryFilter{UserID: u.ID, Kinds: []string{store.KindTransfer}})
	if err != nil || len(list) != 1 || list[0].EffectiveKind() != store.KindTransfer {
		t.Fatalf("Entries(kind transfer) = %v, %v", list, err)
	}

	// Somebody else cannot see or touch it.
	other := newUser(t, db, "other@example.com")
	if _, err := db.Q().EntryByID(ctx, other.ID, e.ID); err == nil {
		t.Error("another user read the entry")
	}
	if err := db.Q().SetEntryNote(ctx, other.ID, e.ID, "x"); err == nil {
		t.Error("another user wrote the entry")
	}

	// Sums and the opening arithmetic.
	sum, err := db.Q().SumThroughDay(ctx, acct.ID, posted)
	if err != nil || sum != -950 {
		t.Errorf("SumThroughDay = %v, %v", sum, err)
	}
	before, err := db.Q().SumBefore(ctx, acct.ID, e.ID)
	if err != nil || before != 0 {
		t.Errorf("SumBefore = %v, %v", before, err)
	}

	// Checkpoints, with a nullable date and a pin.
	due := civil.Date{Year: 2026, Month: time.February, Day: 10}
	cp := store.Checkpoint{ID: uuid.New(), UserID: u.ID, AccountID: acct.ID, AsOf: civil.Date{Year: 2026, Month: 1, Day: 14},
		Balance: -10000, Source: store.SourceManual, DueOn: &due}
	if err := db.Q().CreateCheckpoint(ctx, cp); err != nil {
		t.Fatal(err)
	}
	if err := db.Q().PinCheckpoint(ctx, u.ID, acct.ID, &cp.ID); err != nil {
		t.Fatal(err)
	}
	cps, err := db.Q().CheckpointsByAccount(ctx, u.ID, acct.ID)
	if err != nil || len(cps) != 1 || !cps[0].Pinned || cps[0].DueOn == nil || *cps[0].DueOn != due {
		t.Fatalf("checkpoints = %+v, %v", cps, err)
	}

	// Deleting the account takes everything with it.
	if err := db.Q().DeleteAccount(ctx, u.ID, acct.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Q().EntryByID(ctx, u.ID, e.ID); err == nil {
		t.Error("the entry outlived its account")
	}
}

func TestAnImportedAmountCannotBeEdited(t *testing.T) {
	db := storetest.New(t)
	ctx := context.Background()
	u := newUser(t, db, "g@example.com")
	acct, _, _ := db.Q().EnsureAccount(ctx, store.Account{ID: uuid.New(), UserID: u.ID, Class: "asset",
		Type: "checking", Institution: "bac", ExternalNumber: "1", Currency: "USD"})
	e := store.Entry{ID: uuid.New(), UserID: u.ID, AccountID: acct.ID, BookedOn: civil.Date{Year: 2026, Month: 1, Day: 1},
		Amount: 100, Kind: store.KindIncome, DedupKey: "k"}
	if _, err := db.Q().InsertEntryIfNew(ctx, e); err != nil {
		t.Fatal(err)
	}
	if err := storetest.Exec(t, db, `UPDATE entries SET amount_cents = 5 WHERE id = $1`, e.ID); err == nil {
		t.Error("an imported amount was edited")
	}
}
