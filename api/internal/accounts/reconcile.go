package accounts

import (
	"context"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/civil"
	"github.com/JuanKsPty/corebank/api/internal/money"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

// Reconciliation is everything needed to find where an account stops
// matching its bank: each stated balance against the computed one, each
// statement's opening and closing, and every line's running balance next to
// the one the bank printed.
type Reconciliation struct {
	Account    Account
	Anchor     *store.Checkpoint
	Opening    money.Cents
	Checks     []Check
	Statements []StatementCheck
	Lines      []Line
	// FirstBreak is the first line whose computed running balance disagrees
	// with the bank's by a different amount than the line before it: the
	// line where the difference starts. Nil when every printed balance
	// agrees, or none was printed.
	FirstBreak *uuid.UUID
}

// Check is one stated balance against the computed one.
type Check struct {
	Checkpoint store.Checkpoint
	Computed   money.Cents
}

// Difference is stated minus computed.
func (c Check) Difference() money.Cents { return c.Checkpoint.Balance - c.Computed }

// StatementCheck is one statement's own balances against the computed ones.
type StatementCheck struct {
	Statement store.Statement
	// Opening and Closing are the matching checks, when the statement
	// printed those balances.
	Opening *Check
	Closing *Check
	// Gap is set when this statement starts more than a day after the
	// previous one ends: movements in between were never imported.
	Gap bool
}

// Line is one movement with the running balances.
type Line struct {
	Entry    store.Entry
	Computed money.Cents
	// Difference is the bank's printed balance minus the computed one, when
	// the bank printed one.
	Difference *money.Cents
}

// Reconcile builds the reconciliation for one account.
func (s *Service) Reconcile(ctx context.Context, userID, accountID uuid.UUID) (Reconciliation, error) {
	account, err := s.Get(ctx, userID, accountID)
	if err != nil {
		return Reconciliation{}, err
	}
	cps, err := s.db.Q().CheckpointsByAccount(ctx, userID, accountID)
	if err != nil {
		return Reconciliation{}, err
	}
	statements, err := s.db.Q().StatementsByAccount(ctx, userID, accountID)
	if err != nil {
		return Reconciliation{}, err
	}
	entries, err := s.db.Q().AccountEntriesInOrder(ctx, userID, accountID)
	if err != nil {
		return Reconciliation{}, err
	}

	r := Reconciliation{Account: account}
	if anchor, ok := Anchor(cps); ok {
		r.Anchor = &anchor
		if r.Opening, err = s.Opening(ctx, anchor); err != nil {
			return Reconciliation{}, err
		}
	}

	// Running balances, in balance order. Each checkpoint's computed value is
	// read off the same walk, so the page and the checks cannot disagree.
	running := r.Opening
	before := map[uuid.UUID]money.Cents{} // balance just before an entry
	var prevDiff *money.Cents
	for _, e := range entries {
		before[e.ID] = running
		running += e.Amount
		line := Line{Entry: e, Computed: running}
		if e.BankBalance != nil {
			d := *e.BankBalance - running
			line.Difference = &d
			if r.FirstBreak == nil && d != 0 && (prevDiff == nil || *prevDiff != d) {
				id := e.ID
				r.FirstBreak = &id
			}
			prevDiff = &d
		}
		r.Lines = append(r.Lines, line)
	}

	checks := map[uuid.UUID]*Check{}
	for _, c := range cps {
		computed := r.Opening + sumThrough(entries, c.AsOf)
		if c.BeforeEntryID != nil {
			if b, ok := before[*c.BeforeEntryID]; ok {
				computed = b
			}
		}
		check := Check{Checkpoint: c, Computed: computed}
		r.Checks = append(r.Checks, check)
		checks[c.ID] = &r.Checks[len(r.Checks)-1]
	}

	byStatement := map[uuid.UUID][]*Check{}
	for i := range r.Checks {
		c := &r.Checks[i]
		if c.Checkpoint.StatementID != nil {
			byStatement[*c.Checkpoint.StatementID] = append(byStatement[*c.Checkpoint.StatementID], c)
		}
	}
	var prevEnd civil.Date
	for i, st := range statements {
		sc := StatementCheck{Statement: st}
		for _, c := range byStatement[st.ID] {
			switch c.Checkpoint.Source {
			case store.SourceStatementOpening:
				sc.Opening = c
			case store.SourceStatementClosing:
				sc.Closing = c
			}
		}
		if i > 0 && st.PeriodStart.After(prevEnd.AddDays(1)) {
			sc.Gap = true
		}
		if st.PeriodEnd.After(prevEnd) {
			prevEnd = st.PeriodEnd
		}
		r.Statements = append(r.Statements, sc)
	}
	return r, nil
}

func sumThrough(entries []store.Entry, day civil.Date) money.Cents {
	var sum money.Cents
	for _, e := range entries {
		if !e.BalanceOn.After(day) {
			sum += e.Amount
		}
	}
	return sum
}
