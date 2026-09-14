// Package bankimport turns a bank's own export file into transactions
// corebank can track spend against.
//
// An imported account is never a TigerBeetle account — see the package doc
// in internal/investments for the same rule applied to a brokerage. Nothing
// here can verify what a bank's file claims, so treating it as ledger money
// would contradict corebank's central invariant. Instead, every row lands in
// Postgres only, in external_transactions, clearly separate from the
// TigerBeetle-derived balances the dashboard trusts.
//
// Three real formats are supported directly, discovered by inspecting actual
// exports rather than guessing at a generic shape: a Banco General credit
// card statement (bgcard.go), a Banco General account movement export
// (bgaccount.go), and a BAC account movement export (bacaccount.go). A file
// matching none of them falls back to a configurable column-mapped CSV
// parser for a bank not seen yet.
package bankimport

import (
	"io"
	"time"

	"github.com/JuanKsPty/corebank/api/internal/money"
)

// ParsedRow is one movement as a parser extracted it, before it becomes an
// external_transactions row.
type ParsedRow struct {
	OccurredAt time.Time
	// Amount is signed: positive is money arriving, negative is money
	// leaving — the same convention investments.CashTransaction uses.
	Amount money.Cents
	// Description is the free text a person would recognise the movement by.
	Description string
	// ExternalRef is whatever reference the bank's own file gives this row,
	// or empty when it gives none. Never trusted alone for deduplication —
	// see dedup.go for why.
	ExternalRef string
	// RawCategory is the bank's own category label, when the format
	// provides one (only the Banco General card statement does). Empty
	// otherwise.
	RawCategory string
	// Raw is the original row, kept for the external_transactions.raw JSONB
	// column so a row can always be traced back to exactly what the bank
	// sent, independent of how this package chose to interpret it.
	Raw map[string]string
}

// AccountHint is the account identity a file declares about itself, when its
// format carries one — the "Cuenta:" cell in a Banco General account export,
// the client number and name in a BAC statement. Used to find or create the
// right external_account without asking the customer to type it in by hand.
type AccountHint struct {
	Institution   string
	AccountNumber string
	DisplayName   string
}

// Institution values. Kept as plain strings rather than an enum type so a
// fifth bank is one more string constant, not a new type everything
// downstream has to widen to handle.
const (
	InstitutionBancoGeneral = "banco_general"
	InstitutionBAC          = "bac"
)

// Parser turns a file's bytes into rows and, when the format supports it, the
// account the file says it belongs to.
type Parser interface {
	// Parse extracts every movement row from r.
	Parse(r io.Reader) ([]ParsedRow, error)
	// AccountHint reads just enough of r to identify the account, without
	// necessarily parsing every row — callers needing both call Parse and
	// AccountHint against independent readers over the same bytes, since
	// each consumes its reader.
	AccountHint(r io.Reader) (AccountHint, bool)
}
