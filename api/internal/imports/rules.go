package imports

import (
	"strings"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/statement"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

// defaultCardRules maps Banco General's own card categories onto the default
// categories every user starts with, so a first card import is already mostly
// categorised. "Pagos" and "Cargos" are absent on purpose: a payment is a
// transfer and a charge is the bank's fee, which the importer's kind already
// says; neither is a spending category. "Viajes" has no honest default either.
var defaultCardRules = map[string]string{
	"Comida y Bebida":     "Alimentación",
	"Supermercados":       "Alimentación",
	"Salud":               "Salud",
	"Transporte":          "Transporte",
	"Entretenimiento":     "Entretenimiento",
	"Tiendas y almacenes": "Compras",
}

// match is what a rule decided for a line.
type match struct {
	categoryID *uuid.UUID
	setKind    *string
}

// matchRules tries the user's rules, in the order the store returns them
// (priority, then the most specific text), against the bank's category and
// the line's description. The first rule that names a category decides the
// category; the first that sets a kind decides the kind.
func matchRules(l statement.Line, rules []store.CategoryRule) match {
	haystack := strings.ToLower(l.BankCategory + " " + l.Description)
	var m match
	for _, r := range rules {
		text := strings.ToLower(strings.TrimSpace(r.MatchText))
		if text == "" || !strings.Contains(haystack, text) {
			continue
		}
		if m.categoryID == nil && r.CategoryID != nil {
			m.categoryID = r.CategoryID
		}
		if m.setKind == nil && r.SetKind != nil {
			m.setKind = r.SetKind
		}
		if m.categoryID != nil && m.setKind != nil {
			break
		}
	}
	return m
}
