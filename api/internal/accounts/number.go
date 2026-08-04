// Package accounts opens and reads customer accounts.
//
// It is the only place that knows an account has two halves: a row in PostgreSQL
// describing it and an account in the ledger holding its money. Callers ask for
// "an account with its balance" and never see the seam.
package accounts

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
)

// numberPrefix is the bank's own identifier, the first group of every account
// number. It matches the provided dataset so seeded and newly opened accounts
// are indistinguishable in the interface.
const numberPrefix = "4001"

// GenerateNumber returns an account number in the form 4001-NNNN-NNNN-NNNN.
//
// The twelve variable digits are random rather than sequential. A sequence would
// need a counter that every writer agrees on — and would make account numbers
// enumerable, which is worth avoiding for an identifier customers paste into
// transfer forms. Uniqueness is not left to chance: the caller retries on the
// database's unique constraint, which is the only authority that can settle it.
func GenerateNumber() (string, error) {
	groups := make([]string, 0, 4)
	groups = append(groups, numberPrefix)

	for range 3 {
		n, err := rand.Int(rand.Reader, big.NewInt(10000))
		if err != nil {
			return "", fmt.Errorf("accounts: generating account number: %w", err)
		}
		groups = append(groups, fmt.Sprintf("%04d", n.Int64()))
	}
	return strings.Join(groups, "-"), nil
}
