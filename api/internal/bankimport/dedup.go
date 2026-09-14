package bankimport

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

// dedupKey computes what makes a row the "same movement" across two imports
// of overlapping data.
//
// Every available signal is folded in, including the bank's own reference
// when there is one — never treated as sufficient on its own. That
// conservatism came from a real Banco General credit card statement: a base
// insurance charge and its ITBMS tax line are two distinct movements that
// share one Referencia. Trusting the reference alone would have collapsed
// them into a single stored row and silently dropped one. occurrenceIndex is
// what makes two genuinely identical movements on the same day — two equal
// P2P payments to the same person — distinct from each other while staying
// stable across a re-import of the same file: the first file and a second,
// identical file both produce the pair (0, 1), never (0, 0) or (1, 2).
func dedupKey(accountID uuid.UUID, occurredAt time.Time, amountCents int64, description, externalRef string, occurrenceIndex int) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%d|%s|%s|%d",
		accountID, occurredAt.UTC().Format(time.RFC3339), amountCents,
		normaliseForDedup(description), externalRef, occurrenceIndex)
	return hex.EncodeToString(h.Sum(nil))
}

// occurrenceCounter assigns each row in one file the index of its exact
// duplicate seen so far, so the caller can pass it to dedupKey.
type occurrenceCounter struct {
	seen map[string]int
}

func newOccurrenceCounter() *occurrenceCounter {
	return &occurrenceCounter{seen: make(map[string]int)}
}

// next returns the occurrence index for a row shaped like this, and advances
// the counter. Rows are indexed in whatever order Parse returned them, which
// is the order they appeared in the bank's own file — stable across
// re-imports of the same bytes.
func (c *occurrenceCounter) next(occurredAt time.Time, amountCents int64, description, externalRef string) int {
	key := fmt.Sprintf("%s|%d|%s|%s", occurredAt.UTC().Format(time.RFC3339), amountCents, description, externalRef)
	index := c.seen[key]
	c.seen[key] = index + 1
	return index
}

// normaliseForDedup collapses whitespace and case so that two exports of the
// same movement whose description was re-wrapped or re-cased by the bank's
// own export tooling still hash identically.
func normaliseForDedup(s string) string {
	fields := strings.FieldsFunc(s, unicode.IsSpace)
	return strings.ToLower(strings.Join(fields, " "))
}
