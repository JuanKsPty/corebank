package bankimport

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/JuanKsPty/corebank/api/internal/money"
)

// bgCardHeader is the exact header line of a Banco General credit card
// statement export, semicolon-delimited, UTF-8. Used both to detect the
// format and to locate each column by name rather than by position, so a
// column BG reorders someday does not silently misparse.
var bgCardColumns = []string{
	"Cuenta", "Tarjeta", "Fecha tran", "Fecha proceso", "Descripcion",
	"Referencia", "Categoria", "Cargos (Db)", "Pagos (Cr)",
}

// bgCardHeaderSignature is what detect.go sniffs for: no other supported
// format has a semicolon-delimited header naming both "Tarjeta" and
// "Cargos (Db)".
const bgCardHeaderSignature = "Tarjeta;Fecha tran;Fecha proceso;Descripcion;Referencia;Categoria;Cargos (Db);Pagos (Cr)"

// bgCardParser reads a Banco General credit card statement.
//
// One file mixes several physical cards under one primary account (e.g.
// account **** 9970 with sub-cards **** 9988 and **** 9996) — verified
// against a real export. This is modelled as a single external_account per
// Cuenta, with the sub-card kept as metadata rather than fragmenting one
// credit card bill into several accounts.
type bgCardParser struct{}

func (bgCardParser) AccountHint(r io.Reader) (AccountHint, bool) {
	rows, err := readBGCardRows(r)
	if err != nil || len(rows) == 0 {
		return AccountHint{}, false
	}
	account := rows[0]["Cuenta"]
	if account == "" {
		return AccountHint{}, false
	}
	return AccountHint{
		Institution:   InstitutionBancoGeneral,
		AccountNumber: account,
		DisplayName:   "Tarjeta " + account,
	}, true
}

func (bgCardParser) Parse(r io.Reader) ([]ParsedRow, error) {
	rows, err := readBGCardRows(r)
	if err != nil {
		return nil, err
	}

	out := make([]ParsedRow, 0, len(rows))
	for i, raw := range rows {
		occurredAt, err := time.Parse("02/01/2006", raw["Fecha tran"])
		if err != nil {
			return nil, fmt.Errorf("bankimport: bg card row %d: fecha tran %q: %w", i+1, raw["Fecha tran"], err)
		}

		amountText := raw["Cargos (Db)"]
		if amountText == "" {
			amountText = raw["Pagos (Cr)"]
		}
		amount, err := money.Parse(amountText)
		if err != nil {
			return nil, fmt.Errorf("bankimport: bg card row %d: amount %q: %w", i+1, amountText, err)
		}

		description := raw["Descripcion"]
		if card := raw["Tarjeta"]; card != "" && card != raw["Cuenta"] {
			description = "[Tarjeta " + card + "] " + description
		}

		out = append(out, ParsedRow{
			OccurredAt:  occurredAt,
			Amount:      amount,
			Description: description,
			ExternalRef: raw["Referencia"],
			RawCategory: raw["Categoria"],
			Raw:         raw,
		})
	}
	return out, nil
}

// readBGCardRows parses the semicolon-delimited file into maps keyed by
// column name, tolerating the column order BG happens to use rather than
// assuming positions.
func readBGCardRows(r io.Reader) ([]map[string]string, error) {
	cr := csv.NewReader(r)
	cr.Comma = ';'
	cr.FieldsPerRecord = -1
	// A merchant name legitimately containing a stray quote character must
	// not abort the whole import over one unparseable field.
	cr.LazyQuotes = true

	records, err := cr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("bankimport: reading bg card csv: %w", err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("bankimport: bg card file is empty")
	}

	header := records[0]
	index := make(map[string]int, len(header))
	for i, name := range header {
		index[strings.TrimSpace(name)] = i
	}
	for _, want := range bgCardColumns {
		if _, ok := index[want]; !ok {
			return nil, fmt.Errorf("bankimport: bg card file is missing expected column %q", want)
		}
	}

	rows := make([]map[string]string, 0, len(records)-1)
	for _, record := range records[1:] {
		row := make(map[string]string, len(bgCardColumns))
		for _, name := range bgCardColumns {
			i := index[name]
			if i < len(record) {
				row[name] = strings.TrimSpace(record[i])
			}
		}
		// A wholly blank trailing line reads as a one-field record of "";
		// skip it rather than reporting a movement with no date.
		if row["Cuenta"] == "" && row["Descripcion"] == "" {
			continue
		}
		rows = append(rows, row)
	}
	return rows, nil
}
