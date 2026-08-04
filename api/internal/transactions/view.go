package transactions

import (
	"time"

	"github.com/JuanKsPty/corebank/api/internal/accounts"
	"github.com/JuanKsPty/corebank/api/internal/money"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

// View is a movement as it appears in a JSON response.
//
// Exported so the chat can describe a movement with the same shape the REST API
// uses. One representation means the assistant and the interface can never
// disagree about what happened.
type View struct {
	ID          string       `json:"id"`
	Kind        string       `json:"kind"`
	Status      string       `json:"status"`
	Amount      money.Amount `json:"amount"`
	FromAccount string       `json:"from_account,omitempty"`
	ToAccount   string       `json:"to_account,omitempty"`
	Description string       `json:"description"`
	// Origin is "api" or "chat", so the interface can mark what the assistant did.
	Origin string `json:"origin"`
	// FailureCode is present only on a failed movement, naming the reason the
	// ledger gave.
	FailureCode string `json:"failure_code,omitempty"`
	// Confirmation is present only while the movement holds funds and waits for
	// an answer.
	Confirmation *ConfirmationView `json:"confirmation,omitempty"`
	OccurredAt   time.Time         `json:"occurred_at"`
}

// ConfirmationView is what the interface needs to render a confirmation card.
type ConfirmationView struct {
	// HoldID is the handle the client sends back to confirm or cancel. It is not
	// a secret — ownership is checked server-side on every use — but it is the
	// only identifier the client needs.
	HoldID    string    `json:"hold_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// NewView renders one movement. Exported for the chat's tool results.
func NewView(t store.Transaction) View { return newView(t) }

func newView(t store.Transaction) View {
	v := View{
		ID:          t.ID.String(),
		Kind:        t.Kind.String(),
		Status:      string(t.Status),
		Amount:      t.Amount.Amount(),
		FromAccount: t.FromAccount,
		ToAccount:   t.ToAccount,
		Description: t.Description,
		Origin:      t.Origin,
		FailureCode: t.FailureCode,
		OccurredAt:  t.OccurredAt,
	}
	if t.AwaitingConfirmation() {
		v.Confirmation = &ConfirmationView{
			HoldID:    t.HoldID.String(),
			ExpiresAt: t.HoldExpiresAt,
		}
	}
	return v
}

// NewViews renders a list of movements.
func NewViews(list []store.Transaction) []View { return newViews(list) }

func newViews(list []store.Transaction) []View {
	// Non-nil so an empty history serialises as [] and the client has one shape
	// to handle.
	out := make([]View, 0, len(list))
	for _, t := range list {
		out = append(out, newView(t))
	}
	return out
}

type historyResponse struct {
	Transactions []View `json:"transactions"`
	// NextCursor is empty when there is nothing after this page. A client pages by
	// passing it back verbatim; its contents are not part of the contract.
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

type dashboardResponse struct {
	Accounts       []accounts.View `json:"accounts"`
	TotalAvailable money.Amount    `json:"total_available"`
	Recent         []View          `json:"recent"`
	Flow           []flowPoint     `json:"flow"`
	// Pending are confirmations still awaiting an answer, so a reload restores
	// the cards instead of leaving held funds unexplained.
	Pending []View `json:"pending_confirmations"`
}

type flowPoint struct {
	Day time.Time    `json:"day"`
	In  money.Amount `json:"in"`
	Out money.Amount `json:"out"`
}

func newFlowPoints(points []store.FlowPoint) []flowPoint {
	out := make([]flowPoint, 0, len(points))
	for _, p := range points {
		out = append(out, flowPoint{Day: p.Day, In: p.In.Amount(), Out: p.Out.Amount()})
	}
	return out
}
