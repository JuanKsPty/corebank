package accounts

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/civil"
	"github.com/JuanKsPty/corebank/api/internal/httpx"
	"github.com/JuanKsPty/corebank/api/internal/identity"
	"github.com/JuanKsPty/corebank/api/internal/money"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

// Handler serves the account endpoints, all scoped to the authenticated user.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes returns the /api/accounts subrouter. entries serves an account's
// movements; it lives in the movements package, which owns their filters and
// shape, while the URL belongs here.
func (h *Handler) Routes(entries http.HandlerFunc) http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.list)
	r.Get("/{id}", h.get)
	r.Patch("/{id}", h.update)
	r.Delete("/{id}", h.delete)
	r.Get("/{id}/entries", entries)
	r.Get("/{id}/checkpoints", h.checkpoints)
	r.Post("/{id}/checkpoints", h.addCheckpoint)
	r.Delete("/{id}/checkpoints/{checkpoint}", h.deleteCheckpoint)
	r.Put("/{id}/anchor", h.pin)
	return r
}

// AccountID reads the {id} URL parameter; a malformed id is a 404 like a
// missing one.
func AccountID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: %q", ErrNotFound, chi.URLParam(r, "id"))
	}
	return id, nil
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.List(r.Context(), identity.MustFromContext(r.Context()))
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, NewListView(list))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := AccountID(r)
	if err != nil {
		httpx.Fail(w, r, TranslateError(err))
		return
	}
	a, err := h.svc.Get(r.Context(), identity.MustFromContext(r.Context()), id)
	if err != nil {
		httpx.Fail(w, r, TranslateError(err))
		return
	}
	httpx.JSON(w, r, http.StatusOK, NewView(a))
}

type updateRequest struct {
	// Alias is a pointer so "not changing the alias" and "clearing it" differ.
	Alias *string `json:"alias"`
	Type  *string `json:"type"`
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	var body updateRequest
	if err := httpx.Decode(w, r, &body); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	id, err := AccountID(r)
	if err != nil {
		httpx.Fail(w, r, TranslateError(err))
		return
	}
	userID := identity.MustFromContext(r.Context())
	var a Account
	if body.Alias != nil {
		if a, err = h.svc.Rename(r.Context(), userID, id, *body.Alias); err != nil {
			httpx.Fail(w, r, TranslateError(err))
			return
		}
	}
	if body.Type != nil {
		if a, err = h.svc.SetType(r.Context(), userID, id, *body.Type); err != nil {
			httpx.Fail(w, r, TranslateError(err))
			return
		}
	}
	if body.Alias == nil && body.Type == nil {
		if a, err = h.svc.Get(r.Context(), userID, id); err != nil {
			httpx.Fail(w, r, TranslateError(err))
			return
		}
	}
	httpx.JSON(w, r, http.StatusOK, NewView(a))
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := AccountID(r)
	if err != nil {
		httpx.Fail(w, r, TranslateError(err))
		return
	}
	if err := h.svc.Delete(r.Context(), identity.MustFromContext(r.Context()), id); err != nil {
		httpx.Fail(w, r, TranslateError(err))
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) checkpoints(w http.ResponseWriter, r *http.Request) {
	id, err := AccountID(r)
	if err != nil {
		httpx.Fail(w, r, TranslateError(err))
		return
	}
	list, err := h.svc.Checkpoints(r.Context(), identity.MustFromContext(r.Context()), id)
	if err != nil {
		httpx.Fail(w, r, TranslateError(err))
		return
	}
	out := make([]CheckpointView, 0, len(list))
	for _, c := range list {
		out = append(out, NewCheckpointView(c))
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"checkpoints": out})
}

type checkpointRequest struct {
	AsOf civil.Date `json:"as_of"`
	// Balance is signed from the holder's view: a card owing 14.30 is "-14.30".
	Balance        money.Input  `json:"balance"`
	Pin            bool         `json:"pin"`
	DueOn          *civil.Date  `json:"due_on"`
	MinimumPayment *money.Input `json:"minimum_payment"`
	Note           string       `json:"note"`
}

func (h *Handler) addCheckpoint(w http.ResponseWriter, r *http.Request) {
	var body checkpointRequest
	if err := httpx.Decode(w, r, &body); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	problems := map[string]string{}
	if body.AsOf.IsZero() {
		problems["as_of"] = "La fecha es obligatoria."
	}
	balance, err := money.Parse(string(body.Balance))
	if err != nil {
		problems["balance"] = "El saldo no tiene un formato válido. Usa por ejemplo «-14.30»."
	}
	var minimum *money.Cents
	if body.MinimumPayment != nil && *body.MinimumPayment != "" {
		m, err := money.Parse(string(*body.MinimumPayment))
		if err != nil || m < 0 {
			problems["minimum_payment"] = "El pago mínimo no tiene un formato válido."
		}
		minimum = &m
	}
	if len([]rune(body.Note)) > 200 {
		problems["note"] = "La nota admite como máximo 200 caracteres."
	}
	if len(problems) > 0 {
		httpx.Fail(w, r, httpx.Invalid(problems))
		return
	}
	id, err := AccountID(r)
	if err != nil {
		httpx.Fail(w, r, TranslateError(err))
		return
	}
	c, err := h.svc.AddCheckpoint(r.Context(), identity.MustFromContext(r.Context()), id, CheckpointInput{
		AsOf: body.AsOf, Balance: balance, Pin: body.Pin, DueOn: body.DueOn, MinimumPayment: minimum, Note: body.Note,
	})
	if err != nil {
		httpx.Fail(w, r, TranslateError(err))
		return
	}
	httpx.JSON(w, r, http.StatusCreated, NewCheckpointView(c))
}

func (h *Handler) deleteCheckpoint(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "checkpoint"))
	if err != nil {
		httpx.Fail(w, r, TranslateError(ErrCheckpointNotFound))
		return
	}
	if err := h.svc.DeleteCheckpoint(r.Context(), identity.MustFromContext(r.Context()), id); err != nil {
		httpx.Fail(w, r, TranslateError(err))
		return
	}
	httpx.NoContent(w)
}

type pinRequest struct {
	// CheckpointID is null to unpin, so the earliest checkpoint anchors again.
	CheckpointID *uuid.UUID `json:"checkpoint_id"`
}

func (h *Handler) pin(w http.ResponseWriter, r *http.Request) {
	var body pinRequest
	if err := httpx.Decode(w, r, &body); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	id, err := AccountID(r)
	if err != nil {
		httpx.Fail(w, r, TranslateError(err))
		return
	}
	userID := identity.MustFromContext(r.Context())
	if err := h.svc.Pin(r.Context(), userID, id, body.CheckpointID); err != nil {
		httpx.Fail(w, r, TranslateError(err))
		return
	}
	a, err := h.svc.Get(r.Context(), userID, id)
	if err != nil {
		httpx.Fail(w, r, TranslateError(err))
		return
	}
	httpx.JSON(w, r, http.StatusOK, NewView(a))
}

// TranslateError maps this package's errors to HTTP responses.
func TranslateError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return httpx.NotFound("account_not_found", "La cuenta no existe.").WithCause(err)
	case errors.Is(err, ErrCheckpointNotFound):
		return httpx.NotFound("checkpoint_not_found", "Ese saldo no existe o no se puede borrar.").WithCause(err)
	case errors.Is(err, ErrNotRelabelable):
		return httpx.Unprocessable("not_relabelable",
			"Solo una cuenta bancaria puede marcarse como corriente o de ahorros.").WithCause(err)
	case errors.Is(err, ErrAliasTooLong):
		return httpx.Invalid(map[string]string{
			"alias": fmt.Sprintf("El alias admite como máximo %d caracteres.", maxAliasRunes),
		}).WithCause(err)
	case errors.Is(err, ErrAliasInvalid):
		return httpx.Invalid(map[string]string{
			"alias": "El alias tiene caracteres que no se pueden mostrar.",
		}).WithCause(err)
	default:
		return err
	}
}

// --- response shapes --------------------------------------------------------

// View is an account on the wire.
type View struct {
	ID             uuid.UUID    `json:"id"`
	Class          string       `json:"class"`
	Type           string       `json:"type"`
	Institution    string       `json:"institution"`
	ExternalNumber string       `json:"external_number"`
	DisplayName    string       `json:"display_name"`
	Alias          string       `json:"alias,omitempty"`
	Currency       string       `json:"currency"`
	Balance        money.Amount `json:"balance"`
	// Owed is a card's balance as the bank prints it: a positive amount owed.
	Owed      *money.Amount `json:"owed,omitempty"`
	Anchored  bool          `json:"anchored"`
	Holdings  *money.Amount `json:"holdings,omitempty"`
	Drift     *DriftView    `json:"drift,omitempty"`
	Movements int           `json:"movements"`
	LastDay   civil.Date    `json:"last_day"`
}

// DriftView is a Drift on the wire.
type DriftView struct {
	AsOf       civil.Date   `json:"as_of"`
	Stated     money.Amount `json:"stated"`
	Computed   money.Amount `json:"computed"`
	Difference money.Amount `json:"difference"`
	Source     string       `json:"source"`
}

// NewView builds an account's wire shape.
func NewView(a Account) View {
	v := View{
		ID: a.ID, Class: a.Class, Type: a.Type, Institution: a.Institution, ExternalNumber: a.ExternalNumber,
		DisplayName: a.DisplayName, Alias: a.Alias, Currency: a.Currency, Balance: a.Balance.Amount(),
		Anchored: a.Anchored, Movements: a.Movements, LastDay: a.LastDay,
	}
	if a.Class == "liability" {
		owed := (-a.Balance).Amount()
		v.Owed = &owed
	}
	if a.Type == "brokerage" {
		h := a.Holdings.Amount()
		v.Holdings = &h
	}
	if d := a.Drift; d != nil {
		v.Drift = &DriftView{AsOf: d.AsOf, Stated: d.Stated.Amount(), Computed: d.Computed.Amount(),
			Difference: d.Difference().Amount(), Source: d.Source}
	}
	return v
}

// ListView is the account list with the owner's net worth.
type ListView struct {
	Accounts []View       `json:"accounts"`
	NetWorth NetWorthView `json:"net_worth"`
}

// NetWorthView is assets minus debts plus investments. Liabilities are
// negative; Owed is their positive total.
type NetWorthView struct {
	Assets     money.Amount `json:"assets"`
	Owed       money.Amount `json:"owed"`
	Holdings   money.Amount `json:"holdings"`
	Total      money.Amount `json:"total"`
	Incomplete bool         `json:"incomplete"`
	Unanchored int          `json:"unanchored"`
}

// NewListView builds the account list.
func NewListView(list []Account) ListView {
	views := make([]View, 0, len(list))
	unanchored := 0
	for _, a := range list {
		views = append(views, NewView(a))
		if !a.Anchored && a.Movements > 0 {
			unanchored++
		}
	}
	assets, liabilities, holdings := NetWorth(list)
	return ListView{Accounts: views, NetWorth: NetWorthView{
		Assets: assets.Amount(), Owed: (-liabilities).Amount(), Holdings: holdings.Amount(),
		Total: (assets + liabilities + holdings).Amount(), Incomplete: unanchored > 0, Unanchored: unanchored,
	}}
}

// CheckpointView is a checkpoint on the wire.
type CheckpointView struct {
	ID             uuid.UUID     `json:"id"`
	AsOf           civil.Date    `json:"as_of"`
	Balance        money.Amount  `json:"balance"`
	Source         string        `json:"source"`
	Pinned         bool          `json:"pinned"`
	DueOn          *civil.Date   `json:"due_on,omitempty"`
	MinimumPayment *money.Amount `json:"minimum_payment,omitempty"`
	Note           string        `json:"note,omitempty"`
}

// NewCheckpointView builds a checkpoint's wire shape.
func NewCheckpointView(c store.Checkpoint) CheckpointView {
	v := CheckpointView{ID: c.ID, AsOf: c.AsOf, Balance: c.Balance.Amount(), Source: c.Source,
		Pinned: c.Pinned, DueOn: c.DueOn, Note: c.Note}
	if c.MinimumPayment != nil {
		m := c.MinimumPayment.Amount()
		v.MinimumPayment = &m
	}
	return v
}
