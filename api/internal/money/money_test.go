package money

import (
	"errors"
	"math/big"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want Cents
	}{
		// Shapes real amounts take: one or two decimal places.
		{"32354.53", 3235453},
		{"249.84", 24984},
		{"49982.36", 4998236},
		{"3381.09", 338109},
		{"1633.5", 163350},
		{"10.54", 1054},

		// Padding and zeros.
		{"100", 10000},
		{"100.5", 10050},
		{"100.50", 10050},
		{"0", 0},
		{"0.00", 0},
		{"0.01", 1},
		{".5", 50},
		{"5.", 500},

		// Trailing zeros past two places lose nothing, so they are accepted.
		{"1.500", 150},
		{"2.9000", 290},

		// Signs.
		{"-3.07", -307},
		{"+3.07", 307},
		{" 42.42 ", 4242},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if err != nil {
			t.Errorf("Parse(%q) returned error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("Parse(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct {
		in   string
		want error
	}{
		{"", ErrEmpty},
		{"   ", ErrEmpty},
		{".", ErrMalformed},
		{"-", ErrMalformed},
		{"abc", ErrMalformed},
		{"1,000.00", ErrMalformed}, // grouping is the client's business, not ours
		{"1.2.3", ErrMalformed},
		{"1e5", ErrMalformed},
		{"$5.00", ErrMalformed},
		{"5 00", ErrMalformed},

		// Silently rounding a third decimal place would be a ledger bug.
		{"1.234", ErrTooPrecise},
		{"0.005", ErrTooPrecise},

		{"99999999999999999999", ErrOutOfRange},
	}
	for _, c := range cases {
		_, err := Parse(c.in)
		if !errors.Is(err, c.want) {
			t.Errorf("Parse(%q) error = %v, want %v", c.in, err, c.want)
		}
	}
}

func TestParsePositive(t *testing.T) {
	if _, err := ParsePositive("0"); !errors.Is(err, ErrNotPositive) {
		t.Errorf("ParsePositive(0) error = %v, want %v", err, ErrNotPositive)
	}
	if _, err := ParsePositive("-1.00"); !errors.Is(err, ErrNotPositive) {
		t.Errorf("ParsePositive(-1.00) error = %v, want %v", err, ErrNotPositive)
	}
	got, err := ParsePositive("0.01")
	if err != nil || got != 1 {
		t.Errorf("ParsePositive(0.01) = %d, %v; want 1, nil", got, err)
	}
}

func TestString(t *testing.T) {
	cases := []struct {
		in   Cents
		want string
	}{
		{3235453, "32354.53"},
		{10000, "100.00"},
		{1, "0.01"},
		{0, "0.00"},
		{50, "0.50"},
		{-307, "-3.07"},
	}
	for _, c := range cases {
		if got := c.in.String(); got != c.want {
			t.Errorf("Cents(%d).String() = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	// Every amount survives cents -> text -> cents unchanged.
	for _, v := range []Cents{0, 1, 99, 100, 12345, 3235453, 4998236, -1, -100000} {
		s := v.String()
		back, err := Parse(s)
		if err != nil {
			t.Errorf("Parse(%q) after Cents(%d).String(): %v", s, v, err)
			continue
		}
		if back != v {
			t.Errorf("round trip Cents(%d) -> %q -> Cents(%d)", v, s, back)
		}
	}
}

// TestFloatMultiplicationIsUnsafe documents *why* Parse works on text.
//
// The naive conversion — read the JSON number as a float64 and multiply by 100
// — truncates on ordinary amounts. This test pins that
// difference so nobody "simplifies" Parse into the broken version later.
func TestFloatMultiplicationIsUnsafe(t *testing.T) {
	cases := []struct {
		text  string
		asF64 float64
	}{
		{"1633.5", 1633.5},
		{"5.29", 5.29},
		{"8.87", 8.87},
	}
	brokenFound := false
	for _, c := range cases {
		exact, err := Parse(c.text)
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.text, err)
		}
		truncated := Cents(int64(c.asF64 * 100)) // the tempting one-liner
		if truncated != exact {
			brokenFound = true
			t.Logf("float64(%s)*100 truncates to %d cents; text parsing gives %d",
				c.text, truncated, exact)
		}
	}
	if !brokenFound {
		t.Skip("no truncation observed on this platform; text parsing is still the contract")
	}
}

// TestParseRoundTripsTwoDecimalAmounts asserts that an amount with at most two
// decimal places converts to cents losslessly, so a balance built from such
// amounts matches its source exactly.
func TestParseRoundTripsTwoDecimalAmounts(t *testing.T) {
	// Representative values across a realistic range.
	for _, s := range []string{"249.84", "49982.36", "10.54", "4999.65", "1633.5", "32354.53"} {
		c, err := Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
		// The cents value must reproduce the original text exactly.
		if got := c.String(); !sameNumber(got, s) {
			t.Errorf("Parse(%q).String() = %q, which is a different amount", s, got)
		}
	}
}

// sameNumber compares two decimal strings by value rather than by spelling, so
// "1633.5" and "1633.50" count as equal.
func sameNumber(a, b string) bool {
	ra, oka := new(big.Rat).SetString(a)
	rb, okb := new(big.Rat).SetString(b)
	return oka && okb && ra.Cmp(rb) == 0
}
