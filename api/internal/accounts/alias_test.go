package accounts

import (
	"errors"
	"strings"
	"testing"
)

func TestNormaliseAlias(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
		err   error
	}{
		{
			name:  "an ordinary name is left alone",
			input: "Gastos del mes",
			want:  "Gastos del mes",
		},
		{
			// The reason the limit counts runes. This is 15 characters and 16 bytes,
			// so a byte-based limit would treat it as longer than it is — and at the
			// boundary would cut a Spanish name short for having an accent in it.
			name:  "accents count as one character each",
			input: "Ahorros de mamá",
			want:  "Ahorros de mamá",
		},
		{
			name:  "surrounding whitespace goes",
			input: "   Vacaciones\t",
			want:  "Vacaciones",
		},
		{
			name:  "interior whitespace collapses",
			input: "Gastos    del     mes",
			want:  "Gastos del mes",
		},
		{
			// Pasting out of a spreadsheet brings a newline. Turning it into a space
			// is friendlier than refusing the paste.
			name:  "a pasted newline becomes a space",
			input: "Gastos\ndel mes",
			want:  "Gastos del mes",
		},
		{
			// How somebody removes a name they no longer want. Not an error.
			name:  "empty is allowed and means unnamed",
			input: "",
			want:  "",
		},
		{
			name:  "whitespace only is the same as empty",
			input: "   \t\n  ",
			want:  "",
		},
		{
			name:  "exactly at the limit is allowed",
			input: strings.Repeat("a", maxAliasRunes),
			want:  strings.Repeat("a", maxAliasRunes),
		},
		{
			// Forty accented characters are forty characters, not eighty bytes'
			// worth of refusal.
			name:  "forty accented characters still fit",
			input: strings.Repeat("á", maxAliasRunes),
			want:  strings.Repeat("á", maxAliasRunes),
		},
		{
			name:  "one over the limit is refused",
			input: strings.Repeat("a", maxAliasRunes+1),
			err:   ErrAliasTooLong,
		},
		{
			// Trailing spaces must not cost a character: the length is measured
			// after cleaning, not before.
			name:  "trailing spaces do not count towards the limit",
			input: strings.Repeat("a", maxAliasRunes) + "     ",
			want:  strings.Repeat("a", maxAliasRunes),
		},
		{
			name:  "a control byte is refused",
			input: "Gastos\x00del mes",
			err:   ErrAliasInvalid,
		},
		{
			// An alias of zero-width characters renders as nothing while not being
			// empty, so it could be neither read nor corrected by eye.
			name:  "an invisible alias is refused",
			input: "​​​",
			err:   ErrAliasInvalid,
		},
		{
			// U+202E reverses how the rest of the label renders. An account label is
			// read to decide where money goes, so it must not be able to display
			// something other than what it stores.
			name:  "a right-to-left override is refused",
			input: "Ahorros‮gastos",
			err:   ErrAliasInvalid,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normaliseAlias(tc.input)

			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("normaliseAlias(%q) error = %v, want %v", tc.input, err, tc.err)
				}
				if got != "" {
					t.Errorf("a refused alias returned %q, want the empty string", got)
				}
				return
			}

			if err != nil {
				t.Fatalf("normaliseAlias(%q) = %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("normaliseAlias(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// Whatever normaliseAlias accepts must fit the column, or a valid-looking alias fails
// at the database with a constraint error instead of a readable message.
func TestNormaliseAliasFitsTheColumn(t *testing.T) {
	// The CHECK in 00003_account_alias.sql.
	const columnLimit = 40

	if maxAliasRunes != columnLimit {
		t.Errorf("maxAliasRunes = %d but the column allows %d; a valid alias would be "+
			"rejected by the database instead of by the handler", maxAliasRunes, columnLimit)
	}
}
