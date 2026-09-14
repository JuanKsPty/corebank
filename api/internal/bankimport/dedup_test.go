package bankimport

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDedupKeyIsStableAcrossReimports(t *testing.T) {
	accountID := uuid.New()
	when := time.Date(2026, 3, 18, 0, 0, 0, 0, time.UTC)

	a := dedupKey(accountID, when, -214, "VUDU VERAGUAS PA", "VT260790112017840000141", 0)
	b := dedupKey(accountID, when, -214, "VUDU VERAGUAS PA", "VT260790112017840000141", 0)
	if a != b {
		t.Errorf("dedupKey is not deterministic: %s != %s", a, b)
	}
}

func TestDedupKeyDoesNotCollapseSharedReferenceRows(t *testing.T) {
	// The real finding that shaped this design: a Banco General credit card
	// statement had a base insurance charge and its ITBMS tax line share one
	// Referencia. They must hash differently, or the second import of the
	// same statement would look like the second row was a duplicate of the
	// first.
	accountID := uuid.New()
	when := time.Date(2026, 2, 17, 0, 0, 0, 0, time.UTC)

	base := dedupKey(accountID, when, -13, "SEGURO DE DESGRAVAMEN", "10000000980217998024280", 0)
	tax := dedupKey(accountID, when, -1, "ITBMS CARGO POR SEGURO", "10000000980217998024280", 0)
	if base == tax {
		t.Fatal("two distinct charges sharing a bank reference hashed to the same dedup key")
	}
}

func TestDedupKeyDistinguishesGenuineDuplicatesInOneFile(t *testing.T) {
	// Two real, distinct P2P payments of the same amount to the same person
	// on the same day: occurrenceIndex must keep them apart.
	accountID := uuid.New()
	when := time.Date(2026, 4, 12, 0, 0, 0, 0, time.UTC)

	first := dedupKey(accountID, when, -150, "YAPPY BG A PRUEBA", "", 0)
	second := dedupKey(accountID, when, -150, "YAPPY BG A PRUEBA", "", 1)
	if first == second {
		t.Fatal("two occurrences of the same movement hashed identically")
	}
}

func TestOccurrenceCounterIsStableAcrossReimports(t *testing.T) {
	when := time.Date(2026, 4, 12, 0, 0, 0, 0, time.UTC)

	run := func() []int {
		c := newOccurrenceCounter()
		return []int{
			c.next(when, -150, "yappy bg a prueba", ""),
			c.next(when, -150, "yappy bg a prueba", ""),
			c.next(when, -200, "otro movimiento", ""),
		}
	}

	first, second := run(), run()
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("occurrence index %d differs across runs: %d != %d", i, first[i], second[i])
		}
	}
	if first[0] == first[1] {
		t.Error("two occurrences of an identical row got the same index")
	}
}

func TestNormaliseForDedupCollapsesWhitespaceAndCase(t *testing.T) {
	a := normaliseForDedup("  VUDU   Veraguas  PA ")
	b := normaliseForDedup("VUDU veraguas PA")
	if a != b {
		t.Errorf("normaliseForDedup(%q) = %q, normaliseForDedup(%q) = %q, want equal", "  VUDU   Veraguas  PA ", a, "VUDU veraguas PA", b)
	}
}
