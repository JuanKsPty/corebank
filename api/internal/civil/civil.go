// Package civil holds dates as a bank prints them: a day, with no time and no
// time zone.
//
// A statement row dated 01/06/2026 happened on the first of June wherever the
// reader is. Storing it as a timestamp — midnight UTC — is how every such row
// came to show a day early in Panama and the first of each month fell into the
// previous one. A Date cannot be shifted by a zone because it has none.
package civil

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// Panama is the zone the supported banks' own timestamps are in. A fixed
// offset rather than time.LoadLocation: Panama has had no daylight saving time
// since 1908, and a fixed zone needs no tzdata in the container.
var Panama = time.FixedZone("America/Panama", -5*60*60)

// Date is a calendar day. The zero value is "no date".
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// Of returns t's calendar day in t's own location.
func Of(t time.Time) Date {
	y, m, d := t.Date()
	return Date{Year: y, Month: m, Day: d}
}

// Parse reads the YYYY-MM-DD form.
func Parse(s string) (Date, error) {
	return ParseLayout(time.DateOnly, s)
}

// ParseLayout reads a date in the given time.Parse layout, such as
// "02/01/2006" for the dd/mm/yyyy the supported banks print.
func ParseLayout(layout, s string) (Date, error) {
	t, err := time.Parse(layout, s)
	if err != nil {
		return Date{}, err
	}
	return Of(t), nil
}

// IsZero reports whether d is the zero Date.
func (d Date) IsZero() bool { return d == Date{} }

// Time is midnight at the start of d in loc.
func (d Date) Time(loc *time.Location) time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, loc)
}

// AddDays returns d moved by n days, which may be negative.
func (d Date) AddDays(n int) Date { return Of(d.Time(time.UTC).AddDate(0, 0, n)) }

// Compare returns -1, 0 or +1 as d is before, equal to or after e.
func (d Date) Compare(e Date) int { return d.Time(time.UTC).Compare(e.Time(time.UTC)) }

// Before reports whether d is strictly earlier than e.
func (d Date) Before(e Date) bool { return d.Compare(e) < 0 }

// After reports whether d is strictly later than e.
func (d Date) After(e Date) bool { return d.Compare(e) > 0 }

// String is YYYY-MM-DD, or "" for the zero Date.
func (d Date) String() string {
	if d.IsZero() {
		return ""
	}
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day)
}

// MarshalJSON writes "YYYY-MM-DD", or null for the zero Date, so a client
// never has to run it through a Date constructor that would apply its own
// zone.
func (d Date) MarshalJSON() ([]byte, error) {
	if d.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(d.String())
}

// UnmarshalJSON reads "YYYY-MM-DD" or null.
func (d *Date) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*d = Date{}
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	parsed, err := Parse(s)
	if err != nil {
		return fmt.Errorf("civil: %q is not a YYYY-MM-DD date", s)
	}
	*d = parsed
	return nil
}

// Value stores d in a DATE column; the zero Date is stored as NULL.
func (d Date) Value() (driver.Value, error) {
	if d.IsZero() {
		return nil, nil
	}
	return d.String(), nil
}

// Scan reads a DATE column. pgx hands a DATE over as a time.Time at midnight
// UTC, whose calendar day is the stored one.
func (d *Date) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*d = Date{}
	case time.Time:
		*d = Of(v.UTC())
	case string:
		parsed, err := Parse(v)
		if err != nil {
			return err
		}
		*d = parsed
	case []byte:
		parsed, err := Parse(string(v))
		if err != nil {
			return err
		}
		*d = parsed
	default:
		return fmt.Errorf("civil: cannot scan %T into a Date", src)
	}
	return nil
}
