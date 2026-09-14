package bankimport

import (
	"strings"

	"github.com/google/uuid"
)

// bgCardDefaultCategoryMap seeds category_rules for a newly linked Banco
// General credit card account, translating BG's own category labels onto
// corebank's default categories (categories.defaultSeeds).
//
// "Pagos", "Cargos" and "Viajes" are deliberately absent: the first two are
// not spending categories at all (a card payment and a bank fee/insurance
// line, respectively), and none of corebank's default categories honestly
// describes "Viajes" — leaving a transaction uncategorised until Juan
// categorises it once is more honest than guessing a bucket for it, and his
// own category_rules entry then takes over for the next import.
var bgCardDefaultCategoryMap = map[string]string{
	"Comida y Bebida":     "Alimentación",
	"Supermercados":       "Alimentación",
	"Salud":               "Salud",
	"Transporte":          "Transporte",
	"Entretenimiento":     "Entretenimiento",
	"Tiendas y almacenes": "Compras",
}

// matchHaystack builds the text category_rules matches against.
//
// The bank's own category label is folded in alongside the description when
// the format provides one (only the Banco General card statement does):
// matching on "Comida y Bebida" is a stable rule against a label BG itself
// assigns, rather than trying to out-guess every merchant name that might
// fall under it.
func matchHaystack(row ParsedRow) string {
	haystack := row.Description
	if row.RawCategory != "" {
		haystack = row.RawCategory + " " + row.Description
	}
	return strings.ToLower(haystack)
}

// CategoryRule is a user's own "if this text appears, use this category"
// rule — seeded with defaults for a Banco General card, and otherwise built
// up by hand as Juan categorises rows.
type CategoryRule struct {
	MatchText  string
	CategoryID uuid.UUID
	Priority   int
}

// matchCategory returns the category id of the first rule (highest priority
// first) whose match text appears in the row's haystack.
func matchCategory(row ParsedRow, rules []CategoryRule) (uuid.UUID, bool) {
	haystack := matchHaystack(row)
	for _, rule := range rules {
		if strings.Contains(haystack, strings.ToLower(rule.MatchText)) {
			return rule.CategoryID, true
		}
	}
	return uuid.UUID{}, false
}
