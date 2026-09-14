package bankimport

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/JuanKsPty/corebank/api/internal/money"
)

// bgAccountHeaderCell is the exact text of the header cell every Banco
// General account (Ahorros/Inversión) movement export uses to mark where its
// column headers start. The row it sits in is not fixed — it shifts with how
// much text the "Cuenta:" metadata line above it carries — so this is looked
// up by content, not by row number.
const bgAccountHeaderCell = "Fecha"

// bgAccountColumns are the columns this parser reads by name. Two of the
// real header's cells ("" between Referencia's neighbours) are blank
// spacers BG's own export leaves for layout and are never populated.
var bgAccountColumns = []string{"Fecha", "Referencia", "Transacción", "Descripción", "Débito", "Crédito"}

// excelEpoch is Excel's day zero. Verified against a real export: serial
// 46277.603263888974 decodes to 2026-09-12 14:28, which is the timestamp
// that file's own filename ("...Sept 13 2026") implies for its most recent
// movement.
var excelEpoch = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)

type bgAccountParser struct{}

func (bgAccountParser) AccountHint(r io.Reader) (AccountHint, bool) {
	f, err := excelize.OpenReader(r)
	if err != nil {
		return AccountHint{}, false
	}
	defer f.Close()

	sheet := firstSheet(f)
	rows, err := f.GetRows(sheet)
	if err != nil {
		return AccountHint{}, false
	}

	for _, row := range rows {
		for _, cell := range row {
			alias, number, ok := parseBGAccountLabel(cell)
			if ok {
				return AccountHint{
					Institution:   InstitutionBancoGeneral,
					AccountNumber: number,
					DisplayName:   alias,
				}, true
			}
		}
	}
	return AccountHint{}, false
}

// parseBGAccountLabel splits a "Cuenta:<alias> <número>" cell — the two rich
// text runs BG's export concatenates into one string — into its two halves.
// The account number is always the last whitespace-separated token; it is
// the one part of the label with no spaces of its own, dashes included
// ("04-98-97-958835-1"), which is what makes taking the last token safe even
// if BG's alias were ever more than one word.
func parseBGAccountLabel(cell string) (alias, number string, ok bool) {
	const prefix = "Cuenta:"
	if !strings.HasPrefix(cell, prefix) {
		return "", "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(cell, prefix))
	fields := strings.Fields(rest)
	if len(fields) < 2 {
		return "", "", false
	}
	number = fields[len(fields)-1]
	alias = strings.Join(fields[:len(fields)-1], " ")
	if alias == "" || number == "" {
		return "", "", false
	}
	return alias, number, true
}

func (bgAccountParser) Parse(r io.Reader) ([]ParsedRow, error) {
	f, err := excelize.OpenReader(r, excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, fmt.Errorf("bankimport: opening bg account xlsx: %w", err)
	}
	defer f.Close()

	sheet := firstSheet(f)
	rows, err := f.GetRows(sheet, excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, fmt.Errorf("bankimport: reading bg account xlsx: %w", err)
	}

	headerAt := -1
	var index map[string]int
	for i, row := range rows {
		if idx, ok := bgAccountHeaderIndex(row); ok {
			headerAt, index = i, idx
			break
		}
	}
	if headerAt < 0 {
		return nil, fmt.Errorf("bankimport: bg account xlsx has no %q header row", bgAccountHeaderCell)
	}

	out := make([]ParsedRow, 0, len(rows)-headerAt-1)
	for i, row := range rows[headerAt+1:] {
		cell := func(name string) string {
			if c, ok := index[name]; ok && c < len(row) {
				return strings.TrimSpace(row[c])
			}
			return ""
		}

		dateCell := cell("Fecha")
		if dateCell == "" {
			continue // a trailing blank row
		}
		occurredAt, err := parseExcelSerialDate(dateCell)
		if err != nil {
			return nil, fmt.Errorf("bankimport: bg account row %d: fecha %q: %w", headerAt+i+2, dateCell, err)
		}

		amountText := cell("Débito")
		if amountText == "" {
			amountText = cell("Crédito")
		}
		amount, err := money.Parse(amountText)
		if err != nil {
			return nil, fmt.Errorf("bankimport: bg account row %d: amount %q: %w", headerAt+i+2, amountText, err)
		}

		raw := map[string]string{
			"Fecha": dateCell, "Referencia": cell("Referencia"), "Transacción": cell("Transacción"),
			"Descripción": cell("Descripción"), "Débito": cell("Débito"), "Crédito": cell("Crédito"),
		}
		out = append(out, ParsedRow{
			OccurredAt:  occurredAt,
			Amount:      amount,
			Description: cell("Descripción"),
			ExternalRef: cell("Referencia"),
			Raw:         raw,
		})
	}
	return out, nil
}

// bgAccountHeaderIndex reports whether row is the column-header row (it has
// a cell reading exactly "Fecha"), and if so, maps each expected column name
// to its position in that row.
func bgAccountHeaderIndex(row []string) (map[string]int, bool) {
	hasFecha := false
	for _, cell := range row {
		if strings.TrimSpace(cell) == bgAccountHeaderCell {
			hasFecha = true
			break
		}
	}
	if !hasFecha {
		return nil, false
	}

	index := make(map[string]int, len(bgAccountColumns))
	for i, cell := range row {
		name := strings.TrimSpace(cell)
		for _, want := range bgAccountColumns {
			if name == want {
				index[want] = i
			}
		}
	}
	return index, true
}

// parseExcelSerialDate converts Excel's day-since-1899-12-30 serial, as
// RawCellValue hands it back for a date cell, into a time.Time.
func parseExcelSerialDate(raw string) (time.Time, error) {
	serial, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("not a numeric date serial: %w", err)
	}
	days := time.Duration(serial * float64(24*time.Hour))
	return excelEpoch.Add(days), nil
}

func firstSheet(f *excelize.File) string {
	list := f.GetSheetList()
	if len(list) == 0 {
		return "Sheet1"
	}
	return list[0]
}
