package bankimport

import (
	"strings"
	"testing"
)

// bgCardFixture mirrors the real format's structure — main card plus a
// sub-card, a shared-reference fee pair, and a payment row — with invented
// merchants and numbers rather than real statement data.
const bgCardFixture = "Cuenta;Tarjeta;Fecha tran;Fecha proceso;Descripcion;Referencia;Categoria;Cargos (Db);Pagos (Cr)\r\n" +
	"**** 1111;**** 1111;15/01/2026;15/01/2026;SEGURO DE DESGRAVAMEN;REF-SHARED-001;Cargos;-0.5;\r\n" +
	"**** 1111;**** 1111;15/01/2026;15/01/2026;ITBMS CARGO POR SEGURO;REF-SHARED-001;Cargos;-0.05;\r\n" +
	"**** 1111;**** 1111;20/01/2026;20/01/2026;GRACIAS POR SU PAGO - BANCA MOVIL;REF-PAY-001;Pagos;;100.00\r\n" +
	"**** 1111;**** 2222;18/01/2026;19/01/2026;CAFE DE PRUEBA        PANAMA        PA;REF-002;Comida y Bebida;-9.5;\r\n" +
	"**** 1111;**** 2222;19/01/2026;20/01/2026;FARMACIA DE PRUEBA    PANAMA        PA;REF-003;Salud;-4.25;\r\n"

func TestBGCardAccountHint(t *testing.T) {
	hint, ok := bgCardParser{}.AccountHint(strings.NewReader(bgCardFixture))
	if !ok {
		t.Fatal("AccountHint() ok = false")
	}
	if hint.Institution != InstitutionBancoGeneral {
		t.Errorf("Institution = %q, want %q", hint.Institution, InstitutionBancoGeneral)
	}
	if hint.AccountNumber != "**** 1111" {
		t.Errorf("AccountNumber = %q, want %q", hint.AccountNumber, "**** 1111")
	}
}

func TestBGCardParse(t *testing.T) {
	rows, err := bgCardParser{}.Parse(strings.NewReader(bgCardFixture))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("got %d rows, want 5", len(rows))
	}

	insurance := rows[0]
	if insurance.Amount != -50 || insurance.ExternalRef != "REF-SHARED-001" || insurance.RawCategory != "Cargos" {
		t.Errorf("insurance row = %+v", insurance)
	}

	tax := rows[1]
	if tax.Amount != -5 || tax.ExternalRef != "REF-SHARED-001" {
		t.Errorf("tax row = %+v", tax)
	}
	if insurance.Description == tax.Description {
		t.Error("the two fee rows sharing a reference must still have distinct descriptions")
	}

	payment := rows[2]
	if payment.Amount != 10000 {
		t.Errorf("payment.Amount = %d, want 10000 (a Pagos row must be positive)", payment.Amount)
	}

	subCard := rows[3]
	if !strings.Contains(subCard.Description, "2222") {
		t.Errorf("a sub-card row's description should mention its own card number: %q", subCard.Description)
	}
	if subCard.RawCategory != "Comida y Bebida" {
		t.Errorf("subCard.RawCategory = %q, want %q", subCard.RawCategory, "Comida y Bebida")
	}
}

func TestBGCardDetect(t *testing.T) {
	parser, err := Detect("Banco General Credit Card Statement.txt", []byte(bgCardFixture))
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if _, ok := parser.(bgCardParser); !ok {
		t.Errorf("Detect() returned %T, want bgCardParser", parser)
	}
}
