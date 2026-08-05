package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/JuanKsPty/corebank/api/internal/llm"
	"github.com/JuanKsPty/corebank/api/internal/mcpserver"
	"github.com/JuanKsPty/corebank/api/internal/money"
)

// Fallback answers without a language model.
//
// It exists because the assistant must not be the one feature that disappears on a
// machine with no API key. It drives the *same* MCP tools through the *same*
// agentic loop, so the confirmation flow, the reservation, the card and the
// server-side settlement are all genuinely exercised — only the language
// understanding is replaced, by pattern matching over a handful of phrasings.
//
// It reports IsAI() == false and the interface labels it, because a rule-based
// matcher presented as an AI assistant would be a lie about the product. What it
// demonstrates is the plumbing; what it is not is an assistant.
type Fallback struct{}

func NewFallback() *Fallback { return &Fallback{} }

func (*Fallback) Name() string { return "reglas locales (sin IA)" }
func (*Fallback) IsAI() bool   { return false }

// Complete produces the next turn.
//
// Two situations. If the last turn is a tool result, the tools have run and it is
// time to describe what came back. Otherwise the customer has just written
// something and it has to be classified into a tool call.
func (f *Fallback) Complete(_ context.Context, req llm.Request) (llm.Reply, error) {
	if len(req.Messages) == 0 {
		return llm.Reply{Text: greeting}, nil
	}

	last := req.Messages[len(req.Messages)-1]
	if last.Role == llm.RoleTool {
		// A lookup was a step towards something, not the answer to it: the
		// customer asked to move money and the accounts had to be known first. So
		// their original message is reconsidered, now that it is.
		if isLookup(last) {
			if message, ok := lastUserMessage(req.Messages); ok {
				return f.classify(message, req.Messages), nil
			}
		}
		return llm.Reply{Text: f.describe(req.Messages)}, nil
	}
	if last.Role != llm.RoleUser {
		// Nothing new was asked; say nothing rather than loop.
		return llm.Reply{}, nil
	}
	return f.classify(last.Text, req.Messages), nil
}

// lookupCallID marks a tool call made to learn something rather than to answer.
//
// Without the distinction the loop cannot tell "show me my accounts" from "I need
// the accounts before I can prepare this transfer", and would answer the second by
// listing accounts — leaving the customer's actual request unaddressed.
const lookupCallID = "fallback-lookup"

func isLookup(toolTurn llm.Message) bool {
	for _, result := range toolTurn.Results {
		if result.CallID == lookupCallID {
			return true
		}
	}
	return false
}

func lastUserMessage(messages []llm.Message) (string, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == llm.RoleUser && messages[i].Text != "" {
			return messages[i].Text, true
		}
	}
	return "", false
}

// lookupAccounts asks for the customer's accounts as a step towards an operation.
func lookupAccounts() llm.Reply {
	reply := call(mcpserver.ToolListAccounts, map[string]any{})
	reply.ToolCalls[0].ID = lookupCallID
	return reply
}

const greeting = "Hola. Puedo consultar tus saldos y movimientos, ingresar dinero, " +
	"y preparar retiros y transferencias para que los confirmes."

const helpText = "Sin una clave de IA configurada entiendo frases concretas. Prueba con:\n\n" +
	"• «¿cuánto dinero tengo?»\n" +
	"• «muéstrame mis últimos 5 movimientos»\n" +
	"• «ingresa $200 en mi cuenta»\n" +
	"• «retira $50»\n" +
	"• «transfiere $100 a la cuenta 4001-6629-5214-0685»\n\n" +
	"Configura ANTHROPIC_API_KEY para conversar con un modelo de verdad."

// Patterns are matched against the message with accents and case normalised, so
// «cuánto» and «cuanto» behave the same.
var (
	reBalance     = regexp.MustCompile(`\b(saldo|saldos|cuanto (dinero|tengo|hay)|balance|cuentas|mi dinero)\b`)
	reHistory     = regexp.MustCompile(`\b(movimiento|movimientos|transaccion|transacciones|historial|ultimos|ultimas|gastos)\b`)
	reDeposit     = regexp.MustCompile(`\b(ingresa|ingresar|deposita|depositar|deposito|abona|abonar|mete|meter)\b`)
	reWithdraw    = regexp.MustCompile(`\b(retira|retirar|retiro|saca|sacar|extrae|extraer)\b`)
	reTransfer    = regexp.MustCompile(`\b(transfiere|transferir|transferencia|envia|enviar|manda|mandar|paga|pagar|pasa|pasar|mueve|mover)\b`)
	reHelp        = regexp.MustCompile(`\b(ayuda|help|que puedes|como funciona|para que sirves)\b`)
	reGreeting    = regexp.MustCompile(`^\W*(hola|buenas|buenos dias|buenas tardes|buenas noches|hey|que tal|hi|hello)\W*$`)
	reAccount     = regexp.MustCompile(`\b\d{4}[- ]?\d{4}[- ]?\d{4}[- ]?\d{4}\b`)
	reCount       = regexp.MustCompile(`\b(\d{1,3})\s*(movimiento|movimientos|transaccion|transacciones|ultimos|ultimas)?\b`)
	reAccountKind = regexp.MustCompile(`\b(ahorro|ahorros|corriente|inversion|inversiones)\b`)
	// Amounts: "$1,234.56", "1234.56", "100". The thousands separator is accepted
	// here and stripped, because a person typing into a chat writes it — unlike the
	// REST API, where the amount is machine-generated and grouping is a red flag.
	reAmount = regexp.MustCompile(`(?:\$\s*)?(\d{1,3}(?:,\d{3})+(?:\.\d{1,2})?|\d+(?:\.\d{1,2})?)`)
)

// classify turns a customer message into a tool call.
func (f *Fallback) classify(message string, history []llm.Message) llm.Reply {
	text := normalise(message)

	switch {
	case reGreeting.MatchString(text):
		return llm.Reply{Text: greeting}

	case reHelp.MatchString(text):
		return llm.Reply{Text: helpText}

	// Transfers are checked before withdrawals because "pasa $200 de ahorros a
	// corriente" contains no withdrawal verb but "saca $50 y mándalo a…" contains
	// both, and the transfer is the more specific reading.
	case reTransfer.MatchString(text):
		return f.transfer(text, history)

	case reWithdraw.MatchString(text):
		return f.withdraw(text, history)

	case reDeposit.MatchString(text):
		return f.deposit(text, history)

	case reHistory.MatchString(text):
		return f.history(text)

	case reBalance.MatchString(text):
		return call(mcpserver.ToolListAccounts, map[string]any{})

	default:
		return llm.Reply{Text: "No he entendido la petición.\n\n" + helpText}
	}
}

func (f *Fallback) deposit(text string, history []llm.Message) llm.Reply {
	amount, ok := amountIn(text)
	if !ok {
		return llm.Reply{Text: "¿Cuánto quieres ingresar? Indícame el monto, por ejemplo «ingresa $200»."}
	}

	args := map[string]any{"amount": amount, "description": "Ingreso desde el asistente"}
	if account, reply := f.resolveAccount(text, history); reply != nil {
		return *reply
	} else if account != "" {
		args["account_number"] = account
	}
	return call(mcpserver.ToolDeposit, args)
}

func (f *Fallback) withdraw(text string, history []llm.Message) llm.Reply {
	amount, ok := amountIn(text)
	if !ok {
		return llm.Reply{Text: "¿Cuánto quieres retirar? Indícame el monto, por ejemplo «retira $50»."}
	}

	args := map[string]any{"amount": amount, "description": "Retiro desde el asistente"}
	if account, reply := f.resolveAccount(text, history); reply != nil {
		return *reply
	} else if account != "" {
		args["account_number"] = account
	}
	return call(mcpserver.ToolPrepareWithdrawal, args)
}

func (f *Fallback) transfer(text string, history []llm.Message) llm.Reply {
	amount, ok := amountIn(text)
	if !ok {
		return llm.Reply{Text: "¿Cuánto quieres transferir? Por ejemplo «transfiere $100 a la cuenta 4001-6629-5214-0685»."}
	}

	numbers := reAccount.FindAllString(text, -1)
	kinds := reAccountKind.FindAllString(text, -1)

	// Two account types named means a move between the customer's own accounts:
	// "pasa $200 de ahorros a corriente". That needs their account list, so if it
	// is not already in the conversation the tools are asked for it first and this
	// message is reconsidered on the next pass.
	if len(numbers) == 0 && len(kinds) >= 2 {
		owned, ok := accountsFromHistory(history)
		if !ok {
			return lookupAccounts()
		}
		source, found := matchKind(owned, kinds[0])
		if !found {
			return llm.Reply{Text: fmt.Sprintf("No encuentro una cuenta de %s entre las tuyas.", kinds[0])}
		}
		destination, found := matchKind(owned, kinds[len(kinds)-1])
		if !found {
			return llm.Reply{Text: fmt.Sprintf("No encuentro una cuenta de %s entre las tuyas.", kinds[len(kinds)-1])}
		}
		if source == destination {
			return llm.Reply{Text: "El origen y el destino son la misma cuenta."}
		}
		return call(mcpserver.ToolPrepareTransfer, map[string]any{
			"amount":         amount,
			"account_number": source,
			"to_account":     destination,
			"description":    "Traspaso desde el asistente",
		})
	}

	if len(numbers) == 0 {
		return llm.Reply{Text: "¿A qué cuenta quieres transferir? Dime el número, por ejemplo 4001-6629-5214-0685."}
	}

	args := map[string]any{
		// The last number in the sentence is the destination: "transfiere $100 de
		// la 4001-…-0001 a la 4001-…-0685" reads that way, and so does a sentence
		// with only a destination.
		"to_account":  normaliseAccount(numbers[len(numbers)-1]),
		"amount":      amount,
		"description": "Transferencia desde el asistente",
	}
	if len(numbers) > 1 {
		args["account_number"] = normaliseAccount(numbers[0])
	} else {
		// Only a destination was given, so the source still has to be settled —
		// asked for rather than guessed when the customer has several accounts.
		source, reply := f.resolveAccount(withoutAccounts(text), history)
		if reply != nil {
			return *reply
		}
		if source != "" {
			args["account_number"] = source
		}
	}
	return call(mcpserver.ToolPrepareTransfer, args)
}

func (f *Fallback) history(text string) llm.Reply {
	args := map[string]any{"limit": 10}
	// A number in the sentence is a count, unless it is an account number.
	if m := reCount.FindStringSubmatch(reAccount.ReplaceAllString(text, "")); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 && n <= 50 {
			args["limit"] = n
		}
	}
	if numbers := reAccount.FindAllString(text, -1); len(numbers) > 0 {
		args["account_number"] = normaliseAccount(numbers[0])
	}
	return call(mcpserver.ToolListTransactions, args)
}

// resolveAccount picks the account an operation should debit or credit.
//
// An explicit number wins. A named type — "de ahorros" — is looked up among the
// customer's accounts. With neither, the accounts are consulted anyway: a customer
// with one account needs no question, and a customer with three must be asked
// rather than have one chosen for them. Either way the accounts have to be known
// first, so if they are not in the conversation yet the tools are asked for them
// and the message is reconsidered on the next pass of the loop.
func (f *Fallback) resolveAccount(text string, history []llm.Message) (string, *llm.Reply) {
	if numbers := reAccount.FindAllString(text, -1); len(numbers) > 0 {
		return normaliseAccount(numbers[0]), nil
	}

	owned, ok := accountsFromHistory(history)
	if !ok {
		reply := lookupAccounts()
		return "", &reply
	}

	if kind := reAccountKind.FindString(text); kind != "" {
		number, found := matchKind(owned, kind)
		if !found {
			reply := llm.Reply{Text: fmt.Sprintf("No encuentro una cuenta de %s entre las tuyas.", kind)}
			return "", &reply
		}
		return number, nil
	}

	switch len(owned) {
	case 0:
		return "", &llm.Reply{Text: "No veo ninguna cuenta a tu nombre."}
	case 1:
		// The only account there is; naming it explicitly is clearer to the tool
		// than leaving it to infer the same thing.
		for _, number := range owned {
			return number, nil
		}
		return "", nil
	default:
		var options []string
		for kind, number := range owned {
			options = append(options, fmt.Sprintf("• %s (%s)", number, spanishKind(kind)))
		}
		sort.Strings(options)
		return "", &llm.Reply{Text: "¿Desde cuál de tus cuentas?\n\n" + strings.Join(options, "\n")}
	}
}

// describe renders the tools' results as prose.
func (f *Fallback) describe(messages []llm.Message) string {
	last := messages[len(messages)-1]

	// The call that produced these results, so each is described in the right
	// terms.
	var calls []llm.ToolCall
	for i := len(messages) - 2; i >= 0; i-- {
		if messages[i].Role == llm.RoleAssistant && len(messages[i].ToolCalls) > 0 {
			calls = append(calls, messages[i].ToolCalls...)
			break
		}
	}

	var parts []string
	for i, result := range last.Results {
		name := ""
		if i < len(calls) {
			name = calls[i].Name
		}
		if result.IsError {
			// Only the customer's half of the message; the rest is guidance meant
			// for a model.
			parts = append(parts, mcpserver.CustomerPart(result.Content))
			continue
		}
		parts = append(parts, renderResult(name, result.Content))
	}
	if len(parts) == 0 {
		return "Listo."
	}
	return strings.Join(parts, "\n\n")
}

// renderResult turns one tool's JSON into Spanish.
// grouped renders a tool's decimal amount the way a person writes it. The tools
// return the exact wire form ("27882.74"); prose needs "27,882.74".
func grouped(amount string) string {
	cents, err := money.Parse(amount)
	if err != nil {
		return amount
	}
	return cents.Display()
}

func renderResult(tool, content string) string {
	switch tool {
	case mcpserver.ToolListAccounts:
		var out struct {
			Accounts []struct {
				AccountNumber string `json:"account_number"`
				AccountType   string `json:"account_type"`
				Alias         string `json:"alias"`
				Available     string `json:"available"`
				Held          string `json:"held"`
			} `json:"accounts"`
			TotalAvailable string `json:"total_available"`
		}
		if json.Unmarshal([]byte(content), &out) != nil || len(out.Accounts) == 0 {
			return "No tienes cuentas abiertas."
		}
		lines := []string{fmt.Sprintf("Tienes $%s disponibles en total:", grouped(out.TotalAvailable))}
		for _, a := range out.Accounts {
			// The name the customer gave the account, when they gave it one. Without
			// this the rule-based engine would answer "cuánto tengo" with a list of
			// three identical "(ahorros)" entries — the exact problem naming accounts
			// exists to solve, reintroduced by the one engine that still has to work
			// when there is no API key.
			label := spanishKind(a.AccountType)
			if a.Alias != "" {
				label = a.Alias
			}
			line := fmt.Sprintf("• %s (%s): $%s", a.AccountNumber, label, grouped(a.Available))
			if a.Held != "0.00" && a.Held != "" {
				line += fmt.Sprintf(" — $%s retenidos por una operación sin confirmar", grouped(a.Held))
			}
			lines = append(lines, line)
		}
		return strings.Join(lines, "\n")

	case mcpserver.ToolGetBalance:
		var out struct {
			AccountNumber string `json:"account_number"`
			Available     string `json:"available"`
			Held          string `json:"held"`
		}
		if json.Unmarshal([]byte(content), &out) != nil {
			return "No he podido leer el saldo."
		}
		text := fmt.Sprintf("La cuenta %s tiene $%s disponibles.", out.AccountNumber, grouped(out.Available))
		if out.Held != "0.00" && out.Held != "" {
			text += fmt.Sprintf(" Hay $%s retenidos por una operación sin confirmar.", grouped(out.Held))
		}
		return text

	case mcpserver.ToolListTransactions:
		var out struct {
			Transactions []struct {
				Date        string `json:"date"`
				Kind        string `json:"kind"`
				Amount      string `json:"amount"`
				Description string `json:"description"`
			} `json:"transactions"`
		}
		if json.Unmarshal([]byte(content), &out) != nil || len(out.Transactions) == 0 {
			return "No hay movimientos que mostrar."
		}
		lines := []string{"Estos son tus movimientos más recientes:"}
		for _, t := range out.Transactions {
			date := t.Date
			if len(date) >= 10 {
				date = date[:10]
			}
			lines = append(lines, fmt.Sprintf("• %s — %s de $%s: %s",
				date, spanishMovement(t.Kind), grouped(t.Amount), t.Description))
		}
		return strings.Join(lines, "\n")

	case mcpserver.ToolDeposit:
		var out struct {
			Amount        string `json:"amount"`
			AccountNumber string `json:"account_number"`
			NewBalance    string `json:"new_balance"`
		}
		if json.Unmarshal([]byte(content), &out) != nil {
			return "El ingreso se ha realizado."
		}
		text := fmt.Sprintf("He ingresado $%s en la cuenta %s.", grouped(out.Amount), out.AccountNumber)
		if out.NewBalance != "" {
			text += fmt.Sprintf(" Su saldo disponible es ahora $%s.", grouped(out.NewBalance))
		}
		return text

	case mcpserver.ToolPrepareTransfer, mcpserver.ToolPrepareWithdrawal:
		var out struct {
			Amount             string `json:"amount"`
			FromAccount        string `json:"from_account"`
			ToAccount          string `json:"to_account"`
			BalanceIfConfirmed string `json:"balance_if_confirmed"`
		}
		if json.Unmarshal([]byte(content), &out) != nil {
			return "He preparado la operación y queda a la espera de que la confirmes."
		}
		var text string
		if tool == mcpserver.ToolPrepareWithdrawal {
			text = fmt.Sprintf("He preparado un retiro de $%s de la cuenta %s.", grouped(out.Amount), out.FromAccount)
		} else {
			text = fmt.Sprintf("He preparado una transferencia de $%s de %s a %s.",
				grouped(out.Amount), out.FromAccount, out.ToAccount)
		}
		if out.BalanceIfConfirmed != "" {
			text += fmt.Sprintf(" Si la confirmas te quedarán $%s disponibles.", grouped(out.BalanceIfConfirmed))
		}
		// Stated explicitly, because this is the one thing the customer must not
		// misread. Deliberately without pointing at the card: this text is stored and
		// read again later, when the card is gone because the hold was confirmed,
		// cancelled or expired, and a sentence naming a place on the screen would then
		// be describing something that is not there.
		return text + "\n\nLos fondos están reservados pero **el dinero aún no se ha movido**: " +
			"queda a la espera de que lo confirmes."

	default:
		return content
	}
}

// --- helpers ----------------------------------------------------------------

func call(name string, args map[string]any) llm.Reply {
	encoded, err := json.Marshal(args)
	if err != nil {
		return llm.Reply{Text: "No he podido preparar la consulta."}
	}
	return llm.Reply{ToolCalls: []llm.ToolCall{{
		// The loop pairs results with calls by id, and there is no upstream
		// service to supply one here.
		ID:        "fallback-" + name,
		Name:      name,
		Arguments: encoded,
	}}}
}

// accountsFromHistory finds the customer's accounts in an earlier tool result.
func accountsFromHistory(messages []llm.Message) (map[string]string, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != llm.RoleTool {
			continue
		}
		for _, result := range messages[i].Results {
			if result.IsError {
				continue
			}
			var out struct {
				Accounts []struct {
					AccountNumber string `json:"account_number"`
					AccountType   string `json:"account_type"`
				} `json:"accounts"`
			}
			if json.Unmarshal([]byte(result.Content), &out) != nil || len(out.Accounts) == 0 {
				continue
			}
			byKind := make(map[string]string, len(out.Accounts))
			for _, a := range out.Accounts {
				byKind[a.AccountType] = a.AccountNumber
			}
			return byKind, true
		}
	}
	return nil, false
}

// matchKind maps a Spanish account-type word to one of the customer's accounts.
func matchKind(owned map[string]string, word string) (string, bool) {
	var kind string
	switch {
	case strings.HasPrefix(word, "ahorro"):
		kind = "savings"
	case strings.HasPrefix(word, "corriente"):
		kind = "checking"
	case strings.HasPrefix(word, "inversi"):
		kind = "investment"
	default:
		return "", false
	}
	number, ok := owned[kind]
	return number, ok
}

// amountIn extracts the amount, as text.
//
// The digits the customer typed are carried straight to the tool. Parsing them into
// a float here would reintroduce the very rounding the rest of the system avoids.
func amountIn(text string) (string, bool) {
	// Account numbers are full of digits; removing them first stops the amount
	// matcher finding one.
	candidate := reAmount.FindStringSubmatch(reAccount.ReplaceAllString(text, ""))
	if candidate == nil {
		return "", false
	}
	amount := strings.ReplaceAll(candidate[1], ",", "")
	if amount == "" || amount == "0" {
		return "", false
	}
	return amount, true
}

// withoutAccounts removes account numbers from a sentence, so a later match for
// an account *type* is not confused by the digits.
func withoutAccounts(text string) string { return reAccount.ReplaceAllString(text, "") }

func normaliseAccount(raw string) string {
	digits := strings.NewReplacer("-", "", " ", "").Replace(raw)
	if len(digits) != 16 {
		return raw
	}
	// Back into the grouped form the rest of the system uses.
	return digits[0:4] + "-" + digits[4:8] + "-" + digits[8:12] + "-" + digits[12:16]
}

// normalise lower-cases and strips the accents Spanish input carries, so one
// pattern matches «cuánto» and «cuanto» alike.
func normalise(text string) string {
	replacer := strings.NewReplacer(
		"á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u", "ñ", "n",
		"Á", "a", "É", "e", "Í", "i", "Ó", "o", "Ú", "u", "Ü", "u", "Ñ", "n",
	)
	return replacer.Replace(strings.ToLower(text))
}

func spanishKind(kind string) string {
	switch kind {
	case "savings":
		return "ahorros"
	case "checking":
		return "corriente"
	case "investment":
		return "inversión"
	default:
		return kind
	}
}

func spanishMovement(kind string) string {
	switch kind {
	case "deposit":
		return "ingreso"
	case "withdrawal":
		return "retiro"
	case "transfer":
		return "transferencia"
	case "internal_transfer":
		return "traspaso"
	default:
		return kind
	}
}
