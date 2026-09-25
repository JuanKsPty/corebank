// Package accounts serves the owner's real-world accounts — bank accounts,
// cards and brokerage accounts — with balances computed from what their
// statements printed.
//
// An account's balance is its opening plus the sum of its movements. The
// opening is not stored: it is derived from one anchor, a balance somebody
// stated (a statement's opening, IBKR's cash report, or the owner reading their
// bank or card statement). Every other stated balance is an assertion, and its
// drift — stated minus computed — is how a mismatch is found rather than hidden
// under a correcting entry.
package accounts

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/civil"
	"github.com/JuanKsPty/corebank/api/internal/money"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

var (
	// ErrNotFound means no such account belongs to the caller. An account
	// that exists but is someone else's is reported the same way, so an id
	// cannot be probed.
	ErrNotFound = errors.New("accounts: account not found")
	// ErrNotRelabelable means the account is a card or a brokerage account,
	// whose type follows from what it is.
	ErrNotRelabelable = errors.New("accounts: only a bank account can be relabelled checking or savings")
	// ErrCheckpointNotFound means no such checkpoint belongs to the account.
	ErrCheckpointNotFound = errors.New("accounts: checkpoint not found")
)

// Service reads and manages accounts.
type Service struct {
	db *store.DB
}

func NewService(db *store.DB) *Service { return &Service{db: db} }

// Account is an account with its computed position.
type Account struct {
	store.Account
	// Balance is signed from the holder's view: a card that owes 14.30 has
	// -14.30. It is only meaningful when Anchored.
	Balance money.Cents
	// Anchored reports whether a stated balance fixes the opening. Without
	// one the balance is just the sum of the imported movements, which for a
	// card with no previous balance is not what the bank says.
	Anchored bool
	// Holdings is a brokerage account's positions' market value.
	Holdings money.Cents
	// Drift is the latest checkpoint's stated minus computed balance, when a
	// checkpoint other than the anchor exists.
	Drift     *Drift
	Movements int
	LastDay   civil.Date
}

// Drift is how far a stated balance is from the computed one.
type Drift struct {
	AsOf     civil.Date
	Stated   money.Cents
	Computed money.Cents
	Source   string
}

// Difference is stated minus computed.
func (d Drift) Difference() money.Cents { return d.Stated - d.Computed }

// List returns the user's accounts with their positions.
func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]Account, error) {
	rows, err := s.db.Q().AccountsByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	sums, err := s.db.Q().SumsByAccount(ctx, userID)
	if err != nil {
		return nil, err
	}
	holdings, err := s.db.Q().HoldingsByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	checkpoints, err := s.db.Q().CheckpointsByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	byAccount := map[uuid.UUID][]store.Checkpoint{}
	for _, c := range checkpoints {
		byAccount[c.AccountID] = append(byAccount[c.AccountID], c)
	}

	out := make([]Account, 0, len(rows))
	for _, row := range rows {
		a, err := s.position(ctx, row, sums[row.ID], holdings[row.ID], byAccount[row.ID])
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// Get returns one of the user's accounts with its position.
func (s *Service) Get(ctx context.Context, userID, id uuid.UUID) (Account, error) {
	row, err := s.resolve(ctx, userID, id)
	if err != nil {
		return Account{}, err
	}
	sums, err := s.db.Q().SumsByAccount(ctx, userID)
	if err != nil {
		return Account{}, err
	}
	holdings, err := s.db.Q().HoldingsByUser(ctx, userID)
	if err != nil {
		return Account{}, err
	}
	cps, err := s.db.Q().CheckpointsByAccount(ctx, userID, id)
	if err != nil {
		return Account{}, err
	}
	return s.position(ctx, row, sums[id], holdings[id], cps)
}

func (s *Service) position(ctx context.Context, row store.Account, sums store.AccountSums,
	holdings money.Cents, cps []store.Checkpoint) (Account, error) {
	a := Account{Account: row, Balance: sums.Total, Holdings: holdings, Movements: sums.Count, LastDay: sums.LastBooking}

	anchor, ok := Anchor(cps)
	if !ok {
		return a, nil
	}
	opening, err := s.Opening(ctx, anchor)
	if err != nil {
		return Account{}, err
	}
	a.Anchored = true
	a.Balance = opening + sums.Total

	// The latest assertion is the one worth showing: it says whether the
	// account matches the bank now.
	for i := len(cps) - 1; i >= 0; i-- {
		c := cps[i]
		if c.ID == anchor.ID {
			continue
		}
		computed, err := s.balanceAt(ctx, c, opening)
		if err != nil {
			return Account{}, err
		}
		a.Drift = &Drift{AsOf: c.AsOf, Stated: c.Balance, Computed: computed, Source: c.Source}
		break
	}
	return a, nil
}

// Anchor picks the checkpoint the opening balance is derived from: the pinned
// one, otherwise the earliest — with a statement's own opening preferred over
// anything else stated for the same day. cps must be ordered by as_of.
func Anchor(cps []store.Checkpoint) (store.Checkpoint, bool) {
	if len(cps) == 0 {
		return store.Checkpoint{}, false
	}
	for _, c := range cps {
		if c.Pinned {
			return c, true
		}
	}
	sorted := append([]store.Checkpoint(nil), cps...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if c := sorted[i].AsOf.Compare(sorted[j].AsOf); c != 0 {
			return c < 0
		}
		return rank(sorted[i]) < rank(sorted[j])
	})
	return sorted[0], true
}

func rank(c store.Checkpoint) int {
	switch c.Source {
	case store.SourceStatementOpening:
		return 0
	case store.SourceStatementClosing, store.SourceBroker:
		return 1
	default:
		return 2
	}
}

// Opening is the balance before every movement, derived from an anchor.
func (s *Service) Opening(ctx context.Context, anchor store.Checkpoint) (money.Cents, error) {
	before, err := s.sumAt(ctx, anchor)
	if err != nil {
		return 0, err
	}
	return anchor.Balance - before, nil
}

// BalanceAt is what the movements say the balance was at a checkpoint.
func (s *Service) BalanceAt(ctx context.Context, c store.Checkpoint, opening money.Cents) (money.Cents, error) {
	return s.balanceAt(ctx, c, opening)
}

func (s *Service) balanceAt(ctx context.Context, c store.Checkpoint, opening money.Cents) (money.Cents, error) {
	sum, err := s.sumAt(ctx, c)
	if err != nil {
		return 0, err
	}
	return opening + sum, nil
}

// sumAt totals the movements a checkpoint's balance includes: everything
// before its entry, or everything through the end of its day.
func (s *Service) sumAt(ctx context.Context, c store.Checkpoint) (money.Cents, error) {
	if c.BeforeEntryID != nil {
		return s.db.Q().SumBefore(ctx, c.AccountID, *c.BeforeEntryID)
	}
	return s.db.Q().SumThroughDay(ctx, c.AccountID, c.AsOf)
}

// Rename sets or clears an account's alias.
func (s *Service) Rename(ctx context.Context, userID, id uuid.UUID, rawAlias string) (Account, error) {
	alias, err := normaliseAlias(rawAlias)
	if err != nil {
		return Account{}, err
	}
	if err := s.db.Q().SetAccountAlias(ctx, userID, id, alias); err != nil {
		return Account{}, notFound(err)
	}
	return s.Get(ctx, userID, id)
}

// SetType relabels a bank account as checking or savings.
func (s *Service) SetType(ctx context.Context, userID, id uuid.UUID, typ string) (Account, error) {
	if typ != "checking" && typ != "savings" {
		return Account{}, ErrNotRelabelable
	}
	if _, err := s.resolve(ctx, userID, id); err != nil {
		return Account{}, err
	}
	if err := s.db.Q().SetAccountType(ctx, userID, id, typ); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Account{}, ErrNotRelabelable
		}
		return Account{}, err
	}
	return s.Get(ctx, userID, id)
}

// Delete removes an account with everything imported into it. Importing its
// files again recreates it with the same movements.
func (s *Service) Delete(ctx context.Context, userID, id uuid.UUID) error {
	return notFound(s.db.Q().DeleteAccount(ctx, userID, id))
}

// CheckpointInput is a balance the owner states.
type CheckpointInput struct {
	AsOf civil.Date
	// Balance is signed from the holder's view; for a card the caller turns
	// "owed 14.30" into -14.30.
	Balance        money.Cents
	Pin            bool
	DueOn          *civil.Date
	MinimumPayment *money.Cents
	Note           string
}

// AddCheckpoint records a stated balance. Pinned, it becomes the anchor the
// opening is derived from.
func (s *Service) AddCheckpoint(ctx context.Context, userID, accountID uuid.UUID, in CheckpointInput) (store.Checkpoint, error) {
	if in.AsOf.IsZero() {
		return store.Checkpoint{}, fmt.Errorf("accounts: a checkpoint needs a date")
	}
	c := store.Checkpoint{
		ID: uuid.New(), UserID: userID, AccountID: accountID, AsOf: in.AsOf, Balance: in.Balance,
		Source: store.SourceManual, DueOn: in.DueOn, MinimumPayment: in.MinimumPayment, Note: in.Note,
	}
	err := s.db.InTx(ctx, func(q *store.Queries) error {
		if err := q.LockUser(ctx, userID); err != nil {
			return err
		}
		if _, err := q.AccountByID(ctx, userID, accountID); err != nil {
			return notFound(err)
		}
		if err := q.CreateCheckpoint(ctx, c); err != nil {
			return err
		}
		if in.Pin {
			c.Pinned = true
			return q.PinCheckpoint(ctx, userID, accountID, &c.ID)
		}
		return nil
	})
	return c, err
}

// DeleteCheckpoint removes a checkpoint the owner entered.
func (s *Service) DeleteCheckpoint(ctx context.Context, userID, id uuid.UUID) error {
	if err := s.db.Q().DeleteManualCheckpoint(ctx, userID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrCheckpointNotFound
		}
		return err
	}
	return nil
}

// Pin makes a checkpoint the account's anchor, or unpins with nil so the
// earliest checkpoint anchors again.
func (s *Service) Pin(ctx context.Context, userID, accountID uuid.UUID, checkpointID *uuid.UUID) error {
	return s.db.InTx(ctx, func(q *store.Queries) error {
		if err := q.LockUser(ctx, userID); err != nil {
			return err
		}
		if _, err := q.AccountByID(ctx, userID, accountID); err != nil {
			return notFound(err)
		}
		if err := q.PinCheckpoint(ctx, userID, accountID, checkpointID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return ErrCheckpointNotFound
			}
			return err
		}
		return nil
	})
}

// Checkpoints lists an account's checkpoints.
func (s *Service) Checkpoints(ctx context.Context, userID, accountID uuid.UUID) ([]store.Checkpoint, error) {
	if _, err := s.resolve(ctx, userID, accountID); err != nil {
		return nil, err
	}
	return s.db.Q().CheckpointsByAccount(ctx, userID, accountID)
}

// NetWorth sums every account's balance and holdings. Liability balances are
// negative already, so this is assets minus debts plus investments.
func NetWorth(list []Account) (assets, liabilities, holdings money.Cents) {
	for _, a := range list {
		if a.Class == "liability" {
			liabilities += a.Balance
		} else {
			assets += a.Balance
		}
		holdings += a.Holdings
	}
	return assets, liabilities, holdings
}

func (s *Service) resolve(ctx context.Context, userID, id uuid.UUID) (store.Account, error) {
	a, err := s.db.Q().AccountByID(ctx, userID, id)
	return a, notFound(err)
}

func notFound(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	return err
}
