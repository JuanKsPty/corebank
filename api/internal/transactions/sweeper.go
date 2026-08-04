package transactions

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/ledger"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

// Sweeper reconciles movements whose outcome was never recorded.
//
// It is what makes the dual write safe rather than merely careful. Two things can
// leave a row unresolved:
//
//   - The process died between submitting to the ledger and writing the outcome.
//     The row is pending with no hold; the ledger already knows what happened.
//   - Nobody answered a confirmation. The ledger released the reservation on its
//     own timeout, and the row still claims to be waiting.
//
// Both are settled the same way: ask the ledger, which is authoritative about
// money, and make the row agree. The sweeper never moves money — it only records
// what the ledger already did. That is why it is safe to run repeatedly, and why
// a bug here can misreport a movement but cannot lose one.
type Sweeper struct {
	db     *store.DB
	book   ledger.Ledger
	logger *slog.Logger
	now    func() time.Time

	// grace is how long a pending row is left alone before it is considered
	// stale. It has to exceed the confirmation window, or the sweeper would
	// race a customer who is still deciding.
	grace time.Duration
	// batch bounds one pass, so a large backlog is worked through over several
	// runs instead of in one long transaction.
	batch int
}

func NewSweeper(db *store.DB, book ledger.Ledger, holdTTL time.Duration, logger *slog.Logger) *Sweeper {
	return &Sweeper{
		db:     db,
		book:   book,
		logger: logger.With("component", "sweeper"),
		now:    time.Now,
		grace:  holdTTL + time.Minute,
		batch:  200,
	}
}

// Run reconciles until ctx is cancelled, starting with an immediate pass.
//
// The pass at startup is the important one: it is what recovers rows left behind
// by the crash that caused the restart.
func (s *Sweeper) Run(ctx context.Context, every time.Duration) {
	if n, err := s.Sweep(ctx); err != nil {
		s.logger.Error("startup reconciliation failed", "error", err)
	} else if n > 0 {
		s.logger.Info("startup reconciliation", "reconciled", n)
	}

	ticker := time.NewTicker(every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := s.Sweep(ctx); err != nil {
				s.logger.Error("reconciliation failed", "error", err)
			} else if n > 0 {
				s.logger.Info("reconciled unresolved movements", "count", n)
			}
		}
	}
}

// Sweep performs one reconciliation pass and reports how many rows it settled.
func (s *Sweeper) Sweep(ctx context.Context) (int, error) {
	cutoff := s.now().Add(-s.grace)

	stale, err := s.db.Q().StalePendingTransactions(ctx, cutoff, s.batch)
	if err != nil {
		return 0, err
	}
	if len(stale) == 0 {
		return 0, nil
	}

	// One batched ledger lookup for the whole pass, asking about both ids a row
	// can be known by.
	//
	// Both are needed because a reservation and its resolution are two separate
	// transfers in the ledger. Posting a hold does not modify the hold — it
	// creates a new transfer pointing back at it — so the hold's record still
	// reads "pending" forever. The only way to learn whether a hold was resolved
	// is to ask whether its resolving transfer exists, which is what the row's own
	// id is reserved for.
	ids := make([]uuid.UUID, 0, len(stale)*2)
	for _, t := range stale {
		ids = append(ids, t.ID)
		if t.HoldID != uuid.Nil {
			ids = append(ids, t.HoldID)
		}
	}

	entries, err := s.book.Lookup(ctx, ids)
	if err != nil {
		return 0, err
	}

	found := make(map[uuid.UUID]ledger.Entry, len(entries))
	for _, e := range entries {
		found[e.ID] = e
	}

	var settled int
	for _, t := range stale {
		status, reason := s.verdict(t, found)

		changed, err := s.db.Q().SettleTransaction(ctx, t.ID, status, reason)
		if err != nil {
			// Log and continue: one unreconcilable row must not stop the rest of
			// the batch, and the next pass will try it again.
			s.logger.Error("could not settle a stale movement",
				"transaction_id", t.ID, "error", err)
			continue
		}
		if changed {
			settled++
			s.logger.Warn("stale movement reconciled against the ledger",
				"transaction_id", t.ID, "status", status, "reason", reason,
				"age", s.now().Sub(t.CreatedAt).Round(time.Second))
		}
	}
	return settled, nil
}

// verdict decides what a stale row should say, given what the ledger reports.
func (s *Sweeper) verdict(t store.Transaction, found map[uuid.UUID]ledger.Entry) (store.TxStatus, string) {
	resolution, resolved := found[t.ID]

	// A direct movement: its own id is the transfer, so its presence is the whole
	// answer.
	if t.HoldID == uuid.Nil {
		if resolved {
			// It reached the ledger and completed; only the outcome was never
			// written back here.
			return store.StatusCompleted, ""
		}
		// The ledger never received it, so no money moved — the submission was
		// lost, or the process died before the request left. Failing the row is
		// safe as well as correct: the id is deterministic, so this movement can
		// never reappear later as a duplicate.
		return store.StatusFailed, "not_recorded_in_ledger"
	}

	// A two-phase movement. The resolving transfer, if it exists, says which way
	// the hold went.
	if resolved {
		if resolution.Voids {
			return store.StatusVoided, ""
		}
		return store.StatusCompleted, ""
	}

	if _, held := found[t.HoldID]; !held {
		// Not even the reservation reached the ledger. Nothing was ever held.
		return store.StatusFailed, "not_recorded_in_ledger"
	}

	// The reservation exists and was never resolved, and the row is past its
	// window. The ledger's own timeout releases the funds, so recording the
	// movement as expired matches what the customer sees: the money is back in
	// their available balance and this movement did not happen.
	return store.StatusExpired, "hold_expired"
}
