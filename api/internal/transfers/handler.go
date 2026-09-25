package transfers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/httpx"
	"github.com/JuanKsPty/corebank/api/internal/identity"
	"github.com/JuanKsPty/corebank/api/internal/movements"
)

// Handler serves /api/transfers.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes returns the /api/transfers subrouter.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/suggestions", h.suggestions)
	r.Post("/decisions", h.decide)
	return r
}

type pairView struct {
	Out movements.View `json:"out"`
	In  movements.View `json:"in"`
}

func (h *Handler) suggestions(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Suggestions(r.Context(), identity.MustFromContext(r.Context()))
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	out := make([]pairView, 0, len(list))
	for _, c := range list {
		out = append(out, pairView{Out: movements.NewView(c.Out), In: movements.NewView(c.In)})
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"suggestions": out})
}

type decisionRequest struct {
	OutEntryID uuid.UUID `json:"out_entry_id"`
	InEntryID  uuid.UUID `json:"in_entry_id"`
	Confirm    bool      `json:"confirm"`
}

func (h *Handler) decide(w http.ResponseWriter, r *http.Request) {
	var body decisionRequest
	if err := httpx.Decode(w, r, &body); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	err := h.svc.Decide(r.Context(), identity.MustFromContext(r.Context()), body.OutEntryID, body.InEntryID, body.Confirm, "user")
	if errors.Is(err, ErrNotACandidate) {
		httpx.Fail(w, r, httpx.Unprocessable("not_a_transfer", "Esos dos movimientos no pueden ser una misma transferencia.").WithCause(err))
		return
	}
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.NoContent(w)
}
