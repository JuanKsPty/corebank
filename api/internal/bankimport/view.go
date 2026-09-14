package bankimport

import (
	"fmt"
	"io"
	"time"

	"github.com/JuanKsPty/corebank/api/internal/money"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

// readAllLimited reads at most limit+1 bytes, reporting an error if the
// stream did not end by then — a second bound alongside http.MaxBytesReader,
// since a multipart part is not itself wrapped by that reader.
func readAllLimited(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("bankimport: file exceeds %d bytes", limit)
	}
	return data, nil
}

// accountView is an imported account as it appears in a JSON response.
//
// No balance field: a declared, unverified figure sits deliberately apart
// from the TigerBeetle-derived numbers every other account response
// carries, rather than alongside them in the same shape where it could be
// mistaken for one.
type accountView struct {
	ID            string    `json:"id"`
	Institution   string    `json:"institution"`
	AccountNumber string    `json:"account_number"`
	DisplayName   string    `json:"display_name"`
	Currency      string    `json:"currency"`
	CreatedAt     time.Time `json:"created_at"`
}

func newAccountView(a store.ImportedAccount) accountView {
	return accountView{
		ID:            a.ID.String(),
		Institution:   a.Institution,
		AccountNumber: a.AccountNumber,
		DisplayName:   a.DisplayName,
		Currency:      a.Currency,
		CreatedAt:     a.CreatedAt,
	}
}

func newAccountViews(list []store.ImportedAccount) []accountView {
	out := make([]accountView, 0, len(list))
	for _, a := range list {
		out = append(out, newAccountView(a))
	}
	return out
}

type accountsResponse struct {
	Accounts []accountView `json:"accounts"`
}

// transactionView is an imported movement as it appears in a JSON response.
type transactionView struct {
	ID          string       `json:"id"`
	OccurredAt  time.Time    `json:"occurred_at"`
	Amount      money.Amount `json:"amount"`
	Description string       `json:"description"`
	ExternalRef string       `json:"external_ref,omitempty"`
	CategoryID  *string      `json:"category_id,omitempty"`
}

func newTransactionView(t store.ExternalTransaction) transactionView {
	v := transactionView{
		ID:          t.ID.String(),
		OccurredAt:  t.OccurredAt,
		Amount:      money.Cents(t.AmountCents).Amount(),
		Description: t.Description,
		ExternalRef: t.ExternalRef,
	}
	if t.CategoryID != nil {
		id := t.CategoryID.String()
		v.CategoryID = &id
	}
	return v
}

func newTransactionViews(list []store.ExternalTransaction) []transactionView {
	out := make([]transactionView, 0, len(list))
	for _, t := range list {
		out = append(out, newTransactionView(t))
	}
	return out
}

type transactionsResponse struct {
	Transactions []transactionView `json:"transactions"`
}

// importBatchView is one past import as it appears in a JSON response.
type importBatchView struct {
	ID                string    `json:"id"`
	Filename          string    `json:"filename"`
	Format            string    `json:"format"`
	RowCount          int       `json:"row_count"`
	Imported          int       `json:"imported"`
	SkippedDuplicates int       `json:"skipped_duplicates"`
	CreatedAt         time.Time `json:"created_at"`
}

func newImportBatchViews(list []store.ImportBatch) []importBatchView {
	out := make([]importBatchView, 0, len(list))
	for _, b := range list {
		out = append(out, importBatchView{
			ID:                b.ID.String(),
			Filename:          b.Filename,
			Format:            b.Format,
			RowCount:          b.RowCount,
			Imported:          b.ImportedCount,
			SkippedDuplicates: b.DuplicateCount,
			CreatedAt:         b.CreatedAt,
		})
	}
	return out
}

type importBatchesResponse struct {
	Imports []importBatchView `json:"imports"`
}

// importResultView is what POST .../import returns immediately, describing
// the file just processed.
type importResultView struct {
	ExternalAccountID string `json:"external_account_id"`
	Format            string `json:"format"`
	TotalRows         int    `json:"total_rows"`
	Imported          int    `json:"imported"`
	SkippedDuplicates int    `json:"skipped_duplicates"`
}

type categorySpendView struct {
	CategoryID  *string `json:"category_id,omitempty"`
	AmountCents int64   `json:"amount_cents"`
}

type spendByCategoryResponse struct {
	Spend []categorySpendView `json:"spend"`
}
