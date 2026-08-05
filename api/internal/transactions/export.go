package transactions

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"time"

	"github.com/JuanKsPty/corebank/api/internal/httpx"
	"github.com/JuanKsPty/corebank/api/internal/identity"
	"github.com/JuanKsPty/corebank/api/internal/logging"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

// exportPageSize is how much is read per round trip while streaming.
//
// The maximum the filter allows. Nobody is reading this a page at a time — it is one
// file — so the only thing page size changes here is how many round trips it costs.
const exportPageSize = 200

// exportRowLimit is a runaway guard, not a product limit.
//
// The seeded dataset holds 6429 movements across 1000 customers, so no real export
// comes near this; it exists so that a bug in the cursor cannot turn one request into
// an endless one. If it is ever reached the file is still valid CSV and the truncation
// is logged, because a statement that silently stops short is worse than one that
// admits where it ended.
const exportRowLimit = 50_000

// movementNames and statusNames are the words that go in the file.
//
// Spanish, because the person downloading this opens it in a spreadsheet, and
// "withdrawal" in a column headed "tipo" helps nobody. They are written here rather
// than borrowed because the interface has its own copy for the screen and the
// rule-based assistant has one for prose: a shared table would have to live in a
// package all three could import, which for nine words is more indirection than the
// duplication costs. The `id` column is the stable handle for anything that needs to
// match a row back to the API.
var movementNames = map[string]string{
	"deposit":           "Depósito",
	"withdrawal":        "Retiro",
	"transfer":          "Transferencia",
	"internal_transfer": "Traspaso entre cuentas propias",
}

var statusNames = map[string]string{
	"completed": "Completado",
	"pending":   "Pendiente de confirmar",
	"failed":    "Fallido",
	"voided":    "Cancelado",
	"expired":   "Expirado",
}

func spanish(names map[string]string, key string) string {
	if name, ok := names[key]; ok {
		return name
	}
	// An unmapped value is shown as it is rather than blanked: a row with an
	// unfamiliar status is still a row about somebody's money.
	return key
}

// csvHeader names the columns, in the order csvRow writes them.
var csvHeader = []string{
	"fecha", "tipo", "estado", "monto", "moneda",
	"cuenta_origen", "cuenta_destino", "descripcion", "id",
}

// csvRow renders one movement.
//
// Separate from the streaming so the format — which is the part with decisions in it
// — can be tested without a database behind it.
func csvRow(tx store.Transaction) []string {
	return []string{
		// RFC 3339, so the column is unambiguous whoever opens it. A localised date
		// would be prettier and would stop being parseable.
		tx.OccurredAt.UTC().Format(time.RFC3339),
		spanish(movementNames, tx.Kind.String()),
		spanish(statusNames, string(tx.Status)),
		// The same decimal string the API returns: a period, always two places, no
		// thousands separator. It is what round-trips.
		tx.Amount.String(),
		tx.Currency,
		tx.FromAccount,
		tx.ToAccount,
		tx.Description,
		tx.ID.String(),
	}
}

// exportCSV streams the customer's movements as a CSV file.
//
// It takes the same filters as the paginated history and applies them identically, so
// the file matches what was on screen when the button was pressed. What it does not
// take is a cursor: a statement is not paged, so this walks the pages internally and
// writes them out as one document.
//
// Written straight to the response as each page arrives rather than assembled in
// memory first. The size is the customer's own history so it is never large here, but
// an export that buffers is an export whose memory cost is set by the biggest account
// in the bank.
func (h *Handler) exportCSV(w http.ResponseWriter, r *http.Request) {
	filter, err := parseHistoryQuery(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	// A cursor in the query string is for the paginated view and means nothing here.
	// Honouring it would silently drop the beginning of somebody's statement.
	filter.Cursor = store.Cursor{}
	filter.Limit = exportPageSize

	userID := identity.MustFromContext(r.Context())
	logger := logging.FromContext(r.Context())

	// The first page is fetched before any header is written, so a failure that
	// happens immediately is still a JSON error rather than a half-written file with
	// a 200 on it.
	page, err := h.svc.History(r.Context(), userID, HistoryQuery{Filter: filter})
	if err != nil {
		httpx.Fail(w, r, translate(err))
		return
	}

	filename := fmt.Sprintf("corebank-movimientos-%s.csv", time.Now().UTC().Format("2006-01-02"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	// A UTF-8 byte order mark. Excel reads a CSV as the system's legacy codepage
	// unless one is present, which turns every "Depósito" into mojibake — and this
	// file is mostly Spanish. Everything that is not Excel ignores it.
	if _, err := w.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return
	}

	out := csv.NewWriter(w)
	if err := out.Write(csvHeader); err != nil {
		logger.Warn("could not write the CSV header", "error", err)
		return
	}

	var written int
	for {
		for _, tx := range page.Transactions {
			if err := out.Write(csvRow(tx)); err != nil {
				// The client hung up mid-download. Nothing to report to it.
				logger.Warn("the CSV export was cut short", "error", err, "rows", written)
				return
			}
			written++
		}

		// Flushed per page so a long export arrives progressively instead of landing
		// all at once when the handler returns.
		out.Flush()
		if err := out.Error(); err != nil {
			logger.Warn("the CSV export was cut short", "error", err, "rows", written)
			return
		}

		if !page.HasMore || written >= exportRowLimit {
			if page.HasMore {
				logger.Warn("the CSV export hit its runaway guard and stopped short",
					"rows", written, "limit", exportRowLimit)
			}
			break
		}

		filter.Cursor = page.Next
		page, err = h.svc.History(r.Context(), userID, HistoryQuery{Filter: filter})
		if err != nil {
			// The headers are long gone, so this cannot become an error response.
			// The file ends where it ends and the reason is in the log.
			logger.Error("the CSV export failed part-way", "error", err, "rows", written)
			return
		}
	}

	logger.Info("exported movements as CSV", "rows", written)
}
