package statement

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/JuanKsPty/corebank/api/internal/money"
)

// groupedAmount is a number written with comma thousands separators and a
// point for decimals, as every supported bank prints them: "1,175.75".
var groupedAmount = regexp.MustCompile(`^[0-9]{1,3}(,[0-9]{3})+(\.[0-9]+)?$`)

// parseText reads an amount as a bank prints it and returns its magnitude and
// whether the text itself marked it negative.
//
// Accepted: "12.50", "-12.50", "1,175.75", "(12.50)", "12.50 CR", "12.50 DB".
// A comma is only ever a thousands separator here, and only in groups of
// three; anything else is refused rather than guessed at.
func parseText(s string) (magnitude money.Cents, negative bool, err error) {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")"):
		s, negative = strings.TrimSpace(s[1:len(s)-1]), true
	case strings.HasSuffix(strings.ToUpper(s), " DB"):
		s, negative = strings.TrimSpace(s[:len(s)-3]), true
	case strings.HasSuffix(strings.ToUpper(s), " CR"):
		s = strings.TrimSpace(s[:len(s)-3])
	}
	if strings.HasPrefix(s, "-") {
		s, negative = strings.TrimSpace(s[1:]), !negative
	} else if strings.HasPrefix(s, "+") {
		s = strings.TrimSpace(s[1:])
	}
	if strings.Contains(s, ",") {
		if !groupedAmount.MatchString(s) {
			return 0, false, fmt.Errorf("%q no es un monto válido", s)
		}
		s = strings.ReplaceAll(s, ",", "")
	}
	c, err := money.Parse(s)
	if err != nil {
		return 0, false, fmt.Errorf("%q no es un monto válido: %w", s, err)
	}
	return c, negative, nil
}

// signedFromText is parseText with the sign applied.
func signedFromText(s string) (money.Cents, error) {
	m, neg, err := parseText(s)
	if err != nil {
		return 0, err
	}
	if neg {
		return -m, nil
	}
	return m, nil
}

// centsFromFloat converts a spreadsheet's numeric cell to cents.
//
// An .xlsx stores a number as a double, so 8.87 can come back as
// "8.8699999999999992". That is a whole number of cents plus float noise, and it
// is rounded; a value that is genuinely finer than a cent is refused, because
// rounding it would change the amount.
func centsFromFloat(raw string) (money.Cents, error) {
	f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0, fmt.Errorf("%q no es un número", raw)
	}
	scaled := f * 100
	rounded := math.Round(scaled)
	if math.Abs(scaled-rounded) > 1e-6*math.Max(1, math.Abs(scaled)) {
		return 0, fmt.Errorf("%q tiene fracciones de centavo", raw)
	}
	return money.Cents(rounded), nil
}

// fromColumns turns a debit/credit pair into one signed amount. The column
// decides the sign — money in the debit column leaves the account — and the
// text's own sign only has to agree with it. Both columns non-zero is a line
// that says two different things, and is refused.
func fromColumns(debit, credit money.Cents) (money.Cents, error) {
	d, c := abs(debit), abs(credit)
	switch {
	case d != 0 && c != 0:
		return 0, fmt.Errorf("la línea tiene débito (%s) y crédito (%s) a la vez", d, c)
	case d != 0:
		return -d, nil
	default:
		return c, nil
	}
}

func abs(c money.Cents) money.Cents {
	if c < 0 {
		return -c
	}
	return c
}
