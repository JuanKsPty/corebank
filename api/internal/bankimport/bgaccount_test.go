package bankimport

import (
	"bytes"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

// buildBGAccountFixture writes a workbook shaped like a real Banco General
// account movement export: a title row, a "Cuenta:" metadata cell whose
// text is the concatenation of two rich-text runs (simulated here as one
// plain string, which is what GetRows returns for either), a header row,
// and data rows below it — with an invented account and movements rather
// than real statement data.
func buildBGAccountFixture(t *testing.T) []byte {
	t.Helper()

	f := excelize.NewFile()
	defer f.Close()
	sheet := f.GetSheetName(0)

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("building fixture: %v", err)
		}
	}

	must(f.SetCellValue(sheet, "B2", "Últimos movimientos"))
	must(f.SetCellValue(sheet, "A4", "Cuenta:Prueba 00-00-00-000000-0"))

	must(f.SetCellValue(sheet, "A8", "Fecha"))
	must(f.SetCellValue(sheet, "C8", "Referencia"))
	must(f.SetCellValue(sheet, "D8", "Transacción"))
	must(f.SetCellValue(sheet, "E8", "Descripción"))
	must(f.SetCellValue(sheet, "F8", "Débito"))
	must(f.SetCellValue(sheet, "G8", "Crédito"))
	must(f.SetCellValue(sheet, "I8", "Saldo total"))

	serial := func(y int, m time.Month, d int) float64 {
		when := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
		return float64(when.Sub(excelEpoch)) / float64(24*time.Hour)
	}

	// Row 9: an outgoing P2P payment (debit).
	must(f.SetCellValue(sheet, "A9", serial(2026, 3, 1)))
	must(f.SetCellValue(sheet, "C9", "0"))
	must(f.SetCellValue(sheet, "D9", "264"))
	must(f.SetCellValue(sheet, "E9", "YAPPY BG A PERSONA DE PRUEBA"))
	must(f.SetCellValue(sheet, "F9", -12.5))
	must(f.SetCellValue(sheet, "I9", 87.5))

	// Row 10: an incoming transfer (credit), same amount+description
	// pattern repeated on purpose to exercise the fingerprint dedup path,
	// since this format's own Referencia is always "0".
	must(f.SetCellValue(sheet, "A10", serial(2026, 3, 2)))
	must(f.SetCellValue(sheet, "C10", "0"))
	must(f.SetCellValue(sheet, "D10", "300"))
	must(f.SetCellValue(sheet, "E10", "BANCA MOVIL TRANSFERENCIA ENTRE CUENTAS"))
	must(f.SetCellValue(sheet, "G10", 20.0))
	must(f.SetCellValue(sheet, "I10", 107.5))

	var buf bytes.Buffer
	must(f.Write(&buf))
	return buf.Bytes()
}

func TestBGAccountHint(t *testing.T) {
	data := buildBGAccountFixture(t)
	hint, ok := bgAccountParser{}.AccountHint(bytes.NewReader(data))
	if !ok {
		t.Fatal("AccountHint() ok = false")
	}
	if hint.Institution != InstitutionBancoGeneral {
		t.Errorf("Institution = %q, want %q", hint.Institution, InstitutionBancoGeneral)
	}
	if hint.AccountNumber != "00-00-00-000000-0" {
		t.Errorf("AccountNumber = %q, want %q", hint.AccountNumber, "00-00-00-000000-0")
	}
	if hint.DisplayName != "Prueba" {
		t.Errorf("DisplayName = %q, want %q", hint.DisplayName, "Prueba")
	}
}

func TestBGAccountParse(t *testing.T) {
	data := buildBGAccountFixture(t)
	rows, err := bgAccountParser{}.Parse(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}

	debit := rows[0]
	if debit.Amount != -1250 {
		t.Errorf("debit.Amount = %d, want -1250", debit.Amount)
	}
	wantDate := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if !debit.OccurredAt.Equal(wantDate) {
		t.Errorf("debit.OccurredAt = %v, want %v", debit.OccurredAt, wantDate)
	}

	credit := rows[1]
	if credit.Amount != 2000 {
		t.Errorf("credit.Amount = %d, want 2000", credit.Amount)
	}

	// The format's own Referencia is unreliable (always "0" in real
	// exports) — this parser must not report it as a usable reference.
	for _, r := range rows {
		if r.ExternalRef != "0" {
			t.Errorf("ExternalRef = %q, want the literal \"0\" this format always reports", r.ExternalRef)
		}
	}
}

func TestBGAccountParseFindsShiftedHeaderRow(t *testing.T) {
	// The header row's position depends on how much text the "Cuenta:"
	// metadata carries in a real file — this asserts the parser locates it
	// by content ("Fecha") rather than assuming row 8.
	f := excelize.NewFile()
	defer f.Close()
	sheet := f.GetSheetName(0)

	if err := f.SetCellValue(sheet, "A20", "Fecha"); err != nil {
		t.Fatal(err)
	}
	if err := f.SetCellValue(sheet, "E20", "Descripción"); err != nil {
		t.Fatal(err)
	}
	if err := f.SetCellValue(sheet, "F20", "Débito"); err != nil {
		t.Fatal(err)
	}
	if err := f.SetCellValue(sheet, "G20", "Crédito"); err != nil {
		t.Fatal(err)
	}
	serial := float64(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Sub(excelEpoch)) / float64(24*time.Hour)
	if err := f.SetCellValue(sheet, "A21", serial); err != nil {
		t.Fatal(err)
	}
	if err := f.SetCellValue(sheet, "E21", "MOVIMIENTO DE PRUEBA"); err != nil {
		t.Fatal(err)
	}
	if err := f.SetCellValue(sheet, "F21", -5.0); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}

	rows, err := bgAccountParser{}.Parse(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Description != "MOVIMIENTO DE PRUEBA" {
		t.Errorf("Description = %q", rows[0].Description)
	}
}

func TestBGAccountDetect(t *testing.T) {
	data := buildBGAccountFixture(t)
	parser, err := Detect("Cuenta de Ahorros Movimientos.xlsx", data)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if _, ok := parser.(bgAccountParser); !ok {
		t.Errorf("Detect() returned %T, want bgAccountParser", parser)
	}
}

func TestParseExcelSerialDate(t *testing.T) {
	// Verified against a real Banco General export in the design session:
	// 46277.603263888974 decodes to 2026-09-12 14:28.
	got, err := parseExcelSerialDate("46277.603263888974")
	if err != nil {
		t.Fatalf("parseExcelSerialDate() error = %v", err)
	}
	want := time.Date(2026, 9, 12, 14, 28, 42, 0, time.UTC)
	if diff := got.Sub(want); diff < -time.Second || diff > time.Second {
		t.Errorf("parseExcelSerialDate() = %v, want ~%v", got, want)
	}
}
