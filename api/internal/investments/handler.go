package investments

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/civil"
	"github.com/JuanKsPty/corebank/api/internal/httpx"
	"github.com/JuanKsPty/corebank/api/internal/identity"
	"github.com/JuanKsPty/corebank/api/internal/money"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

// Handler serves /api/investments.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes returns the /api/investments subrouter.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Post("/link", h.link)
	r.Route("/accounts/{id}", func(r chi.Router) {
		r.Get("/link", h.linkStatus)
		r.Put("/link", h.relink)
		r.Post("/sync", h.sync)
		r.Get("/portfolio", h.portfolio)
		r.Get("/trades", h.trades)
	})
	return r
}

type linkRequest struct {
	IBKRAccountID string `json:"ibkr_account_id"`
	FlexQueryID   string `json:"flex_query_id"`
	// FlexToken is sent once and never echoed back.
	FlexToken string `json:"flex_token"`
}

func (r linkRequest) problems(needAccount bool) map[string]string {
	p := map[string]string{}
	if needAccount && strings.TrimSpace(r.IBKRAccountID) == "" {
		p["ibkr_account_id"] = "El identificador de cuenta de IBKR es obligatorio."
	}
	if strings.TrimSpace(r.FlexQueryID) == "" {
		p["flex_query_id"] = "El identificador de la Flex Query es obligatorio."
	}
	if strings.TrimSpace(r.FlexToken) == "" {
		p["flex_token"] = "El token de Flex Web Service es obligatorio."
	}
	return p
}

func (h *Handler) link(w http.ResponseWriter, r *http.Request) {
	var body linkRequest
	if err := httpx.Decode(w, r, &body); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if p := body.problems(true); len(p) > 0 {
		httpx.Fail(w, r, httpx.Invalid(p))
		return
	}
	account, err := h.svc.Link(r.Context(), identity.MustFromContext(r.Context()),
		body.IBKRAccountID, body.FlexQueryID, body.FlexToken)
	if err != nil {
		httpx.Fail(w, r, translate(err))
		return
	}
	httpx.JSON(w, r, http.StatusCreated, map[string]any{"account_id": account.ID})
}

func (h *Handler) relink(w http.ResponseWriter, r *http.Request) {
	var body linkRequest
	if err := httpx.Decode(w, r, &body); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if p := body.problems(false); len(p) > 0 {
		httpx.Fail(w, r, httpx.Invalid(p))
		return
	}
	id, ok := accountID(w, r)
	if !ok {
		return
	}
	userID := identity.MustFromContext(r.Context())
	current, err := h.svc.LinkStatus(r.Context(), userID, id)
	if err != nil && !errors.Is(err, ErrNotLinked) {
		httpx.Fail(w, r, translate(err))
		return
	}
	ibkrAccount := current.IBKRAccountID
	if ibkrAccount == "" {
		a, err := h.svc.brokerage(r.Context(), userID, id)
		if err != nil {
			httpx.Fail(w, r, translate(err))
			return
		}
		ibkrAccount = a.ExternalNumber
	}
	if _, err := h.svc.Link(r.Context(), userID, ibkrAccount, body.FlexQueryID, body.FlexToken); err != nil {
		httpx.Fail(w, r, translate(err))
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) linkStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := accountID(w, r)
	if !ok {
		return
	}
	link, err := h.svc.LinkStatus(r.Context(), identity.MustFromContext(r.Context()), id)
	if err != nil {
		httpx.Fail(w, r, translate(err))
		return
	}
	httpx.JSON(w, r, http.StatusOK, linkStatusView{
		IBKRAccountID: link.IBKRAccountID, LastSyncedAt: link.LastSyncedAt,
		LastSyncStatus: link.LastSyncStatus, LastSyncError: link.LastSyncError,
	})
}

func (h *Handler) sync(w http.ResponseWriter, r *http.Request) {
	id, ok := accountID(w, r)
	if !ok {
		return
	}
	result, err := h.svc.Sync(r.Context(), identity.MustFromContext(r.Context()), id)
	if err != nil {
		httpx.Fail(w, r, translate(err))
		return
	}
	view := syncResultView{
		RunID: result.RunID, CashNew: result.CashNew, CashDuplicate: result.CashDuplicate,
		CashFailed: result.CashFailed, TradesNew: result.TradesNew, TradesDuplicate: result.TradesDuplicate,
		Positions: result.Positions, PeriodFrom: day(result.PeriodFrom), PeriodTo: day(result.PeriodTo),
		MissingSections: nonNil(result.MissingSections), Warnings: nonNil(result.Warnings),
	}
	if !result.GeneratedAt.IsZero() {
		view.GeneratedAt = &result.GeneratedAt
	}
	if result.ReportedCash != nil {
		a := result.ReportedCash.Amount()
		view.ReportedCash = &a
	}
	httpx.JSON(w, r, http.StatusOK, view)
}

func (h *Handler) portfolio(w http.ResponseWriter, r *http.Request) {
	id, ok := accountID(w, r)
	if !ok {
		return
	}
	p, err := h.svc.Portfolio(r.Context(), identity.MustFromContext(r.Context()), id)
	if err != nil {
		httpx.Fail(w, r, translate(err))
		return
	}
	out := portfolioView{Holdings: p.Holdings.Amount(), Positions: make([]positionView, 0, len(p.Positions))}
	for _, pos := range p.Positions {
		out.Positions = append(out.Positions, positionView{
			Symbol: pos.Symbol, AssetClass: pos.AssetClass, Quantity: pos.Quantity,
			MarkPrice: amount(pos.MarkPrice), MarketValue: amount(pos.MarketValue), CostBasis: amount(pos.CostBasis),
			AsOf: pos.AsOf,
		})
	}
	httpx.JSON(w, r, http.StatusOK, out)
}

func (h *Handler) trades(w http.ResponseWriter, r *http.Request) {
	id, ok := accountID(w, r)
	if !ok {
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			httpx.Fail(w, r, httpx.BadRequest("invalid_limit", "El parámetro limit debe ser un entero positivo."))
			return
		}
		limit = n
	}
	trades, err := h.svc.Trades(r.Context(), identity.MustFromContext(r.Context()), id, limit)
	if err != nil {
		httpx.Fail(w, r, translate(err))
		return
	}
	out := make([]tradeView, 0, len(trades))
	for _, t := range trades {
		out = append(out, newTradeView(t))
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"trades": out})
}

func accountID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Fail(w, r, translate(ErrAccountNotFound))
		return uuid.Nil, false
	}
	return id, true
}

func translate(err error) error {
	switch {
	case errors.Is(err, ErrAccountNotFound):
		return httpx.NotFound("account_not_found", "La cuenta no existe.").WithCause(err)
	case errors.Is(err, ErrNotBrokerage):
		return httpx.BadRequest("not_brokerage_account", "Esta operación solo aplica a cuentas de inversión.").WithCause(err)
	case errors.Is(err, ErrNotLinked):
		return httpx.NotFound("ibkr_not_linked", "Esta cuenta todavía no tiene una Flex Query de IBKR configurada.").WithCause(err)
	case errors.Is(err, ErrAccountMismatch):
		return httpx.Unprocessable("ibkr_account_mismatch",
			"La Flex Query devolvió el reporte de otra cuenta de IBKR. Revisa que el ID de la cuenta vinculada y la Flex Query correspondan a la misma cuenta.").WithCause(err)
	case errors.Is(err, ErrEncryptionUnavailable):
		return httpx.Unprocessable("ibkr_unavailable",
			"La vinculación con IBKR no está habilitada en este servidor.").WithCause(err)
	default:
		return err
	}
}

// --- response shapes --------------------------------------------------------

type linkStatusView struct {
	IBKRAccountID  string     `json:"ibkr_account_id"`
	LastSyncedAt   *time.Time `json:"last_synced_at,omitempty"`
	LastSyncStatus string     `json:"last_sync_status"`
	LastSyncError  string     `json:"last_sync_error,omitempty"`
}

type syncResultView struct {
	RunID           uuid.UUID     `json:"run_id"`
	CashNew         int           `json:"cash_new"`
	CashDuplicate   int           `json:"cash_duplicate"`
	CashFailed      int           `json:"cash_failed"`
	TradesNew       int           `json:"trades_new"`
	TradesDuplicate int           `json:"trades_duplicate"`
	Positions       int           `json:"positions"`
	PeriodFrom      civil.Date    `json:"period_from"`
	PeriodTo        civil.Date    `json:"period_to"`
	GeneratedAt     *time.Time    `json:"generated_at,omitempty"`
	ReportedCash    *money.Amount `json:"reported_cash,omitempty"`
	MissingSections []string      `json:"missing_sections"`
	Warnings        []string      `json:"warnings"`
}

type portfolioView struct {
	Holdings  money.Amount   `json:"holdings"`
	Positions []positionView `json:"positions"`
}

type positionView struct {
	Symbol      string        `json:"symbol"`
	AssetClass  string        `json:"asset_class"`
	Quantity    float64       `json:"quantity"`
	MarkPrice   *money.Amount `json:"mark_price,omitempty"`
	MarketValue *money.Amount `json:"market_value,omitempty"`
	CostBasis   *money.Amount `json:"cost_basis,omitempty"`
	AsOf        civil.Date    `json:"as_of"`
}

type tradeView struct {
	ExternalRef string       `json:"id"`
	Symbol      string       `json:"symbol"`
	AssetClass  string       `json:"asset_class"`
	Side        string       `json:"side"`
	Quantity    float64      `json:"quantity"`
	Price       money.Amount `json:"price"`
	Commission  money.Amount `json:"commission"`
	NetCash     money.Amount `json:"net_cash"`
	TradeDate   civil.Date   `json:"trade_date"`
}

func newTradeView(t store.Trade) tradeView {
	return tradeView{ExternalRef: t.ExternalRef, Symbol: t.Symbol, AssetClass: t.AssetClass, Side: t.Side,
		Quantity: t.Quantity, Price: t.Price.Amount(), Commission: t.Commission.Amount(),
		NetCash: t.NetCash.Amount(), TradeDate: t.TradeDate}
}

func amount(c *money.Cents) *money.Amount {
	if c == nil {
		return nil
	}
	a := c.Amount()
	return &a
}
