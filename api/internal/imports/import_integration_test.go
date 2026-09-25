package imports_test

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"

	"github.com/JuanKsPty/corebank/api/internal/accounts"
	"github.com/JuanKsPty/corebank/api/internal/categories"
	"github.com/JuanKsPty/corebank/api/internal/civil"
	"github.com/JuanKsPty/corebank/api/internal/imports"
	"github.com/JuanKsPty/corebank/api/internal/money"
	"github.com/JuanKsPty/corebank/api/internal/store"
	"github.com/JuanKsPty/corebank/api/internal/storetest"
)

type env struct {
	db       *store.DB
	imports  *imports.Service
	accounts *accounts.Service
	user     uuid.UUID
}

func setup(t *testing.T) env {
	t.Helper()
	db := storetest.New(t)
	ctx := context.Background()
	u, err := db.Q().CreateUser(ctx, store.User{ID: uuid.New(), Email: "o@example.com", PasswordHash: "x", FullName: "O"})
	if err != nil {
		t.Fatal(err)
	}
	cats := categories.NewService(db)
	if err := db.InTx(ctx, func(q *store.Queries) error { return cats.SeedDefaults(ctx, q, u.ID) }); err != nil {
		t.Fatal(err)
	}
	return env{db: db, imports: imports.NewService(db, cats), accounts: accounts.NewService(db), user: u.ID}
}

func (e env) importFile(t *testing.T, name string, data []byte) imports.Result {
	t.Helper()
	r, err := e.imports.Import(context.Background(), e.user, name, data, nil)
	if err != nil {
		t.Fatalf("Import(%s): %v", name, err)
	}
	return r
}

func (e env) account(t *testing.T, id uuid.UUID) accounts.Account {
	t.Helper()
	a, err := e.accounts.Get(context.Background(), e.user, id)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// --- fixtures ----------------------------------------------------------------

var excelEpoch = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)

type bgRow struct {
	day           int
	desc          string
	debit, credit float64
	balance       float64
}

func bgFile(t *testing.T, rows []bgRow) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	s := f.GetSheetName(0)
	set := func(c string, v any) {
		if err := f.SetCellValue(s, c, v); err != nil {
			t.Fatal(err)
		}
	}
	set("A4", "Cuenta:Prueba 00-00-00-000000-0")
	for col, name := range map[string]string{"A": "Fecha", "D": "Transacción", "E": "Descripción", "F": "Débito", "G": "Crédito", "I": "Saldo total"} {
		set(col+"8", name)
	}
	for i, r := range rows {
		n := strconv.Itoa(9 + i)
		set("A"+n, float64(time.Date(2026, 3, r.day, 10, 0, 0, 0, time.UTC).Sub(excelEpoch))/float64(24*time.Hour))
		set("D"+n, "264")
		set("E"+n, r.desc)
		if r.debit != 0 {
			set("F"+n, r.debit)
		}
		if r.credit != 0 {
			set("G"+n, r.credit)
		}
		set("I"+n, r.balance)
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func latin1(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		out = append(out, byte(r))
	}
	return out
}

func bacFile(inicial, libros string, detail ...string) []byte {
	lines := []string{
		"Número de Clientes, Nombre, Producto, Moneda, Saldo Inicial, Saldo en Libros, Retenidos y Diferidos, Saldo Disponible, Fecha",
		"900000001, PERSONA, 100000001, USD, " + inicial + ", " + libros + ", 0.00, " + libros + ", 30/06/2026",
		"Detalle de Estado Bancario",
		"Fecha de Transacción, Referencia de Transacción, Código de Transacción, Descripción de Transacción, Débito de Transacción, Crédito de Transacción, Balance de Transacción",
	}
	return latin1(strings.Join(append(lines, detail...), "\r\n"))
}

const cardFile = "Cuenta;Tarjeta;Fecha tran;Fecha proceso;Descripcion;Referencia;Categoria;Cargos (Db);Pagos (Cr)\r\n" +
	"**** 1111;**** 1111;15/01/2026;15/01/2026;SEGURO DE DESGRAVAMEN;R1;Cargos;-0.5;\r\n" +
	"**** 1111;**** 1111;15/01/2026;15/01/2026;ITBMS CARGO POR SEGURO;R1;Cargos;-0.05;\r\n" +
	"**** 1111;**** 1111;20/01/2026;20/01/2026;GRACIAS POR SU PAGO - BANCA MOVIL;R2;Pagos;;100.00\r\n" +
	"**** 1111;**** 2222;18/01/2026;19/01/2026;CAFE DE PRUEBA PANAMA;R3;Comida y Bebida;-9.5;\r\n" +
	"**** 1111;**** 2222;19/01/2026;20/01/2026;FARMACIA DE PRUEBA PANAMA;R4;Salud;-4.25;\r\n"

// --- the cases the rebuild exists for ---------------------------------------

func TestABankAccountEndsWhereTheBankSays(t *testing.T) {
	e := setup(t)
	r := e.importFile(t, "m.xlsx", bgFile(t, []bgRow{
		{day: 1, desc: "YAPPY", debit: -12.5, balance: 87.5},
		{day: 2, desc: "TRANSFERENCIA", credit: 20, balance: 107.5},
	}))
	a := e.account(t, r.Accounts[0].AccountID)
	// The TigerBeetle mirror ended this file at 20.00.
	if !a.Anchored || a.Balance != 10750 {
		t.Fatalf("balance = %s (anchored %v), want 107.50", a.Balance, a.Anchored)
	}
	if a.Drift == nil || a.Drift.Difference() != 0 {
		t.Errorf("drift = %+v, want the closing to match exactly", a.Drift)
	}
}

func TestABACStatementUsesItsRealOpening(t *testing.T) {
	e := setup(t)
	r := e.importFile(t, "estado.csv", bacFile("500.00", "750.75",
		"15/06/2026, 1, 4A, TRANSFERENCIA, 25.00, 0.00, 475.00",
		"20/06/2026, 2, 4C, DEPOSITO, 0.00, 275.75, 750.75"))
	if a := e.account(t, r.Accounts[0].AccountID); a.Balance != 75075 {
		t.Errorf("balance = %s, want 750.75", a.Balance)
	}
}

func TestACardOwesWhatItsStatementSaysOnceAnchored(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	r := e.importFile(t, "tarjeta.txt", []byte(cardFile))
	res := r.Accounts[0]
	if !res.NeedsOpening {
		t.Error("a card with no balance fields must ask for its starting balance")
	}
	if a := e.account(t, res.AccountID); a.Anchored || a.Class != "liability" {
		t.Fatalf("card = %+v, want an unanchored liability", a)
	}

	// The owner types "owed 100.00 on 2026-01-14" from the PDF.
	if _, err := e.accounts.AddCheckpoint(ctx, e.user, res.AccountID, accounts.CheckpointInput{
		AsOf: civil.Date{Year: 2026, Month: 1, Day: 14}, Balance: -10000, Pin: true,
	}); err != nil {
		t.Fatal(err)
	}
	a := e.account(t, res.AccountID)
	// The old view showed +85.70 for this card.
	if !a.Anchored || a.Balance != -1430 {
		t.Fatalf("card balance = %s, want -14.30 (owed 14.30)", a.Balance)
	}

	// The payment is a transfer, not income; the purchases and fees are not.
	list, err := e.db.Q().Entries(ctx, store.EntryFilter{UserID: e.user, Kinds: store.SpendKinds})
	if err != nil {
		t.Fatal(err)
	}
	var spend money.Cents
	for _, en := range list {
		spend -= en.Amount
	}
	if spend != 1430 {
		t.Errorf("spend = %s, want 14.30 counted once", spend)
	}

	// Net worth with a bank account of 500 is 485.70.
	e.importFile(t, "estado.csv", bacFile("500.00", "500.00"))
	all, err := e.accounts.List(ctx, e.user)
	if err != nil {
		t.Fatal(err)
	}
	assets, liabilities, holdings := accounts.NetWorth(all)
	if total := assets + liabilities + holdings; total != 48570 {
		t.Errorf("net worth = %s, want 485.70", total)
	}
}

func TestReimportingIsIdempotentAndOverlapsDedupe(t *testing.T) {
	e := setup(t)
	first := bgFile(t, []bgRow{
		{day: 1, desc: "A", debit: -12.5, balance: 87.5},
		{day: 2, desc: "B", credit: 20, balance: 107.5},
	})
	e.importFile(t, "m.xlsx", first)
	if r := e.importFile(t, "m.xlsx", first); !r.Unchanged {
		t.Error("the same bytes again must report unchanged")
	}
	// A later export overlapping the first by one day.
	r := e.importFile(t, "m2.xlsx", bgFile(t, []bgRow{
		{day: 2, desc: "B", credit: 20, balance: 107.5},
		{day: 3, desc: "C", debit: -7.5, balance: 100},
	}))
	res := r.Accounts[0]
	if res.New != 1 || res.Duplicates != 1 {
		t.Errorf("new = %d, duplicates = %d; want 1 and 1", res.New, res.Duplicates)
	}
	if a := e.account(t, res.AccountID); a.Balance != 10000 || a.Drift == nil || a.Drift.Difference() != 0 {
		t.Errorf("balance = %s, drift = %+v; want 100.00 matching the bank", a.Balance, a.Drift)
	}
}

func TestAMissingLineShowsUpAsDrift(t *testing.T) {
	e := setup(t)
	// The first file stops on the 1st; the second starts on the 3rd, so the
	// 2nd's +20.00 was never imported. The second file's opening says 107.50.
	e.importFile(t, "a.xlsx", bgFile(t, []bgRow{{day: 1, desc: "A", debit: -12.5, balance: 87.5}}))
	r := e.importFile(t, "b.xlsx", bgFile(t, []bgRow{{day: 3, desc: "C", debit: -7.5, balance: 100}}))
	a := e.account(t, r.Accounts[0].AccountID)
	if a.Drift == nil || a.Drift.Difference() != 2000 {
		t.Errorf("drift = %+v, want +20.00: exactly the missing line", a.Drift)
	}
}

func TestAnImportFromTheWrongAccountPageIsRefused(t *testing.T) {
	e := setup(t)
	other := uuid.New()
	_, err := e.imports.Import(context.Background(), e.user, "tarjeta.txt", []byte(cardFile), &other)
	if !errors.Is(err, imports.ErrAccountMismatch) {
		t.Errorf("err = %v, want ErrAccountMismatch", err)
	}
	// Nothing was written: the whole file is one transaction.
	if list, _ := e.accounts.List(context.Background(), e.user); len(list) != 0 {
		t.Errorf("%d accounts were created by a refused import", len(list))
	}
}

func TestRulesCategoriseAndOneUserNeverSeesAnothersData(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.importFile(t, "tarjeta.txt", []byte(cardFile))
	list, err := e.db.Q().Entries(ctx, store.EntryFilter{UserID: e.user, Search: "FARMACIA"})
	if err != nil || len(list) != 1 || list[0].CategoryID == nil {
		t.Fatalf("the pharmacy line is %+v, want it filed under Salud by the default rule", list)
	}

	stranger, err := e.db.Q().CreateUser(ctx, store.User{ID: uuid.New(), Email: "s@example.com", PasswordHash: "x", FullName: "S"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := accounts.NewService(e.db).List(ctx, stranger.ID); len(got) != 0 {
		t.Errorf("a stranger sees %d accounts", len(got))
	}
	if got, _ := e.db.Q().Entries(ctx, store.EntryFilter{UserID: stranger.ID}); len(got) != 0 {
		t.Errorf("a stranger sees %d movements", len(got))
	}
}

func TestReconciliationPointsAtTheLineAfterAMissingOne(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	// One export holds the 1st; the next starts on the 3rd, so the 2nd's
	// +20.00 was never imported.
	e.importFile(t, "a.xlsx", bgFile(t, []bgRow{{day: 1, desc: "A", debit: -12.5, balance: 87.5}}))
	r := e.importFile(t, "b.xlsx", bgFile(t, []bgRow{
		{day: 3, desc: "C", debit: -7.5, balance: 100},
		{day: 4, desc: "D", credit: 5, balance: 105},
	}))
	rec, err := e.accounts.Reconcile(ctx, e.user, r.Accounts[0].AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.FirstBreak == nil {
		t.Fatal("no first break found")
	}
	var first accounts.Line
	for _, l := range rec.Lines {
		if l.Entry.ID == *rec.FirstBreak {
			first = l
		}
	}
	if first.Entry.Description != "C" || first.Difference == nil || *first.Difference != 2000 {
		t.Errorf("first break = %q with difference %v; want line C, +20.00", first.Entry.Description, first.Difference)
	}
	if len(rec.Statements) != 2 || !rec.Statements[1].Gap {
		t.Errorf("statements = %+v, want the second flagged as starting after a gap", rec.Statements)
	}
	if o := rec.Statements[1].Opening; o == nil || o.Difference() != 2000 {
		t.Errorf("second statement's opening check = %+v, want 20.00 off", o)
	}
	if c := rec.Statements[0].Closing; c == nil || c.Difference() != 0 {
		t.Errorf("first statement's closing check = %+v, want it to match", c)
	}
}

func TestReconciliationIsQuietWhenEverythingMatches(t *testing.T) {
	e := setup(t)
	r := e.importFile(t, "a.xlsx", bgFile(t, []bgRow{
		{day: 1, desc: "A", debit: -12.5, balance: 87.5},
		{day: 2, desc: "B", credit: 20, balance: 107.5},
	}))
	rec, err := e.accounts.Reconcile(context.Background(), e.user, r.Accounts[0].AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.FirstBreak != nil {
		t.Errorf("FirstBreak = %v on a statement that adds up", rec.FirstBreak)
	}
	for _, c := range rec.Checks {
		if c.Difference() != 0 {
			t.Errorf("check %s is off by %s", c.Checkpoint.Source, c.Difference())
		}
	}
}
