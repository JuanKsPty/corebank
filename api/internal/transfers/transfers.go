// Package transfers finds and records movements that are one transfer between
// the owner's own accounts — paying the card from checking, moving money to
// IBKR — so it counts once as a transfer and never as spending plus income.
package transfers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/civil"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

// Window is how many days apart the two sides of a transfer may be booked. A
// card payment posts a few days after the bank debits it.
const Window = 5

// ErrNotACandidate means the two movements cannot be one transfer.
var ErrNotACandidate = errors.New("transfers: these two movements are not one transfer")

// Service finds and decides transfer pairs.
type Service struct {
	db *store.DB
}

func NewService(db *store.DB) *Service { return &Service{db: db} }

// Suggestions lists the pairs that look like one transfer and have not been
// decided.
func (s *Service) Suggestions(ctx context.Context, userID uuid.UUID) ([]store.TransferCandidate, error) {
	return s.db.Q().TransferCandidates(ctx, userID, Window, civil.Date{}, civil.Date{})
}

// Decide confirms or rejects a pair. Confirmed, both movements count as a
// transfer; rejected, the pair is never suggested again.
func (s *Service) Decide(ctx context.Context, userID, outID, inID uuid.UUID, confirm bool, by string) error {
	return s.db.InTx(ctx, func(q *store.Queries) error {
		if err := q.LockUser(ctx, userID); err != nil {
			return err
		}
		out, err := q.EntryByID(ctx, userID, outID)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrNotACandidate, err)
		}
		in, err := q.EntryByID(ctx, userID, inID)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrNotACandidate, err)
		}
		if out.AccountID == in.AccountID || out.Amount != -in.Amount || out.Amount >= 0 {
			return ErrNotACandidate
		}
		decision := "rejected"
		if confirm {
			decision = "confirmed"
			for _, id := range []uuid.UUID{outID, inID} {
				if err := q.MarkTransfer(ctx, userID, id); err != nil {
					return err
				}
			}
		}
		return q.RecordTransferDecision(ctx, userID, outID, inID, decision, by)
	})
}

// AutoMatch confirms the pairs that need no judgement, inside the caller's
// transaction: a movement the importer already knows is one side of a
// transfer, and the one movement of the opposite amount within the window. Anything ambiguous — two candidates for one side —
// is left as a suggestion. from and to are the period just imported; the
// window is added on both sides, and a zero bound means unbounded.
func AutoMatch(ctx context.Context, q *store.Queries, userID uuid.UUID, from, to civil.Date) (int, error) {
	if !from.IsZero() {
		from = from.AddDays(-Window)
	}
	if !to.IsZero() {
		to = to.AddDays(Window)
	}
	candidates, err := q.TransferCandidates(ctx, userID, Window, from, to)
	if err != nil {
		return 0, err
	}
	perSide := map[uuid.UUID]int{}
	for _, c := range candidates {
		perSide[c.Out.ID]++
		perSide[c.In.ID]++
	}
	matched := 0
	for _, c := range candidates {
		if perSide[c.Out.ID] != 1 || perSide[c.In.ID] != 1 {
			continue
		}
		if !isKnownTransferLeg(c.In) && !isKnownTransferLeg(c.Out) {
			continue
		}
		for _, id := range []uuid.UUID{c.Out.ID, c.In.ID} {
			if err := q.MarkTransfer(ctx, userID, id); err != nil {
				return matched, err
			}
		}
		if err := q.RecordTransferDecision(ctx, userID, c.Out.ID, c.In.ID, "confirmed", "auto"); err != nil {
			return matched, err
		}
		matched++
	}
	return matched, nil
}

// isKnownTransferLeg reports whether the importer already knows an entry is
// one side of a transfer: a card's "Pagos" line, or an IBKR deposit or
// withdrawal. Its counterpart in the owner's bank is then the only thing to
// find.
func isKnownTransferLeg(e store.Entry) bool {
	return e.Kind == store.KindTransfer &&
		(strings.EqualFold(e.BankCategory, "Pagos") || e.BankCategory == "Deposits/Withdrawals")
}
