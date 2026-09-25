// Package statement turns a bank's own export file into what that file says:
// which account it is for, the period it covers, the balances the bank printed
// and every movement line with its date, amount and running balance.
//
// It is pure — no database, no HTTP — and it keeps everything the bank said,
// balances included. The parsers it replaces threw the bank's balance columns
// away, which is why no import could ever be checked against the statement it
// came from. Keeping them is what makes a mismatch findable: the running balance
// the bank printed next to each line is compared with the one computed from the
// lines themselves, and the first line where they part is where to look.
//
// Amounts are signed from the holder's point of view: positive makes the holder
// richer, negative poorer. A card charge is negative and a card that owes 14.30
// has a balance of -14.30 — the sign the card file itself already uses.
package statement

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/JuanKsPty/corebank/api/internal/civil"
	"github.com/JuanKsPty/corebank/api/internal/money"
)

// Source names the file format.
type Source string

const (
	SourceBGAccount  Source = "bg_account"
	SourceBACAccount Source = "bac_account"
	SourceBGCard     Source = "bg_card"
)

// Institution values, stable because they are part of an account's identity.
const (
	InstitutionBancoGeneral = "banco_general"
	InstitutionBAC          = "bac"
)

// Class is which side of net worth an account sits on.
type Class string

const (
	ClassAsset     Class = "asset"
	ClassLiability Class = "liability"
)

// Kind is what a movement is, as far as the file can tell. Reports read it:
// transfers never count as spending or income.
type Kind string

const (
	KindIncome   Kind = "income"
	KindExpense  Kind = "expense"
	KindRefund   Kind = "refund"
	KindFee      Kind = "fee"
	KindInterest Kind = "interest"
	KindTransfer Kind = "transfer"
)

// Identity is the account a statement belongs to, keyed the way the
// institution keys it.
type Identity struct {
	Institution    string
	ExternalNumber string
	Currency       string
	DisplayName    string
	Class          Class
	// Type is checking or credit_card; a bank account's export does not say
	// whether it is checking or savings, so the owner can relabel it.
	Type string
}

// Line is one movement the file printed.
type Line struct {
	// LineNo is the line's position in the file, 1-based, for messages that
	// point back at it.
	LineNo int
	// BookedOn is the day the bank printed for the movement.
	BookedOn civil.Date
	// BookedAt is set when the file carries a time (the BG account export),
	// in Panama time.
	BookedAt *time.Time
	// PostedOn is the card's processing date ("Fecha proceso"), when present.
	PostedOn *civil.Date
	// Amount is signed from the holder's point of view.
	Amount       money.Cents
	Description  string
	BankRef      string
	BankCategory string
	// RunningBalance is the balance the bank printed after this line, when
	// the format prints one.
	RunningBalance *money.Cents
	Kind           Kind
	// Occurrence tells two identical lines in one file apart: the first is 0,
	// the second 1. It is part of the dedup key, so re-importing the same file
	// yields the same keys.
	Occurrence int
	// Raw is every column of the original line, balances included.
	Raw map[string]string
}

// BalanceOn is the day a line affects the balance: the processing date when
// there is one, the booking date otherwise.
func (l Line) BalanceOn() civil.Date {
	if l.PostedOn != nil && !l.PostedOn.IsZero() {
		return *l.PostedOn
	}
	return l.BookedOn
}

// Statement is what one file says about one account for one period.
type Statement struct {
	Identity    Identity
	PeriodStart civil.Date
	PeriodEnd   civil.Date
	// Opening and Closing are the balances before the first line and after
	// the last; nil when the format prints none (the BG card export).
	Opening *money.Cents
	Closing *money.Cents
	// Available and Held are the bank's available balance and funds on hold,
	// when printed (BAC).
	Available *money.Cents
	Held      *money.Cents
	// Lines are in chronological order.
	Lines []Line
	// ChainBreak is the first line whose printed running balance disagrees
	// with the opening plus the lines before it, or nil when they all agree
	// or the format prints no running balance.
	ChainBreak *ChainBreak
	// Warnings are checks that failed without making the file unreadable, in
	// Spanish, ready to show.
	Warnings []string
}

// ChainBreak is where a statement's own arithmetic stops adding up.
type ChainBreak struct {
	LineNo   int
	Bank     money.Cents
	Computed money.Cents
}

// File is a parsed upload. A card export can hold several cards, so a file
// can carry more than one statement.
type File struct {
	Source     Source
	Statements []Statement
}

// ErrUnrecognised means the file matches none of the supported formats.
var ErrUnrecognised = errors.New("statement: file format not recognised")

// Parse detects the format and parses the file. It fails only on structural
// problems — an unreadable date or amount, a missing column, a line with the
// wrong number of fields. A balance that does not add up is a warning on the
// statement, because it is exactly what the owner needs to see, not a reason
// to show nothing.
func Parse(filename string, data []byte) (File, error) {
	switch detect(filename, data) {
	case SourceBGAccount:
		return parseBGAccount(data)
	case SourceBACAccount:
		return parseBAC(data)
	case SourceBGCard:
		return parseBGCard(data)
	default:
		return File{}, ErrUnrecognised
	}
}

const (
	bgCardSignature = "Tarjeta;Fecha tran;Fecha proceso;Descripcion;Referencia;Categoria;Cargos (Db);Pagos (Cr)"
	bacSignature    = "Número de Clientes"
)

func detect(filename string, data []byte) Source {
	if strings.HasSuffix(strings.ToLower(filename), ".xlsx") {
		return SourceBGAccount
	}
	head := data[:min(len(data), 2048)]
	if strings.Contains(string(head), bgCardSignature) {
		return SourceBGCard
	}
	if strings.HasPrefix(latin1(head), bacSignature) {
		return SourceBACAccount
	}
	return ""
}

// DedupKey is what makes a line "the same movement" across two imports of
// overlapping files. It is built from the account's external identity and the
// line alone — never from an internal id — so deleting an account and
// importing its file again produces the same keys.
func DedupKey(id Identity, l Line) string {
	h := sha256.New()
	bookedAt := ""
	if l.BookedAt != nil {
		bookedAt = l.BookedAt.UTC().Format(time.RFC3339)
	}
	fmt.Fprintf(h, "%s|%s|%s|%s|%s|%d|%s|%s|%s|%d",
		id.Institution, id.ExternalNumber, l.Raw["Tarjeta"], l.BookedOn, bookedAt,
		l.Amount, normalise(l.Description), l.BankRef, l.BankCategory, l.Occurrence)
	return hex.EncodeToString(h.Sum(nil))
}

// normalise collapses whitespace and case, so a description the bank re-wrapped
// between two exports still matches.
func normalise(s string) string {
	return strings.ToLower(strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " "))
}

// assignOccurrences numbers identical lines in file order.
func assignOccurrences(lines []Line) {
	seen := make(map[string]int, len(lines))
	for i := range lines {
		l := &lines[i]
		bookedAt := ""
		if l.BookedAt != nil {
			bookedAt = l.BookedAt.Format(time.RFC3339)
		}
		key := fmt.Sprintf("%s|%s|%s|%d|%s|%s", l.Raw["Tarjeta"], l.BookedOn, bookedAt,
			l.Amount, normalise(l.Description), l.BankRef)
		l.Occurrence = seen[key]
		seen[key]++
	}
}

// checkChain walks the lines from the opening balance and reports the first
// one whose printed running balance disagrees with the computed one.
func checkChain(s *Statement) {
	if s.Opening == nil {
		return
	}
	running := *s.Opening
	for _, l := range s.Lines {
		running += l.Amount
		if l.RunningBalance != nil && *l.RunningBalance != running {
			s.ChainBreak = &ChainBreak{LineNo: l.LineNo, Bank: *l.RunningBalance, Computed: running}
			s.Warnings = append(s.Warnings, fmt.Sprintf(
				"La línea %d no cuadra: el banco muestra un saldo de %s y los movimientos suman %s.",
				l.LineNo, l.RunningBalance.String(), running.String()))
			return
		}
	}
}

// period sets the statement's period from its lines when the format does not
// state one.
func period(s *Statement) {
	for i, l := range s.Lines {
		if i == 0 || l.BookedOn.Before(s.PeriodStart) {
			s.PeriodStart = l.BookedOn
		}
		if i == 0 || l.BookedOn.After(s.PeriodEnd) {
			s.PeriodEnd = l.BookedOn
		}
	}
}

func cents(c money.Cents) *money.Cents { return &c }

// latin1 decodes ISO-8859-1, where every byte is the code point of the same
// value.
func latin1(b []byte) string {
	runes := make([]rune, len(b))
	for i, c := range b {
		runes[i] = rune(c)
	}
	return string(runes)
}
