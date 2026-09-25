package statement

import (
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/JuanKsPty/corebank/api/internal/civil"
	"github.com/JuanKsPty/corebank/api/internal/httpx"
	"github.com/JuanKsPty/corebank/api/internal/money"
)

// maxUploadSize bounds a statement upload. A year of movements from one bank
// is a few hundred KB.
const maxUploadSize = 20 << 20

// Handler serves the statement endpoints that need no database.
type Handler struct{}

func NewHandler() *Handler { return &Handler{} }

// Routes returns the /api/imports subrouter.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Post("/dry-run", h.dryRun)
	return r
}

// dryRun parses an uploaded file and reports what it says, writing nothing.
//
// It exists so a real file can be checked on the real deployment before
// anything depends on the parse being right: identity, period, the bank's own
// balances, and the first line where they stop adding up.
func (h *Handler) dryRun(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		httpx.Fail(w, r, httpx.BadRequest("upload_too_large_or_malformed",
			"El archivo es demasiado grande o la petición no es un formulario multipart válido."))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httpx.Fail(w, r, httpx.BadRequest("missing_file", "Falta el campo \"file\" con el archivo a revisar."))
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		httpx.Fail(w, r, httpx.BadRequest("upload_unreadable", "No se pudo leer el archivo."))
		return
	}

	parsed, err := Parse(header.Filename, data)
	if err != nil {
		if errors.Is(err, ErrUnrecognised) {
			httpx.Fail(w, r, httpx.Unprocessable("unrecognised_format",
				"El archivo no corresponde a ningún formato soportado (Banco General cuenta o tarjeta, BAC)."))
			return
		}
		httpx.Fail(w, r, httpx.Unprocessable("unreadable_statement", err.Error()).WithCause(err))
		return
	}
	httpx.JSON(w, r, http.StatusOK, NewFileView(parsed))
}

// FileView is a parsed file as the API reports it.
type FileView struct {
	Source     Source          `json:"source"`
	Statements []StatementView `json:"statements"`
}

// StatementView summarises one statement: enough to compare with the bank's
// own document at a glance.
type StatementView struct {
	Institution    string          `json:"institution"`
	ExternalNumber string          `json:"external_number"`
	DisplayName    string          `json:"display_name"`
	Class          Class           `json:"class"`
	Type           string          `json:"type"`
	Currency       string          `json:"currency"`
	PeriodStart    civil.Date      `json:"period_start"`
	PeriodEnd      civil.Date      `json:"period_end"`
	Opening        *money.Amount   `json:"opening,omitempty"`
	Closing        *money.Amount   `json:"closing,omitempty"`
	Available      *money.Amount   `json:"available,omitempty"`
	Held           *money.Amount   `json:"held,omitempty"`
	LineCount      int             `json:"line_count"`
	MoneyIn        money.Amount    `json:"money_in"`
	MoneyOut       money.Amount    `json:"money_out"`
	ChainBreak     *chainBreakView `json:"chain_break,omitempty"`
	Warnings       []string        `json:"warnings"`
}

type chainBreakView struct {
	LineNo   int          `json:"line_no"`
	Bank     money.Amount `json:"bank"`
	Computed money.Amount `json:"computed"`
}

// NewFileView builds the report for a parsed file.
func NewFileView(f File) FileView {
	out := FileView{Source: f.Source, Statements: make([]StatementView, 0, len(f.Statements))}
	for _, s := range f.Statements {
		v := StatementView{
			Institution:    s.Identity.Institution,
			ExternalNumber: s.Identity.ExternalNumber,
			DisplayName:    s.Identity.DisplayName,
			Class:          s.Identity.Class,
			Type:           s.Identity.Type,
			Currency:       s.Identity.Currency,
			PeriodStart:    s.PeriodStart,
			PeriodEnd:      s.PeriodEnd,
			Opening:        amount(s.Opening),
			Closing:        amount(s.Closing),
			Available:      amount(s.Available),
			Held:           amount(s.Held),
			LineCount:      len(s.Lines),
			Warnings:       append([]string{}, s.Warnings...),
		}
		var in, outflow money.Cents
		for _, l := range s.Lines {
			if l.Amount > 0 {
				in += l.Amount
			} else {
				outflow -= l.Amount
			}
		}
		v.MoneyIn, v.MoneyOut = in.Amount(), outflow.Amount()
		if b := s.ChainBreak; b != nil {
			v.ChainBreak = &chainBreakView{LineNo: b.LineNo, Bank: b.Bank.Amount(), Computed: b.Computed.Amount()}
		}
		out.Statements = append(out.Statements, v)
	}
	return out
}

func amount(c *money.Cents) *money.Amount {
	if c == nil {
		return nil
	}
	a := c.Amount()
	return &a
}
