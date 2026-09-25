package movements

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/accounts"
	"github.com/JuanKsPty/corebank/api/internal/categories"
	"github.com/JuanKsPty/corebank/api/internal/civil"
	"github.com/JuanKsPty/corebank/api/internal/httpx"
	"github.com/JuanKsPty/corebank/api/internal/identity"
	"github.com/JuanKsPty/corebank/api/internal/money"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

// Handler serves /api/entries.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes returns the /api/entries subrouter.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.list)
	r.Patch("/{id}", h.update)
	return r
}

// AccountEntries serves GET /api/accounts/{id}/entries: the same list,
// restricted to one account.
func (h *Handler) AccountEntries(w http.ResponseWriter, r *http.Request) {
	id, err := accounts.AccountID(r)
	if err != nil {
		httpx.Fail(w, r, accounts.TranslateError(err))
		return
	}
	h.serve(w, r, []uuid.UUID{id})
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	var ids []uuid.UUID
	for _, raw := range r.URL.Query()["account_id"] {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.Fail(w, r, httpx.BadRequest("invalid_account", "account_id no es válido."))
			return
		}
		ids = append(ids, id)
	}
	h.serve(w, r, ids)
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request, accountIDs []uuid.UUID) {
	q, err := ParseQuery(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	q.AccountIDs = accountIDs
	page, err := h.svc.List(r.Context(), identity.MustFromContext(r.Context()), q)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	out := PageView{Entries: NewViews(page.Entries)}
	if page.Next != nil {
		out.NextCursor = encodeCursor(*page.Next)
		out.HasMore = true
	}
	httpx.JSON(w, r, http.StatusOK, out)
}

// ParseQuery reads the filters every movement listing shares.
func ParseQuery(r *http.Request) (Query, error) {
	v := r.URL.Query()
	q := Query{Search: v.Get("search"), Uncategorized: v.Get("uncategorized") == "true"}
	for name, dest := range map[string]*civil.Date{"from": &q.From, "to": &q.To} {
		if raw := v.Get(name); raw != "" {
			d, err := civil.Parse(raw)
			if err != nil {
				return Query{}, httpx.BadRequest("invalid_date", "Las fechas van en formato AAAA-MM-DD.")
			}
			*dest = d
		}
	}
	for _, k := range v["kind"] {
		if !validKind(k) {
			return Query{}, httpx.BadRequest("invalid_kind", "kind no es válido.")
		}
		q.Kinds = append(q.Kinds, k)
	}
	for _, raw := range v["category_id"] {
		id, err := uuid.Parse(raw)
		if err != nil {
			return Query{}, httpx.BadRequest("invalid_category", "category_id no es válido.")
		}
		q.CategoryIDs = append(q.CategoryIDs, id)
	}
	if raw := v.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			return Query{}, httpx.BadRequest("invalid_limit", "limit debe estar entre 1 y 200.")
		}
		q.Limit = n
	}
	if raw := v.Get("cursor"); raw != "" {
		c, err := decodeCursor(raw)
		if err != nil {
			return Query{}, httpx.BadRequest("invalid_cursor", "El cursor no es válido.")
		}
		q.Before = &c
	}
	return q, nil
}

func validKind(k string) bool {
	switch k {
	case store.KindIncome, store.KindExpense, store.KindRefund, store.KindFee,
		store.KindInterest, store.KindTransfer, store.KindTrade:
		return true
	}
	return false
}

// updateRequest changes what a movement means. Every field is optional;
// category_id is raw so that null (clear) and absent (leave alone) differ.
type updateRequest struct {
	CategoryID json.RawMessage `json:"category_id"`
	Note       *string         `json:"note"`
	Transfer   *bool           `json:"transfer"`
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	var body updateRequest
	if err := httpx.Decode(w, r, &body); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Fail(w, r, translate(ErrNotFound))
		return
	}
	userID := identity.MustFromContext(r.Context())
	ctx := r.Context()

	e, err := h.svc.Get(ctx, userID, id)
	if err != nil {
		httpx.Fail(w, r, translate(err))
		return
	}
	if len(body.CategoryID) > 0 {
		var raw *string
		if err := json.Unmarshal(body.CategoryID, &raw); err != nil {
			httpx.Fail(w, r, httpx.Invalid(map[string]string{"category_id": "La categoría no es válida."}))
			return
		}
		var cat *uuid.UUID
		if raw != nil {
			parsed, err := uuid.Parse(*raw)
			if err != nil {
				httpx.Fail(w, r, httpx.Invalid(map[string]string{"category_id": "La categoría no es válida."}))
				return
			}
			cat = &parsed
		}
		if e, err = h.svc.SetCategory(ctx, userID, id, cat, "user"); err != nil {
			httpx.Fail(w, r, translate(err))
			return
		}
	}
	if body.Note != nil {
		if e, err = h.svc.SetNote(ctx, userID, id, *body.Note); err != nil {
			httpx.Fail(w, r, translate(err))
			return
		}
	}
	if body.Transfer != nil {
		if e, err = h.svc.SetTransfer(ctx, userID, id, *body.Transfer); err != nil {
			httpx.Fail(w, r, translate(err))
			return
		}
	}
	httpx.JSON(w, r, http.StatusOK, NewView(e))
}

func translate(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return httpx.NotFound("entry_not_found", "El movimiento no existe.").WithCause(err)
	case errors.Is(err, ErrNoteTooLong):
		return httpx.Invalid(map[string]string{"note": "La nota admite como máximo 500 caracteres."}).WithCause(err)
	default:
		return categories.TranslateError(err)
	}
}

func encodeCursor(c store.EntryCursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(c.BookedOn.String() + "|" + c.ID.String()))
}

func decodeCursor(s string) (store.EntryCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return store.EntryCursor{}, err
	}
	day, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return store.EntryCursor{}, errors.New("malformed cursor")
	}
	d, err := civil.Parse(day)
	if err != nil {
		return store.EntryCursor{}, err
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return store.EntryCursor{}, err
	}
	return store.EntryCursor{BookedOn: d, ID: u}, nil
}

// PageView is one page of movements.
type PageView struct {
	Entries    []View `json:"entries"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// View is a movement on the wire.
type View struct {
	ID        uuid.UUID    `json:"id"`
	AccountID uuid.UUID    `json:"account_id"`
	BookedOn  civil.Date   `json:"booked_on"`
	PostedOn  *civil.Date  `json:"posted_on,omitempty"`
	BookedAt  *time.Time   `json:"booked_at,omitempty"`
	Amount    money.Amount `json:"amount"`
	// Kind is what the movement counts as; ImportedKind is what the
	// importer decided, shown when the owner or a rule overrode it.
	Kind           string        `json:"kind"`
	ImportedKind   string        `json:"imported_kind"`
	Description    string        `json:"description"`
	BankRef        string        `json:"bank_ref,omitempty"`
	BankCategory   string        `json:"bank_category,omitempty"`
	BankBalance    *money.Amount `json:"bank_balance,omitempty"`
	CategoryID     *uuid.UUID    `json:"category_id,omitempty"`
	CategorySource *string       `json:"category_source,omitempty"`
	Note           string        `json:"note,omitempty"`
}

// NewView builds a movement's wire shape.
func NewView(e store.Entry) View {
	v := View{
		ID: e.ID, AccountID: e.AccountID, BookedOn: e.BookedOn, PostedOn: e.PostedOn, BookedAt: e.BookedAt,
		Amount: e.Amount.Amount(), Kind: e.EffectiveKind(), ImportedKind: e.Kind, Description: e.Description,
		BankRef: e.BankRef, BankCategory: e.BankCategory, CategoryID: e.CategoryID,
		CategorySource: e.CategorySource, Note: e.Note,
	}
	if e.BankBalance != nil {
		b := e.BankBalance.Amount()
		v.BankBalance = &b
	}
	return v
}

// NewViews builds a list of wire shapes.
func NewViews(list []store.Entry) []View {
	out := make([]View, 0, len(list))
	for _, e := range list {
		out = append(out, NewView(e))
	}
	return out
}
