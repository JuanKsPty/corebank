package statement

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/JuanKsPty/corebank/api/internal/money"
)

// --- Banco General account (.xlsx) -------------------------------------------

type bgRow struct {
	day           int
	hour          int
	code, desc    string
	debit, credit float64
	balance       float64
	omitBalance   bool
}

// bgAccountFile builds a workbook shaped like a real BG export, with invented
// movements.
func bgAccountFile(t *testing.T, rows []bgRow) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	sheet := f.GetSheetName(0)
	set := func(cell string, v any) {
		t.Helper()
		if err := f.SetCellValue(sheet, cell, v); err != nil {
			t.Fatal(err)
		}
	}
	set("B2", "Últimos movimientos")
	set("A4", "Cuenta:Prueba 00-00-00-000000-0")
	for col, name := range map[string]string{"A": "Fecha", "C": "Referencia", "D": "Transacción",
		"E": "Descripción", "F": "Débito", "G": "Crédito", "I": "Saldo total"} {
		set(col+"8", name)
	}
	for i, r := range rows {
		n := strconv.Itoa(9 + i)
		when := time.Date(2026, 3, r.day, r.hour, 0, 0, 0, time.UTC)
		set("A"+n, float64(when.Sub(excelEpoch))/float64(24*time.Hour))
		set("C"+n, "0")
		set("D"+n, r.code)
		set("E"+n, r.desc)
		if r.debit != 0 {
			set("F"+n, r.debit)
		}
		if r.credit != 0 {
			set("G"+n, r.credit)
		}
		if !r.omitBalance {
			set("I"+n, r.balance)
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

var bgFixture = []bgRow{
	{day: 1, hour: 9, code: "264", desc: "YAPPY BG A PERSONA DE PRUEBA", debit: -12.5, balance: 87.5},
	{day: 2, hour: 10, code: "300", desc: "BANCA MOVIL TRANSFERENCIA ENTRE CUENTAS", credit: 20, balance: 107.5},
}

func only(t *testing.T, f File) Statement {
	t.Helper()
	if len(f.Statements) != 1 {
		t.Fatalf("got %d statements, want 1", len(f.Statements))
	}
	return f.Statements[0]
}

func TestBGAccountKeepsTheBanksBalances(t *testing.T) {
	file, err := Parse("movimientos.xlsx", bgAccountFile(t, bgFixture))
	if err != nil {
		t.Fatal(err)
	}
	st := only(t, file)

	// The old import ended this fixture at 20.00: it invented an opening just
	// large enough to stay non-negative. The bank says 100.00 then 107.50.
	if st.Opening == nil || *st.Opening != 10000 {
		t.Errorf("Opening = %v, want 100.00", st.Opening)
	}
	if st.Closing == nil || *st.Closing != 10750 {
		t.Errorf("Closing = %v, want 107.50", st.Closing)
	}
	if st.ChainBreak != nil || len(st.Warnings) != 0 {
		t.Errorf("ChainBreak = %+v, Warnings = %q; want none", st.ChainBreak, st.Warnings)
	}
	if st.Identity.ExternalNumber != "00-00-00-000000-0" || st.Identity.Class != ClassAsset {
		t.Errorf("Identity = %+v", st.Identity)
	}
	if got := st.Lines[0]; got.Amount != -1250 || got.Kind != KindExpense || got.BookedOn.String() != "2026-03-01" {
		t.Errorf("first line = %+v", got)
	}
	if st.PeriodStart.String() != "2026-03-01" || st.PeriodEnd.String() != "2026-03-02" {
		t.Errorf("period = %s..%s", st.PeriodStart, st.PeriodEnd)
	}
}

func TestBGAccountReadsTimesAsPanama(t *testing.T) {
	st := only(t, must(Parse("m.xlsx", bgAccountFile(t, bgFixture))))
	at := *st.Lines[0].BookedAt
	if at.Hour() != 9 || at.Format("-07:00") != "-05:00" {
		t.Errorf("BookedAt = %v, want 09:00 at -05:00", at)
	}
}

func TestBGAccountOrdersANewestFirstExport(t *testing.T) {
	reversed := []bgRow{bgFixture[1], bgFixture[0]}
	st := only(t, must(Parse("m.xlsx", bgAccountFile(t, reversed))))
	if st.Lines[0].BookedOn.String() != "2026-03-01" {
		t.Errorf("first line is %s, want the oldest", st.Lines[0].BookedOn)
	}
	if *st.Opening != 10000 || st.ChainBreak != nil {
		t.Errorf("Opening = %v, ChainBreak = %+v", *st.Opening, st.ChainBreak)
	}
}

func TestBGAccountPointsAtTheFirstLineThatDoesNotAddUp(t *testing.T) {
	rows := append([]bgRow(nil), bgFixture...)
	rows = append(rows, bgRow{day: 3, hour: 11, code: "264", desc: "PAGO", debit: -10, balance: 90})
	st := only(t, must(Parse("m.xlsx", bgAccountFile(t, rows))))
	if st.ChainBreak == nil || st.ChainBreak.LineNo != 11 || st.ChainBreak.Bank != 9000 || st.ChainBreak.Computed != 9750 {
		t.Fatalf("ChainBreak = %+v, want line 11, bank 90.00, computed 97.50", st.ChainBreak)
	}
	if len(st.Warnings) != 1 || !strings.Contains(st.Warnings[0], "línea 11") {
		t.Errorf("Warnings = %q", st.Warnings)
	}
}

func TestBGAccountRefusesALineWithDebitAndCredit(t *testing.T) {
	_, err := Parse("m.xlsx", bgAccountFile(t, []bgRow{{day: 1, desc: "X", debit: -1, credit: 1, balance: 0}}))
	if err == nil || !strings.Contains(err.Error(), "débito") {
		t.Errorf("err = %v, want a débito/crédito error", err)
	}
}

// --- BAC (.csv, Latin-1) ------------------------------------------------------

func latin1Bytes(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		out = append(out, byte(r))
	}
	return out
}

func bacFile(saldoInicial, libros string, detail ...string) []byte {
	lines := []string{
		"Número de Clientes, Nombre, Producto, Moneda, Saldo Inicial, Saldo en Libros, Retenidos y Diferidos, Saldo Disponible, Fecha, STBGAV, STBUNC, Mensaje1",
		"900000001, PERSONA DE PRUEBA, 100000001, USD, " + saldoInicial + ", " + libros + ", 0.00, " + libros + ", 31/08/2026, 0.00, 0.00, ",
		"",
		"Detalle de Estado Bancario",
		"Fecha de Transacción, Referencia de Transacción, Código de Transacción, Descripción de Transacción, Débito de Transacción, Crédito de Transacción, Balance de Transacción",
	}
	lines = append(lines, detail...)
	lines = append(lines, "", "Resumen de Estado Bancario",
		"Código Transacción Totales, Cantidad Débitos Totales, Montos Débitos Totales, Cantidad Créditos Totales, Montos Créditos Totales",
		"Total, 1, 25.00, 2, 275.75")
	return latin1Bytes(strings.Join(lines, "\r\n"))
}

var bacDetail = []string{
	"01/06/2026, 1000001, OP, APERTURA DE SALDOS, 0.00, 100.00, 100.00",
	"15/06/2026, 1000002, 4A, TRANSFERENCIA DE PRUEBA, 25.00, 0.00, 75.00",
	"20/06/2026, 1000003, 4C, DEPOSITO DE PRUEBA, 0.00, 175.75, 250.75",
}

func TestBACFoldsTheOpeningRowAndChecksEveryTotal(t *testing.T) {
	st := only(t, must(Parse("estado.csv", bacFile("0.00", "250.75", bacDetail...))))
	if len(st.Lines) != 2 {
		t.Fatalf("got %d lines, want 2: APERTURA DE SALDOS is the opening, not a movement", len(st.Lines))
	}
	if *st.Opening != 10000 || *st.Closing != 25075 {
		t.Errorf("Opening = %v, Closing = %v; want 100.00 and 250.75", *st.Opening, *st.Closing)
	}
	if len(st.Warnings) != 0 || st.ChainBreak != nil {
		t.Errorf("Warnings = %q, ChainBreak = %+v", st.Warnings, st.ChainBreak)
	}
	// Identity is the product, not the client: two products of one client
	// are two accounts.
	if st.Identity.ExternalNumber != "100000001" {
		t.Errorf("ExternalNumber = %q, want the Producto", st.Identity.ExternalNumber)
	}
	if st.Lines[0].BookedOn.String() != "2026-06-15" || st.PeriodEnd.String() != "2026-08-31" {
		t.Errorf("first line %s, period end %s", st.Lines[0].BookedOn, st.PeriodEnd)
	}
	if st.Available == nil || *st.Available != 25075 {
		t.Errorf("Available = %v", st.Available)
	}
}

func TestBACUsesTheRealOpeningBalance(t *testing.T) {
	// A normal monthly statement: the account already held 500.00. The old
	// import showed 250.75 for it, the bank 750.75.
	detail := []string{
		"15/06/2026, 1000002, 4A, TRANSFERENCIA DE PRUEBA, 25.00, 0.00, 475.00",
		"20/06/2026, 1000003, 4C, DEPOSITO DE PRUEBA, 0.00, 275.75, 750.75",
	}
	st := only(t, must(Parse("estado.csv", bacFile("500.00", "750.75", detail...))))
	if *st.Opening != 50000 || *st.Closing != 75075 || st.ChainBreak != nil {
		t.Errorf("Opening = %v, Closing = %v, ChainBreak = %+v", *st.Opening, *st.Closing, st.ChainBreak)
	}
}

func TestBACReadsQuotedThousandsAndRefusesUnquotedOnes(t *testing.T) {
	quoted := []string{`20/06/2026, 1000003, 4C, "DEPOSITO, GRANDE", 0.00, "1,175.75", "1,175.75"`}
	st := only(t, must(Parse("estado.csv", bacFile("0.00", "1,175.75", quoted...))))
	if st.Lines[0].Amount != 117575 || st.Lines[0].Description != "DEPOSITO, GRANDE" {
		t.Errorf("line = %+v", st.Lines[0])
	}

	unquoted := []string{"20/06/2026, 1000003, 4C, DEPOSITO, 0.00, 1,175.75, 1,175.75"}
	if _, err := Parse("estado.csv", bacFile("0.00", "1175.75", unquoted...)); err == nil ||
		!strings.Contains(err.Error(), "fields") {
		t.Errorf("err = %v, want a field-count error instead of a silent +1.00", err)
	}
}

func TestBACWarnsWhenTheBalancesDoNotAddUp(t *testing.T) {
	st := only(t, must(Parse("estado.csv", bacFile("0.00", "300.00", bacDetail...))))
	if len(st.Warnings) == 0 || !strings.Contains(st.Warnings[0], "saldo en libros") {
		t.Errorf("Warnings = %q", st.Warnings)
	}
}

// --- Banco General card (.txt) -----------------------------------------------

const cardFile = "Cuenta;Tarjeta;Fecha tran;Fecha proceso;Descripcion;Referencia;Categoria;Cargos (Db);Pagos (Cr)\r\n" +
	"**** 1111;**** 1111;15/01/2026;15/01/2026;SEGURO DE DESGRAVAMEN;REF-SHARED-001;Cargos;-0.5;\r\n" +
	"**** 1111;**** 1111;15/01/2026;15/01/2026;ITBMS CARGO POR SEGURO;REF-SHARED-001;Cargos;-0.05;\r\n" +
	"**** 1111;**** 1111;20/01/2026;20/01/2026;GRACIAS POR SU PAGO - BANCA MOVIL;REF-PAY-001;Pagos;;100.00\r\n" +
	"**** 1111;**** 2222;18/01/2026;19/01/2026;CAFE DE PRUEBA        PANAMA        PA;REF-002;Comida y Bebida;-9.5;\r\n" +
	"**** 1111;**** 2222;19/01/2026;20/01/2026;FARMACIA DE PRUEBA    PANAMA        PA;REF-003;Salud;-4.25;\r\n"

func TestBGCardIsALiabilityWithKindsFromTheBanksCategory(t *testing.T) {
	st := only(t, must(Parse("tarjeta.txt", []byte(cardFile))))
	if st.Identity.Class != ClassLiability || st.Identity.Type != "credit_card" {
		t.Errorf("Identity = %+v", st.Identity)
	}
	if st.Opening != nil || st.Closing != nil {
		t.Error("the card file prints no balance; Opening and Closing must be nil")
	}
	var sum money.Cents
	for _, l := range st.Lines {
		sum += l.Amount
	}
	if sum != 8570 {
		t.Errorf("sum = %s, want 85.70", sum)
	}
	for _, l := range st.Lines {
		switch {
		case l.BankCategory == "Pagos" && l.Kind != KindTransfer:
			t.Errorf("a payment is %s, want transfer — paying the card is not income", l.Kind)
		case l.BankCategory == "Cargos" && l.Kind != KindFee:
			t.Errorf("a bank charge is %s, want fee", l.Kind)
		case l.BankCategory == "Salud" && l.Kind != KindExpense:
			t.Errorf("a purchase is %s, want expense", l.Kind)
		}
	}
	// Balance day is the processing date: the café on the 18th posts on the 19th.
	for _, l := range st.Lines {
		if strings.Contains(l.Description, "CAFE") && l.BalanceOn().String() != "2026-01-19" {
			t.Errorf("BalanceOn = %s, want 2026-01-19", l.BalanceOn())
		}
	}
}

func TestBGCardSplitsOneStatementPerAccount(t *testing.T) {
	two := cardFile + "**** 3333;**** 3333;21/01/2026;21/01/2026;OTRA TARJETA;REF-9;Tiendas;-1.00;\r\n"
	file := must(Parse("tarjeta.txt", []byte(two)))
	if len(file.Statements) != 2 || file.Statements[1].Identity.ExternalNumber != "**** 3333" {
		t.Errorf("got %d statements", len(file.Statements))
	}
}

func TestBGCardRefund(t *testing.T) {
	refund := cardFile + "**** 1111;**** 1111;22/01/2026;22/01/2026;REVERSO FARMACIA;REF-10;Salud;;4.25\r\n"
	st := only(t, must(Parse("tarjeta.txt", []byte(refund))))
	last := st.Lines[len(st.Lines)-1]
	if last.Kind != KindRefund || last.Amount != 425 {
		t.Errorf("refund line = %+v", last)
	}
}

// --- dedup and detection -----------------------------------------------------

func TestDedupKeysAreStableAndTellTwinsApart(t *testing.T) {
	twins := cardFile + "**** 1111;**** 1111;23/01/2026;23/01/2026;CAFE;R;Comida y Bebida;-2.50;\r\n" +
		"**** 1111;**** 1111;23/01/2026;23/01/2026;CAFE;R;Comida y Bebida;-2.50;\r\n"
	a := only(t, must(Parse("t.txt", []byte(twins))))
	b := only(t, must(Parse("t.txt", []byte(twins))))

	keys := map[string]bool{}
	for i, l := range a.Lines {
		k := DedupKey(a.Identity, l)
		if k != DedupKey(b.Identity, b.Lines[i]) {
			t.Fatalf("line %d: the key changed between two parses of the same bytes", l.LineNo)
		}
		if keys[k] {
			t.Fatalf("line %d: two different lines share a key", l.LineNo)
		}
		keys[k] = true
	}
}

func TestParseRejectsAnUnknownFile(t *testing.T) {
	if _, err := Parse("otro.csv", []byte("a,b,c\n1,2,3")); !errors.Is(err, ErrUnrecognised) {
		t.Errorf("err = %v, want ErrUnrecognised", err)
	}
}

func TestAmountText(t *testing.T) {
	for in, want := range map[string]money.Cents{
		"12.50": 1250, "-12.50": -1250, "1,175.75": 117575, "(12.50)": -1250,
		"12.50 CR": 1250, "12.50 DB": -1250, "0.00": 0,
	} {
		got, err := signedFromText(in)
		if err != nil || got != want {
			t.Errorf("signedFromText(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"1,17.5", "12,5", "abc"} {
		if _, err := signedFromText(bad); err == nil {
			t.Errorf("signedFromText(%q) accepted a malformed amount", bad)
		}
	}
	if c, err := centsFromFloat("8.8699999999999992"); err != nil || c != 887 {
		t.Errorf("centsFromFloat(float noise) = %v, %v; want 887", c, err)
	}
	if _, err := centsFromFloat("0.004"); err == nil {
		t.Error("centsFromFloat accepted a sub-cent amount")
	}
}

func must(f File, err error) File {
	if err != nil {
		panic(err)
	}
	return f
}
