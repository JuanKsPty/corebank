package bankimport

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// utf8ToLatin1 is the inverse of latin1ToUTF8Reader, for building test
// fixtures that need to round-trip through the real encoding this format
// uses. Every rune here is expected to be in the Latin-1 range; a real BAC
// statement never contains one that is not.
func utf8ToLatin1(t *testing.T, s string) []byte {
	t.Helper()
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r > 0xFF {
			t.Fatalf("utf8ToLatin1: rune %q is outside Latin-1", r)
		}
		out = append(out, byte(r))
	}
	return out
}

// bacFixture mirrors the real format's three sections — client/balance
// header, the detail table this parser reads, and a summary table that must
// be ignored — with invented client data and movements rather than real
// statement data.
func bacFixture() string {
	lines := []string{
		"Número de Clientes, Nombre, Producto, Moneda, Saldo Inicial, Saldo en Libros, Retenidos y Diferidos, Saldo Disponible, Fecha, STBGAV, STBUNC, Mensaje1, Mensaje2, Mensaje3, Mensaje4, Mensaje5, Mensaje6",
		"900000001, PERSONA DE PRUEBA, 100000001, USD, 0.00, 250.75, 0.00, 250.75, 31/08/2026, 0.00, 0.00, , , , , , ",
		"",
		"Detalle de Estado Bancario",
		"Fecha de Transacción, Referencia de Transacción, Código de Transacción, Descripción de Transacción, Débito de Transacción, Crédito de Transacción, Balance de Transacción",
		"01/06/2026, 1000001, OP, APERTURA DE SALDOS, 0.00, 100.00, 100.00",
		"15/06/2026, 1000002, 4A, TRANSFERENCIA DE PRUEBA, 25.00, 0.00, 75.00",
		"20/06/2026, 1000003, 4C, DEPOSITO DE PRUEBA, 0.00, 175.75, 250.75",
		"",
		"Resumen de Estado Bancario",
		"Código Transacción Totales, Cantidad Débitos Totales, Montos Débitos Totales, Cantidad Créditos Totales, Montos Créditos Totales",
		"OP, 0, 0.00, 1, 100.00",
		"4A, 1, 25.00, 0, 0.00",
		"4C, 0, 0.00, 1, 175.75",
		"Total, 1, 25.00, 2, 275.75",
	}
	return strings.Join(lines, "\r\n")
}

func TestBACAccountHint(t *testing.T) {
	data := utf8ToLatin1(t, bacFixture())
	hint, ok := bacAccountParser{}.AccountHint(bytes.NewReader(data))
	if !ok {
		t.Fatal("AccountHint() ok = false")
	}
	if hint.Institution != InstitutionBAC {
		t.Errorf("Institution = %q, want %q", hint.Institution, InstitutionBAC)
	}
	if hint.AccountNumber != "900000001" {
		t.Errorf("AccountNumber = %q, want %q", hint.AccountNumber, "900000001")
	}
	if hint.DisplayName != "PERSONA DE PRUEBA" {
		t.Errorf("DisplayName = %q, want %q", hint.DisplayName, "PERSONA DE PRUEBA")
	}
}

func TestBACAccountParse(t *testing.T) {
	data := utf8ToLatin1(t, bacFixture())
	rows, err := bacAccountParser{}.Parse(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3 (the Resumen section must not be parsed as movements)", len(rows))
	}

	opening := rows[0]
	if opening.Amount != 10000 || opening.ExternalRef != "1000001" {
		t.Errorf("opening row = %+v", opening)
	}
	wantDate := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if !opening.OccurredAt.Equal(wantDate) {
		t.Errorf("opening.OccurredAt = %v, want %v", opening.OccurredAt, wantDate)
	}

	debit := rows[1]
	if debit.Amount != -2500 {
		t.Errorf("debit.Amount = %d, want -2500", debit.Amount)
	}

	credit := rows[2]
	if credit.Amount != 17575 {
		t.Errorf("credit.Amount = %d, want 17575", credit.Amount)
	}
}

func TestBACAccountDetect(t *testing.T) {
	data := utf8ToLatin1(t, bacFixture())
	parser, err := Detect("Monthly Transactions.csv", data)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if _, ok := parser.(bacAccountParser); !ok {
		t.Errorf("Detect() returned %T, want bacAccountParser", parser)
	}
}
