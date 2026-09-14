package categories

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// maxNameRunes matches the CHECK in 00004_categories.sql.
const maxNameRunes = 60

var (
	// ErrNameRequired means the category was given no name at all.
	ErrNameRequired = errors.New("categories: name is required")
	// ErrNameTooLong means the name is longer than the column allows.
	ErrNameTooLong = errors.New("categories: name is too long")
	// ErrNameInvalid means the name contains characters that cannot be
	// displayed or that could misrepresent what is on screen — the same
	// concern accounts.normaliseAlias guards against, and for the same
	// reason: this text is read by a person deciding how to file a
	// transaction, and later by a model summarising spend by category.
	ErrNameInvalid = errors.New("categories: name contains characters that are not allowed")
)

// normaliseName cleans up what a customer typed, or reports why it cannot be
// used. Unlike an account alias, an empty category name is never valid: a
// category with no name would be indistinguishable from "Sin categorizar".
func normaliseName(raw string) (string, error) {
	var b strings.Builder
	b.Grow(len(raw))

	for _, r := range raw {
		switch {
		case unicode.IsSpace(r):
			b.WriteRune(' ')
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			return "", fmt.Errorf("%w: %q", ErrNameInvalid, r)
		default:
			b.WriteRune(r)
		}
	}

	name := strings.Join(strings.Fields(b.String()), " ")
	if name == "" {
		return "", ErrNameRequired
	}
	if n := len([]rune(name)); n > maxNameRunes {
		return "", fmt.Errorf("%w: %d characters, the limit is %d", ErrNameTooLong, n, maxNameRunes)
	}
	return name, nil
}
