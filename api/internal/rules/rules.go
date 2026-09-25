// Package rules manages the owner's category rules: "movements whose text
// contains X go under category Y", optionally "and are a transfer between my
// own accounts".
package rules

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/categories"
	"github.com/JuanKsPty/corebank/api/internal/httpx"
	"github.com/JuanKsPty/corebank/api/internal/identity"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

var (
	// ErrInvalid means the rule is missing its text or does nothing.
	ErrInvalid = errors.New("rules: a rule needs a text and a category or a kind")
	// ErrNotFound means no such rule belongs to the caller.
	ErrNotFound = errors.New("rules: rule not found")
	// ErrDuplicate means the user already has a rule for the same text.
	ErrDuplicate = errors.New("rules: a rule for that text already exists")
)

// Service manages rules.
type Service struct {
	db         *store.DB
	categories *categories.Service
}

func NewService(db *store.DB, cats *categories.Service) *Service {
	return &Service{db: db, categories: cats}
}

// Input is a rule to create or replace.
type Input struct {
	MatchText  string
	CategoryID *uuid.UUID
	Transfer   bool
	Priority   int
	// ApplyToExisting files already-imported movements that match, never
	// overwriting a category someone chose by hand.
	ApplyToExisting bool
}

func (s *Service) validate(ctx context.Context, userID uuid.UUID, in *Input) error {
	in.MatchText = strings.TrimSpace(in.MatchText)
	if in.MatchText == "" || utf8.RuneCountInString(in.MatchText) > 60 || (in.CategoryID == nil && !in.Transfer) {
		return ErrInvalid
	}
	if in.CategoryID != nil {
		if _, err := s.categories.Get(ctx, userID, *in.CategoryID); err != nil {
			return err
		}
	}
	return nil
}

func toRule(userID, id uuid.UUID, in Input) store.CategoryRule {
	r := store.CategoryRule{ID: id, UserID: userID, MatchText: in.MatchText, CategoryID: in.CategoryID, Priority: in.Priority}
	if in.Transfer {
		k := store.KindTransfer
		r.SetKind = &k
	}
	return r
}

// List returns the user's rules in the order they are tried.
func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]store.CategoryRule, error) {
	return s.db.Q().CategoryRulesByUser(ctx, userID)
}

// Create adds a rule and, when asked, applies it to what is already imported.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, in Input) (store.CategoryRule, int64, error) {
	if err := s.validate(ctx, userID, &in); err != nil {
		return store.CategoryRule{}, 0, err
	}
	r := toRule(userID, uuid.New(), in)
	var applied int64
	err := s.db.InTx(ctx, func(q *store.Queries) error {
		created, err := q.CreateCategoryRuleIfNew(ctx, r)
		if err != nil {
			return err
		}
		if !created {
			return ErrDuplicate
		}
		if in.ApplyToExisting {
			applied, err = q.ApplyCategoryRule(ctx, r)
		}
		return err
	})
	return r, applied, err
}

// Update replaces a rule and, when asked, applies it again.
func (s *Service) Update(ctx context.Context, userID, id uuid.UUID, in Input) (store.CategoryRule, int64, error) {
	if err := s.validate(ctx, userID, &in); err != nil {
		return store.CategoryRule{}, 0, err
	}
	r := toRule(userID, id, in)
	var applied int64
	err := s.db.InTx(ctx, func(q *store.Queries) error {
		if err := q.UpdateCategoryRule(ctx, r); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return ErrNotFound
			}
			if errors.Is(err, store.ErrConflict) {
				return ErrDuplicate
			}
			return err
		}
		if in.ApplyToExisting {
			var err error
			applied, err = q.ApplyCategoryRule(ctx, r)
			return err
		}
		return nil
	})
	return r, applied, err
}

// Delete removes a rule. Movements it already filed keep their category.
func (s *Service) Delete(ctx context.Context, userID, id uuid.UUID) error {
	if err := s.db.Q().DeleteCategoryRule(ctx, userID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

// Handler serves /api/category-rules.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes returns the /api/category-rules subrouter.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Put("/{id}", h.update)
	r.Delete("/{id}", h.delete)
	return r
}

type ruleRequest struct {
	MatchText       string     `json:"match_text"`
	CategoryID      *uuid.UUID `json:"category_id"`
	Transfer        bool       `json:"transfer"`
	Priority        int        `json:"priority"`
	ApplyToExisting bool       `json:"apply_to_existing"`
}

func (b ruleRequest) input() Input {
	return Input{MatchText: b.MatchText, CategoryID: b.CategoryID, Transfer: b.Transfer, Priority: b.Priority,
		ApplyToExisting: b.ApplyToExisting}
}

// View is a rule on the wire.
type View struct {
	ID         uuid.UUID  `json:"id"`
	MatchText  string     `json:"match_text"`
	CategoryID *uuid.UUID `json:"category_id,omitempty"`
	Transfer   bool       `json:"transfer"`
	Priority   int        `json:"priority"`
	// Applied is how many existing movements the change filed, when asked to.
	Applied int64 `json:"applied,omitempty"`
}

// NewView builds a rule's wire shape.
func NewView(r store.CategoryRule) View {
	return View{ID: r.ID, MatchText: r.MatchText, CategoryID: r.CategoryID,
		Transfer: r.SetKind != nil && *r.SetKind == store.KindTransfer, Priority: r.Priority}
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.List(r.Context(), identity.MustFromContext(r.Context()))
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	out := make([]View, 0, len(list))
	for _, rule := range list {
		out = append(out, NewView(rule))
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"rules": out})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var body ruleRequest
	if err := httpx.Decode(w, r, &body); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	rule, applied, err := h.svc.Create(r.Context(), identity.MustFromContext(r.Context()), body.input())
	if err != nil {
		httpx.Fail(w, r, translate(err))
		return
	}
	v := NewView(rule)
	v.Applied = applied
	httpx.JSON(w, r, http.StatusCreated, v)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	var body ruleRequest
	if err := httpx.Decode(w, r, &body); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Fail(w, r, translate(ErrNotFound))
		return
	}
	rule, applied, err := h.svc.Update(r.Context(), identity.MustFromContext(r.Context()), id, body.input())
	if err != nil {
		httpx.Fail(w, r, translate(err))
		return
	}
	v := NewView(rule)
	v.Applied = applied
	httpx.JSON(w, r, http.StatusOK, v)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Fail(w, r, translate(ErrNotFound))
		return
	}
	if err := h.svc.Delete(r.Context(), identity.MustFromContext(r.Context()), id); err != nil {
		httpx.Fail(w, r, translate(err))
		return
	}
	httpx.NoContent(w)
}

func translate(err error) error {
	switch {
	case errors.Is(err, ErrInvalid):
		return httpx.Invalid(map[string]string{
			"match_text": "La regla necesita un texto de hasta 60 caracteres y una categoría o marcarse como transferencia.",
		}).WithCause(err)
	case errors.Is(err, ErrNotFound):
		return httpx.NotFound("rule_not_found", "La regla no existe.").WithCause(err)
	case errors.Is(err, ErrDuplicate):
		return httpx.Conflict("rule_exists", "Ya tienes una regla para ese texto.").WithCause(err)
	default:
		return categories.TranslateError(err)
	}
}
