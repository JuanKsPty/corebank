// Package money represents monetary amounts as integer minor units (cents).
//
// No value in this system is ever held as a float. Balances and transfer
// amounts are integers end to end — cents in Postgres (BIGINT), cents in
// TigerBeetle (u128), cents over the wire — and formatting to a decimal string
// happens only at the edges. Parsing works on the decimal *text* rather than a
// float64 so that a value like "32354.53", which has no exact binary
// representation, cannot drift by a cent on the way in.
package money

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// CurrencyUSD is the only currency this system handles. It is a named constant
// rather than a literal scattered across packages so that adding a second
// currency later surfaces every place that assumed there was one.
const CurrencyUSD = "USD"

// Cents is an amount in USD cents. A negative Cents is meaningful for
// bookkeeping deltas but never for a transfer amount.
type Cents int64

// Input is an amount as it arrives in a request body, kept as the client's own
// text until it is parsed.
//
// It accepts both a JSON string ("100.50") and a JSON number (100.50), taking the
// raw token in either case. That is the whole point: decoding a number into a
// float64 first and multiplying by 100 loses cents — 8.87 becomes 886 — so the
// digits the client sent are never converted to a float on the way in. A string
// is still the recommended form, because JSON numbers are defined in terms of
// doubles and some clients will have already rounded before we see them.
type Input string

// UnmarshalJSON captures the amount's raw text.
func (i *Input) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "null" {
		*i = ""
		return nil
	}
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		// A quoted amount. Unquoting via the JSON decoder would be needed for
		// escapes, but no valid amount contains any, so anything unusual in here
		// is left for Parse to reject with a message about the amount.
		raw = raw[1 : len(raw)-1]
	}
	*i = Input(strings.TrimSpace(raw))
	return nil
}

// Cents parses the input, requiring a positive amount.
func (i Input) Cents() (Cents, error) { return ParsePositive(string(i)) }

// Amount is how a monetary value appears in a JSON response.
//
// Cents is the authoritative field and the one to compute with; Formatted is the
// same value as a decimal string, so a response is readable on its own and a log
// or a tool result does not have to be mentally divided by 100. The frontend
// renders from Cents and never does arithmetic on Formatted.
type Amount struct {
	Cents     int64  `json:"cents"`
	Formatted string `json:"formatted"`
	Currency  string `json:"currency"`
}

// Amount returns the wire representation of c.
func (c Cents) Amount() Amount {
	return Amount{Cents: int64(c), Formatted: c.String(), Currency: CurrencyUSD}
}

// CheckCurrency rejects anything but USD, so an amount labelled in another
// currency is refused at the edge rather than quietly treated as dollars.
func CheckCurrency(code string) error {
	if code == "" || strings.EqualFold(code, CurrencyUSD) {
		return nil
	}
	return fmt.Errorf("%w: %q", ErrUnsupportedCurrency, code)
}

var (
	ErrEmpty               = errors.New("money: empty amount")
	ErrMalformed           = errors.New("money: malformed amount")
	ErrTooPrecise          = errors.New("money: more than two decimal places")
	ErrOutOfRange          = errors.New("money: amount out of range")
	ErrNotPositive         = errors.New("money: amount must be positive")
	ErrUnsupportedCurrency = errors.New("money: unsupported currency")
)

// Parse converts a decimal amount written as text into Cents.
//
// It accepts an optional sign, up to two decimal places, and an optional
// leading or trailing zero ("100", "100.5", "100.50", ".5", "-3.07"). It
// rejects anything with more precision than a cent rather than silently
// rounding it, because a truncated amount in a ledger is a bug, not a nuance.
// Thousands separators are not accepted: the caller should not be guessing at
// locale when money is involved.
func Parse(s string) (Cents, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, ErrEmpty
	}

	neg := false
	switch s[0] {
	case '+':
		s = s[1:]
	case '-':
		neg = true
		s = s[1:]
	}
	if s == "" {
		return 0, ErrMalformed
	}

	intPart, fracPart := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, fracPart = s[:i], s[i+1:]
	}
	// "5." and ".5" are fine; "." alone is not.
	if intPart == "" && fracPart == "" {
		return 0, ErrMalformed
	}
	if !allDigits(intPart) || !allDigits(fracPart) {
		return 0, ErrMalformed
	}
	if len(fracPart) > 2 {
		// Allow trailing zeros beyond two places ("1.500") since they carry no
		// value; reject anything that would actually lose precision.
		for _, r := range fracPart[2:] {
			if r != '0' {
				return 0, ErrTooPrecise
			}
		}
		fracPart = fracPart[:2]
	}

	var units int64
	if intPart != "" {
		var err error
		units, err = strconv.ParseInt(intPart, 10, 64)
		if err != nil {
			return 0, ErrOutOfRange
		}
	}

	// Pad so "5" -> 00, "5.7" -> 70, "5.73" -> 73.
	frac := int64(0)
	switch len(fracPart) {
	case 0:
	case 1:
		frac = int64(fracPart[0]-'0') * 10
	default:
		frac = int64(fracPart[0]-'0')*10 + int64(fracPart[1]-'0')
	}

	const maxUnits = (1<<63 - 1) / 100
	if units > maxUnits {
		return 0, ErrOutOfRange
	}
	total := units*100 + frac
	if total < 0 {
		return 0, ErrOutOfRange
	}
	if neg {
		total = -total
	}
	return Cents(total), nil
}

// ParsePositive is Parse plus the check every money-moving endpoint needs:
// the amount must be strictly greater than zero.
func ParsePositive(s string) (Cents, error) {
	c, err := Parse(s)
	if err != nil {
		return 0, err
	}
	if c <= 0 {
		return 0, ErrNotPositive
	}
	return c, nil
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// String renders the amount as a plain decimal with exactly two places and no
// currency symbol or grouping — the canonical form for API responses, which
// leaves presentation to the client's locale.
func (c Cents) String() string {
	neg := c < 0
	v := int64(c)
	if neg {
		v = -v
	}
	s := fmt.Sprintf("%d.%02d", v/100, v%100)
	if neg {
		return "-" + s
	}
	return s
}

// Uint64 returns the amount for handing to TigerBeetle, which stores transfer
// amounts as unsigned. Negative amounts are a programming error here.
func (c Cents) Uint64() (uint64, error) {
	if c < 0 {
		return 0, ErrNotPositive
	}
	return uint64(c), nil
}

// Add and Sub exist so arithmetic on money stays inside this type rather than
// being open-coded on raw int64s across the codebase.
func (c Cents) Add(o Cents) Cents { return c + o }
func (c Cents) Sub(o Cents) Cents { return c - o }
