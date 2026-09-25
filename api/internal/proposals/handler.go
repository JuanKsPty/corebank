package proposals

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/accounts"
	"github.com/JuanKsPty/corebank/api/internal/httpx"
	"github.com/JuanKsPty/corebank/api/internal/identity"
	"github.com/JuanKsPty/corebank/api/internal/movements"
	"github.com/JuanKsPty/corebank/api/internal/rules"
	"github.com/JuanKsPty/corebank/api/internal/store"
	"github.com/JuanKsPty/corebank/api/internal/transfers"
)

// Handler serves /api/proposals.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes returns the /api/proposals subrouter.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.pending)
	r.Post("/{id}/apply", h.decide(h.svc.Apply))
	r.Post("/{id}/reject", h.decide(h.svc.Reject))
	return r
}

// View is a proposal on the wire.
type View struct {
	ID        uuid.UUID `json:"id"`
	Kind      string    `json:"kind"`
	Summary   string    `json:"summary"`
	Status    string    `json:"status"`
	ExpiresAt time.Time `json:"expires_at"`
}

func NewView(p store.Proposal) View {
	return View{ID: p.ID, Kind: p.Kind, Summary: p.Summary, Status: p.Status, ExpiresAt: p.ExpiresAt}
}

func (h *Handler) pending(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Pending(r.Context(), identity.MustFromContext(r.Context()))
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	out := make([]View, 0, len(list))
	for _, p := range list {
		out = append(out, NewView(p))
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"proposals": out})
}

func (h *Handler) decide(fn func(context.Context, uuid.UUID, uuid.UUID) (store.Proposal, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			httpx.Fail(w, r, httpx.NotFound("proposal_not_found", "La propuesta no existe."))
			return
		}
		p, err := fn(r.Context(), identity.MustFromContext(r.Context()), id)
		if err != nil {
			httpx.Fail(w, r, translate(err))
			return
		}
		httpx.JSON(w, r, http.StatusOK, NewView(p))
	}
}

func translate(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return httpx.NotFound("proposal_not_found", "La propuesta no existe.").WithCause(err)
	case errors.Is(err, ErrDecided):
		return httpx.Conflict("proposal_decided", "Esta propuesta ya se decidió o venció.").WithCause(err)
	case errors.Is(err, ErrInvalid):
		return httpx.Unprocessable("proposal_stale", "Ya no se puede aplicar: los datos cambiaron desde que se propuso.").WithCause(err)
	case errors.Is(err, rules.ErrDuplicate):
		return httpx.Conflict("rule_exists", "Ya tienes una regla para ese texto.").WithCause(err)
	case errors.Is(err, transfers.ErrNotACandidate), errors.Is(err, movements.ErrNotFound):
		return httpx.Unprocessable("proposal_stale", "Ya no se puede aplicar: los datos cambiaron desde que se propuso.").WithCause(err)
	default:
		return accounts.TranslateError(err)
	}
}
