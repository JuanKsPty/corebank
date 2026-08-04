package transactions

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/accounts"
	"github.com/JuanKsPty/corebank/api/internal/money"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

// HistoryQuery is a request for a customer's movements.
//
// Account is optional; empty means every account the customer holds. Whatever it
// says, the result is confined to accounts they own — the account list is
// resolved from the authenticated user, never taken from the query.
type HistoryQuery struct {
	Account string
	Filter  store.HistoryFilter
}

// History lists a customer's movements, newest first.
func (s *Service) History(ctx context.Context, userID uuid.UUID, q HistoryQuery) (store.Page, error) {
	scope, err := s.scope(ctx, userID, q.Account)
	if err != nil {
		return store.Page{}, err
	}
	if len(scope) == 0 {
		// A customer with no accounts has no movements. Returning an empty page
		// is the honest answer; querying with an empty account list would be
		// refused by the store, which is the safer default there.
		return store.Page{}, nil
	}

	filter := q.Filter
	filter.Accounts = scope
	return s.db.Q().History(ctx, filter)
}

// Flow returns daily money in and out for the dashboard chart.
//
// The window follows the data. For an account in use it is the last `days`; for one
// whose history ends earlier — as the imported dataset's does — it moves back to
// where the activity is, and says so, rather than returning an empty series that
// would read as a broken chart.
func (s *Service) Flow(ctx context.Context, userID uuid.UUID, days int) (store.Flow, error) {
	scope, err := s.scope(ctx, userID, "")
	if err != nil {
		return store.Flow{}, err
	}
	if len(scope) == 0 {
		return store.Flow{}, nil
	}
	return s.db.Q().DailyFlowWindow(ctx, scope, days, s.now().UTC())
}

// PendingConfirmations lists the customer's movements still holding funds.
//
// The interface needs this after a reload: a confirmation card the customer never
// answered has to reappear, or the money looks missing from the available balance
// with nothing on screen explaining why.
func (s *Service) PendingConfirmations(ctx context.Context, userID uuid.UUID) ([]store.Transaction, error) {
	scope, err := s.scope(ctx, userID, "")
	if err != nil {
		return nil, err
	}
	if len(scope) == 0 {
		return nil, nil
	}

	page, err := s.db.Q().History(ctx, store.HistoryFilter{Accounts: scope, Limit: 200})
	if err != nil {
		return nil, err
	}

	var pending []store.Transaction
	for _, t := range page.Transactions {
		if t.AwaitingConfirmation() && t.HoldExpiresAt.After(s.now()) {
			pending = append(pending, t)
		}
	}
	return pending, nil
}

// Summary is the dashboard's headline figures.
type Summary struct {
	Accounts       []accounts.Account
	TotalAvailable money.Cents
	Recent         []store.Transaction
	Flow           store.Flow
	Pending        []store.Transaction
}

// Dashboard assembles everything the landing page shows in one call, so the page
// renders from a single request rather than five that can arrive out of order.
func (s *Service) Dashboard(ctx context.Context, userID uuid.UUID, recent, flowDays int) (Summary, error) {
	list, err := s.accounts.List(ctx, userID)
	if err != nil {
		return Summary{}, err
	}

	summary := Summary{Accounts: list, TotalAvailable: s.accounts.Total(ctx, list)}
	if len(list) == 0 {
		return summary, nil
	}

	page, err := s.History(ctx, userID, HistoryQuery{Filter: store.HistoryFilter{Limit: recent}})
	if err != nil {
		return Summary{}, err
	}
	summary.Recent = page.Transactions

	if summary.Flow, err = s.Flow(ctx, userID, flowDays); err != nil {
		return Summary{}, err
	}
	if summary.Pending, err = s.PendingConfirmations(ctx, userID); err != nil {
		return Summary{}, err
	}
	return summary, nil
}

// scope returns the account numbers a query may touch.
//
// This is the authorisation boundary for every read in this file. A named account
// is checked for ownership; an unnamed one expands to exactly the customer's own
// accounts. There is no path here that can widen the scope beyond them.
func (s *Service) scope(ctx context.Context, userID uuid.UUID, account string) ([]string, error) {
	if account != "" {
		owned, err := s.accounts.Resolve(ctx, userID, account)
		if err != nil {
			if errors.Is(err, accounts.ErrNotFound) || errors.Is(err, accounts.ErrNotOwned) {
				return nil, fmt.Errorf("%w: %s", accounts.ErrNotFound, account)
			}
			return nil, err
		}
		return []string{owned.Number}, nil
	}

	owned, err := s.db.Q().AccountsByUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	numbers := make([]string, 0, len(owned))
	for _, a := range owned {
		numbers = append(numbers, a.Number)
	}
	return numbers, nil
}
