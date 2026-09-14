package investments

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/JuanKsPty/corebank/api/internal/accounts"
	"github.com/JuanKsPty/corebank/api/internal/httpx"
	"github.com/JuanKsPty/corebank/api/internal/identity"
	"github.com/JuanKsPty/corebank/api/internal/money"
)

// Handler serves the investment endpoints. Everything is scoped to the
// authenticated user and to one of their own "investment"-kind accounts,
// exactly like accounts.Handler.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes returns the /api/investments subrouter.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Route("/accounts/{number}", func(r chi.Router) {
		r.Put("/link", h.setLink)
		r.Get("/link", h.linkStatus)
		r.Post("/sync", h.sync)
		r.Get("/portfolio", h.portfolio)
		r.Get("/positions", h.positions)
		r.Get("/trades", h.trades)
	})
	return r
}

type linkRequest struct {
	IBKRAccountID string `json:"ibkr_account_id"`
	FlexQueryID   string `json:"flex_query_id"`
	// FlexToken is the Flex Web Service token from Client Portal, sent once
	// and never echoed back — GET .../link never returns it, only whether a
	// link exists and how its last sync went.
	FlexToken string `json:"flex_token"`
}

func (h *Handler) setLink(w http.ResponseWriter, r *http.Request) {
	var req linkRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	problems := map[string]string{}
	if req.IBKRAccountID == "" {
		problems["ibkr_account_id"] = "El identificador de cuenta de IBKR es obligatorio."
	}
	if req.FlexQueryID == "" {
		problems["flex_query_id"] = "El identificador de la Flex Query es obligatorio."
	}
	if req.FlexToken == "" {
		problems["flex_token"] = "El token de Flex Web Service es obligatorio."
	}
	if len(problems) > 0 {
		httpx.Fail(w, r, httpx.Invalid(problems))
		return
	}

	number := chi.URLParam(r, "number")
	err := h.svc.SetLink(r.Context(), identity.MustFromContext(r.Context()),
		number, req.IBKRAccountID, req.FlexQueryID, req.FlexToken)
	if err != nil {
		httpx.Fail(w, r, translate(err))
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) linkStatus(w http.ResponseWriter, r *http.Request) {
	status, err := h.svc.GetLinkStatus(r.Context(), identity.MustFromContext(r.Context()), chi.URLParam(r, "number"))
	if err != nil {
		httpx.Fail(w, r, translate(err))
		return
	}
	httpx.JSON(w, r, http.StatusOK, linkStatusView{
		IBKRAccountID:  status.IBKRAccountID,
		LastSyncedAt:   status.LastSyncedAt,
		LastSyncStatus: status.LastSyncStatus,
		LastSyncError:  status.LastSyncError,
	})
}

func (h *Handler) sync(w http.ResponseWriter, r *http.Request) {
	result, err := h.svc.Sync(r.Context(), identity.MustFromContext(r.Context()), chi.URLParam(r, "number"))
	if err != nil {
		httpx.Fail(w, r, translate(err))
		return
	}
	httpx.JSON(w, r, http.StatusOK, syncResultView{
		CashMovementsPosted:  result.CashMovementsPosted,
		CashMovementsSkipped: result.CashMovementsSkipped,
		TradesRecorded:       result.TradesRecorded,
		TradesSkipped:        result.TradesSkipped,
		Positions:            result.Positions,
	})
}

func (h *Handler) portfolio(w http.ResponseWriter, r *http.Request) {
	p, err := h.svc.Portfolio(r.Context(), identity.MustFromContext(r.Context()), chi.URLParam(r, "number"))
	if err != nil {
		httpx.Fail(w, r, translate(err))
		return
	}
	httpx.JSON(w, r, http.StatusOK, portfolioView{
		Cash:          p.Cash.Amount(),
		HoldingsValue: p.HoldingsValue.Amount(),
		// TotalValue is a convenience sum, but the two figures above are the
		// ones to trust individually — see the package doc on why they come
		// from different sources of truth.
		TotalValue: p.Cash.Add(p.HoldingsValue).Amount(),
		Positions:  newPositionViews(p.Positions),
	})
}

func (h *Handler) positions(w http.ResponseWriter, r *http.Request) {
	positions, err := h.svc.Positions(r.Context(), identity.MustFromContext(r.Context()), chi.URLParam(r, "number"))
	if err != nil {
		httpx.Fail(w, r, translate(err))
		return
	}
	httpx.JSON(w, r, http.StatusOK, positionsResponse{Positions: newPositionViews(positions)})
}

func (h *Handler) trades(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			httpx.Fail(w, r, httpx.BadRequest("invalid_limit", "El parámetro limit debe ser un entero positivo."))
			return
		}
		limit = n
	}

	trades, err := h.svc.Trades(r.Context(), identity.MustFromContext(r.Context()), chi.URLParam(r, "number"), limit)
	if err != nil {
		httpx.Fail(w, r, translate(err))
		return
	}
	httpx.JSON(w, r, http.StatusOK, tradesResponse{Trades: newTradeViews(trades)})
}

// translate maps this package's and its collaborators' errors onto responses.
func translate(err error) error {
	switch {
	case errors.Is(err, ErrNotInvestmentAccount):
		return httpx.BadRequest("not_investment_account",
			"Esta operación solo aplica a cuentas de tipo investment.").WithCause(err)
	case errors.Is(err, ErrNotLinked):
		return httpx.NotFound("ibkr_not_linked",
			"Esta cuenta todavía no tiene una Flex Query de IBKR configurada.").WithCause(err)
	case errors.Is(err, ErrEncryptionUnavailable):
		return httpx.Internal(err)
	default:
		return accounts.TranslateError(err)
	}
}

// --- response shapes ---------------------------------------------------------

type linkStatusView struct {
	IBKRAccountID  string     `json:"ibkr_account_id"`
	LastSyncedAt   *time.Time `json:"last_synced_at,omitempty"`
	LastSyncStatus string     `json:"last_sync_status"`
	LastSyncError  string     `json:"last_sync_error,omitempty"`
}

type syncResultView struct {
	CashMovementsPosted  int `json:"cash_movements_posted"`
	CashMovementsSkipped int `json:"cash_movements_skipped"`
	TradesRecorded       int `json:"trades_recorded"`
	TradesSkipped        int `json:"trades_skipped"`
	Positions            int `json:"positions"`
}

type portfolioView struct {
	Cash          money.Amount   `json:"cash"`
	HoldingsValue money.Amount   `json:"holdings_value"`
	TotalValue    money.Amount   `json:"total_value"`
	Positions     []positionView `json:"positions"`
}

type positionsResponse struct {
	Positions []positionView `json:"positions"`
}

type positionView struct {
	Symbol      string       `json:"symbol"`
	AssetClass  string       `json:"asset_class"`
	Quantity    float64      `json:"quantity"`
	MarkPrice   money.Amount `json:"mark_price"`
	MarketValue money.Amount `json:"market_value"`
	CostBasis   money.Amount `json:"cost_basis"`
	AsOf        time.Time    `json:"as_of"`
}

func newPositionViews(list []Position) []positionView {
	out := make([]positionView, 0, len(list))
	for _, p := range list {
		out = append(out, positionView{
			Symbol:      p.Symbol,
			AssetClass:  p.AssetClass,
			Quantity:    p.Quantity,
			MarkPrice:   p.MarkPrice.Amount(),
			MarketValue: p.MarketValue.Amount(),
			CostBasis:   p.CostBasis.Amount(),
			AsOf:        p.AsOf,
		})
	}
	return out
}

type tradesResponse struct {
	Trades []tradeView `json:"trades"`
}

type tradeView struct {
	Symbol     string       `json:"symbol"`
	AssetClass string       `json:"asset_class"`
	Side       string       `json:"side"`
	Quantity   float64      `json:"quantity"`
	Price      money.Amount `json:"price"`
	Commission money.Amount `json:"commission"`
	NetCash    money.Amount `json:"net_cash"`
	TradeDate  time.Time    `json:"trade_date"`
}

func newTradeViews(list []Trade) []tradeView {
	out := make([]tradeView, 0, len(list))
	for _, t := range list {
		out = append(out, tradeView{
			Symbol:     t.Symbol,
			AssetClass: t.AssetClass,
			Side:       t.Side,
			Quantity:   t.Quantity,
			Price:      t.Price.Amount(),
			Commission: t.Commission.Amount(),
			NetCash:    t.NetCash.Amount(),
			TradeDate:  t.TradeDate,
		})
	}
	return out
}
