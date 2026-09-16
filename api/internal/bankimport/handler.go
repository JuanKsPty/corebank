package bankimport

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/categories"
	"github.com/JuanKsPty/corebank/api/internal/httpx"
	"github.com/JuanKsPty/corebank/api/internal/identity"
)

// maxUploadSize bounds a statement upload. The largest real file seen while
// building this package (a year of Banco General account movements) is a
// few hundred KB; this leaves generous room without letting an unbounded
// body become a memory problem — the first multipart endpoint in this
// codebase, everywhere else takes small JSON bodies that httpx.Decode's own
// 1 MiB cap already covers.
const maxUploadSize = 20 << 20 // 20 MiB

// Handler serves the bank-import endpoints. Everything is scoped to the
// authenticated user, exactly like accounts.Handler.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes returns the /api/external-accounts subrouter. The
// /api/external-transactions/{id}/category endpoint is mounted separately
// by server.go, the same way transactions/{id}/category sits under
// /api/transactions rather than under /api/accounts — the category belongs
// to the transaction's URL space, not the account's.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.list)
	r.Post("/import", h.importFile)
	r.Get("/{id}/transactions", h.transactions)
	r.Get("/{id}/imports", h.imports)
	r.Get("/{id}/spend-by-category", h.spendByCategory)
	return r
}

// SetCategory handles PATCH /api/external-transactions/{id}/category.
// Exported so server.go can mount it under a different path prefix.
func (h *Handler) SetCategory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CategoryID *string `json:"category_id"`
	}
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

	if err := h.svc.SetTransactionCategory(r.Context(), identity.MustFromContext(r.Context()), id, categoryID); err != nil {
		httpx.Fail(w, r, translate(err))
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	accounts, err := h.svc.Accounts(r.Context(), identity.MustFromContext(r.Context()))
	if err != nil {
		httpx.Fail(w, r, translate(err))
		return
	}
	httpx.JSON(w, r, http.StatusOK, accountsResponse{Accounts: newAccountViews(accounts)})
}

func (h *Handler) importFile(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		httpx.Fail(w, r, httpx.BadRequest("upload_too_large_or_malformed",
			"El archivo es demasiado grande o la petición no es un formulario multipart válido."))
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		httpx.Fail(w, r, httpx.BadRequest("missing_file", "Falta el campo \"file\" con el archivo a importar."))
		return
	}
	defer file.Close()

	data, err := readAllLimited(file, maxUploadSize)
	if err != nil {
		httpx.Fail(w, r, httpx.BadRequest("upload_too_large", "El archivo es demasiado grande."))
		return
	}

	// Optional, and only meaningful the first time this file's account is
	// seen: which of the customer's own accounts a bank-account statement
	// (never a card) should be linked to. Left empty, one is opened
	// automatically. r.FormValue reads from the multipart form already
	// parsed above, so no second parse is needed.
	targetAccountNumber := r.FormValue("account_number")

	result, err := h.svc.Import(r.Context(), identity.MustFromContext(r.Context()), header.Filename, data, targetAccountNumber)
	if err != nil {
		httpx.Fail(w, r, translate(err))
		return
	}

	view := importResultView{
		ExternalAccountID: result.ImportedAccountID.String(),
		Format:            result.Format,
		TotalRows:         result.TotalRows,
		Imported:          result.Imported,
		SkippedDuplicates: result.SkippedDuplicates,
		IsCard:            result.IsCard,
	}
	if result.LinkedAccountNumber != "" {
		view.LinkedAccountNumber = &result.LinkedAccountNumber
	}
	httpx.JSON(w, r, http.StatusOK, view)
}

func (h *Handler) transactions(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Fail(w, r, httpx.NotFound("account_not_found", "La cuenta importada no existe."))
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

	list, err := h.svc.Transactions(r.Context(), identity.MustFromContext(r.Context()), id, limit)
	if err != nil {
		httpx.Fail(w, r, translate(err))
		return
	}
	httpx.JSON(w, r, http.StatusOK, transactionsResponse{Transactions: newTransactionViews(list)})
}

func (h *Handler) imports(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Fail(w, r, httpx.NotFound("account_not_found", "La cuenta importada no existe."))
		return
	}

	list, err := h.svc.ImportHistory(r.Context(), identity.MustFromContext(r.Context()), id)
	if err != nil {
		httpx.Fail(w, r, translate(err))
		return
	}
	httpx.JSON(w, r, http.StatusOK, importBatchesResponse{Imports: newImportBatchViews(list)})
}

func (h *Handler) spendByCategory(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Fail(w, r, httpx.NotFound("account_not_found", "La cuenta importada no existe."))
		return
	}

	spend, err := h.svc.SpendByCategory(r.Context(), identity.MustFromContext(r.Context()), id)
	if err != nil {
		httpx.Fail(w, r, translate(err))
		return
	}

	out := make([]categorySpendView, 0, len(spend))
	for _, s := range spend {
		v := categorySpendView{AmountCents: s.AmountCents}
		if s.CategoryID != nil {
			id := s.CategoryID.String()
			v.CategoryID = &id
		}
		out = append(out, v)
	}
	httpx.JSON(w, r, http.StatusOK, spendByCategoryResponse{Spend: out})
}

// translate maps this package's and its collaborators' errors onto responses.
func translate(err error) error {
	switch {
	case errors.Is(err, ErrAccountNotFound), errors.Is(err, ErrAccountNotOwned):
		return httpx.NotFound("account_not_found", "La cuenta importada no existe.").WithCause(err)
	case errors.Is(err, ErrTransactionNotFound):
		return httpx.NotFound("transaction_not_found", "El movimiento no existe.").WithCause(err)
	case errors.Is(err, ErrNoAccountHint):
		return httpx.Unprocessable("account_not_identified",
			"No se pudo identificar a qué cuenta pertenece este archivo.").WithCause(err)
	case errors.Is(err, ErrUnrecognisedFormat):
		return httpx.Unprocessable("format_not_recognised",
			"No se reconoce el formato de este archivo.").WithCause(err)
	case errors.Is(err, ErrTargetAccountNotFound):
		return httpx.Invalid(map[string]string{
			"account_number": "Esa cuenta no existe.",
		}).WithCause(err)
	case errors.Is(err, categories.ErrNotFound), errors.Is(err, categories.ErrNotOwned):
		return httpx.Invalid(map[string]string{"category_id": "La categoría no existe."}).WithCause(err)
	default:
		return err
	}
}
