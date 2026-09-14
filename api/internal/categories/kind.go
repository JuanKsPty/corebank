package categories

import "fmt"

// Kind says whether a category groups money coming in or going out. A
// transaction's own sign already says which of the two happened; a
// category's kind is what lets the UI offer "Salario" only when picking a
// category for a deposit and "Comida" only for a withdrawal, rather than one
// flat list where half the options never apply.
type Kind string

const (
	KindIncome  Kind = "income"
	KindExpense Kind = "expense"
)

// ParseKind maps the wire representation to a Kind.
func ParseKind(s string) (Kind, error) {
	switch Kind(s) {
	case KindIncome, KindExpense:
		return Kind(s), nil
	default:
		return "", fmt.Errorf("categories: unknown kind %q", s)
	}
}

func (k Kind) String() string { return string(k) }
