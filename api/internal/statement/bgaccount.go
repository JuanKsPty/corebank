package statement

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/JuanKsPty/corebank/api/internal/civil"
	"github.com/JuanKsPty/corebank/api/internal/money"
)

// excelEpoch is Excel's day zero. A BG export's date cell is a serial number of
// days since it, fractional part included, in Panama wall-clock time.
var excelEpoch = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)

// parseBGAccount reads a Banco General account movement export (.xlsx):
// a "Cuenta:<alias> <number>" cell, a header row found by its "Fecha" cell,
// and one row per movement with Débito, Crédito and the running "Saldo total".
func parseBGAccount(data []byte) (File, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{RawCellValue: true})
	if err != nil {
		return File{}, fmt.Errorf("statement: opening the BG account file: %w", err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return File{}, fmt.Errorf("statement: the BG account file has no sheets")
	}
	rows, err := f.GetRows(sheets[0], excelize.Options{RawCellValue: true})
	if err != nil {
		return File{}, fmt.Errorf("statement: reading the BG account file: %w", err)
	}

	id := Identity{
		Institution: InstitutionBancoGeneral,
		Currency:    "USD", // the export states no currency; BG accounts here are USD
		Class:       ClassAsset,
		Type:        "checking",
	}
	header, index := -1, map[string]int{}
	for i, row := range rows {
		for _, cell := range row {
			if alias, number, ok := bgAccountLabel(cell); ok && id.ExternalNumber == "" {
				id.ExternalNumber, id.DisplayName = number, alias
			}
		}
		if header < 0 && hasCell(row, "Fecha") {
			header = i
			for c, name := range row {
				index[strings.TrimSpace(name)] = c
			}
		}
	}
	if id.ExternalNumber == "" {
		return File{}, fmt.Errorf("statement: the BG account file does not say which account it is (no \"Cuenta:\" cell)")
	}
	if header < 0 {
		return File{}, fmt.Errorf("statement: the BG account file has no header row with \"Fecha\"")
	}
	for _, want := range []string{"Fecha", "Descripción", "Débito", "Crédito"} {
		if _, ok := index[want]; !ok {
			return File{}, fmt.Errorf("statement: the BG account file is missing the %q column", want)
		}
	}
	_, hasSaldo := index["Saldo total"]

	var lines []Line
	for i, row := range rows[header+1:] {
		lineNo := header + i + 2
		cell := func(name string) string {
			if c, ok := index[name]; ok && c < len(row) {
				return strings.TrimSpace(row[c])
			}
			return ""
		}
		if cell("Fecha") == "" {
			continue
		}
		at, err := excelDateTime(cell("Fecha"))
		if err != nil {
			return File{}, fmt.Errorf("statement: BG account line %d: fecha %q: %w", lineNo, cell("Fecha"), err)
		}

		var debit, credit money.Cents
		if t := cell("Débito"); t != "" {
			if debit, err = centsFromFloat(t); err != nil {
				return File{}, fmt.Errorf("statement: BG account line %d: débito: %w", lineNo, err)
			}
		}
		if t := cell("Crédito"); t != "" {
			if credit, err = centsFromFloat(t); err != nil {
				return File{}, fmt.Errorf("statement: BG account line %d: crédito: %w", lineNo, err)
			}
		}
		amount, err := fromColumns(debit, credit)
		if err != nil {
			return File{}, fmt.Errorf("statement: BG account line %d: %w", lineNo, err)
		}
		if amount == 0 {
			continue
		}

		raw := map[string]string{}
		for name, c := range index {
			if c < len(row) && name != "" {
				raw[name] = strings.TrimSpace(row[c])
			}
		}
		l := Line{
			LineNo:      lineNo,
			BookedOn:    civil.Of(at),
			BookedAt:    &at,
			Amount:      amount,
			Description: cell("Descripción"),
			// BG's own Referencia is always "0"; the transaction code is the
			// one reference-like field that carries information.
			BankRef:      cell("Transacción"),
			BankCategory: cell("Transacción"),
			Kind:         bankKind(amount),
			Raw:          raw,
		}
		if hasSaldo && cell("Saldo total") != "" {
			bal, err := centsFromFloat(cell("Saldo total"))
			if err != nil {
				return File{}, fmt.Errorf("statement: BG account line %d: saldo total: %w", lineNo, err)
			}
			l.RunningBalance = &bal
		}
		lines = append(lines, l)
	}

	assignOccurrences(lines)
	st := Statement{Identity: id, Lines: chronological(lines)}
	period(&st)
	if len(st.Lines) > 0 {
		first, last := st.Lines[0], st.Lines[len(st.Lines)-1]
		if first.RunningBalance != nil {
			st.Opening = cents(*first.RunningBalance - first.Amount)
		}
		if last.RunningBalance != nil {
			st.Closing = cents(*last.RunningBalance)
		}
	}
	checkChain(&st)
	return File{Source: SourceBGAccount, Statements: []Statement{st}}, nil
}

// chronological orders lines oldest first. A BG export can list newest first,
// and two lines can share a timestamp; the file's own order between those is
// kept only if it is the one the running balances agree with.
func chronological(lines []Line) []Line {
	out := append([]Line(nil), lines...)
	// File order reversed is the other candidate for same-time lines: an
	// export that lists newest first lists ties newest first too.
	if newestFirst(lines) {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].BookedAt.Before(*out[j].BookedAt) })
	return out
}

// newestFirst reports whether the running balances link each line to the one
// after it (a newest-first listing) better than to the one before it.
func newestFirst(lines []Line) bool {
	forward, backward := 0, 0
	for i := 1; i < len(lines); i++ {
		a, b := lines[i-1], lines[i]
		if a.RunningBalance == nil || b.RunningBalance == nil {
			continue
		}
		if *b.RunningBalance == *a.RunningBalance+b.Amount {
			forward++
		}
		if *a.RunningBalance == *b.RunningBalance+a.Amount {
			backward++
		}
	}
	return backward > forward
}

// excelDateTime reads a date cell's serial as Panama wall-clock time, rounded
// to the second to shed float noise.
func excelDateTime(raw string) (time.Time, error) {
	serial, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("no es una fecha de Excel")
	}
	seconds := math.Round(serial * 24 * 60 * 60)
	wall := excelEpoch.Add(time.Duration(seconds) * time.Second)
	return time.Date(wall.Year(), wall.Month(), wall.Day(),
		wall.Hour(), wall.Minute(), wall.Second(), 0, civil.Panama), nil
}

// bgAccountLabel splits "Cuenta:<alias> <number>". The number is the last
// token; it has no spaces of its own ("04-98-97-958835-1").
func bgAccountLabel(cell string) (alias, number string, ok bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(cell), "Cuenta:")
	if !found {
		return "", "", false
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return "", "", false
	}
	number = fields[len(fields)-1]
	alias = strings.Join(fields[:len(fields)-1], " ")
	if alias == "" {
		alias = "Cuenta " + number
	}
	return alias, number, true
}

// bankKind is the importer's first guess for a bank-account line. Transfers
// between the owner's own accounts cannot be told from the line alone; rules
// and transfer matching refine it later.
func bankKind(amount money.Cents) Kind {
	if amount > 0 {
		return KindIncome
	}
	return KindExpense
}

func hasCell(row []string, want string) bool {
	for _, cell := range row {
		if strings.TrimSpace(cell) == want {
			return true
		}
	}
	return false
}
