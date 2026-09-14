package categories

// defaultSeed is one of the categories every new customer starts with.
type defaultSeed struct {
	name string
	kind Kind
}

// defaultSeeds is what SeedDefaults inserts for a newly registered customer.
//
// A flat, opinionated starting set rather than an empty list: a customer who
// has never used the app should be able to categorise their first
// transaction immediately, not be sent to a settings page first. Every entry
// here is a system category (deletable, but not the thing "Sin categorizar"
// falls back to being confused with).
var defaultSeeds = []defaultSeed{
	{"Salario", KindIncome},
	{"Freelance", KindIncome},
	{"Inversiones", KindIncome},
	{"Otros ingresos", KindIncome},

	{"Alimentación", KindExpense},
	{"Transporte", KindExpense},
	{"Vivienda", KindExpense},
	{"Servicios", KindExpense},
	{"Salud", KindExpense},
	{"Entretenimiento", KindExpense},
	{"Compras", KindExpense},
	{"Educación", KindExpense},
	{"Suscripciones", KindExpense},
	{"Otros gastos", KindExpense},
}
