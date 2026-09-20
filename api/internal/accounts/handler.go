package accounts

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/JuanKsPty/corebank/api/internal/httpx"
	"github.com/JuanKsPty/corebank/api/internal/identity"
	"github.com/JuanKsPty/corebank/api/internal/ledger"
	"github.com/JuanKsPty/corebank/api/internal/money"
)

// Handler serves the account endpoints. Everything it exposes is scoped to the
// authenticated user; there is no endpoint here that can read someone else's
// account.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes returns the /api/accounts subrouter. It must be mounted behind the
// authentication middleware.
//
// history is injected rather than implemented here because an account's statement
// belongs to the transactions package — the filters, the pagination and the
// response shape are all its — while the URL belongs under this account. Passing
// the handler keeps the URL where a reader expects it without this package
// growing a dependency on the other.
func (h *Handler) Routes(history http.HandlerFunc) http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.list)
	r.Post("/", h.open)
	r.Get("/{number}", h.get)
	// The first update endpoint in this API. PATCH rather than PUT because it
	// changes one field of an account and leaves the rest alone; a PUT would imply
	// the body is the whole account, which it never is — nobody may replace a
	// balance or a number.
	r.Patch("/{number}", h.rename)
	r.Delete("/{number}", h.delete)
	r.Get("/{number}/balance", h.balance)
	r.Get("/{number}/transactions", history)
	return r
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.List(r.Context(), identity.MustFromContext(r.Context()))
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	httpx.JSON(w, r, http.StatusOK, accountListResponse{
		Accounts: NewViews(list),
		Total:    h.svc.Total(r.Context(), list).Amount(),
	})
}

// open opens an additional account for the authenticated customer.
//
// The owner is taken from the request's identity and never from the body. That is the
// same rule the MCP tools follow, and for the same reason: an endpoint that accepted a
// user id would be one forged field away from opening an account in somebody else's
// name.
func (h *Handler) open(w http.ResponseWriter, r *http.Request) {
	var req openRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	kind, err := ledger.ParseAccountKind(req.AccountType)
	if err != nil {
		httpx.Fail(w, r, httpx.Invalid(map[string]string{
			"account_type": "El tipo de cuenta debe ser savings, checking o investment.",
		}))
		return
	}

	account, err := h.svc.OpenFor(r.Context(), identity.MustFromContext(r.Context()), kind, req.Alias)
	if err != nil {
		httpx.Fail(w, r, TranslateError(err))
		return
	}

	httpx.JSON(w, r, http.StatusCreated, NewView(account))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	account, err := h.svc.Get(r.Context(), identity.MustFromContext(r.Context()), chi.URLParam(r, "number"))
	if err != nil {
		httpx.Fail(w, r, TranslateError(err))
		return
	}
	httpx.JSON(w, r, http.StatusOK, NewView(account))
}

func (h *Handler) balance(w http.ResponseWriter, r *http.Request) {
	account, err := h.svc.Get(r.Context(), identity.MustFromContext(r.Context()), chi.URLParam(r, "number"))
	if err != nil {
		httpx.Fail(w, r, TranslateError(err))
		return
	}

	httpx.JSON(w, r, http.StatusOK, balanceResponse{
		AccountNumber: account.Number,
		Available:     account.Balance.Available.Amount(),
		Posted:        account.Balance.Posted.Amount(),
		Held:          account.Balance.Held.Amount(),
		ReadAt:        time.Now().UTC(),
	})
}

// TranslateError maps this package's errors onto HTTP responses.
//
// ErrNotOwned deliberately becomes 404 rather than 403. A 403 would confirm that
// the account number exists, which turns any authenticated session into a probe
// for valid account numbers; the customer sees the same answer either way, and
// the real reason is in the log.
func TranslateError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrNotOwned):
		return httpx.NotFound("account_not_found", "La cuenta no existe.").WithCause(err)
	case errors.Is(err, ErrTooManyAccounts):
		return httpx.Conflict(
			"too_many_accounts",
			"Ya tienes el máximo de cuentas que puedes abrir.",
		).WithCause(err)
	case errors.Is(err, ErrAccountNotEmpty):
		return httpx.Conflict(
			"account_not_empty",
			"Esta cuenta todavía tiene saldo. Muévelo antes de eliminarla.",
		).WithCause(err)
	case errors.Is(err, ErrLastAccount):
		return httpx.Conflict(
			"last_account",
			"No puedes eliminar tu única cuenta.",
		).WithCause(err)
	case errors.Is(err, ErrAccountLinked):
		return httpx.Conflict(
			"account_linked",
			"Esta cuenta todavía está vinculada a IBKR. Desvincúlala antes de eliminarla.",
		).WithCause(err)
	case errors.Is(err, ErrAliasTooLong):
		return httpx.Invalid(map[string]string{
			"alias": fmt.Sprintf("El alias admite como máximo %d caracteres.", maxAliasRunes),
		}).WithCause(err)
	case errors.Is(err, ErrAliasInvalid):
		// Deliberately not quoting the offending character back: the ones this
		// rejects are invisible or reverse the text around them, so echoing one into
		// an error message would produce a message that cannot be read either.
		return httpx.Invalid(map[string]string{
			"alias": "El alias tiene caracteres que no se pueden mostrar.",
		}).WithCause(err)
	case errors.Is(err, ErrNumberUnavailable):
		return httpx.Internal(err)
	default:
		return err
	}
}

// --- response shapes --------------------------------------------------------

type openRequest struct {
	AccountType string `json:"account_type"`
	// Alias is optional. Omitting it opens an account the interface labels by its
	// type, which is what every account did before names existed.
	Alias string `json:"alias"`
}

type renameRequest struct {
	Alias string `json:"alias"`
}

// rename changes what the customer calls one of their accounts.
//
// The only thing this can change is a label. It cannot move money, cannot reach an
// account the caller does not hold, and an empty alias is a valid request rather than
// a missing field — it is how a name is removed.
func (h *Handler) rename(w http.ResponseWriter, r *http.Request) {
	var req renameRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	account, err := h.svc.Rename(r.Context(),
		identity.MustFromContext(r.Context()), chi.URLParam(r, "number"), req.Alias)
	if err != nil {
		httpx.Fail(w, r, TranslateError(err))
		return
	}

	httpx.JSON(w, r, http.StatusOK, NewView(account))
}

// delete closes an account of the authenticated customer's own — only one
// with nothing left in it, which TranslateError is what turns into the
// specific, actionable messages a customer sees for why not.
func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	err := h.svc.Delete(r.Context(),
		identity.MustFromContext(r.Context()), chi.URLParam(r, "number"))
	if err != nil {
		httpx.Fail(w, r, TranslateError(err))
		return
	}
	httpx.NoContent(w)
}

type accountListResponse struct {
	Accounts []View       `json:"accounts"`
	Total    money.Amount `json:"total_available"`
}

// View is an account as it appears in a JSON response. Exported because
// /api/me embeds the same shape.
type View struct {
	ID     string `json:"id"`
	Number string `json:"account_number"`
	Type   string `json:"account_type"`
	// Alias is what the customer calls this account, or empty if they have not named
	// it. The interface falls back to the type, and does so in one place so the two
	// cannot drift.
	Alias string `json:"alias"`
	// Available is what can be spent: the posted balance minus anything held by
	// an unconfirmed movement. It is the figure the interface shows.
	Available money.Amount `json:"available"`
	Posted    money.Amount `json:"posted"`
	Held      money.Amount `json:"held"`
	Currency  string       `json:"currency"`
	CreatedAt time.Time    `json:"created_at"`
}

// NewView renders one account.
func NewView(a Account) View {
	return View{
		ID:        a.ID.String(),
		Number:    a.Number,
		Type:      a.Kind.String(),
		Alias:     a.Alias,
		Available: a.Balance.Available.Amount(),
		Posted:    a.Balance.Posted.Amount(),
		Held:      a.Balance.Held.Amount(),
		Currency:  a.Currency,
		CreatedAt: a.CreatedAt,
	}
}

// NewViews renders a list of accounts.
func NewViews(list []Account) []View {
	// A non-nil empty slice so the field serialises as [] rather than null; a
	// client should not have to handle both for "no accounts".
	out := make([]View, 0, len(list))
	for _, a := range list {
		out = append(out, NewView(a))
	}
	return out
}

type balanceResponse struct {
	AccountNumber string       `json:"account_number"`
	Available     money.Amount `json:"available"`
	Posted        money.Amount `json:"posted"`
	Held          money.Amount `json:"held"`
	ReadAt        time.Time    `json:"read_at"`
}
