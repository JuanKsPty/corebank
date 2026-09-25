package statement

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"

	"github.com/JuanKsPty/corebank/api/internal/civil"
	"github.com/JuanKsPty/corebank/api/internal/money"
)

const (
	bacDetailMarker  = "Detalle de Estado Bancario"
	bacSummaryMarker = "Resumen de Estado Bancario"
	// bacOpeningCode marks "APERTURA DE SALDOS": the balance the account
	// started the period with, printed as if it were a credit. It is folded
	// into the opening balance, never counted as income.
	bacOpeningCode = "OP"
)

// parseBAC reads a BAC account statement: a Latin-1, comma-and-space CSV in
// three sections — the account and its balances, the movements with a running
// balance, and totals per transaction code.
func parseBAC(data []byte) (File, error) {
	sections := splitBACSections(latin1(data))
	if len(sections.header) < 2 {
		return File{}, fmt.Errorf("statement: the BAC file has no account line")
	}
	if len(sections.detail) < 1 {
		return File{}, fmt.Errorf("statement: the BAC file has no %q section", bacDetailMarker)
	}

	head, err := bacRecord(sections.header[0])
	if err != nil {
		return File{}, err
	}
	values, err := bacRecord(sections.header[1])
	if err != nil {
		return File{}, err
	}
	field := func(name string) string {
		for i, h := range head {
			if h == name && i < len(values) {
				return values[i]
			}
		}
		return ""
	}

	id := Identity{
		Institution:    InstitutionBAC,
		ExternalNumber: field("Producto"),
		Currency:       strings.ToUpper(field("Moneda")),
		DisplayName:    "BAC " + field("Producto"),
		Class:          ClassAsset,
		Type:           "checking",
	}
	if id.ExternalNumber == "" {
		return File{}, fmt.Errorf("statement: the BAC file does not say which product it is for")
	}
	if id.Currency != "USD" {
		return File{}, fmt.Errorf("statement: the BAC file is in %q; only USD is supported", id.Currency)
	}

	st := Statement{Identity: id}
	var saldoInicial, saldoLibros money.Cents
	for name, dest := range map[string]*money.Cents{"Saldo Inicial": &saldoInicial, "Saldo en Libros": &saldoLibros} {
		if *dest, err = signedFromText(field(name)); err != nil {
			return File{}, fmt.Errorf("statement: BAC %s: %w", name, err)
		}
	}
	if t := field("Saldo Disponible"); t != "" {
		v, err := signedFromText(t)
		if err != nil {
			return File{}, fmt.Errorf("statement: BAC Saldo Disponible: %w", err)
		}
		st.Available = &v
	}
	if t := field("Retenidos y Diferidos"); t != "" {
		v, err := signedFromText(t)
		if err != nil {
			return File{}, fmt.Errorf("statement: BAC Retenidos y Diferidos: %w", err)
		}
		st.Held = &v
	}
	asOf, asOfErr := civil.ParseLayout("02/01/2006", field("Fecha"))

	columns, err := bacRecord(sections.detail[0].text)
	if err != nil {
		return File{}, err
	}
	col := map[string]int{}
	for i, c := range columns {
		col[c] = i
	}
	for _, want := range []string{"Fecha de Transacción", "Referencia de Transacción", "Código de Transacción",
		"Descripción de Transacción", "Débito de Transacción", "Crédito de Transacción"} {
		if _, ok := col[want]; !ok {
			return File{}, fmt.Errorf("statement: the BAC file is missing the %q column", want)
		}
	}
	_, hasBalance := col["Balance de Transacción"]

	opening := saldoInicial
	var debitCount, creditCount int
	var debitTotal, creditTotal money.Cents
	for _, raw := range sections.detail[1:] {
		record, err := bacRecord(raw.text)
		if err != nil {
			return File{}, fmt.Errorf("statement: BAC line %d: %w", raw.lineNo, err)
		}
		if len(record) != len(columns) {
			return File{}, fmt.Errorf("statement: BAC line %d has %d fields, the header has %d — "+
				"probably a thousands separator without quotes or a comma inside a description",
				raw.lineNo, len(record), len(columns))
		}
		cell := func(name string) string { return record[col[name]] }

		bookedOn, err := civil.ParseLayout("02/01/2006", cell("Fecha de Transacción"))
		if err != nil {
			return File{}, fmt.Errorf("statement: BAC line %d: fecha %q", raw.lineNo, cell("Fecha de Transacción"))
		}
		debit, err := signedFromText(cell("Débito de Transacción"))
		if err != nil {
			return File{}, fmt.Errorf("statement: BAC line %d: débito: %w", raw.lineNo, err)
		}
		credit, err := signedFromText(cell("Crédito de Transacción"))
		if err != nil {
			return File{}, fmt.Errorf("statement: BAC line %d: crédito: %w", raw.lineNo, err)
		}
		if debit < 0 || credit < 0 {
			return File{}, fmt.Errorf("statement: BAC line %d: débito and crédito must not be negative", raw.lineNo)
		}
		amount, err := fromColumns(debit, credit)
		if err != nil {
			return File{}, fmt.Errorf("statement: BAC line %d: %w", raw.lineNo, err)
		}
		if debit != 0 {
			debitCount++
			debitTotal += debit
		} else if credit != 0 {
			creditCount++
			creditTotal += credit
		}

		if cell("Código de Transacción") == bacOpeningCode {
			opening += amount
			continue
		}
		if amount == 0 {
			continue
		}

		rawMap := make(map[string]string, len(columns))
		for i, c := range columns {
			rawMap[c] = record[i]
		}
		l := Line{
			LineNo:       raw.lineNo,
			BookedOn:     bookedOn,
			Amount:       amount,
			Description:  cell("Descripción de Transacción"),
			BankRef:      cell("Referencia de Transacción"),
			BankCategory: cell("Código de Transacción"),
			Kind:         bankKind(amount),
			Raw:          rawMap,
		}
		if hasBalance && cell("Balance de Transacción") != "" {
			bal, err := signedFromText(cell("Balance de Transacción"))
			if err != nil {
				return File{}, fmt.Errorf("statement: BAC line %d: balance: %w", raw.lineNo, err)
			}
			l.RunningBalance = &bal
		}
		st.Lines = append(st.Lines, l)
	}

	assignOccurrences(st.Lines)
	period(&st)
	if asOfErr == nil && (st.PeriodEnd.IsZero() || asOf.After(st.PeriodEnd)) {
		st.PeriodEnd = asOf
	}
	st.Opening, st.Closing = cents(opening), cents(saldoLibros)

	var sum money.Cents
	for _, l := range st.Lines {
		sum += l.Amount
	}
	if opening+sum != saldoLibros {
		st.Warnings = append(st.Warnings, fmt.Sprintf(
			"El saldo inicial (%s) más los movimientos (%s) no da el saldo en libros que muestra BAC (%s).",
			opening, sum, saldoLibros))
	}
	checkBACSummary(&st, sections.summary, debitCount, debitTotal, creditCount, creditTotal)
	checkChain(&st)
	return File{Source: SourceBACAccount, Statements: []Statement{st}}, nil
}

type bacLine struct {
	lineNo int
	text   string
}

type bacSections struct {
	header  []string
	detail  []bacLine
	summary []string
}

// splitBACSections cuts the file at its two section markers, dropping blank
// lines and keeping each detail line's position for messages.
func splitBACSections(text string) bacSections {
	var s bacSections
	part := 0
	for i, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			continue
		case strings.HasPrefix(trimmed, bacDetailMarker):
			part = 1
			continue
		case strings.HasPrefix(trimmed, bacSummaryMarker):
			part = 2
			continue
		}
		switch part {
		case 0:
			s.header = append(s.header, line)
		case 1:
			s.detail = append(s.detail, bacLine{lineNo: i + 1, text: line})
		default:
			s.summary = append(s.summary, line)
		}
	}
	return s
}

// bacRecord parses one line. The file separates fields with ", ", so leading
// spaces are trimmed; a quoted field keeps its commas.
func bacRecord(line string) ([]string, error) {
	r := csv.NewReader(strings.NewReader(line))
	r.TrimLeadingSpace = true
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	record, err := r.Read()
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("statement: unreadable BAC line %q: %w", line, err)
	}
	for i := range record {
		record[i] = strings.TrimSpace(record[i])
	}
	return record, nil
}

// checkBACSummary compares the Resumen's Total line with the detail it
// summarises.
func checkBACSummary(st *Statement, summary []string, dCount int, dTotal money.Cents, cCount int, cTotal money.Cents) {
	for _, line := range summary {
		record, err := bacRecord(line)
		if err != nil || len(record) < 5 || record[0] != "Total" {
			continue
		}
		var n [2]int
		if _, err := fmt.Sscan(record[1], &n[0]); err != nil {
			return
		}
		if _, err := fmt.Sscan(record[3], &n[1]); err != nil {
			return
		}
		debits, err1 := signedFromText(record[2])
		credits, err2 := signedFromText(record[4])
		if err1 != nil || err2 != nil {
			return
		}
		if n[0] != dCount || debits != dTotal || n[1] != cCount || credits != cTotal {
			st.Warnings = append(st.Warnings, fmt.Sprintf(
				"El resumen de BAC dice %d débitos por %s y %d créditos por %s; el detalle tiene %d por %s y %d por %s.",
				n[0], debits, n[1], credits, dCount, dTotal, cCount, cTotal))
		}
		return
	}
}
