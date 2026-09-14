package categories

import (
	"errors"
	"strings"
	"testing"
)

func TestNormaliseName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
		err   error
	}{
		{
			name:  "an ordinary name is left alone",
			input: "Alimentación",
			want:  "Alimentación",
		},
		{
			name:  "surrounding whitespace goes",
			input: "   Transporte\t",
			want:  "Transporte",
		},
		{
			name:  "interior whitespace collapses",
			input: "Gastos    del     hogar",
			want:  "Gastos del hogar",
		},
		{
			name:  "empty is not allowed",
			input: "",
			err:   ErrNameRequired,
		},
		{
			name:  "whitespace only is the same as empty",
			input: "   \t\n  ",
			err:   ErrNameRequired,
		},
		{
			name:  "exactly at the limit is allowed",
			input: strings.Repeat("a", maxNameRunes),
			want:  strings.Repeat("a", maxNameRunes),
		},
		{
			name:  "one over the limit is refused",
			input: strings.Repeat("a", maxNameRunes+1),
			err:   ErrNameTooLong,
		},
		{
			name:  "a control byte is refused",
			input: "Ocio\x00nocturno",
			err:   ErrNameInvalid,
		},
		{
			name:  "an invisible name is refused",
			input: "​​​",
			err:   ErrNameInvalid,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normaliseName(tc.input)

			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("normaliseName(%q) error = %v, want %v", tc.input, err, tc.err)
				}
				return
			}

			if err != nil {
				t.Fatalf("normaliseName(%q) = %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("normaliseName(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// Whatever normaliseName accepts must fit the column, or a valid-looking name
// fails at the database with a constraint error instead of a readable message.
func TestNormaliseNameFitsTheColumn(t *testing.T) {
	// The CHECK in 00004_categories.sql.
	const columnLimit = 60

	if maxNameRunes != columnLimit {
		t.Errorf("maxNameRunes = %d but the column allows %d; a valid name would be "+
			"rejected by the database instead of by the service", maxNameRunes, columnLimit)
	}
}
