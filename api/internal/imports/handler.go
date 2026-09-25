package imports

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/civil"
	"github.com/JuanKsPty/corebank/api/internal/httpx"
	"github.com/JuanKsPty/corebank/api/internal/identity"
	"github.com/JuanKsPty/corebank/api/internal/money"
	"github.com/JuanKsPty/corebank/api/internal/statement"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

const maxUploadSize = 20 << 20

// Handler serves /api/imports.
type Handler struct {
	svc     *Service
	db      *store.DB
	dryRuns *statement.Handler
}

func NewHandler(svc *Service, db *store.DB) *Handler {
	return &Handler{svc: svc, db: db, dryRuns: statement.NewHandler()}
}

// Routes returns the /api/imports subrouter.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Post("/", h.importFile)
	r.Get("/", h.runs)
	r.Post("/dry-run", h.dryRuns.DryRun)
	return r
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
	data, err := io.ReadAll(file)
	if err != nil {
		httpx.Fail(w, r, httpx.BadRequest("upload_unreadable", "No se pudo leer el archivo."))
		return
	}

	// Set when the import started from an account's own page: a file for any
	// other account is then refused instead of filed elsewhere.
	var expect *uuid.UUID
	if raw := r.FormValue("account_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.Fail(w, r, httpx.Invalid(map[string]string{"account_id": "La cuenta no es válida."}))
			return
		}
		expect = &id
	}

	result, err := h.svc.Import(r.Context(), identity.MustFromContext(r.Context()), header.Filename, data, expect)
	if err != nil {
		httpx.Fail(w, r, translate(err))
		return
	}
	httpx.JSON(w, r, http.StatusOK, newResultView(result))
}

func (h *Handler) runs(w http.ResponseWriter, r *http.Request) {
	accountID, err := uuid.Parse(r.URL.Query().Get("account_id"))
	if err != nil {
		httpx.Fail(w, r, httpx.BadRequest("account_required", "Indica account_id."))
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	runs, err := h.db.Q().ImportRunsByAccount(r.Context(), identity.MustFromContext(r.Context()), accountID, limit)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	out := make([]RunView, 0, len(runs))
	for _, run := range runs {
		out = append(out, NewRunView(run))
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"runs": out})
}

func translate(err error) error {
	switch {
	case errors.Is(err, statement.ErrUnrecognised):
		return httpx.Unprocessable("unrecognised_format",
			"El archivo no corresponde a ningún formato soportado (Banco General cuenta o tarjeta, BAC).").WithCause(err)
	case errors.Is(err, ErrAccountMismatch):
		return httpx.Conflict("account_mismatch",
			"Este archivo es de otra cuenta. Impórtalo desde la página de esa cuenta o desde Cuentas.").WithCause(err)
	case errors.Is(err, ErrUnreadable):
		// The parser's message names the line and the problem, which is what
		// the owner needs to fix the file or report the format.
		return httpx.Unprocessable("unreadable_statement", errors.Unwrap(err).Error()).WithCause(err)
	default:
		return err
	}
}

// ResultView is an import's outcome on the wire.
type ResultView struct {
	Source    statement.Source    `json:"source"`
	Unchanged bool                `json:"unchanged"`
	Accounts  []AccountResultView `json:"accounts"`
}

// AccountResultView is what one statement in the file did.
type AccountResultView struct {
	AccountID    uuid.UUID       `json:"account_id"`
	DisplayName  string          `json:"display_name"`
	Created      bool            `json:"created"`
	PeriodStart  civil.Date      `json:"period_start"`
	PeriodEnd    civil.Date      `json:"period_end"`
	Lines        int             `json:"lines"`
	New          int             `json:"new"`
	Duplicates   int             `json:"duplicates"`
	Opening      *money.Amount   `json:"opening,omitempty"`
	Closing      *money.Amount   `json:"closing,omitempty"`
	ChainBreak   *chainBreakView `json:"chain_break,omitempty"`
	Warnings     []string        `json:"warnings"`
	NeedsOpening bool            `json:"needs_opening"`
}

type chainBreakView struct {
	LineNo   int          `json:"line_no"`
	Bank     money.Amount `json:"bank"`
	Computed money.Amount `json:"computed"`
}

func newResultView(r Result) ResultView {
	v := ResultView{Source: r.Source, Unchanged: r.Unchanged, Accounts: []AccountResultView{}}
	for _, a := range r.Accounts {
		av := AccountResultView{
			AccountID: a.AccountID, DisplayName: a.DisplayName, Created: a.Created,
			PeriodStart: a.PeriodStart, PeriodEnd: a.PeriodEnd, Lines: a.Lines, New: a.New,
			Duplicates: a.Duplicates, Opening: amountPtr(a.Opening), Closing: amountPtr(a.Closing),
			Warnings: append([]string{}, a.Warnings...), NeedsOpening: a.NeedsOpening,
		}
		if b := a.ChainBreak; b != nil {
			av.ChainBreak = &chainBreakView{LineNo: b.LineNo, Bank: b.Bank.Amount(), Computed: b.Computed.Amount()}
		}
		v.Accounts = append(v.Accounts, av)
	}
	return v
}

// RunView is one import run on the wire.
type RunView struct {
	ID          uuid.UUID      `json:"id"`
	Source      string         `json:"source"`
	Status      string         `json:"status"`
	Filename    string         `json:"filename,omitempty"`
	PeriodStart civil.Date     `json:"period_start"`
	PeriodEnd   civil.Date     `json:"period_end"`
	Seen        int            `json:"lines_seen"`
	New         int            `json:"lines_new"`
	Duplicates  int            `json:"lines_duplicate"`
	Failed      int            `json:"lines_failed"`
	Details     map[string]any `json:"details"`
	Error       string         `json:"error,omitempty"`
	CreatedAt   string         `json:"created_at"`
}

// NewRunView builds a run's wire shape.
func NewRunView(r store.ImportRun) RunView {
	details := r.Details
	if details == nil {
		details = map[string]any{}
	}
	return RunView{ID: r.ID, Source: r.Source, Status: r.Status, Filename: r.Filename,
		PeriodStart: r.PeriodStart, PeriodEnd: r.PeriodEnd, Seen: r.LinesSeen, New: r.LinesNew,
		Duplicates: r.LinesDuplicate, Failed: r.LinesFailed, Details: details, Error: r.Error,
		CreatedAt: r.CreatedAt.UTC().Format("2006-01-02T15:04:05Z")}
}

func amountPtr(c *money.Cents) *money.Amount {
	if c == nil {
		return nil
	}
	a := c.Amount()
	return &a
}
