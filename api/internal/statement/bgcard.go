package statement

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strings"

	"github.com/JuanKsPty/corebank/api/internal/civil"
	"github.com/JuanKsPty/corebank/api/internal/money"
)

var bgCardColumns = []string{
	"Cuenta", "Tarjeta", "Fecha tran", "Fecha proceso", "Descripcion",
	"Referencia", "Categoria", "Cargos (Db)", "Pagos (Cr)",
}

// parseBGCard reads a Banco General credit card export: semicolon-separated,
// one line per movement, several physical cards possibly sharing one account.
//
// The file prints no balance at all — no previous balance, no closing, no
// credit limit — so its statements carry no Opening or Closing. A card's
// starting balance has to come from the owner, read off the PDF statement.
func parseBGCard(data []byte) (File, error) {
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	r.Comma = ';'
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	records, err := r.ReadAll()
	if err != nil {
		return File{}, fmt.Errorf("statement: reading the BG card file: %w", err)
	}
	if len(records) == 0 {
		return File{}, fmt.Errorf("statement: the BG card file is empty")
	}
	col := map[string]int{}
	for i, name := range records[0] {
		col[strings.TrimSpace(name)] = i
	}
	for _, want := range bgCardColumns {
		if _, ok := col[want]; !ok {
			return File{}, fmt.Errorf("statement: the BG card file is missing the %q column", want)
		}
	}

	byAccount := map[string]*Statement{}
	var order []string
	for i, record := range records[1:] {
		lineNo := i + 2
		cell := func(name string) string {
			if c := col[name]; c < len(record) {
				return strings.TrimSpace(record[c])
			}
			return ""
		}
		if cell("Cuenta") == "" && cell("Descripcion") == "" {
			continue
		}
		account := cell("Cuenta")
		if account == "" {
			return File{}, fmt.Errorf("statement: BG card line %d has no Cuenta", lineNo)
		}

		bookedOn, err := civil.ParseLayout("02/01/2006", cell("Fecha tran"))
		if err != nil {
			return File{}, fmt.Errorf("statement: BG card line %d: fecha tran %q", lineNo, cell("Fecha tran"))
		}
		var postedOn *civil.Date
		if t := cell("Fecha proceso"); t != "" {
			d, err := civil.ParseLayout("02/01/2006", t)
			if err != nil {
				return File{}, fmt.Errorf("statement: BG card line %d: fecha proceso %q", lineNo, t)
			}
			postedOn = &d
		}

		var charge, payment money.Cents
		if t := cell("Cargos (Db)"); t != "" {
			if charge, err = signedFromText(t); err != nil {
				return File{}, fmt.Errorf("statement: BG card line %d: cargos: %w", lineNo, err)
			}
		}
		if t := cell("Pagos (Cr)"); t != "" {
			if payment, err = signedFromText(t); err != nil {
				return File{}, fmt.Errorf("statement: BG card line %d: pagos: %w", lineNo, err)
			}
		}
		amount, err := fromColumns(charge, payment)
		if err != nil {
			return File{}, fmt.Errorf("statement: BG card line %d: %w", lineNo, err)
		}
		if amount == 0 {
			continue
		}

		raw := make(map[string]string, len(bgCardColumns))
		for _, name := range bgCardColumns {
			raw[name] = cell(name)
		}
		description := cell("Descripcion")
		if card := cell("Tarjeta"); card != "" && card != account {
			description = "[Tarjeta " + card + "] " + description
		}

		st, ok := byAccount[account]
		if !ok {
			st = &Statement{Identity: Identity{
				Institution:    InstitutionBancoGeneral,
				ExternalNumber: account,
				Currency:       "USD",
				DisplayName:    "Tarjeta " + account,
				Class:          ClassLiability,
				Type:           "credit_card",
			}}
			byAccount[account] = st
			order = append(order, account)
		}
		st.Lines = append(st.Lines, Line{
			LineNo:       lineNo,
			BookedOn:     bookedOn,
			PostedOn:     postedOn,
			Amount:       amount,
			Description:  strings.Join(strings.Fields(description), " "),
			BankRef:      cell("Referencia"),
			BankCategory: cell("Categoria"),
			Kind:         cardKind(cell("Categoria"), description, amount),
			Raw:          raw,
		})
	}

	file := File{Source: SourceBGCard}
	for _, account := range order {
		st := byAccount[account]
		assignOccurrences(st.Lines)
		sortByBalanceDay(st.Lines)
		period(st)
		file.Statements = append(file.Statements, *st)
	}
	return file, nil
}

// cardKind classifies a card line from the bank's own category. "Pagos" is
// money arriving from the owner to pay the card: a transfer between their own
// accounts, never income. "Cargos" are the bank's fees and insurance, or
// interest when the description says so. Any other positive amount is a
// refund of a purchase.
func cardKind(category, description string, amount money.Cents) Kind {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "pagos":
		return KindTransfer
	case "cargos":
		if strings.Contains(strings.ToUpper(description), "INTERES") {
			return KindInterest
		}
		return KindFee
	}
	if amount > 0 {
		return KindRefund
	}
	return KindExpense
}

// sortByBalanceDay orders card lines by the day they reach the balance, keeping
// the file's order between lines of the same day.
func sortByBalanceDay(lines []Line) {
	for i := 1; i < len(lines); i++ {
		for j := i; j > 0 && lines[j].BalanceOn().Before(lines[j-1].BalanceOn()); j-- {
			lines[j], lines[j-1] = lines[j-1], lines[j]
		}
	}
}
