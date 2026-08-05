package transactions

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/ledger"
	"github.com/JuanKsPty/corebank/api/internal/money"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

func TestCSVRow(t *testing.T) {
	id := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	at := time.Date(2026, 8, 4, 17, 30, 0, 0, time.FixedZone("PA", -5*3600))

	tx := store.Transaction{
		ID:          id,
		Kind:        ledger.MovementWithdrawal,
		Status:      store.StatusCompleted,
		Amount:      money.Cents(123456),
		Currency:    money.CurrencyUSD,
		FromAccount: "4001-2889-2517-1327",
		ToAccount:   "EXTERNAL",
		Description: "Café y pan",
		OccurredAt:  at,
	}

	row := csvRow(tx)

	if len(row) != len(csvHeader) {
		t.Fatalf("the row has %d fields and the header has %d", len(row), len(csvHeader))
	}

	// The date is normalised to UTC, so a file does not carry the server's timezone
	// into somebody's spreadsheet. 17:30 at UTC-5 is 22:30Z.
	if want := "2026-08-04T22:30:00Z"; row[0] != want {
		t.Errorf("fecha = %q, want %q", row[0], want)
	}
	if want := "Retiro"; row[1] != want {
		t.Errorf("tipo = %q, want %q", row[1], want)
	}
	if want := "Completado"; row[2] != want {
		t.Errorf("estado = %q, want %q", row[2], want)
	}
	// A period, two places, no separators — the form that round-trips back through
	// money.Parse. A localised "1.234,56" would read nicer and stop being data.
	if want := "1234.56"; row[3] != want {
		t.Errorf("monto = %q, want %q", row[3], want)
	}
	if row[7] != "Café y pan" {
		t.Errorf("descripcion = %q, want the accents intact", row[7])
	}
	if row[8] != id.String() {
		t.Errorf("id = %q, want %q", row[8], id.String())
	}
}

// An unmapped value is shown rather than blanked: a row with an unfamiliar status is
// still a row about somebody's money, and an empty cell hides that it exists.
func TestSpanishFallsThrough(t *testing.T) {
	if got := spanish(statusNames, "reversed"); got != "reversed" {
		t.Errorf("spanish() = %q, want the original value back", got)
	}
	if got := spanish(movementNames, "deposit"); got != "Depósito" {
		t.Errorf("spanish() = %q, want the mapped name", got)
	}
}

// Whatever a customer typed has to survive being written and read back. A description
// holding a comma, a quote or a newline must not shift the columns of the row it is
// in — or of any row after it.
func TestCSVSurvivesAwkwardDescriptions(t *testing.T) {
	awkward := []string{
		`Pago, con coma`,
		`Dijo "gracias"`,
		"Dos\nlíneas",
		`Punto y coma; y tabulación	dentro`,
	}

	var buf bytes.Buffer
	out := csv.NewWriter(&buf)
	if err := out.Write(csvHeader); err != nil {
		t.Fatalf("writing the header: %v", err)
	}
	for _, description := range awkward {
		err := out.Write(csvRow(store.Transaction{
			ID:          uuid.New(),
			Kind:        ledger.MovementDeposit,
			Status:      store.StatusCompleted,
			Amount:      money.Cents(100),
			Currency:    money.CurrencyUSD,
			Description: description,
			OccurredAt:  time.Now(),
		}))
		if err != nil {
			t.Fatalf("writing a row: %v", err)
		}
	}
	out.Flush()
	if err := out.Error(); err != nil {
		t.Fatalf("flushing: %v", err)
	}

	records, err := csv.NewReader(strings.NewReader(buf.String())).ReadAll()
	if err != nil {
		t.Fatalf("the file this produced cannot be read back: %v", err)
	}
	if len(records) != len(awkward)+1 {
		t.Fatalf("read back %d records, want %d — a description broke the row structure",
			len(records), len(awkward)+1)
	}
	for i, description := range awkward {
		row := records[i+1]
		if len(row) != len(csvHeader) {
			t.Errorf("row %d has %d fields, want %d", i, len(row), len(csvHeader))
			continue
		}
		if row[7] != description {
			t.Errorf("descripcion round-tripped as %q, want %q", row[7], description)
		}
	}
}
