package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/JuanKsPty/corebank/api/internal/categories"
	"github.com/JuanKsPty/corebank/api/internal/civil"
	"github.com/JuanKsPty/corebank/api/internal/investments"
	"github.com/JuanKsPty/corebank/api/internal/money"
	"github.com/JuanKsPty/corebank/api/internal/proposals"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

// The analyst's tools: reports over the owner's movements, and proposals.
//
// A propose_* tool never changes anything. It checks the change and stores it
// for the owner, who applies or discards it from a card in the chat.
const (
	ToolListCategories     = "list_categories"
	ToolSpending           = "spending_by_category"
	ToolCashFlow           = "cash_flow"
	ToolExplainDrift       = "explain_drift"
	ToolTransferSuggest    = "list_transfer_suggestions"
	ToolPortfolio          = "get_portfolio"
	ToolListRules          = "list_rules"
	ToolProposeCategory    = "propose_recategorize"
	ToolProposeNote        = "propose_note"
	ToolProposeRule        = "propose_rule"
	ToolProposeTransfer    = "propose_transfer_decision"
	ToolProposeCheckpoint  = "propose_checkpoint"
	proposalPendingMessage = "La persona verá una tarjeta para aplicarla o descartarla. " +
		"Todavía no se ha hecho nada: no digas que ya está hecho."
)

type periodArgs struct {
	From string `json:"from,omitempty" jsonschema:"first day, YYYY-MM-DD, inclusive. Defaults to the first of this month."`
	To   string `json:"to,omitempty" jsonschema:"last day, YYYY-MM-DD, inclusive. Defaults to today."`
}

type spendingArgs struct {
	periodArgs
	Income bool `json:"income,omitempty" jsonschema:"true for income by category instead of spending"`
}

type categoryRow struct {
	CategoryID string `json:"category_id,omitempty"`
	Name       string `json:"name"`
	Total      string `json:"total" jsonschema:"in dollars, positive"`
	Count      int    `json:"count"`
}

type spendingResult struct {
	From       string        `json:"from"`
	To         string        `json:"to"`
	Total      string        `json:"total"`
	Categories []categoryRow `json:"categories"`
	Note       string        `json:"note"`
}

type flowRow struct {
	Month string `json:"month" jsonschema:"YYYY-MM"`
	In    string `json:"in" jsonschema:"income, in dollars"`
	Out   string `json:"out" jsonschema:"spending, in dollars"`
}

type flowResult struct {
	Months []flowRow `json:"months"`
	Note   string    `json:"note"`
}

type categoriesResult struct {
	Categories []categoryOption `json:"categories"`
}

type categoryOption struct {
	ID   string `json:"id"`
	Name string `json:"name" jsonschema:"a child category is shown as Padre › Hija"`
	Kind string `json:"kind" jsonschema:"income or expense"`
}

type accountArgs struct {
	AccountID string `json:"account_id" jsonschema:"the account, from list_accounts"`
}

type driftCheck struct {
	AsOf       string `json:"as_of"`
	Source     string `json:"source" jsonschema:"statement_opening, statement_closing, broker or manual"`
	Stated     string `json:"stated"`
	Computed   string `json:"computed"`
	Difference string `json:"difference" jsonschema:"stated minus computed; 0.00 means it matches the bank"`
}

type driftResult struct {
	Account    string       `json:"account"`
	Anchored   bool         `json:"anchored"`
	Opening    string       `json:"opening" jsonschema:"the derived balance before the first imported movement"`
	Checks     []driftCheck `json:"checks"`
	Gaps       []string     `json:"gaps,omitempty" jsonschema:"statements that start more than a day after the previous one ends: movements in between were never imported"`
	FirstBreak *movementRef `json:"first_break,omitempty" jsonschema:"the movement where the running balance first stops matching the bank's printed balance"`
	Note       string       `json:"note,omitempty"`
}

type movementRef struct {
	ID          string `json:"id"`
	Date        string `json:"date"`
	Description string `json:"description"`
	Amount      string `json:"amount"`
	BankBalance string `json:"bank_balance,omitempty"`
	Difference  string `json:"difference,omitempty" jsonschema:"bank balance minus computed"`
}

type suggestionsResult struct {
	Pairs []pairRow `json:"pairs"`
	Note  string    `json:"note,omitempty"`
}

type pairRow struct {
	Out movementRef `json:"out" jsonschema:"the side money left"`
	In  movementRef `json:"in" jsonschema:"the side money arrived"`
}

type portfolioResult struct {
	Holdings   string        `json:"holdings"`
	Positions  []positionRow `json:"positions"`
	LastSync   string        `json:"last_sync,omitempty"`
	SyncStatus string        `json:"sync_status" jsonschema:"never, ok or error"`
	SyncError  string        `json:"sync_error,omitempty"`
}

type positionRow struct {
	Symbol      string  `json:"symbol"`
	Quantity    float64 `json:"quantity"`
	MarketValue string  `json:"market_value,omitempty"`
	CostBasis   string  `json:"cost_basis,omitempty"`
	AsOf        string  `json:"as_of"`
}

type rulesResult struct {
	Rules []ruleRow `json:"rules"`
}

type ruleRow struct {
	MatchText string `json:"match_text"`
	Category  string `json:"category,omitempty"`
	Transfer  bool   `json:"transfer"`
}

type proposeCategoryArgs struct {
	EntryID    string `json:"entry_id" jsonschema:"the movement's id, from list_movements"`
	CategoryID string `json:"category_id,omitempty" jsonschema:"the category's id, from list_categories"`
	Transfer   *bool  `json:"transfer,omitempty" jsonschema:"true if it is a transfer between the owner's own accounts, false if it is not"`
}

type proposeNoteArgs struct {
	EntryID string `json:"entry_id" jsonschema:"the movement's id, from list_movements"`
	Note    string `json:"note" jsonschema:"up to 500 characters; empty removes the note"`
}

type proposeRuleArgs struct {
	MatchText       string `json:"match_text" jsonschema:"text the movement's description contains, up to 60 characters"`
	CategoryID      string `json:"category_id,omitempty" jsonschema:"the category's id, from list_categories"`
	Transfer        bool   `json:"transfer,omitempty" jsonschema:"matching movements are transfers between the owner's accounts"`
	ApplyToExisting bool   `json:"apply_to_existing,omitempty" jsonschema:"also file the movements already imported"`
}

type proposeTransferArgs struct {
	OutEntryID string `json:"out_entry_id" jsonschema:"the movement money left, negative"`
	InEntryID  string `json:"in_entry_id" jsonschema:"the movement money arrived, the same amount positive"`
	Confirm    bool   `json:"confirm" jsonschema:"true to confirm it is one transfer, false to dismiss the suggestion"`
}

type proposeCheckpointArgs struct {
	AccountID string `json:"account_id" jsonschema:"the account, from list_accounts"`
	AsOf      string `json:"as_of" jsonschema:"the day the balance is at the close of, YYYY-MM-DD"`
	Balance   string `json:"balance" jsonschema:"in dollars, signed from the owner's view: for a card that owes 14.30 pass -14.30"`
	Note      string `json:"note,omitempty"`
}

type proposalResult struct {
	ProposalID string `json:"proposal_id"`
	Kind       string `json:"kind"`
	Summary    string `json:"summary"`
	Status     string `json:"status"`
	Note       string `json:"note"`
}

func addAnalystTools(server *mcp.Server, deps Deps, userID uuid.UUID) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        ToolListCategories,
		Description: "Lista las categorías de la persona con su id. Úsala antes de proponer una categoría o una regla.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, categoriesResult, error) {
		names, list, err := categoryNames(ctx, deps.Categories, userID)
		if err != nil {
			return nil, categoriesResult{}, err
		}
		out := categoriesResult{Categories: make([]categoryOption, 0, len(list))}
		for _, c := range list {
			out.Categories = append(out.Categories, categoryOption{ID: c.ID.String(), Name: names[c.ID], Kind: string(c.Kind)})
		}
		sort.Slice(out.Categories, func(i, j int) bool { return out.Categories[i].Name < out.Categories[j].Name })
		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: ToolSpending,
		Description: "Suma el gasto (o el ingreso) de la persona por categoría en un período, en todas sus cuentas. " +
			"Las transferencias entre sus cuentas, los pagos de tarjeta y la compraventa de valores no cuentan.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args spendingArgs) (*mcp.CallToolResult, spendingResult, error) {
		from, to, err := period(args.periodArgs)
		if err != nil {
			return nil, spendingResult{}, err
		}
		totals := deps.Reports.Spending
		if args.Income {
			totals = deps.Reports.Income
		}
		rows, err := totals(ctx, userID, from, to, nil)
		if err != nil {
			return nil, spendingResult{}, err
		}
		names, _, err := categoryNames(ctx, deps.Categories, userID)
		if err != nil {
			return nil, spendingResult{}, err
		}
		out := spendingResult{From: from.String(), To: to.String(), Categories: []categoryRow{},
			Note: "Un reembolso resta del gasto de su categoría."}
		var sum money.Cents
		for _, r := range rows {
			row := categoryRow{Name: "Sin categoría", Total: r.Total.String(), Count: r.Count}
			if r.CategoryID != nil {
				row.CategoryID = r.CategoryID.String()
				row.Name = names[*r.CategoryID]
			}
			sum += r.Total
			out.Categories = append(out.Categories, row)
		}
		out.Total = sum.String()
		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        ToolCashFlow,
		Description: "Ingresos y gastos de la persona por mes en un período. Sin transferencias entre sus cuentas.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args periodArgs) (*mcp.CallToolResult, flowResult, error) {
		if args.From == "" {
			today := civil.Of(time.Now().In(civil.Panama))
			args.From = civil.Date{Year: today.Year, Month: today.Month, Day: 1}.AddDays(-150).String()
		}
		from, to, err := period(args)
		if err != nil {
			return nil, flowResult{}, err
		}
		points, err := deps.Reports.Flow(ctx, userID, from, to, true)
		if err != nil {
			return nil, flowResult{}, err
		}
		out := flowResult{Months: make([]flowRow, 0, len(points)), Note: "in y out en dólares positivos."}
		for _, p := range points {
			out.Months = append(out.Months, flowRow{Month: p.Day.String()[:7], In: p.In.String(), Out: p.Out.String()})
		}
		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: ToolExplainDrift,
		Description: "Explica por qué el saldo de una cuenta no cuadra con el del banco: cada saldo declarado contra el calculado, " +
			"los huecos entre estados de cuenta y el primer movimiento donde deja de cuadrar.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args accountArgs) (*mcp.CallToolResult, driftResult, error) {
		id, err := parseID(args.AccountID, "account_id", "list_accounts")
		if err != nil {
			return nil, driftResult{}, err
		}
		rec, err := deps.Accounts.Reconcile(ctx, userID, id)
		if err != nil {
			return nil, driftResult{}, toolError(err)
		}
		out := driftResult{Account: rec.Account.DisplayName, Anchored: rec.Account.Anchored, Opening: rec.Opening.String(),
			Checks: []driftCheck{}}
		if !rec.Account.Anchored {
			out.Note = "La cuenta no tiene saldo inicial: ingresa el saldo de un estado de cuenta con propose_checkpoint."
		}
		for _, c := range rec.Checks {
			out.Checks = append(out.Checks, driftCheck{AsOf: c.Checkpoint.AsOf.String(), Source: c.Checkpoint.Source,
				Stated: c.Checkpoint.Balance.String(), Computed: c.Computed.String(), Difference: c.Difference().String()})
		}
		for _, st := range rec.Statements {
			if st.Gap {
				out.Gaps = append(out.Gaps, fmt.Sprintf("%s a %s", st.Statement.PeriodStart, st.Statement.PeriodEnd))
			}
		}
		if rec.FirstBreak != nil {
			for _, l := range rec.Lines {
				if l.Entry.ID == *rec.FirstBreak {
					ref := ref(l.Entry)
					if l.Difference != nil {
						ref.Difference = l.Difference.String()
					}
					out.FirstBreak = &ref
					out.Note = untrustedDataNote
				}
			}
		}
		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: ToolTransferSuggest,
		Description: "Lista pares de movimientos que parecen una sola transferencia entre cuentas de la persona " +
			"(por ejemplo, el pago de la tarjeta desde su cuenta) y que nadie ha decidido todavía.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, suggestionsResult, error) {
		list, err := deps.Transfers.Suggestions(ctx, userID)
		if err != nil {
			return nil, suggestionsResult{}, err
		}
		out := suggestionsResult{Pairs: make([]pairRow, 0, len(list))}
		for _, c := range list {
			out.Pairs = append(out.Pairs, pairRow{Out: ref(c.Out), In: ref(c.In)})
		}
		if len(list) > 0 {
			out.Note = untrustedDataNote
		}
		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        ToolPortfolio,
		Description: "Las posiciones de una cuenta de IBKR a valor de mercado y cómo fue su última sincronización.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args accountArgs) (*mcp.CallToolResult, portfolioResult, error) {
		id, err := parseID(args.AccountID, "account_id", "list_accounts")
		if err != nil {
			return nil, portfolioResult{}, err
		}
		p, err := deps.Investments.Portfolio(ctx, userID, id)
		if err != nil {
			return nil, portfolioResult{}, investmentsError(err)
		}
		out := portfolioResult{Holdings: p.Holdings.String(), Positions: make([]positionRow, 0, len(p.Positions)), SyncStatus: "never"}
		for _, pos := range p.Positions {
			row := positionRow{Symbol: pos.Symbol, Quantity: pos.Quantity, AsOf: pos.AsOf.String()}
			if pos.MarketValue != nil {
				row.MarketValue = pos.MarketValue.String()
			}
			if pos.CostBasis != nil {
				row.CostBasis = pos.CostBasis.String()
			}
			out.Positions = append(out.Positions, row)
		}
		if link, err := deps.Investments.LinkStatus(ctx, userID, id); err == nil {
			out.SyncStatus = link.LastSyncStatus
			out.SyncError = link.LastSyncError
			if link.LastSyncedAt != nil {
				out.LastSync = link.LastSyncedAt.In(civil.Panama).Format("2006-01-02 15:04")
			}
		}
		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        ToolListRules,
		Description: "Lista las reglas con que se clasifican los movimientos de la persona al importarlos.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, rulesResult, error) {
		list, err := deps.Rules.List(ctx, userID)
		if err != nil {
			return nil, rulesResult{}, err
		}
		names, _, err := categoryNames(ctx, deps.Categories, userID)
		if err != nil {
			return nil, rulesResult{}, err
		}
		out := rulesResult{Rules: make([]ruleRow, 0, len(list))}
		for _, r := range list {
			row := ruleRow{MatchText: r.MatchText, Transfer: r.SetKind != nil && *r.SetKind == store.KindTransfer}
			if r.CategoryID != nil {
				row.Category = names[*r.CategoryID]
			}
			out.Rules = append(out.Rules, row)
		}
		return nil, out, nil
	})

	propose := func(ctx context.Context, payload any) (*mcp.CallToolResult, proposalResult, error) {
		p, err := deps.Proposals.Propose(ctx, userID, payload)
		if err != nil {
			return nil, proposalResult{}, proposalError(err)
		}
		return nil, proposalResult{ProposalID: p.ID.String(), Kind: p.Kind, Summary: p.Summary,
			Status: p.Status, Note: proposalPendingMessage}, nil
	}

	mcp.AddTool(server, &mcp.Tool{
		Name: ToolProposeCategory,
		Description: "Propone cambiar la categoría de un movimiento, o marcarlo como transferencia entre cuentas de la persona. " +
			"No cambia nada: la persona lo aplica desde una tarjeta.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args proposeCategoryArgs) (*mcp.CallToolResult, proposalResult, error) {
		entryID, err := parseID(args.EntryID, "entry_id", "list_movements")
		if err != nil {
			return nil, proposalResult{}, err
		}
		payload := proposals.Recategorize{EntryID: entryID, Transfer: args.Transfer}
		if args.CategoryID != "" {
			id, err := parseID(args.CategoryID, "category_id", "list_categories")
			if err != nil {
				return nil, proposalResult{}, err
			}
			payload.CategoryID = &id
		}
		return propose(ctx, payload)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        ToolProposeNote,
		Description: "Propone ponerle una nota a un movimiento. No cambia nada: la persona lo aplica desde una tarjeta.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args proposeNoteArgs) (*mcp.CallToolResult, proposalResult, error) {
		entryID, err := parseID(args.EntryID, "entry_id", "list_movements")
		if err != nil {
			return nil, proposalResult{}, err
		}
		return propose(ctx, proposals.Note{EntryID: entryID, Note: args.Note})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: ToolProposeRule,
		Description: "Propone una regla: los movimientos cuyo concepto contenga un texto van a una categoría, o son transferencias. " +
			"No cambia nada: la persona la aplica desde una tarjeta.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args proposeRuleArgs) (*mcp.CallToolResult, proposalResult, error) {
		payload := proposals.Rule{MatchText: args.MatchText, Transfer: args.Transfer, ApplyToExisting: args.ApplyToExisting}
		if args.CategoryID != "" {
			id, err := parseID(args.CategoryID, "category_id", "list_categories")
			if err != nil {
				return nil, proposalResult{}, err
			}
			payload.CategoryID = &id
		}
		return propose(ctx, payload)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: ToolProposeTransfer,
		Description: "Propone confirmar (o descartar) que dos movimientos son una sola transferencia entre cuentas de la persona, " +
			"normalmente un par de list_transfer_suggestions. No cambia nada: la persona lo aplica desde una tarjeta.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args proposeTransferArgs) (*mcp.CallToolResult, proposalResult, error) {
		outID, err := parseID(args.OutEntryID, "out_entry_id", "list_transfer_suggestions")
		if err != nil {
			return nil, proposalResult{}, err
		}
		inID, err := parseID(args.InEntryID, "in_entry_id", "list_transfer_suggestions")
		if err != nil {
			return nil, proposalResult{}, err
		}
		return propose(ctx, proposals.TransferDecision{OutEntryID: outID, InEntryID: inID, Confirm: args.Confirm})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: ToolProposeCheckpoint,
		Description: "Propone registrar el saldo que el banco dice que tenía una cuenta al cierre de un día, por ejemplo el adeudado " +
			"al corte de una tarjeta. Solo sirve para comprobar los movimientos: no crea ninguno. La persona lo aplica desde una tarjeta.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args proposeCheckpointArgs) (*mcp.CallToolResult, proposalResult, error) {
		accountID, err := parseID(args.AccountID, "account_id", "list_accounts")
		if err != nil {
			return nil, proposalResult{}, err
		}
		day, err := civil.Parse(args.AsOf)
		if err != nil {
			return nil, proposalResult{}, fmt.Errorf("la fecha %q no es válida: usa AAAA-MM-DD", args.AsOf)
		}
		balance, err := money.Parse(args.Balance)
		if err != nil {
			return nil, proposalResult{}, fmt.Errorf("el saldo %q no es válido: usa un número como -14.30", args.Balance)
		}
		return propose(ctx, proposals.Checkpoint{AccountID: accountID, AsOf: day, BalanceCents: int64(balance), Note: args.Note})
	})
}

func ref(e store.Entry) movementRef {
	r := movementRef{ID: e.ID.String(), Date: e.BookedOn.String(), Description: e.Description, Amount: e.Amount.String()}
	if e.BankBalance != nil {
		r.BankBalance = e.BankBalance.String()
	}
	return r
}

func parseID(raw, field, source string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%s no es válido: usa el id que devuelve %s", field, source)
	}
	return id, nil
}

// period reads a from/to pair, defaulting to this month so far.
func period(args periodArgs) (civil.Date, civil.Date, error) {
	today := civil.Of(time.Now().In(civil.Panama))
	from := civil.Date{Year: today.Year, Month: today.Month, Day: 1}
	to := today
	for raw, dest := range map[string]*civil.Date{args.From: &from, args.To: &to} {
		if raw == "" {
			continue
		}
		d, err := civil.Parse(raw)
		if err != nil {
			return civil.Date{}, civil.Date{}, fmt.Errorf("la fecha %q no es válida: usa AAAA-MM-DD", raw)
		}
		*dest = d
	}
	if to.Before(from) {
		return civil.Date{}, civil.Date{}, errors.New("el período termina antes de empezar")
	}
	return from, to, nil
}

func categoryNames(ctx context.Context, svc *categories.Service, userID uuid.UUID) (map[uuid.UUID]string, []categories.Category, error) {
	list, err := svc.List(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	byID := make(map[uuid.UUID]categories.Category, len(list))
	for _, c := range list {
		byID[c.ID] = c
	}
	names := make(map[uuid.UUID]string, len(list))
	for _, c := range list {
		names[c.ID] = c.Name
		if c.ParentID != nil {
			if parent, ok := byID[*c.ParentID]; ok {
				names[c.ID] = parent.Name + " › " + c.Name
			}
		}
	}
	return names, list, nil
}

func proposalError(err error) error {
	switch {
	case errors.Is(err, proposals.ErrInvalid):
		// The message after the sentinel already says why, in Spanish.
		return errors.New("No se pudo proponer ese cambio." + HintSeparator + err.Error())
	default:
		return toolError(err)
	}
}

func investmentsError(err error) error {
	if errors.Is(err, investments.ErrNotBrokerage) {
		return errors.New("Esa cuenta no es de inversiones." + HintSeparator + "Usa una cuenta de tipo brokerage de list_accounts.")
	}
	return toolError(err)
}
