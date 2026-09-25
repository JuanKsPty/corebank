package chat

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/identity"
	"github.com/JuanKsPty/corebank/api/internal/llm"
	"github.com/JuanKsPty/corebank/api/internal/logging"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

// Engine names which implementation answered, so the interface can say so instead
// of presenting a rule-based reply as an assistant's.
type Engine string

const (
	// EngineAI is the language model, answering normally.
	EngineAI Engine = "ai"
	// EngineUnconfigured means no API key was ever supplied.
	EngineUnconfigured Engine = "unconfigured"
	// EngineBudgetExhausted means a spend ceiling was reached. Nothing is broken;
	// the money ran out.
	EngineBudgetExhausted Engine = "budget_exhausted"
	// EngineDegraded means the model could not be reached for a reason that may
	// clear on its own, or that needs an operator — a rejected key, an overloaded
	// API.
	EngineDegraded Engine = "degraded"
)

// ProviderState is what the interface needs to label the assistant honestly.
type ProviderState struct {
	// Name is the model when one is answering, and the stand-in's own name
	// otherwise.
	Name   string
	IsAI   bool
	Engine Engine
}

// stateful is implemented by a provider that can report why it is answering the
// way it is. Declared as an interface so Service works with a bare provider too —
// the bare Unavailable provider does not need to know any of this.
type stateful interface{ State() ProviderState }

// Caps are the three spend ceilings, in micro-dollars.
type Caps struct {
	Total     int64
	Daily     int64
	UserDaily int64
}

// Keeper is the persistence the spend ceiling needs.
//
// An interface rather than *store.DB so the decisions made here — when to latch,
// which engine answers, what clears a degradation — can be tested without a
// database. That decision logic is the part worth testing hard: it is what stands
// between a public demo and somebody else's exhausted API key. The SQL underneath
// has its own reasons to be right and its own place to be checked.
type Keeper interface {
	// Reserve holds micros against every scope, or returns a
	// *store.BudgetExhaustedError naming the ceiling that refused, having held
	// nothing.
	Reserve(ctx context.Context, micros int64, scopes []store.BudgetScope) error
	// Settle releases a reservation, books what was actually spent, and records the
	// call in the audit trail.
	Settle(ctx context.Context, reserved int64, scopes []store.BudgetScope, usage store.AIUsage) error
	// Remaining is the lifetime ceiling's headroom, for the log.
	Remaining(ctx context.Context) (int64, error)
}

// dbKeeper is the real implementation, over PostgreSQL.
type dbKeeper struct{ db *store.DB }

// NewKeeper returns the budget's persistence over a database.
func NewKeeper(db *store.DB) Keeper { return dbKeeper{db: db} }

// Reserve runs all three ceilings in one transaction, so a reservation that cleared
// the daily ceiling but not the lifetime one leaves nothing behind to leak the day's
// allowance.
func (k dbKeeper) Reserve(ctx context.Context, micros int64, scopes []store.BudgetScope) error {
	return k.db.InTx(ctx, func(q *store.Queries) error {
		return q.ReserveAIBudget(ctx, micros, scopes)
	})
}

func (k dbKeeper) Settle(ctx context.Context, reserved int64, scopes []store.BudgetScope, usage store.AIUsage) error {
	return k.db.InTx(ctx, func(q *store.Queries) error {
		if err := q.SettleAIBudget(ctx, reserved, usage.CostMicros, scopes); err != nil {
			return err
		}
		return q.RecordAIUsage(ctx, usage)
	})
}

func (k dbKeeper) Remaining(ctx context.Context) (int64, error) {
	state, err := k.db.Q().AIBudget(ctx, store.BudgetTotal, "")
	if err != nil {
		return 0, err
	}
	return state.Remaining(), nil
}

// Budgeted is a provider that spends money, and stops when it has spent enough.
//
// It wraps the model and the rule-based engine, and decides per call which one
// answers. Everything it does is one of two things: refusing to start a call that
// would cross a ceiling, or noticing that the model has stopped working and
// answering anyway.
//
// It lives in this package rather than in llm because it needs Unavailable and the
// database, and llm must not depend on either — a provider should not know what a
// transaction is. What llm does own is the pricing and the distinction between a
// failure worth retrying and one that is not, so nothing here imports the Anthropic
// SDK.
//
// The mechanism is the one the bank already uses for money. A transfer reserves
// funds before it moves them, because checking a balance and then debiting it is
// two statements and twenty concurrent requests all read the same encouraging
// number. A call reserves its estimated cost before it is made, for exactly that
// reason, and settles the real figure afterwards. The ceiling holds under
// concurrency because the check and the increment are a single statement — the
// same shape as the ledger's own refusal to let debits exceed credits.
//
// What this does and does not guarantee, precisely. A call cannot begin unless its
// *estimated* cost fits under every ceiling, and the estimate is deliberately high:
// it charges every input token at the uncached rate and assumes the model writes to
// its full output allowance, neither of which is usually true. So the ceiling is
// exceeded only if a call's real cost comes in above its estimate, which for the
// text this assistant handles it does not. Settling returns the unspent difference,
// which is why more calls fit than the estimate predicted — that is the mechanism
// working rather than a leak.
type Budgeted struct {
	ai          llm.Provider
	unavailable llm.Provider
	keeper      Keeper
	caps        Caps
	now         func() time.Time

	// latched records a failure that will not clear by itself: a rejected key, an
	// exhausted account, the lifetime ceiling. Once set, the model is not called
	// again for the life of the process. Without it, every message would spend a
	// round trip rediscovering the same dead end and tell the customer to "try
	// again in a moment" forever.
	latched atomic.Bool
	// engine is the last reason the model did not answer, for the label. Stored
	// separately from latched because a transient degradation clears on the next
	// successful call while a latched one does not.
	engine atomic.Value // Engine
}

// NewBudgeted wraps a model in its spend ceilings.
func NewBudgeted(ai, unavailable llm.Provider, k Keeper, caps Caps) *Budgeted {
	b := &Budgeted{ai: ai, unavailable: unavailable, keeper: k, caps: caps, now: time.Now}
	b.engine.Store(EngineAI)
	return b
}

func (b *Budgeted) Name() string { return b.ai.Name() }

// IsAI reports whether a model is currently answering.
//
// False once the ceiling is reached or the key stops working, because from that
// moment the replies are the rule-based engine's and saying otherwise would be a
// lie about the product.
func (b *Budgeted) IsAI() bool { return b.currentEngine() == EngineAI }

// State reports which engine is answering and why.
func (b *Budgeted) State() ProviderState {
	engine := b.currentEngine()
	if engine == EngineAI {
		return ProviderState{Name: b.ai.Name(), IsAI: true, Engine: EngineAI}
	}
	return ProviderState{Name: b.unavailable.Name(), IsAI: false, Engine: engine}
}

func (b *Budgeted) currentEngine() Engine {
	engine, _ := b.engine.Load().(Engine)
	if engine == "" {
		return EngineAI
	}
	return engine
}

// Complete answers one turn, from the model if the budget allows and the model
// works, and from the rule-based engine otherwise.
//
// It never returns an error for "out of budget" or "the model is unreachable".
// Those are answered, not reported: a customer asking for their balance should get
// their balance from whichever engine can produce it, and the interface says which
// one that was. Only a genuine fault — the database being unreachable — comes back
// as an error.
func (b *Budgeted) Complete(ctx context.Context, req llm.Request) (llm.Reply, error) {
	logger := logging.FromContext(ctx)

	if b.latched.Load() {
		return b.unavailable.Complete(ctx, req)
	}

	scopes := b.scopes(ctx)
	estimate := llm.EstimateCost(b.ai.Name(), req, b.now())

	if err := b.keeper.Reserve(ctx, estimate, scopes); err != nil {
		var exhausted *store.BudgetExhaustedError
		if !errors.As(err, &exhausted) {
			// The database is not answering. That is a real fault and not something
			// to paper over by spending unmetered.
			return llm.Reply{}, err
		}
		// Which ceiling was hit decides whether this is the end. The lifetime one is;
		// a daily one is not, and tomorrow the model answers again — so latching on
		// it would silence the assistant for good over a busy afternoon.
		if exhausted.Scope == store.BudgetTotal {
			b.latch(EngineBudgetExhausted)
			logger.Warn("the lifetime AI budget is exhausted; the assistant is now unavailable",
				"cap_micros", b.caps.Total)
		} else {
			b.engine.Store(EngineBudgetExhausted)
			logger.Info("an AI spend ceiling was reached; the assistant is unavailable",
				"scope", exhausted.Scope)
		}
		return b.unavailable.Complete(ctx, req)
	}

	reply, callErr := b.ai.Complete(ctx, req)

	// The reservation is released either way. On success it is replaced by what the
	// call really cost, which is almost always less; on failure there is no usage to
	// book, because a rejected request consumes nothing.
	if callErr != nil {
		b.settle(ctx, estimate, llm.Usage{}, scopes)

		switch {
		case errors.Is(callErr, context.Canceled), errors.Is(callErr, context.DeadlineExceeded):
			// The customer left, or the whole exchange ran out of time. Not a
			// provider problem, and nobody is left to answer.
			return llm.Reply{}, callErr

		case errors.Is(callErr, llm.ErrPermanent):
			b.latch(EngineDegraded)
			logger.Error("the AI provider failed permanently; the assistant is now unavailable",
				"error", callErr)
			return b.unavailable.Complete(ctx, req)

		default:
			// Transient: this message goes unanswered, and the next one
			// tries the model again.
			b.engine.Store(EngineDegraded)
			logger.Warn("the AI provider is unavailable; this message goes unanswered",
				"error", callErr)
			return b.unavailable.Complete(ctx, req)
		}
	}

	b.settle(ctx, estimate, reply.Usage(), scopes)

	// A call that worked clears a transient degradation. A latched one is not
	// cleared here, because a latched provider is never called in the first place.
	b.engine.Store(EngineAI)
	return reply, nil
}

// settle books the real cost and records the call.
//
// Failures here are logged rather than returned. The model has already answered and
// the money is already spent; refusing to hand over the reply because the
// bookkeeping did not land would throw away something the customer is owed and that
// has been paid for. The reservation is the safety net either way: it errs toward
// refusing to spend, which is the direction to fail in.
func (b *Budgeted) settle(ctx context.Context, reserved int64, usage llm.Usage, scopes []store.BudgetScope) {
	logger := logging.FromContext(ctx)
	model := b.ai.Name()

	cost, priced := llm.Cost(model, usage, b.now())
	if !priced {
		logger.Warn("no price is known for this model; charging the dearest rate known",
			"model", model, "charged_micros", cost)
	}

	var userID *uuid.UUID
	if id, ok := identity.FromContext(ctx); ok {
		userID = &id
	}

	// A context that has already been cancelled would make the writes fail, losing
	// the record of money genuinely spent — and a customer navigating away mid-reply
	// is the ordinary case, not an unusual one. So the settle gets its own short
	// budget, detached from the request's.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	err := b.keeper.Settle(ctx, reserved, scopes, store.AIUsage{
		UserID:           userID,
		Model:            model,
		InputTokens:      usage.InputTokens,
		OutputTokens:     usage.OutputTokens,
		CacheWriteTokens: usage.CacheWriteTokens,
		CacheReadTokens:  usage.CacheReadTokens,
		CostMicros:       cost,
	})
	if err != nil {
		logger.Error("could not record AI spend", "error", err, "cost_micros", cost)
		return
	}

	// The remaining budget goes to the log and nowhere else. Telling a visitor how
	// much is left tells anybody who wants to exhaust it exactly where they stand.
	remaining, err := b.keeper.Remaining(ctx)
	if err != nil {
		return
	}
	logger.Info("AI spend",
		"model", model,
		"cost_micros", cost,
		"input_tokens", usage.InputTokens,
		"output_tokens", usage.OutputTokens,
		"cache_write_tokens", usage.CacheWriteTokens,
		"cache_read_tokens", usage.CacheReadTokens,
		"remaining_micros", remaining)
}

// scopes builds the three ceilings this call has to clear.
//
// The day is UTC rather than local: a ceiling whose window moves with the server's
// timezone is a ceiling nobody can reason about, and the deployment and the people
// reading its logs are not in the same place.
func (b *Budgeted) scopes(ctx context.Context) []store.BudgetScope {
	day := b.now().UTC().Format(time.DateOnly)

	scopes := []store.BudgetScope{
		{Scope: store.BudgetTotal, Key: "", Cap: b.caps.Total},
		{Scope: store.BudgetDay, Key: day, Cap: b.caps.Daily},
	}
	// The per-customer ceiling needs a customer. Chat only runs behind
	// authentication, so this is always present in practice; when it is not, the
	// other two still apply.
	if userID, ok := identity.FromContext(ctx); ok {
		scopes = append(scopes, store.BudgetScope{
			Scope: store.BudgetUserDay,
			Key:   userID.String() + ":" + day,
			Cap:   b.caps.UserDaily,
		})
	}
	return scopes
}

func (b *Budgeted) latch(engine Engine) {
	b.latched.Store(true)
	b.engine.Store(engine)
}
