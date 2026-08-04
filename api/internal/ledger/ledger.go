// Package ledger is the domain model for money movement.
//
// It owns the vocabulary — accounts, movements, holds, balances — and the
// contract every ledger backend must satisfy, but it knows nothing about
// TigerBeetle. The concrete implementation lives in internal/tigerbeetle; the
// rest of the application depends on the Ledger interface declared here, so
// services can be tested without a ledger process running.
//
// Two properties of this design carry most of the system's safety:
//
//   - Balances are never stored in Postgres. They are derived from the ledger,
//     which is the single source of truth for money.
//   - Overdrafts are impossible by construction rather than by validation. A
//     customer account is created so that its debits can never exceed its
//     credits, so a withdrawal beyond the balance is rejected by the ledger
//     itself, not by an `if` that someone could forget to write.
package ledger

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/money"
)

// LedgerUSD is the ISO 4217 numeric code for the US dollar. Every account and
// movement in this system is on this one ledger; the backend refuses transfers
// between accounts on different ledgers, which is what makes a multi-currency
// mistake impossible rather than merely unlikely.
const LedgerUSD uint32 = 840

// WorldAccountID is the bank's equity counterparty.
//
// Double-entry bookkeeping has no single-sided postings: money credited to a
// customer has to be debited from somewhere. Deposits are debited from this
// account and withdrawals are credited back to it, so it runs an unbounded
// debit balance by design and is deliberately created without a
// balance-limiting rule. Its id is reserved and cannot collide with an id
// derived from an account number, which is always 16 digits.
const WorldAccountID AccountID = 1

// AccountID is an account's identity in the ledger.
//
// For customer accounts it is the account number with the separators removed
// ("4001-6588-5247-0001" becomes 4001658852470001). Deriving it rather than
// generating it is what makes seeding and retries idempotent: the same account
// number always maps to the same ledger account, so re-running the seeder
// reports "already exists" instead of creating duplicates.
type AccountID uint64

// AccountKind classifies an account. The values are stable and are stored in
// the ledger's per-account `code` field, so they must not be renumbered.
type AccountKind uint16

const (
	KindSavings    AccountKind = 1
	KindChecking   AccountKind = 2
	KindInvestment AccountKind = 3
	KindWorld      AccountKind = 999
)

// ParseAccountKind maps the wire/database representation to a kind.
func ParseAccountKind(s string) (AccountKind, error) {
	switch s {
	case "savings":
		return KindSavings, nil
	case "checking":
		return KindChecking, nil
	case "investment":
		return KindInvestment, nil
	default:
		return 0, fmt.Errorf("ledger: unknown account type %q", s)
	}
}

func (k AccountKind) String() string {
	switch k {
	case KindSavings:
		return "savings"
	case KindChecking:
		return "checking"
	case KindInvestment:
		return "investment"
	case KindWorld:
		return "world"
	default:
		return "unknown"
	}
}

// MovementKind classifies a posting. Like AccountKind these values are stored
// in the ledger's `code` field and must remain stable.
type MovementKind uint16

const (
	MovementDeposit          MovementKind = 1
	MovementWithdrawal       MovementKind = 2
	MovementTransfer         MovementKind = 3
	MovementInternalTransfer MovementKind = 4
	MovementOpening          MovementKind = 10
)

// ParseMovementKind maps the database representation back to a kind.
//
// MovementOpening is deliberately absent: opening transfers exist only in the
// ledger, so a row claiming to be one means the seeder and this code have
// diverged.
func ParseMovementKind(s string) (MovementKind, error) {
	switch s {
	case "deposit":
		return MovementDeposit, nil
	case "withdrawal":
		return MovementWithdrawal, nil
	case "transfer":
		return MovementTransfer, nil
	case "internal_transfer":
		return MovementInternalTransfer, nil
	default:
		return 0, fmt.Errorf("ledger: unknown movement kind %q", s)
	}
}

func (k MovementKind) String() string {
	switch k {
	case MovementDeposit:
		return "deposit"
	case MovementWithdrawal:
		return "withdrawal"
	case MovementTransfer:
		return "transfer"
	case MovementInternalTransfer:
		return "internal_transfer"
	case MovementOpening:
		return "opening"
	default:
		return "unknown"
	}
}

// AccountIDFromNumber derives a ledger account id from a formatted account
// number. Separators are ignored, so both "4001-6588-5247-0001" and
// "4001658852470001" resolve to the same account.
func AccountIDFromNumber(number string) (AccountID, error) {
	// Rejecting unexpected characters outright matters more than it looks:
	// silently skipping them would map a malformed number like
	// "4001-ABCD-5247-0001" onto a different, valid account id.
	var (
		id     uint64
		digits int
	)
	for _, r := range number {
		switch {
		case r >= '0' && r <= '9':
			if digits >= 18 {
				// Beyond this the value would not comfortably fit a uint64,
				// and no account number this system issues is that long.
				return 0, fmt.Errorf("%w: %q is too long", ErrInvalidAccountNumber, number)
			}
			id = id*10 + uint64(r-'0')
			digits++
		case r == '-' || r == ' ':
			// Separators are cosmetic.
		default:
			return 0, fmt.Errorf("%w: %q", ErrInvalidAccountNumber, number)
		}
	}
	if digits == 0 {
		return 0, fmt.Errorf("%w: %q", ErrInvalidAccountNumber, number)
	}
	if AccountID(id) == WorldAccountID {
		// The world account's id is reserved and must never be reachable from
		// a customer-supplied account number.
		return 0, fmt.Errorf("%w: %q is reserved", ErrInvalidAccountNumber, number)
	}
	return AccountID(id), nil
}

// NewAccount describes an account to create in the ledger.
type NewAccount struct {
	ID   AccountID
	Kind AccountKind
	// Owner is the user the account belongs to, carried into the ledger for
	// traceability. It is the zero UUID for the world account.
	Owner uuid.UUID
}

// World is the equity counterparty's definition, created once at startup and on
// every seed. Creating it is idempotent, so it is safe to call unconditionally.
func World() NewAccount {
	return NewAccount{ID: WorldAccountID, Kind: KindWorld}
}

// Balance is an account's position.
//
// Held is money reserved by an unresolved two-phase movement: it has left the
// available balance but has not reached its destination. Available is what the
// customer can actually spend and is the number the UI shows.
type Balance struct {
	AccountID AccountID
	Posted    money.Cents
	Held      money.Cents
	Available money.Cents
}

// Movement is a single posting: an amount moving from one account to another.
//
// ID is supplied by the caller, not generated here. The transaction row in
// Postgres mints it and the ledger reuses it verbatim, which is what makes a
// crash-retry safe — resubmitting the same id is reported as already applied
// instead of moving the money twice.
type Movement struct {
	ID     uuid.UUID
	From   AccountID
	To     AccountID
	Amount money.Cents
	Kind   MovementKind
	// Actor is the user who initiated the movement, recorded for audit.
	Actor uuid.UUID
}

// Validate catches the mistakes worth rejecting before touching the ledger, so
// callers get a domain error rather than a backend status code.
func (m Movement) Validate() error {
	if m.ID == uuid.Nil {
		return errors.New("ledger: movement id is required")
	}
	if m.Amount <= 0 {
		return ErrAmountNotPositive
	}
	if m.From == 0 || m.To == 0 {
		return errors.New("ledger: both accounts are required")
	}
	if m.From == m.To {
		return ErrSameAccount
	}
	return nil
}

// Entry is a movement as recorded by the ledger, including the two-phase
// bookkeeping fields needed to reconstruct a hold's lifecycle.
type Entry struct {
	ID     uuid.UUID
	From   AccountID
	To     AccountID
	Amount money.Cents
	Kind   MovementKind
	At     time.Time

	// Pending marks a reservation that has not yet been settled or released.
	Pending bool
	// Settles and Voids identify which hold this entry resolved, if any.
	Settles bool
	Voids   bool
	HoldID  uuid.UUID
}

// EntryQuery bounds a history lookup.
type EntryQuery struct {
	// Limit caps the number of entries returned. Zero means the backend's
	// default.
	Limit uint32
	// Newest returns the most recent entries first when true.
	Newest bool
}

// Ledger is the contract the money layer depends on.
//
// Implementations must be safe for concurrent use. Every method takes a context
// so callers can carry deadlines and cancellation, though a given backend may
// not be able to interrupt an in-flight request.
type Ledger interface {
	// EnsureAccounts creates accounts, treating an account that already exists
	// with the same configuration as success. This makes it safe to call on
	// every boot and from the seeder.
	EnsureAccounts(ctx context.Context, accounts []NewAccount) error

	// Balances returns the position of each requested account. Accounts that do
	// not exist are absent from the result rather than an error, so callers can
	// distinguish "no such account" from a transport failure.
	Balances(ctx context.Context, ids []AccountID) (map[AccountID]Balance, error)

	// Post applies a movement immediately.
	Post(ctx context.Context, m Movement) error

	// PostMany applies movements in as few round trips as the backend allows,
	// returning one error per movement in the order given — nil where it
	// succeeded. It exists for the seeder, which opens sixteen hundred accounts
	// at once and would otherwise pay a round trip for each.
	//
	// Unlike Post it does not fail on the first rejection: a bulk import needs to
	// know which rows were refused, not just that one was.
	PostMany(ctx context.Context, movements []Movement) ([]error, error)

	// Hold reserves funds without delivering them, expiring automatically after
	// ttl if it is never resolved. This is what backs the assistant's
	// confirmation step: the money is guaranteed to be there when the user taps
	// Confirm, and nothing has moved if they walk away.
	Hold(ctx context.Context, m Movement, ttl time.Duration) error

	// Settle completes a hold, moving the reserved funds to their destination.
	// entryID identifies the new posting and must be unique.
	Settle(ctx context.Context, holdID, entryID uuid.UUID, amount money.Cents, kind MovementKind) error

	// Void releases a hold, returning the reserved funds to the source.
	Void(ctx context.Context, holdID, entryID uuid.UUID, amount money.Cents, kind MovementKind) error

	// Entries returns an account's postings.
	Entries(ctx context.Context, id AccountID, q EntryQuery) ([]Entry, error)

	// Lookup returns the entries for the given movement ids, which the
	// reconciliation sweeper uses to discover whether a movement it lost track
	// of actually landed.
	Lookup(ctx context.Context, ids []uuid.UUID) ([]Entry, error)

	// Close releases the backend connection.
	Close() error
}

// Domain errors. Callers match on these rather than on backend status codes, so
// the HTTP layer has a single place to map them to responses and the wording
// shown to users lives in one place.
var (
	// ErrInsufficientFunds means the source account does not have the money.
	// The ledger enforces this; it is not a pre-check.
	ErrInsufficientFunds = errors.New("ledger: insufficient funds")

	ErrSourceNotFound       = errors.New("ledger: source account not found")
	ErrDestinationNotFound  = errors.New("ledger: destination account not found")
	ErrSameAccount          = errors.New("ledger: source and destination must differ")
	ErrAmountNotPositive    = errors.New("ledger: amount must be positive")
	ErrInvalidAccountNumber = errors.New("ledger: invalid account number")

	// ErrAlreadyApplied reports that this movement id was already recorded.
	// It is not a failure: the caller's earlier attempt succeeded, and the
	// correct response is to treat the operation as done.
	ErrAlreadyApplied = errors.New("ledger: movement already applied")

	// ErrPreviouslyFailed reports that this movement id was already rejected.
	// Retrying it will never succeed; the caller must mint a new id.
	ErrPreviouslyFailed = errors.New("ledger: movement id previously failed")

	ErrHoldNotFound       = errors.New("ledger: hold not found")
	ErrHoldAlreadySettled = errors.New("ledger: hold already settled")
	ErrHoldAlreadyVoided  = errors.New("ledger: hold already voided")
	ErrHoldExpired        = errors.New("ledger: hold expired")

	// ErrAccountConflict means an account with this id exists but was created
	// with different properties, which indicates a seeding or configuration
	// bug rather than a transient problem.
	ErrAccountConflict = errors.New("ledger: account exists with different configuration")
)
