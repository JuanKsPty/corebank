package transactions

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/accounts"
	"github.com/JuanKsPty/corebank/api/internal/categories"
	"github.com/JuanKsPty/corebank/api/internal/httpx"
	"github.com/JuanKsPty/corebank/api/internal/identity"
	"github.com/JuanKsPty/corebank/api/internal/ledger"
	"github.com/JuanKsPty/corebank/api/internal/money"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

// idempotencyHeader lets a client make a retry safe. A balance correction sent
// twice because a response was lost is the failure mode worth designing against,
// and the client is the only party that can tell "the same correction again"
// from "the same correction twice on purpose".
const idempotencyHeader = "Idempotency-Key"

// Handler serves the history, export, category and balance-correction
// endpoints. All of them require authentication.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes returns the /api/transactions subrouter.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.history)
	// A separate route rather than a format parameter on the one above. The two
	// differ in more than encoding: this one is not paginated, because a statement
	// is a document and not a page of one.
	r.Get("/export.csv", h.exportCSV)
	r.Post("/reconcile", h.reconcile)
	r.Patch("/{id}/category", h.setCategory)
	return r
}

// AccountHistory handles GET /api/accounts/{number}/transactions. It lives here
// rather than in the accounts package because the query, the filters and the
// response shape are all this package's.
func (h *Handler) AccountHistory(w http.ResponseWriter, r *http.Request) {
	h.serveHistory(w, r, chi.URLParam(r, "number"))
}

// Dashboard handles GET /api/dashboard/summary.
func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	const (
		recentMovements = 8
		flowDays        = 30
	)

	summary, err := h.svc.Dashboard(r.Context(), identity.MustFromContext(r.Context()), recentMovements, flowDays)
	if err != nil {
		httpx.Fail(w, r, translate(err))
		return
	}

	httpx.JSON(w, r, http.StatusOK, dashboardResponse{
		Accounts:       accounts.NewViews(summary.Accounts),
		TotalAvailable: summary.TotalAvailable.Amount(),
		Recent:         newViews(summary.Recent),
		Flow:           newFlow(summary.Flow),
	})
}

// --- request shapes ---------------------------------------------------------

func amountProblem(err error) string {
	switch {
	case errors.Is(err, money.ErrNotPositive):
		return "El monto debe ser mayor que cero."
	case errors.Is(err, money.ErrTooPrecise):
		return "El monto admite como máximo dos decimales."
	case errors.Is(err, money.ErrOutOfRange):
		return "El monto está fuera del rango admitido."
	case errors.Is(err, money.ErrEmpty):
		return "El monto es obligatorio."
	default:
		return "El monto no tiene un formato válido. Usa por ejemplo «100.50»."
	}
}

// --- handlers ---------------------------------------------------------------

// reconcileRequest is the body of a balance correction. TargetBalance is
// parsed with money.Parse rather than money.Input.Cents: unlike a movement
// amount, zero and negative are both valid balances (an empty account, an
// overdrawn one), not a client mistake.
type reconcileRequest struct {
	Account       string      `json:"account_number"`
	TargetBalance money.Input `json:"target_balance"`
}

type reconcileResponse struct {
	// Adjusted is false when the stated balance already matched — nothing was
	// posted, so there is no Transaction to show.
	Adjusted    bool  `json:"adjusted"`
	Transaction *View `json:"transaction,omitempty"`
}

func (h *Handler) reconcile(w http.ResponseWriter, r *http.Request) {
	var body reconcileRequest
	if err := httpx.Decode(w, r, &body); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	target, err := money.Parse(string(body.TargetBalance))
	if err != nil {
		httpx.Fail(w, r, httpx.Invalid(map[string]string{
			"target_balance": amountProblem(err),
		}))
		return
	}

	key := r.Header.Get(idempotencyHeader)
	if len(key) > 128 {
		httpx.Fail(w, r, httpx.BadRequest("idempotency_key_too_long",
			"El encabezado Idempotency-Key admite como máximo 128 caracteres."))
		return
	}

	tx, err := h.svc.Reconcile(r.Context(), identity.MustFromContext(r.Context()), ReconcileRequest{
		Account:        body.Account,
		TargetBalance:  target,
		IdempotencyKey: key,
	})
	if err != nil {
		if errors.Is(err, ErrAlreadyReconciled) {
			httpx.JSON(w, r, http.StatusOK, reconcileResponse{Adjusted: false})
			return
		}
		httpx.Fail(w, r, translate(err))
		return
	}

	view := newView(tx)
	httpx.JSON(w, r, http.StatusCreated, reconcileResponse{Adjusted: true, Transaction: &view})
}

type categoryRequest struct {
	// CategoryID is a pointer so JSON `null` (clear the category) is
	// distinguishable from the field being omitted, which httpx.Decode's
	// DisallowUnknownFields already treats as a client mistake elsewhere but
	// which here would otherwise silently do nothing.
	CategoryID *string `json:"category_id"`
}

func (h *Handler) setCategory(w http.ResponseWriter, r *http.Request) {
	var req categoryRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Fail(w, r, httpx.NotFound("transaction_not_found", "El movimiento no existe."))
		return
	}

	var categoryID *uuid.UUID
	if req.CategoryID != nil {
		parsed, err := uuid.Parse(*req.CategoryID)
		if err != nil {
			httpx.Fail(w, r, httpx.Invalid(map[string]string{
				"category_id": "El identificador de la categoría no es válido.",
			}))
			return
		}
		categoryID = &parsed
	}

	tx, err := h.svc.SetCategory(r.Context(), identity.MustFromContext(r.Context()), id, categoryID)
	if err != nil {
		httpx.Fail(w, r, translate(err))
		return
	}
	httpx.JSON(w, r, http.StatusOK, newView(tx))
}

func (h *Handler) history(w http.ResponseWriter, r *http.Request) {
	h.serveHistory(w, r, r.URL.Query().Get("account_number"))
}

func (h *Handler) serveHistory(w http.ResponseWriter, r *http.Request, account string) {
	filter, err := parseHistoryQuery(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	page, err := h.svc.History(r.Context(), identity.MustFromContext(r.Context()),
		HistoryQuery{Account: account, Filter: filter})
	if err != nil {
		httpx.Fail(w, r, translate(err))
		return
	}

	httpx.JSON(w, r, http.StatusOK, historyResponse{
		Transactions: newViews(page.Transactions),
		NextCursor:   page.Next.Encode(),
		HasMore:      page.HasMore,
	})
}

// parseHistoryQuery reads the filters from the query string.
func parseHistoryQuery(r *http.Request) (store.HistoryFilter, error) {
	q := r.URL.Query()
	filter := store.HistoryFilter{Search: q.Get("search")}

	if raw := q.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 200 {
			return filter, httpx.BadRequest("invalid_limit", "El parámetro limit debe estar entre 1 y 200.")
		}
		filter.Limit = limit
	}
	if raw := q.Get("kind"); raw != "" {
		kind, err := ledger.ParseMovementKind(raw)
		if err != nil {
			return filter, httpx.BadRequest("invalid_kind",
				"El parámetro kind debe ser deposit, withdrawal, transfer o internal_transfer.")
		}
		filter.Kind = kind
	}
	for name, dest := range map[string]*time.Time{"since": &filter.Since, "until": &filter.Until} {
		raw := q.Get(name)
		if raw == "" {
			continue
		}
		at, err := parseDate(raw)
		if err != nil {
			return filter, httpx.BadRequest("invalid_date",
				"Las fechas deben ir en formato AAAA-MM-DD o RFC 3339.")
		}
		*dest = at
	}
	if !filter.Since.IsZero() && !filter.Until.IsZero() && filter.Until.Before(filter.Since) {
		return filter, httpx.BadRequest("invalid_range", "La fecha final es anterior a la inicial.")
	}
	if raw := q.Get("cursor"); raw != "" {
		cursor, err := store.DecodeCursor(raw)
		if err != nil {
			return filter, httpx.BadRequest("invalid_cursor",
				"El cursor de paginación no es válido.").WithCause(err)
		}
		filter.Cursor = cursor
	}
	return filter, nil
}

// parseDate accepts a plain date or a full timestamp, so a URL a person typed by
// hand works as well as one the client generated.
func parseDate(raw string) (time.Time, error) {
	if at, err := time.Parse(time.RFC3339, raw); err == nil {
		return at, nil
	}
	return time.Parse("2006-01-02", raw)
}

// translate maps this package's and its collaborators' errors onto responses.
//
// Anything not named here falls through to the shared mapper in httpx, which
// covers the ledger's own errors — insufficient funds, unknown destination — so
// they are not restated in every handler.
func translate(err error) error {
	switch {
	case errors.Is(err, ErrConfirmationNotFound):
		return httpx.NotFound("confirmation_not_found", "Esta confirmación ya no existe.").WithCause(err)

	case errors.Is(err, ErrConfirmationSettled):
		return httpx.Conflict("confirmation_already_resolved",
			"Esta operación ya fue confirmada o cancelada.").WithCause(err)

	case errors.Is(err, ErrAccountRequired):
		return httpx.BadRequest("account_required",
			"Tienes varias cuentas: indica desde cuál quieres operar.").WithCause(err)

	case errors.Is(err, ErrIdempotencyMismatch):
		return httpx.Conflict("idempotency_key_reused",
			"Ya usaste este Idempotency-Key para otra operación distinta.").WithCause(err)

	case errors.Is(err, ErrTransactionNotFound):
		return httpx.NotFound("transaction_not_found", "El movimiento no existe.").WithCause(err)

	case errors.Is(err, categories.ErrNotFound), errors.Is(err, categories.ErrNotOwned):
		return httpx.Invalid(map[string]string{
			"category_id": "La categoría no existe.",
		}).WithCause(err)

	default:
		return accounts.TranslateError(err)
	}
}
