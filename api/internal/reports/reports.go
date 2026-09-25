// Package reports totals the owner's movements: spending and income by
// category, and money in and out over time.
//
// Every figure reads the same kinds: spending is expenses, fees, interest and
// refunds (a refund reducing it); income is income. Transfers between the
// owner's own accounts and trades are never either, which is what keeps paying
// the card from counting as spending twice.
package reports

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/civil"
	"github.com/JuanKsPty/corebank/api/internal/httpx"
	"github.com/JuanKsPty/corebank/api/internal/identity"
	"github.com/JuanKsPty/corebank/api/internal/money"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

// Service computes reports.
type Service struct {
	db *store.DB
}

func NewService(db *store.DB) *Service { return &Service{db: db} }

// CategoryTotal is one category's total over a period, as a positive amount.
type CategoryTotal struct {
	CategoryID *uuid.UUID
	Total      money.Cents
	Count      int
}

// Spending totals spending per category, as positive amounts: a refund
// reduces its category's total.
func (s *Service) Spending(ctx context.Context, userID uuid.UUID, from, to civil.Date, accounts []uuid.UUID) ([]CategoryTotal, error) {
	return s.totals(ctx, store.EntryFilter{UserID: userID, From: from, To: to, AccountIDs: accounts, Kinds: store.SpendKinds}, -1)
}

// Income totals income per category.
func (s *Service) Income(ctx context.Context, userID uuid.UUID, from, to civil.Date, accounts []uuid.UUID) ([]CategoryTotal, error) {
	return s.totals(ctx, store.EntryFilter{UserID: userID, From: from, To: to, AccountIDs: accounts, Kinds: store.IncomeKinds}, 1)
}

func (s *Service) totals(ctx context.Context, f store.EntryFilter, sign money.Cents) ([]CategoryTotal, error) {
	rows, err := s.db.Q().TotalsByCategory(ctx, f)
	if err != nil {
		return nil, err
	}
	out := make([]CategoryTotal, 0, len(rows))
	for _, r := range rows {
		out = append(out, CategoryTotal{CategoryID: r.CategoryID, Total: r.Total * sign, Count: r.Count})
	}
	return out, nil
}

// Flow is money in and out per day or month.
func (s *Service) Flow(ctx context.Context, userID uuid.UUID, from, to civil.Date, monthly bool) ([]store.FlowPoint, error) {
	return s.db.Q().Flow(ctx, userID, nil, from, to, monthly)
}

// Handler serves /api/reports.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes returns the /api/reports subrouter.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/categories", h.categories)
	r.Get("/flow", h.flow)
	return r
}

func dates(r *http.Request) (civil.Date, civil.Date, error) {
	var from, to civil.Date
	for name, dest := range map[string]*civil.Date{"from": &from, "to": &to} {
		if raw := r.URL.Query().Get(name); raw != "" {
			d, err := civil.Parse(raw)
			if err != nil {
				return civil.Date{}, civil.Date{}, httpx.BadRequest("invalid_date", "Las fechas van en formato AAAA-MM-DD.")
			}
			*dest = d
		}
	}
	return from, to, nil
}

type totalView struct {
	CategoryID *uuid.UUID   `json:"category_id"`
	Total      money.Amount `json:"total"`
	Count      int          `json:"count"`
}

func (h *Handler) categories(w http.ResponseWriter, r *http.Request) {
	from, to, err := dates(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	var accounts []uuid.UUID
	for _, raw := range r.URL.Query()["account_id"] {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.Fail(w, r, httpx.BadRequest("invalid_account", "account_id no es válido."))
			return
		}
		accounts = append(accounts, id)
	}
	userID := identity.MustFromContext(r.Context())
	var list []CategoryTotal
	if r.URL.Query().Get("kind") == "income" {
		list, err = h.svc.Income(r.Context(), userID, from, to, accounts)
	} else {
		list, err = h.svc.Spending(r.Context(), userID, from, to, accounts)
	}
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	out := make([]totalView, 0, len(list))
	for _, t := range list {
		out = append(out, totalView{CategoryID: t.CategoryID, Total: t.Total.Amount(), Count: t.Count})
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"totals": out})
}

type pointView struct {
	Day civil.Date   `json:"day"`
	In  money.Amount `json:"in"`
	Out money.Amount `json:"out"`
}

func (h *Handler) flow(w http.ResponseWriter, r *http.Request) {
	from, to, err := dates(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	points, err := h.svc.Flow(r.Context(), identity.MustFromContext(r.Context()), from, to,
		r.URL.Query().Get("granularity") == "month")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	out := make([]pointView, 0, len(points))
	for _, p := range points {
		out = append(out, pointView{Day: p.Day, In: p.In.Amount(), Out: p.Out.Amount()})
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"points": out})
}
