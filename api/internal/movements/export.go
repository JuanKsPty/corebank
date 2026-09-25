package movements

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/httpx"
	"github.com/JuanKsPty/corebank/api/internal/identity"
	"github.com/JuanKsPty/corebank/api/internal/logging"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

// kindNames are the kinds as a Spanish reader of the file expects them.
var kindNames = map[string]string{
	store.KindIncome: "ingreso", store.KindExpense: "gasto", store.KindRefund: "reembolso",
	store.KindFee: "comisión", store.KindInterest: "intereses", store.KindTransfer: "transferencia",
	store.KindTrade: "compraventa",
}

// Export serves GET /api/entries/export.csv: every movement matching the same
// filters as the list, oldest first, as a file a spreadsheet opens.
func (h *Handler) Export(w http.ResponseWriter, r *http.Request) {
	q, err := ParseQuery(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	for _, raw := range r.URL.Query()["account_id"] {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.Fail(w, r, httpx.BadRequest("invalid_account", "account_id no es válido."))
			return
		}
		q.AccountIDs = append(q.AccountIDs, id)
	}
	q.Before, q.Limit = nil, 0
	userID := identity.MustFromContext(r.Context())
	ctx := r.Context()

	accounts, err := h.svc.db.Q().AccountsByUser(ctx, userID)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	byID := map[uuid.UUID]store.Account{}
	for _, a := range accounts {
		byID[a.ID] = a
	}
	names, err := h.svc.categoryNames(ctx, userID)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="movimientos-%s.csv"`, time.Now().Format("2006-01-02")))
	// A byte-order mark so a spreadsheet reads the accents as UTF-8.
	_, _ = w.Write([]byte("\xef\xbb\xbf"))
	out := csv.NewWriter(w)
	_ = out.Write([]string{"fecha", "cuenta", "institucion", "tipo", "categoria", "monto", "moneda",
		"descripcion", "nota", "referencia_banco", "saldo_banco", "id"})

	err = h.svc.Each(ctx, userID, q, func(e store.Entry) error {
		a := byID[e.AccountID]
		name := a.Alias
		if name == "" {
			name = a.DisplayName
		}
		category := ""
		if e.CategoryID != nil {
			category = names[*e.CategoryID]
		}
		balance := ""
		if e.BankBalance != nil {
			balance = e.BankBalance.String()
		}
		return out.Write([]string{e.BookedOn.String(), name, a.Institution, kindNames[e.EffectiveKind()],
			category, e.Amount.String(), "USD", e.Description, e.Note, e.BankRef, balance, e.ID.String()})
	})
	out.Flush()
	if err != nil {
		// Headers are already sent; the truncated file is the only signal left,
		// so the failure goes to the log.
		logging.FromContext(ctx).Error("csv export failed partway", "error", err)
	}
}
