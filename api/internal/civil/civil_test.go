package civil

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDateNeverShiftsWithAZone(t *testing.T) {
	d, err := ParseLayout("02/01/2006", "01/06/2026")
	if err != nil {
		t.Fatal(err)
	}
	if d.String() != "2026-06-01" {
		t.Errorf("String() = %q, want 2026-06-01", d.String())
	}
	// The failure this package exists to prevent: the same day read back in
	// Panama must still be the first, not the 31st of May.
	if got := Of(d.Time(Panama)); got != d {
		t.Errorf("Of(d.Time(Panama)) = %v, want %v", got, d)
	}
}

func TestDateJSONRoundTrip(t *testing.T) {
	d := Date{2026, time.January, 15}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `"2026-01-15"` {
		t.Errorf("Marshal = %s", b)
	}
	var back Date
	if err := json.Unmarshal(b, &back); err != nil || back != d {
		t.Errorf("Unmarshal = %v, %v", back, err)
	}
	if b, _ := json.Marshal(Date{}); string(b) != "null" {
		t.Errorf("zero Date marshals to %s, want null", b)
	}
}

func TestAddDaysAndCompare(t *testing.T) {
	d := Date{2026, time.March, 1}
	if prev := d.AddDays(-1); prev != (Date{2026, time.February, 28}) {
		t.Errorf("AddDays(-1) = %v", prev)
	}
	if !d.AddDays(-1).Before(d) || !d.After(d.AddDays(-1)) {
		t.Error("Before/After disagree with AddDays")
	}
}

func TestScanDateColumn(t *testing.T) {
	var d Date
	if err := d.Scan(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)); err != nil || d.String() != "2026-06-01" {
		t.Errorf("Scan(time) = %v, %v", d, err)
	}
	if err := d.Scan(nil); err != nil || !d.IsZero() {
		t.Errorf("Scan(nil) = %v, %v", d, err)
	}
}
