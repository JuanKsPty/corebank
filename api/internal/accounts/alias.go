package accounts

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// maxAliasRunes bounds what a customer may call an account.
//
// Counted in runes, not bytes, and that distinction is the point in Spanish:
// len("Ahorros de mamá") is 16 bytes for 15 characters, so a byte limit quietly costs
// an accented name a character it should have had. PostgreSQL's char_length counts
// code points too, so the CHECK constraint in the migration and this limit agree.
//
// Forty is "Gastos de la casa del mes" with room to spare, and narrow enough that a
// card in the accounts grid keeps its shape.
const maxAliasRunes = 40

var (
	// ErrAliasTooLong means the alias is longer than an account card can show.
	ErrAliasTooLong = errors.New("accounts: the alias is too long")
	// ErrAliasInvalid means the alias contains characters that cannot be displayed
	// or that could misrepresent what is on screen.
	ErrAliasInvalid = errors.New("accounts: the alias contains characters that are not allowed")
)

// normaliseAlias cleans up what a customer typed, or reports why it cannot be used.
//
// An empty result is a valid outcome and not an error: it is how somebody removes a
// name they no longer want, and it is what an account has before it is ever named.
//
// Three things happen here, and each has a reason beyond tidiness:
//
// Whitespace of any kind collapses to single spaces and is trimmed. Pasting a name out
// of a spreadsheet brings a newline or a tab with it, and turning that into a space is
// friendlier than refusing the paste.
//
// Control and format characters are refused. A control byte cannot be rendered, and
// format characters are worse than useless here: a name made only of zero-width
// characters looks blank on screen while not being empty, so it cannot be spotted or
// corrected by eye, and U+202E RIGHT-TO-LEFT OVERRIDE can make a label render in an
// order other than the one it is stored in. An account label is a thing people read to
// decide where money goes, so it must not be able to lie about what it says.
//
// The length is checked after cleaning, so trailing spaces never cost a character.
func normaliseAlias(raw string) (string, error) {
	var b strings.Builder
	b.Grow(len(raw))

	for _, r := range raw {
		switch {
		case unicode.IsSpace(r):
			// Collapsed below; written as a plain space so tabs and newlines lose
			// their identity here rather than downstream.
			b.WriteRune(' ')
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			return "", fmt.Errorf("%w: %q", ErrAliasInvalid, r)
		default:
			b.WriteRune(r)
		}
	}

	alias := strings.Join(strings.Fields(b.String()), " ")

	if n := len([]rune(alias)); n > maxAliasRunes {
		return "", fmt.Errorf("%w: %d characters, the limit is %d", ErrAliasTooLong, n, maxAliasRunes)
	}
	return alias, nil
}
