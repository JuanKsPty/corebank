package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// ErrBudgetExhausted means a reservation would take the spend past a ceiling.
//
// It is a normal outcome, not a failure: the assistant answers from its
// rule-based engine instead and says so. The caller must not treat it as an error
// to report to the customer as a fault.
var ErrBudgetExhausted = errors.New("store: the AI budget is exhausted")

// BudgetExhaustedError names which ceiling refused the reservation.
//
// The scope is carried in the error rather than left for the caller to parse out of
// a message, because the answer changes what happens next: a daily ceiling reopens
// tomorrow, while the lifetime one is the end of it. Same shape as ConstraintError
// in this package, and for the same reason — a caller deciding what to do should
// not be pattern-matching on prose.
type BudgetExhaustedError struct{ Scope string }

func (e *BudgetExhaustedError) Error() string {
	return fmt.Sprintf("store: the %s AI budget is exhausted", e.Scope)
}

// Is makes errors.Is(err, ErrBudgetExhausted) true for any scope, so a caller that
// does not care which ceiling fired can still handle it in one place.
func (e *BudgetExhaustedError) Is(target error) bool { return target == ErrBudgetExhausted }

// Budget scopes. Each is a row in ai_budget, and a reservation has to clear all
// three — the narrowest ceiling wins, which is the point of having more than one.
const (
	// BudgetTotal is the lifetime ceiling. Once reached, that is the end of it.
	BudgetTotal = "total"
	// BudgetDay is one calendar day, so a bad afternoon cannot spend the month.
	BudgetDay = "day"
	// BudgetUserDay is one customer for one day, so one account cannot spend
	// everybody else's share.
	BudgetUserDay = "user_day"
)

// BudgetScope is one ceiling to check against.
type BudgetScope struct {
	Scope string
	Key   string
	// Cap is used only if the row does not exist yet, which is how a new day or a
	// new customer gets its ceiling without a migration or a scheduled job.
	Cap int64
}

// AIUsage is what one call to the model consumed.
type AIUsage struct {
	// UserID is nil for a call made outside a customer's session.
	UserID           *uuid.UUID
	Model            string
	InputTokens      int
	OutputTokens     int
	CacheWriteTokens int
	CacheReadTokens  int
	CostMicros       int64
}

// ReserveAIBudget holds micros against every scope, or reports that one of them is
// exhausted without holding anything.
//
// The reservation is what makes the ceiling hold under concurrency. Checking a
// total and then spending against it is a read-modify-write across two statements:
// twenty simultaneous requests all read the same "plenty left" and all proceed. So
// the check and the increment are one statement, and the increment happens before
// the money is spent rather than after — the same reason a transfer reserves funds
// before it moves them.
//
// All three scopes move together inside the caller's transaction: a reservation
// that cleared the daily ceiling but not the lifetime one must leave nothing
// behind.
func (q *Queries) ReserveAIBudget(ctx context.Context, micros int64, scopes []BudgetScope) error {
	if micros <= 0 {
		return nil
	}

	for _, scope := range scopes {
		// The ceiling row is created on first use. ON CONFLICT DO NOTHING rather
		// than an existence check, so two concurrent first calls for the same day
		// cannot race into a duplicate key.
		const ensure = `
			INSERT INTO ai_budget (scope, key, cap_micros)
			VALUES ($1, $2, $3)
			ON CONFLICT (scope, key) DO NOTHING`

		if _, err := q.q.Exec(ctx, ensure, scope.Scope, scope.Key, scope.Cap); err != nil {
			return wrap("store.ReserveAIBudget", err)
		}

		// The guard, in one statement. Zero rows updated means the ceiling would be
		// crossed — the same shape as the ledger's own refusal to let debits exceed
		// credits, and for the same reason: the rule belongs where the write
		// happens, not in the code that decided to write.
		const reserve = `
			UPDATE ai_budget
			   SET reserved_micros = reserved_micros + $3,
			       updated_at      = now()
			 WHERE scope = $1
			   AND key   = $2
			   AND spent_micros + reserved_micros + $3 <= cap_micros`

		tag, err := q.q.Exec(ctx, reserve, scope.Scope, scope.Key, micros)
		if err != nil {
			return wrap("store.ReserveAIBudget", err)
		}
		if tag.RowsAffected() == 0 {
			return &BudgetExhaustedError{Scope: scope.Scope}
		}
	}
	return nil
}

// SettleAIBudget releases a reservation and books what was actually spent.
//
// Called with the real cost once the model has answered, which is almost always
// less than the estimate that was reserved. GREATEST guards the floor: a settle
// without its reservation — a restart between the two — would otherwise drive
// reserved_micros negative and trip the column's own check constraint, turning a
// bookkeeping slip into a failed request.
func (q *Queries) SettleAIBudget(ctx context.Context, reserved, actual int64, scopes []BudgetScope) error {
	const query = `
		UPDATE ai_budget
		   SET reserved_micros = GREATEST(reserved_micros - $3, 0),
		       spent_micros    = spent_micros + $4,
		       updated_at      = now()
		 WHERE scope = $1 AND key = $2`

	for _, scope := range scopes {
		if _, err := q.q.Exec(ctx, query, scope.Scope, scope.Key, reserved, actual); err != nil {
			return wrap("store.SettleAIBudget", err)
		}
	}
	return nil
}

// ReleaseAIBudget drops a reservation without booking a cost, for a call that
// never reached the model.
func (q *Queries) ReleaseAIBudget(ctx context.Context, micros int64, scopes []BudgetScope) error {
	return q.SettleAIBudget(ctx, micros, 0, scopes)
}

// RecordAIUsage appends to the audit trail.
//
// Separate from settling the budget because the two answer different questions:
// the budget knows how much is left, this knows what it was spent on. It is also
// what makes the cost figures in the README measured rather than estimated.
func (q *Queries) RecordAIUsage(ctx context.Context, u AIUsage) error {
	const query = `
		INSERT INTO ai_usage (
			user_id, model,
			input_tokens, output_tokens, cache_write_tokens, cache_read_tokens,
			cost_micros
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`

	_, err := q.q.Exec(ctx, query,
		u.UserID, u.Model,
		u.InputTokens, u.OutputTokens, u.CacheWriteTokens, u.CacheReadTokens,
		u.CostMicros)
	return wrap("store.RecordAIUsage", err)
}

// AIBudgetState is a ceiling and what has been drawn against it.
type AIBudgetState struct {
	Scope    string
	Key      string
	Cap      int64
	Spent    int64
	Reserved int64
}

// Remaining is what is still available under this ceiling.
func (s AIBudgetState) Remaining() int64 {
	if left := s.Cap - s.Spent - s.Reserved; left > 0 {
		return left
	}
	return 0
}

// AIBudget reads one ceiling's state, for the log.
//
// Returns a zero-valued state and no error when the row does not exist yet:
// nothing has been spent, which is exactly what a missing row means.
func (q *Queries) AIBudget(ctx context.Context, scope, key string) (AIBudgetState, error) {
	const query = `
		SELECT cap_micros, spent_micros, reserved_micros
		FROM ai_budget
		WHERE scope = $1 AND key = $2`

	state := AIBudgetState{Scope: scope, Key: key}
	err := q.q.QueryRow(ctx, query, scope, key).Scan(&state.Cap, &state.Spent, &state.Reserved)
	if err != nil {
		if err := wrap("store.AIBudget", err); !errors.Is(err, ErrNotFound) {
			return AIBudgetState{}, err
		}
		return AIBudgetState{Scope: scope, Key: key}, nil
	}
	return state, nil
}
