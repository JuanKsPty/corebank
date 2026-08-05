package chat

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/identity"
	"github.com/JuanKsPty/corebank/api/internal/llm"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

// --- doubles -----------------------------------------------------------------

// fakeProvider answers with whatever it was told to answer with, and counts how
// often it was asked. The count is the assertion that matters most here: a latched
// provider that still gets called is a provider that still spends money.
type fakeProvider struct {
	name  string
	isAI  bool
	reply llm.Reply
	err   error

	mu    sync.Mutex
	calls int
}

func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) IsAI() bool   { return f.isAI }

func (f *fakeProvider) Complete(context.Context, llm.Request) (llm.Reply, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return f.reply, f.err
}

func (f *fakeProvider) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeKeeper is an in-memory budget with the same guarantee as the SQL one: a
// reservation either fits under every ceiling or holds nothing.
type fakeKeeper struct {
	mu       sync.Mutex
	caps     map[string]int64
	spent    map[string]int64
	reserved map[string]int64

	reserveErr error // a database fault, as opposed to an exhausted ceiling
	settles    []settled
}

type settled struct {
	reserved int64
	usage    store.AIUsage
}

func newFakeKeeper(caps Caps) *fakeKeeper {
	return &fakeKeeper{
		caps: map[string]int64{
			store.BudgetTotal:   caps.Total,
			store.BudgetDay:     caps.Daily,
			store.BudgetUserDay: caps.UserDaily,
		},
		spent:    map[string]int64{},
		reserved: map[string]int64{},
	}
}

func (k *fakeKeeper) Reserve(_ context.Context, micros int64, scopes []store.BudgetScope) error {
	if k.reserveErr != nil {
		return k.reserveErr
	}

	k.mu.Lock()
	defer k.mu.Unlock()

	// Check every ceiling before touching any, so a refusal leaves nothing held —
	// the same all-or-nothing the real one gets from running in one transaction.
	for _, s := range scopes {
		if k.spent[s.Scope]+k.reserved[s.Scope]+micros > k.caps[s.Scope] {
			return &store.BudgetExhaustedError{Scope: s.Scope}
		}
	}
	for _, s := range scopes {
		k.reserved[s.Scope] += micros
	}
	return nil
}

func (k *fakeKeeper) Settle(_ context.Context, reserved int64, scopes []store.BudgetScope, usage store.AIUsage) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	for _, s := range scopes {
		k.reserved[s.Scope] = max(k.reserved[s.Scope]-reserved, 0)
		k.spent[s.Scope] += usage.CostMicros
	}
	k.settles = append(k.settles, settled{reserved: reserved, usage: usage})
	return nil
}

func (k *fakeKeeper) Remaining(context.Context) (int64, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.caps[store.BudgetTotal] - k.spent[store.BudgetTotal] - k.reserved[store.BudgetTotal], nil
}

func (k *fakeKeeper) spentOn(scope string) int64 {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.spent[scope]
}

// --- fixtures ----------------------------------------------------------------

// generousCaps are large enough that no test hits a ceiling by accident.
var generousCaps = Caps{Total: 10_000_000, Daily: 10_000_000, UserDaily: 10_000_000}

func authedContext() context.Context {
	return identity.WithUser(context.Background(), uuid.New())
}

func aRequest() llm.Request {
	return llm.Request{
		System:   "You are a banking assistant.",
		Messages: []llm.Message{{Role: llm.RoleUser, Text: "¿Cuál es mi saldo?"}},
	}
}

// newTestBudgeted returns a Budgeted with a fixed clock, so pricing does not drift
// across the boundary of a promotional rate mid-test-suite.
func newTestBudgeted(ai, fallback llm.Provider, k Keeper, caps Caps) *Budgeted {
	b := NewBudgeted(ai, fallback, k, caps)
	b.now = func() time.Time { return time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC) }
	return b
}

// --- the happy path ----------------------------------------------------------

func TestBudgetedUsesTheModelAndBooksWhatItCost(t *testing.T) {
	ai := &fakeProvider{name: "claude-sonnet-5", isAI: true, reply: llm.Reply{
		Text: "Tienes $1,000.00.", InputTokens: 2000, OutputTokens: 100, CacheReadTokens: 1700,
	}}
	fallback := &fakeProvider{name: "reglas"}
	keeper := newFakeKeeper(generousCaps)

	b := newTestBudgeted(ai, fallback, keeper, generousCaps)

	reply, err := b.Complete(authedContext(), aRequest())
	if err != nil {
		t.Fatalf("Complete() = %v", err)
	}
	if reply.Text != "Tienes $1,000.00." {
		t.Errorf("reply came from the wrong engine: %q", reply.Text)
	}
	if fallback.callCount() != 0 {
		t.Errorf("the fallback answered %d times; it should not have been asked", fallback.callCount())
	}

	if got := b.State(); !got.IsAI || got.Engine != EngineAI {
		t.Errorf("State() = %+v, want the model answering", got)
	}

	// The cost booked has to be the real usage, not the estimate that was reserved.
	//   2000 input       * $2/1M        = 4000
	//    100 output      * $10/1M       = 1000
	//   1700 cache read  * $2/1M * 0.1  =  340
	const wantCost = 5_340
	if len(keeper.settles) != 1 {
		t.Fatalf("got %d settlements, want 1", len(keeper.settles))
	}
	if got := keeper.settles[0].usage.CostMicros; got != wantCost {
		t.Errorf("booked %d micros, want %d", got, wantCost)
	}
	if got := keeper.spentOn(store.BudgetTotal); got != wantCost {
		t.Errorf("lifetime spend = %d micros, want %d", got, wantCost)
	}

	// The reservation must be released, or the ceiling would be consumed by
	// estimates rather than by actual spend.
	if got := keeper.reserved[store.BudgetTotal]; got != 0 {
		t.Errorf("%d micros left reserved after settling, want 0", got)
	}
}

func TestBudgetedRecordsTheFourTokenCountsSeparately(t *testing.T) {
	ai := &fakeProvider{name: "claude-sonnet-5", isAI: true, reply: llm.Reply{
		InputTokens: 11, OutputTokens: 22, CacheWriteTokens: 33, CacheReadTokens: 44,
	}}
	keeper := newFakeKeeper(generousCaps)

	b := newTestBudgeted(ai, &fakeProvider{name: "reglas"}, keeper, generousCaps)
	if _, err := b.Complete(authedContext(), aRequest()); err != nil {
		t.Fatalf("Complete() = %v", err)
	}

	// Folding these into one number would misprice every cached call and hide
	// whether the cache is working at all.
	got := keeper.settles[0].usage
	if got.InputTokens != 11 || got.OutputTokens != 22 ||
		got.CacheWriteTokens != 33 || got.CacheReadTokens != 44 {
		t.Errorf("recorded usage = %+v, want the four counts kept apart", got)
	}
}

// --- the ceilings ------------------------------------------------------------

func TestBudgetedFallsBackWhenTheLifetimeCeilingIsReached(t *testing.T) {
	ai := &fakeProvider{name: "claude-sonnet-5", isAI: true}
	fallback := &fakeProvider{name: "reglas", reply: llm.Reply{Text: "Puedo consultar tu saldo."}}

	// A ceiling of one micro-dollar: any estimate exceeds it.
	caps := Caps{Total: 1, Daily: 1, UserDaily: 1}
	b := newTestBudgeted(ai, fallback, newFakeKeeper(caps), caps)

	reply, err := b.Complete(authedContext(), aRequest())
	if err != nil {
		t.Fatalf("an exhausted budget must not be an error, got %v", err)
	}
	if reply.Text != "Puedo consultar tu saldo." {
		t.Errorf("reply = %q, want the fallback's", reply.Text)
	}
	if ai.callCount() != 0 {
		t.Errorf("the model was called %d times with no budget", ai.callCount())
	}

	state := b.State()
	if state.IsAI {
		t.Error("State() claims the model is answering when the budget is gone")
	}
	if state.Engine != EngineBudgetExhausted {
		t.Errorf("State().Engine = %q, want %q", state.Engine, EngineBudgetExhausted)
	}
	if state.Name != "reglas" {
		t.Errorf("State().Name = %q, want the fallback's name", state.Name)
	}
}

// The lifetime ceiling is the end; a daily one is not. Latching on a daily ceiling
// would silence the assistant for the life of the process over one busy afternoon.
func TestBudgetedDoesNotLatchOnADailyCeiling(t *testing.T) {
	ai := &fakeProvider{name: "claude-sonnet-5", isAI: true, reply: llm.Reply{Text: "Hola."}}
	fallback := &fakeProvider{name: "reglas", reply: llm.Reply{Text: "reglas"}}

	keeper := newFakeKeeper(generousCaps)
	keeper.caps[store.BudgetDay] = 1 // only today's allowance is gone

	b := newTestBudgeted(ai, fallback, keeper, generousCaps)

	if _, err := b.Complete(authedContext(), aRequest()); err != nil {
		t.Fatalf("Complete() = %v", err)
	}
	if b.latched.Load() {
		t.Error("a daily ceiling latched the provider off permanently")
	}

	// Tomorrow's allowance restores service without a restart.
	keeper.mu.Lock()
	keeper.caps[store.BudgetDay] = generousCaps.Daily
	keeper.mu.Unlock()

	reply, err := b.Complete(authedContext(), aRequest())
	if err != nil {
		t.Fatalf("Complete() = %v", err)
	}
	if reply.Text != "Hola." {
		t.Errorf("reply = %q, want the model's once the ceiling reopened", reply.Text)
	}
}

func TestBudgetedLatchesOnTheLifetimeCeiling(t *testing.T) {
	ai := &fakeProvider{name: "claude-sonnet-5", isAI: true}
	caps := Caps{Total: 1, Daily: 1, UserDaily: 1}
	b := newTestBudgeted(ai, &fakeProvider{name: "reglas"}, newFakeKeeper(caps), caps)

	for range 3 {
		if _, err := b.Complete(authedContext(), aRequest()); err != nil {
			t.Fatalf("Complete() = %v", err)
		}
	}
	if !b.latched.Load() {
		t.Error("the lifetime ceiling did not latch the provider off")
	}
	if ai.callCount() != 0 {
		t.Errorf("the model was called %d times past its lifetime ceiling", ai.callCount())
	}
}

// A database that will not answer is a real fault. Spending unmetered because the
// bookkeeping is unavailable is the one thing this wrapper must never do.
func TestBudgetedRefusesToSpendWhenTheBudgetCannotBeRead(t *testing.T) {
	ai := &fakeProvider{name: "claude-sonnet-5", isAI: true}
	fallback := &fakeProvider{name: "reglas"}

	keeper := newFakeKeeper(generousCaps)
	keeper.reserveErr = errors.New("postgres is unreachable")

	b := newTestBudgeted(ai, fallback, keeper, generousCaps)

	if _, err := b.Complete(authedContext(), aRequest()); err == nil {
		t.Fatal("Complete() succeeded with an unreadable budget; it must refuse")
	}
	if ai.callCount() != 0 {
		t.Errorf("the model was called %d times without a reservation", ai.callCount())
	}
	if fallback.callCount() != 0 {
		t.Error("a database fault was quietly answered from rules instead of reported")
	}
}

// The ceiling has to hold when calls overlap. Twenty goroutines against a small
// allowance is the scenario the reservation exists for: a plain check-then-spend
// lets them all read the same encouraging total and every one of them proceed.
//
// The assertion is on money, not on the number of calls. The estimate assumes the
// full output allowance while a real reply uses a fraction of it, so settling
// returns the difference and more calls fit than the estimate predicted — which is
// the mechanism working, not a leak. What must never happen is spending past the
// ceiling.
func TestBudgetedCeilingHoldsUnderConcurrency(t *testing.T) {
	ai := &fakeProvider{name: "claude-sonnet-5", isAI: true, reply: llm.Reply{
		InputTokens: 1000, OutputTokens: 100,
	}}
	fallback := &fakeProvider{name: "reglas"}

	estimate := llm.EstimateCost("claude-sonnet-5", aRequest(),
		time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC))
	caps := Caps{Total: estimate * 5, Daily: estimate * 5, UserDaily: estimate * 5}

	keeper := newFakeKeeper(caps)
	b := newTestBudgeted(ai, fallback, keeper, caps)

	const callers = 20
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := b.Complete(authedContext(), aRequest()); err != nil {
				t.Errorf("Complete() = %v", err)
			}
		}()
	}
	wg.Wait()

	if got := keeper.spentOn(store.BudgetTotal); got > caps.Total {
		t.Errorf("spent %d micros against a ceiling of %d", got, caps.Total)
	}
	// Nothing may be left held: a reservation that is never released is budget
	// nobody can spend and nobody can account for.
	keeper.mu.Lock()
	leftReserved := keeper.reserved[store.BudgetTotal]
	keeper.mu.Unlock()
	if leftReserved != 0 {
		t.Errorf("%d micros stayed reserved after every call finished", leftReserved)
	}
	// Every caller gets an answer from one engine or the other. Running out of
	// budget is not a reason to leave somebody's question unanswered.
	if got := ai.callCount() + fallback.callCount(); got != callers {
		t.Errorf("%d of %d callers went unanswered", callers-got, callers)
	}
}

// --- provider failures -------------------------------------------------------

func TestBudgetedLatchesOnAPermanentFailure(t *testing.T) {
	ai := &fakeProvider{
		name: "claude-sonnet-5", isAI: true,
		err: fmt.Errorf("%w: the API key was rejected (401)", llm.ErrPermanent),
	}
	fallback := &fakeProvider{name: "reglas", reply: llm.Reply{Text: "reglas"}}
	b := newTestBudgeted(ai, fallback, newFakeKeeper(generousCaps), generousCaps)

	reply, err := b.Complete(authedContext(), aRequest())
	if err != nil {
		t.Fatalf("a rejected key must be answered, not returned: %v", err)
	}
	if reply.Text != "reglas" {
		t.Errorf("reply = %q, want the fallback's", reply.Text)
	}

	// The second message must not spend another round trip rediscovering the same
	// dead end.
	if _, err := b.Complete(authedContext(), aRequest()); err != nil {
		t.Fatalf("Complete() = %v", err)
	}
	if ai.callCount() != 1 {
		t.Errorf("the model was called %d times after a permanent failure, want 1", ai.callCount())
	}
	if got := b.State().Engine; got != EngineDegraded {
		t.Errorf("State().Engine = %q, want %q", got, EngineDegraded)
	}
}

// A rate limit clears on its own, so it must not latch — and the reservation it
// consumed has to come back, or a run of transient failures would eat the budget
// without a single answer being paid for.
func TestBudgetedRetriesAfterATransientFailure(t *testing.T) {
	ai := &fakeProvider{
		name: "claude-sonnet-5", isAI: true,
		err: fmt.Errorf("%w: 429", llm.ErrUnavailable),
	}
	fallback := &fakeProvider{name: "reglas", reply: llm.Reply{Text: "reglas"}}
	keeper := newFakeKeeper(generousCaps)
	b := newTestBudgeted(ai, fallback, keeper, generousCaps)

	if _, err := b.Complete(authedContext(), aRequest()); err != nil {
		t.Fatalf("Complete() = %v", err)
	}
	if b.latched.Load() {
		t.Error("a rate limit latched the provider off permanently")
	}
	if got := b.State().Engine; got != EngineDegraded {
		t.Errorf("State().Engine = %q, want %q", got, EngineDegraded)
	}
	if got := keeper.spentOn(store.BudgetTotal); got != 0 {
		t.Errorf("a failed call booked %d micros; a rejected request consumes nothing", got)
	}
	if got := keeper.reserved[store.BudgetTotal]; got != 0 {
		t.Errorf("%d micros stayed reserved after a failed call", got)
	}

	// Recovery: the model works again and the label goes back to saying so.
	ai.err = nil
	ai.reply = llm.Reply{Text: "Tienes $1,000.00."}

	reply, err := b.Complete(authedContext(), aRequest())
	if err != nil {
		t.Fatalf("Complete() = %v", err)
	}
	if reply.Text != "Tienes $1,000.00." {
		t.Errorf("reply = %q, want the model's after recovery", reply.Text)
	}
	if got := b.State(); !got.IsAI || got.Engine != EngineAI {
		t.Errorf("State() = %+v, want the model answering again", got)
	}
}

// A cancelled request is the customer navigating away. There is nobody to answer
// and the fallback cannot use a dead context, so this is the one failure that does
// come back as an error.
func TestBudgetedPropagatesCancellation(t *testing.T) {
	ai := &fakeProvider{name: "claude-sonnet-5", isAI: true, err: context.Canceled}
	fallback := &fakeProvider{name: "reglas"}
	b := newTestBudgeted(ai, fallback, newFakeKeeper(generousCaps), generousCaps)

	if _, err := b.Complete(authedContext(), aRequest()); !errors.Is(err, context.Canceled) {
		t.Errorf("Complete() = %v, want context.Canceled", err)
	}
	if fallback.callCount() != 0 {
		t.Error("a cancelled request was answered from rules")
	}
	if b.latched.Load() {
		t.Error("a cancelled request latched the provider off")
	}
}

// --- scopes ------------------------------------------------------------------

// Without a user there is no per-customer ceiling to check, but the other two still
// have to apply. Chat only runs behind authentication, so this is a belt-and-braces
// case rather than a live one.
func TestBudgetedScopesWithoutAUser(t *testing.T) {
	b := newTestBudgeted(&fakeProvider{name: "claude-sonnet-5", isAI: true},
		&fakeProvider{name: "reglas"}, newFakeKeeper(generousCaps), generousCaps)

	scopes := b.scopes(context.Background())
	if len(scopes) != 2 {
		t.Fatalf("got %d scopes without a user, want 2", len(scopes))
	}
	for _, s := range scopes {
		if s.Scope == store.BudgetUserDay {
			t.Error("a per-user ceiling was built with no user to attribute it to")
		}
	}
}

func TestBudgetedScopesAreDatedInUTC(t *testing.T) {
	b := newTestBudgeted(&fakeProvider{name: "claude-sonnet-5", isAI: true},
		&fakeProvider{name: "reglas"}, newFakeKeeper(generousCaps), generousCaps)

	userID := uuid.New()
	scopes := b.scopes(identity.WithUser(context.Background(), userID))
	if len(scopes) != 3 {
		t.Fatalf("got %d scopes, want 3", len(scopes))
	}

	// A window that moves with the server's timezone is a window nobody can reason
	// about from a log.
	const wantDay = "2026-08-04"
	for _, s := range scopes {
		switch s.Scope {
		case store.BudgetDay:
			if s.Key != wantDay {
				t.Errorf("daily key = %q, want %q", s.Key, wantDay)
			}
		case store.BudgetUserDay:
			if want := userID.String() + ":" + wantDay; s.Key != want {
				t.Errorf("per-user key = %q, want %q", s.Key, want)
			}
		case store.BudgetTotal:
			if s.Key != "" {
				t.Errorf("the lifetime ceiling is keyed %q, want an empty key", s.Key)
			}
		}
	}
}

// The plain fallback cannot report a state, and must be described as what it is:
// an application with no key configured, not one whose budget ran out.
func TestProviderStateOfABareFallback(t *testing.T) {
	svc := &Service{provider: NewFallback()}

	got := svc.ProviderState()
	if got.IsAI {
		t.Error("ProviderState() claims the rule-based engine is an AI")
	}
	if got.Engine != EngineUnconfigured {
		t.Errorf("ProviderState().Engine = %q, want %q", got.Engine, EngineUnconfigured)
	}
}
