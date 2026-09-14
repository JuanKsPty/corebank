package bankimport

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/JuanKsPty/corebank/api/internal/money"
)

// bacHeaderPrefix is the first column of a BAC account statement's opening
// line, checked after decoding from Latin-1 — the file is not valid UTF-8
// (verified: it fails to decode as UTF-8 on the accented client-info line).
const bacHeaderPrefix = "Número de Clientes"

// bacDetailMarker and bacSummaryMarker delimit the three sections of a BAC
// statement: client/balance info, the transaction table this parser reads,
// and a totals-by-code summary that must never be parsed as movements.
const (
	bacDetailMarker  = "Detalle de Estado Bancario"
	bacSummaryMarker = "Resumen de Estado Bancario"
)

var bacDetailColumns = []string{
	"Fecha de Transacción", "Referencia de Transacción", "Código de Transacción",
	"Descripción de Transacción", "Débito de Transacción", "Crédito de Transacción",
}

type bacAccountParser struct{}

func (bacAccountParser) AccountHint(r io.Reader) (AccountHint, bool) {
	text, err := latin1ToUTF8Reader(r)
	if err != nil {
		return AccountHint{}, false
	}
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	if !scanner.Scan() {
		return AccountHint{}, false
	}
	header := splitCSVLine(scanner.Text())
	if !scanner.Scan() {
		return AccountHint{}, false
	}
	values := splitCSVLine(scanner.Text())

	col := indexOfTrimmed(header, "Número de Clientes")
	nameCol := indexOfTrimmed(header, "Nombre")
	if col < 0 || nameCol < 0 || col >= len(values) || nameCol >= len(values) {
		return AccountHint{}, false
	}

	return AccountHint{
		Institution:   InstitutionBAC,
		AccountNumber: strings.TrimSpace(values[col]),
		DisplayName:   strings.TrimSpace(values[nameCol]),
	}, true
}

func (bacAccountParser) Parse(r io.Reader) ([]ParsedRow, error) {
	text, err := latin1ToUTF8Reader(r)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(text, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), bacDetailMarker) {
			start = i + 1 // the header line follows the marker
			break
		}
	}
	if start < 0 || start >= len(lines) {
		return nil, fmt.Errorf("bankimport: bac statement has no %q section", bacDetailMarker)
	}

	header := splitCSVLine(lines[start])
	index := make(map[string]int, len(bacDetailColumns))
	for _, want := range bacDetailColumns {
		i := indexOfTrimmed(header, want)
		if i < 0 {
			return nil, fmt.Errorf("bankimport: bac statement is missing expected column %q", want)
		}
		index[want] = i
	}

	var out []ParsedRow
	for lineNo, line := range lines[start+1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, bacSummaryMarker) {
			break // the rest of the file is the ignored totals section
		}

		fields := splitCSVLine(line)
		cell := func(name string) string {
			i := index[name]
			if i < len(fields) {
				return strings.TrimSpace(fields[i])
			}
			return ""
		}

		dateText := cell("Fecha de Transacción")
		occurredAt, err := time.Parse("02/01/2006", dateText)
		if err != nil {
			return nil, fmt.Errorf("bankimport: bac row %d: fecha %q: %w", start+lineNo+2, dateText, err)
		}

		amount, err := bacSignedAmount(cell("Débito de Transacción"), cell("Crédito de Transacción"))
		if err != nil {
			return nil, fmt.Errorf("bankimport: bac row %d: %w", start+lineNo+2, err)
		}

		out = append(out, ParsedRow{
			OccurredAt:  occurredAt,
			Amount:      amount,
			Description: cell("Descripción de Transacción"),
			ExternalRef: cell("Referencia de Transacción"),
			Raw: map[string]string{
				"Fecha de Transacción":       dateText,
				"Referencia de Transacción":  cell("Referencia de Transacción"),
				"Código de Transacción":      cell("Código de Transacción"),
				"Descripción de Transacción": cell("Descripción de Transacción"),
				"Débito de Transacción":      cell("Débito de Transacción"),
				"Crédito de Transacción":     cell("Crédito de Transacción"),
			},
		})
	}
	return out, nil
}

// bacSignedAmount combines BAC's separate, always-present debit/credit
// columns (a zero in whichever did not apply, unlike Banco General's blank
// field) into one signed amount.
func bacSignedAmount(debit, credit string) (money.Cents, error) {
	d, err := money.Parse(zeroIfEmpty(debit))
	if err != nil {
		return 0, fmt.Errorf("débito %q: %w", debit, err)
	}
	c, err := money.Parse(zeroIfEmpty(credit))
	if err != nil {
		return 0, fmt.Errorf("crédito %q: %w", credit, err)
	}
	return c.Sub(d), nil
}

func zeroIfEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "0"
	}
	return s
}

// splitCSVLine parses one comma-separated line through encoding/csv rather
// than strings.Split, so a description that happens to contain a quoted
// comma is not split apart.
func splitCSVLine(line string) []string {
	cr := csv.NewReader(strings.NewReader(line))
	cr.FieldsPerRecord = -1
	fields, err := cr.Read()
	if err != nil {
		// A line encoding/csv cannot parse (an unterminated quote, most
		// likely) falls back to a plain split rather than aborting the
		// whole statement over one malformed line.
		return strings.Split(line, ",")
	}
	return fields
}

func indexOfTrimmed(fields []string, want string) int {
	for i, f := range fields {
		if strings.TrimSpace(f) == want {
			return i
		}
	}
	return -1
}

// latin1ToUTF8Reader decodes an ISO-8859-1 stream into a UTF-8 string.
//
// Latin-1 maps every byte 0x00-0xFF onto the identical Unicode code point,
// so the conversion is exactly "treat each byte as a rune" — no table, and
// no dependency on golang.org/x/text/encoding for a codec this simple.
func latin1ToUTF8Reader(r io.Reader) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("bankimport: reading bac statement: %w", err)
	}
	runes := make([]rune, len(data))
	for i, b := range data {
		runes[i] = rune(b)
	}
	return string(runes), nil
}
