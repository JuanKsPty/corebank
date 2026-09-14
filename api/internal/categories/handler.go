package categories

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/httpx"
	"github.com/JuanKsPty/corebank/api/internal/identity"
)

// Handler serves the category endpoints. Everything it exposes is scoped to
// the authenticated user, exactly like accounts.Handler.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes returns the /api/categories subrouter.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.tree)
	r.Post("/", h.create)
	r.Patch("/{id}", h.rename)
	r.Delete("/{id}", h.delete)
	return r
}

func (h *Handler) tree(w http.ResponseWriter, r *http.Request) {
	nodes, err := h.svc.Tree(r.Context(), identity.MustFromContext(r.Context()))
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, treeResponse{Categories: newNodeViews(nodes)})
}

type createRequest struct {
	Name     string  `json:"name"`
	Kind     string  `json:"kind"`
	ParentID *string `json:"parent_id"`
	Color    string  `json:"color"`
	Icon     string  `json:"icon"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	kind, err := ParseKind(req.Kind)
	if err != nil {
		httpx.Fail(w, r, httpx.Invalid(map[string]string{
			"kind": "El tipo debe ser income o expense.",
		}))
		return
	}

	var parentID *uuid.UUID
	if req.ParentID != nil && *req.ParentID != "" {
		id, err := uuid.Parse(*req.ParentID)
		if err != nil {
			httpx.Fail(w, r, httpx.Invalid(map[string]string{
				"parent_id": "El identificador de la categoría padre no es válido.",
			}))
			return
		}
		parentID = &id
	}

	category, err := h.svc.Create(r.Context(), identity.MustFromContext(r.Context()), CreateInput{
		Name:     req.Name,
		Kind:     kind,
		ParentID: parentID,
		Color:    req.Color,
		Icon:     req.Icon,
	})
	if err != nil {
		httpx.Fail(w, r, TranslateError(err))
		return
	}
	httpx.JSON(w, r, http.StatusCreated, NewView(category))
}

type renameRequest struct {
	Name  string `json:"name"`
	Color string `json:"color"`
	Icon  string `json:"icon"`
}

func (h *Handler) rename(w http.ResponseWriter, r *http.Request) {
	var req renameRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Fail(w, r, httpx.NotFound("category_not_found", "La categoría no existe."))
		return
	}

	category, err := h.svc.Rename(r.Context(), identity.MustFromContext(r.Context()), id, req.Name, req.Color, req.Icon)
	if err != nil {
		httpx.Fail(w, r, TranslateError(err))
		return
	}
	httpx.JSON(w, r, http.StatusOK, NewView(category))
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Fail(w, r, httpx.NotFound("category_not_found", "La categoría no existe."))
		return
	}

	if err := h.svc.Delete(r.Context(), identity.MustFromContext(r.Context()), id); err != nil {
		httpx.Fail(w, r, TranslateError(err))
		return
	}
	httpx.NoContent(w)
}

// TranslateError maps this package's errors onto HTTP responses.
//
// ErrNotOwned becomes 404, not 403, for the same reason accounts.TranslateError
// does it: a 403 would confirm the category id exists, turning an authenticated
// session into a probe.
func TranslateError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrNotOwned):
		return httpx.NotFound("category_not_found", "La categoría no existe.").WithCause(err)
	case errors.Is(err, ErrParentNotFound):
		return httpx.Invalid(map[string]string{
			"parent_id": "La categoría padre no existe.",
		}).WithCause(err)
	case errors.Is(err, ErrParentIsChild):
		return httpx.Invalid(map[string]string{
			"parent_id": "Una categoría hija no puede usarse como padre.",
		}).WithCause(err)
	case errors.Is(err, ErrKindMismatch):
		return httpx.Invalid(map[string]string{
			"kind": "El tipo debe coincidir con el de la categoría padre.",
		}).WithCause(err)
	case errors.Is(err, ErrNameRequired):
		return httpx.Invalid(map[string]string{"name": "El nombre es obligatorio."}).WithCause(err)
	case errors.Is(err, ErrNameTooLong):
		return httpx.Invalid(map[string]string{
			"name": "El nombre admite como máximo 60 caracteres.",
		}).WithCause(err)
	case errors.Is(err, ErrNameInvalid):
		return httpx.Invalid(map[string]string{
			"name": "El nombre tiene caracteres que no se pueden mostrar.",
		}).WithCause(err)
	case errors.Is(err, ErrNameTaken):
		return httpx.Conflict("category_name_taken",
			"Ya tienes una categoría con ese nombre en este nivel.").WithCause(err)
	default:
		return err
	}
}

// --- response shapes ---------------------------------------------------------

// View is a category as it appears in a JSON response.
type View struct {
	ID       string  `json:"id"`
	ParentID *string `json:"parent_id,omitempty"`
	Name     string  `json:"name"`
	Kind     string  `json:"kind"`
	Color    string  `json:"color,omitempty"`
	Icon     string  `json:"icon,omitempty"`
	IsSystem bool    `json:"is_system"`
}

// NewView renders one category.
func NewView(c Category) View {
	v := View{
		ID:       c.ID.String(),
		Name:     c.Name,
		Kind:     string(c.Kind),
		Color:    c.Color,
		Icon:     c.Icon,
		IsSystem: c.IsSystem,
	}
	if c.ParentID != nil {
		id := c.ParentID.String()
		v.ParentID = &id
	}
	return v
}

// NodeView is a category together with its own children.
type NodeView struct {
	View
	Children []View `json:"children"`
}

func newNodeView(n Node) NodeView {
	children := make([]View, 0, len(n.Children))
	for _, c := range n.Children {
		children = append(children, NewView(c))
	}
	return NodeView{View: NewView(n.Category), Children: children}
}

func newNodeViews(nodes []Node) []NodeView {
	out := make([]NodeView, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, newNodeView(n))
	}
	return out
}

type treeResponse struct {
	Categories []NodeView `json:"categories"`
}
