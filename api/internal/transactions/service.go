// Package transactions moves money.
//
// Every path through it follows the same shape, and the shape is the point.
// There are two stores with no transaction spanning them: the ledger owns the
// money, PostgreSQL owns the description. Writing to both cannot be atomic, so
// the order is chosen to make every possible interruption recoverable:
//
//  1. Authorise, and check the amount and the accounts.
//  2. Write the PostgreSQL row as pending. Its id *is* the ledger transfer id.
//  3. Submit to the ledger.
//  4. Record the outcome on the row.
//
// A crash between 2 and 4 leaves a pending row whose id is exactly what to look
// up in the ledger, so the sweeper can always tell whether the money moved. The
// reverse order — ledger first — would allow money to move with no record of
// why, which nothing can repair.
//
// What this package deliberately does not contain is a balance check. Overdrafts
// are refused by the ledger itself, so there is no `if balance < amount` here to
// forget, duplicate, or get wrong on a new code path.
package transactions

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/accounts"
	"github.com/JuanKsPty/corebank/api/internal/categories"
	"github.com/JuanKsPty/corebank/api/internal/ledger"
	"github.com/JuanKsPty/corebank/api/internal/logging"
	"github.com/JuanKsPty/corebank/api/internal/money"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

var (
	// ErrConfirmationNotFound means no pending movement matches that hold.
	ErrConfirmationNotFound = errors.New("transactions: confirmation not found")

	// ErrConfirmationSettled means the movement was already confirmed or
	// cancelled. Retrying is pointless and must not move money twice.
	ErrConfirmationSettled = errors.New("transactions: confirmation already resolved")

	// ErrIdempotencyMismatch means a key was reused with different parameters.
	// Returning the first result would be wrong — the caller asked for something
	// else — so it is refused instead.
	ErrIdempotencyMismatch = errors.New("transactions: idempotency key reused with different parameters")

	// ErrTransactionNotFound means no movement matches that id, or it touches
	// none of the caller's own accounts. The two are deliberately reported
	// identically, for the same reason accounts.ErrNotOwned is folded into
	// ErrNotFound at the HTTP layer: telling them apart would let a client
	// probe for valid transaction ids.
	ErrTransactionNotFound = errors.New("transactions: transaction not found")
)

// Service performs and records movements.
type Service struct {
	db         *store.DB
	book       ledger.Ledger
	accounts   *accounts.Service
	categories *categories.Service
	holdTTL    time.Duration
	now        func() time.Time
}

func NewService(db *store.DB, book ledger.Ledger, accts *accounts.Service, cats *categories.Service, holdTTL time.Duration) *Service {
	return &Service{db: db, book: book, accounts: accts, categories: cats, holdTTL: holdTTL, now: time.Now}
}

// HoldTTL is how long a reservation survives without an answer.
func (s *Service) HoldTTL() time.Duration { return s.holdTTL }

// Request is a movement a customer asked for.
//
// Account is the customer's own account: the destination of a deposit, and the
// source of a withdrawal or a transfer. Counterparty is only used by transfers.
// Empty Account means "the customer's only account", which is what lets the
// assistant say "deposit 100" without inventing an account number.
type Request struct {
	Account      string
	Counterparty string
	Amount       money.Cents
	Description  string
	// Origin distinguishes a movement the customer performed themselves from one
	// the assistant performed on their behalf.
	Origin string
	// IdempotencyKey, when set, makes a retry of the same request return the
	// original movement instead of performing a second one.
	IdempotencyKey string
}

// Deposit credits the customer's account from outside the bank.
//
// No confirmation step: it can only increase the customer's balance, so there is
// nothing for them to lose by it happening.
func (s *Service) Deposit(ctx context.Context, userID uuid.UUID, req Request) (store.Transaction, error) {
	account, err := s.resolveOwn(ctx, userID, req.Account)
	if err != nil {
		return store.Transaction{}, err
	}

	return s.post(ctx, userID, movement{
		kind:        ledger.MovementDeposit,
		from:        ledger.WorldAccountID,
		to:          account.LedgerID,
		fromNumber:  store.ExternalAccount,
		toNumber:    account.Number,
		amount:      req.Amount,
		description: defaultDescription(req.Description, "Depósito"),
		origin:      req.Origin,
		idempotency: req.IdempotencyKey,
	})
}

// Withdraw debits the customer's account to outside the bank.
func (s *Service) Withdraw(ctx context.Context, userID uuid.UUID, req Request) (store.Transaction, error) {
	account, err := s.resolveOwn(ctx, userID, req.Account)
	if err != nil {
		return store.Transaction{}, err
	}

	return s.post(ctx, userID, movement{
		kind:        ledger.MovementWithdrawal,
		from:        account.LedgerID,
		to:          ledger.WorldAccountID,
		fromNumber:  account.Number,
		toNumber:    store.ExternalAccount,
		amount:      req.Amount,
		description: defaultDescription(req.Description, "Retiro"),
		origin:      req.Origin,
		idempotency: req.IdempotencyKey,
	})
}

// Transfer moves money between two accounts in this bank.
func (s *Service) Transfer(ctx context.Context, userID uuid.UUID, req Request) (store.Transaction, error) {
	plan, err := s.planTransfer(ctx, userID, req)
	if err != nil {
		return store.Transaction{}, err
	}
	return s.post(ctx, userID, plan)
}

// PrepareTransfer reserves the funds for a transfer without delivering them, and
// returns the movement awaiting confirmation.
//
// This is what the assistant calls. The reservation is a real two-phase ledger
// transfer, which buys two things a "pending" flag in a table would not: the
// money is guaranteed to still be there when the customer confirms, because
// nothing else can spend it meanwhile; and if nobody answers, the ledger releases
// it on its own timeout, so there is no cleanup job whose failure would leave
// funds stranded.
func (s *Service) PrepareTransfer(ctx context.Context, userID uuid.UUID, req Request) (store.Transaction, error) {
	plan, err := s.planTransfer(ctx, userID, req)
	if err != nil {
		return store.Transaction{}, err
	}
	return s.hold(ctx, userID, plan)
}

// PrepareWithdrawal reserves the funds for a withdrawal, awaiting confirmation.
func (s *Service) PrepareWithdrawal(ctx context.Context, userID uuid.UUID, req Request) (store.Transaction, error) {
	account, err := s.resolveOwn(ctx, userID, req.Account)
	if err != nil {
		return store.Transaction{}, err
	}

	return s.hold(ctx, userID, movement{
		kind:        ledger.MovementWithdrawal,
		from:        account.LedgerID,
		to:          ledger.WorldAccountID,
		fromNumber:  account.Number,
		toNumber:    store.ExternalAccount,
		amount:      req.Amount,
		description: defaultDescription(req.Description, "Retiro"),
		origin:      req.Origin,
	})
}

// Confirm settles a reservation: the money moves.
//
// The ownership check here is the security boundary of the whole confirmation
// flow. A hold id is the only thing the client sends, so this is where "is this
// yours to confirm?" is answered — and it is answered from the authenticated
// session, never from anything the client or a language model supplied.
func (s *Service) Confirm(ctx context.Context, userID uuid.UUID, holdID uuid.UUID) (store.Transaction, error) {
	return s.resolveHold(ctx, userID, holdID, true)
}

// Cancel releases a reservation: the funds return to the source and nothing moved.
func (s *Service) Cancel(ctx context.Context, userID uuid.UUID, holdID uuid.UUID) (store.Transaction, error) {
	return s.resolveHold(ctx, userID, holdID, false)
}

// SetCategory files a movement under one of the caller's own categories, or
// clears it when categoryID is nil.
//
// This is metadata only — nothing here touches the ledger or the movement's
// amount, kind or accounts — but the two ownership checks still matter: the
// movement has to be one that touches an account of the caller's, and the
// category has to be one the caller created, or a customer could label
// somebody else's spending, or file their own under a category that leaks
// which categories another customer happens to have.
func (s *Service) SetCategory(ctx context.Context, userID, transactionID uuid.UUID, categoryID *uuid.UUID) (store.Transaction, error) {
	row, err := s.db.Q().TransactionByID(ctx, transactionID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Transaction{}, fmt.Errorf("%w: %s", ErrTransactionNotFound, transactionID)
		}
		return store.Transaction{}, err
	}
	owns, err := s.ownsMovement(ctx, userID, row)
	if err != nil {
		return store.Transaction{}, err
	}
	if !owns {
		return store.Transaction{}, fmt.Errorf("%w: %s", ErrTransactionNotFound, transactionID)
	}

	if categoryID != nil {
		if _, err := s.categories.Get(ctx, userID, *categoryID); err != nil {
			return store.Transaction{}, err
		}
	}

	if err := s.db.Q().SetTransactionCategory(ctx, transactionID, categoryID); err != nil {
		return store.Transaction{}, err
	}
	row.CategoryID = categoryID
	return row, nil
}

// ownsMovement reports whether one side of a movement is an account userID
// holds. A transfer's row is shared between both parties — one from_account,
// one to_account — so either side qualifies; the sentinel EXTERNAL side of a
// deposit or withdrawal never resolves to anyone and is skipped rather than
// treated as a lookup failure.
func (s *Service) ownsMovement(ctx context.Context, userID uuid.UUID, row store.Transaction) (bool, error) {
	for _, number := range []string{row.FromAccount, row.ToAccount} {
		if number == "" || number == store.ExternalAccount {
			continue
		}
		switch _, err := s.accounts.Resolve(ctx, userID, number); {
		case err == nil:
			return true, nil
		case errors.Is(err, accounts.ErrNotFound), errors.Is(err, accounts.ErrNotOwned):
			continue
		default:
			return false, err
		}
	}
	return false, nil
}

// --- internals --------------------------------------------------------------

// movement is a fully resolved, authorised movement ready for the ledger.
type movement struct {
	kind       ledger.MovementKind
	from, to   ledger.AccountID
	fromNumber string
	toNumber   string
	amount     money.Cents

	description string
	origin      string
	idempotency string
}

// planTransfer resolves and authorises a transfer's two accounts.
func (s *Service) planTransfer(ctx context.Context, userID uuid.UUID, req Request) (movement, error) {
	source, err := s.resolveOwn(ctx, userID, req.Account)
	if err != nil {
		return movement{}, err
	}

	if req.Counterparty == "" {
		return movement{}, fmt.Errorf("%w: falta la cuenta de destino", ledger.ErrInvalidAccountNumber)
	}

	// The destination is looked up without an ownership check, because sending
	// money to someone else's account is what a transfer is. It still has to
	// exist: a transfer into a number nobody holds must fail loudly rather than
	// vanish.
	destination, err := s.accounts.Lookup(ctx, req.Counterparty)
	if err != nil {
		if errors.Is(err, accounts.ErrNotFound) {
			return movement{}, fmt.Errorf("%w: %s", ledger.ErrDestinationNotFound, req.Counterparty)
		}
		return movement{}, err
	}
	if destination.Number == source.Number {
		return movement{}, ledger.ErrSameAccount
	}

	// A move between two of the customer's own accounts is labelled differently,
	// because on a statement it is not money leaving — it is money changing
	// pocket.
	kind := ledger.MovementTransfer
	fallbackDescription := "Transferencia a " + destination.Number
	if destination.UserID == userID {
		kind = ledger.MovementInternalTransfer
		fallbackDescription = "Traspaso entre cuentas propias"
	}

	return movement{
		kind:        kind,
		from:        source.LedgerID,
		to:          destination.LedgerID,
		fromNumber:  source.Number,
		toNumber:    destination.Number,
		amount:      req.Amount,
		description: defaultDescription(req.Description, fallbackDescription),
		origin:      req.Origin,
		idempotency: req.IdempotencyKey,
	}, nil
}

// resolveOwn returns the customer's account named by number, or their only
// account when number is empty.
func (s *Service) resolveOwn(ctx context.Context, userID uuid.UUID, number string) (store.Account, error) {
	if number != "" {
		return s.accounts.Resolve(ctx, userID, number)
	}

	owned, err := s.db.Q().AccountsByUser(ctx, userID)
	if err != nil {
		return store.Account{}, err
	}
	switch len(owned) {
	case 0:
		return store.Account{}, fmt.Errorf("%w: el usuario no tiene cuentas", accounts.ErrNotFound)
	case 1:
		return owned[0], nil
	default:
		// Guessing which of several accounts the customer meant is exactly the
		// kind of helpfulness that moves money from the wrong place. The caller —
		// including the assistant — has to ask.
		return store.Account{}, fmt.Errorf("%w: especifica cuál de tus %d cuentas",
			ErrAccountRequired, len(owned))
	}
}

// ErrAccountRequired means the customer has several accounts and did not say
// which one.
var ErrAccountRequired = errors.New("transactions: account must be specified")

// post performs a movement immediately: row, ledger, outcome.
func (s *Service) post(ctx context.Context, userID uuid.UUID, m movement) (store.Transaction, error) {
	if m.amount <= 0 {
		return store.Transaction{}, ledger.ErrAmountNotPositive
	}

	if replay, found, err := s.replay(ctx, userID, m); err != nil {
		return store.Transaction{}, err
	} else if found {
		return replay, nil
	}

	now := s.now().UTC()
	row := store.Transaction{
		ID:          uuid.New(),
		Kind:        m.kind,
		Status:      store.StatusPending,
		Amount:      m.amount,
		Currency:    money.CurrencyUSD,
		FromAccount: m.fromNumber,
		ToAccount:   m.toNumber,
		Description: m.description,
		Source:      store.SourceLive,
		Origin:      originOrDefault(m.origin),
		InitiatedBy: userID,
		OccurredAt:  now,
	}

	// The row and its idempotency claim are written together, so a key can never
	// be claimed for a movement that was not recorded.
	err := s.db.InTx(ctx, func(q *store.Queries) error {
		created, err := q.CreateTransaction(ctx, row)
		if err != nil {
			return err
		}
		row = created
		if m.idempotency != "" {
			return q.SaveIdempotencyKey(ctx, userID, m.idempotency, row.ID)
		}
		return nil
	})
	if err != nil {
		return store.Transaction{}, err
	}

	ledgerErr := s.book.Post(ctx, ledger.Movement{
		ID:     row.ID,
		From:   m.from,
		To:     m.to,
		Amount: m.amount,
		Kind:   m.kind,
		Actor:  userID,
	})
	return s.recordOutcome(ctx, row, ledgerErr)
}

// hold reserves funds and leaves the movement awaiting confirmation.
func (s *Service) hold(ctx context.Context, userID uuid.UUID, m movement) (store.Transaction, error) {
	if m.amount <= 0 {
		return store.Transaction{}, ledger.ErrAmountNotPositive
	}

	now := s.now().UTC()
	// Two ids up front: one for the reservation, one reserved for whichever
	// transfer eventually resolves it. Minting both now is what lets a crash
	// mid-flight be reconciled from the row alone.
	row := store.Transaction{
		ID:            uuid.New(),
		Kind:          m.kind,
		Status:        store.StatusPending,
		Amount:        m.amount,
		Currency:      money.CurrencyUSD,
		FromAccount:   m.fromNumber,
		ToAccount:     m.toNumber,
		Description:   m.description,
		Source:        store.SourceLive,
		Origin:        originOrDefault(m.origin),
		InitiatedBy:   userID,
		HoldID:        uuid.New(),
		HoldExpiresAt: now.Add(s.holdTTL),
		OccurredAt:    now,
	}

	created, err := s.db.Q().CreateTransaction(ctx, row)
	if err != nil {
		return store.Transaction{}, err
	}
	row = created

	err = s.book.Hold(ctx, ledger.Movement{
		ID:     row.HoldID,
		From:   m.from,
		To:     m.to,
		Amount: m.amount,
		Kind:   m.kind,
		Actor:  userID,
	}, s.holdTTL)
	if err != nil {
		// The reservation was refused — most often for insufficient funds. Mark
		// the row failed so nothing is left looking like it is waiting for an
		// answer, then report the original reason.
		if _, markErr := s.db.Q().SettleTransaction(ctx, row.ID, store.StatusFailed, failureCode(err)); markErr != nil {
			logging.FromContext(ctx).Error("could not mark a rejected hold as failed",
				"transaction_id", row.ID, "error", markErr)
		}
		return store.Transaction{}, err
	}

	logging.FromContext(ctx).Info("funds reserved awaiting confirmation",
		"transaction_id", row.ID, "hold_id", row.HoldID,
		"amount_cents", int64(row.Amount), "expires_at", row.HoldExpiresAt)
	return row, nil
}

// resolveHold settles or voids a reservation.
func (s *Service) resolveHold(ctx context.Context, userID, holdID uuid.UUID, settle bool) (store.Transaction, error) {
	row, err := s.db.Q().TransactionByHold(ctx, holdID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Transaction{}, fmt.Errorf("%w: %s", ErrConfirmationNotFound, holdID)
		}
		return store.Transaction{}, err
	}

	// Someone else's reservation is reported as not found, not as forbidden: a
	// 403 would confirm that the hold id is real.
	if row.InitiatedBy != userID {
		return store.Transaction{}, fmt.Errorf("%w: %s belongs to another user", ErrConfirmationNotFound, holdID)
	}
	if !row.AwaitingConfirmation() {
		return store.Transaction{}, fmt.Errorf("%w: %s is %s", ErrConfirmationSettled, holdID, row.Status)
	}

	if settle {
		err = s.book.Settle(ctx, row.HoldID, row.ID, row.Amount, row.Kind)
	} else {
		err = s.book.Void(ctx, row.HoldID, row.ID, row.Amount, row.Kind)
	}

	switch {
	case err == nil, errors.Is(err, ledger.ErrAlreadyApplied):
		// Already applied means a previous attempt got through and the response
		// was lost. The money is where it should be, so this is success.
	case errors.Is(err, ledger.ErrHoldExpired):
		// The ledger already released the funds on timeout. Record that and tell
		// the customer to start over — the amount they were shown is no longer
		// reserved.
		if _, markErr := s.db.Q().SettleTransaction(ctx, row.ID, store.StatusExpired, "hold_expired"); markErr != nil {
			logging.FromContext(ctx).Error("could not mark an expired hold",
				"transaction_id", row.ID, "error", markErr)
		}
		return store.Transaction{}, err
	case errors.Is(err, ledger.ErrHoldAlreadySettled), errors.Is(err, ledger.ErrHoldAlreadyVoided):
		// The ledger and the row disagree: the hold was resolved without this
		// row being updated. Reconcile the row to match the ledger, which is
		// authoritative about money.
		return s.reconcileResolvedHold(ctx, row, err)
	default:
		return store.Transaction{}, err
	}

	final := store.StatusCompleted
	if !settle {
		final = store.StatusVoided
	}
	if _, err := s.db.Q().SettleTransaction(ctx, row.ID, final, ""); err != nil {
		// The money moved; only the record is behind. Surfacing this as a failure
		// would be a lie, so it is logged for the sweeper and the movement is
		// reported as what it is.
		logging.FromContext(ctx).Error("ledger resolved but the row was not updated",
			"transaction_id", row.ID, "status", final, "error", err)
	}

	row.Status = final
	logging.FromContext(ctx).Info("confirmation resolved",
		"transaction_id", row.ID, "hold_id", row.HoldID, "status", final)
	return row, nil
}

// reconcileResolvedHold aligns a row with a hold the ledger has already resolved.
func (s *Service) reconcileResolvedHold(ctx context.Context, row store.Transaction, cause error) (store.Transaction, error) {
	status := store.StatusCompleted
	if errors.Is(cause, ledger.ErrHoldAlreadyVoided) {
		status = store.StatusVoided
	}
	if _, err := s.db.Q().SettleTransaction(ctx, row.ID, status, ""); err != nil {
		return store.Transaction{}, err
	}
	row.Status = status

	logging.FromContext(ctx).Warn("row reconciled against the ledger",
		"transaction_id", row.ID, "hold_id", row.HoldID, "status", status)
	return row, fmt.Errorf("%w: %s", ErrConfirmationSettled, status)
}

// recordOutcome writes the ledger's verdict onto the row and translates it.
func (s *Service) recordOutcome(ctx context.Context, row store.Transaction, ledgerErr error) (store.Transaction, error) {
	switch {
	case ledgerErr == nil, errors.Is(ledgerErr, ledger.ErrAlreadyApplied):
		// Already applied is success: this exact movement is in the ledger, once.
		if _, err := s.db.Q().SettleTransaction(ctx, row.ID, store.StatusCompleted, ""); err != nil {
			logging.FromContext(ctx).Error("money moved but the row was not updated",
				"transaction_id", row.ID, "error", err)
		}
		row.Status = store.StatusCompleted

		logging.FromContext(ctx).Info("movement completed",
			"transaction_id", row.ID, "kind", row.Kind.String(),
			"amount_cents", int64(row.Amount), "origin", row.Origin)
		return row, nil

	default:
		if _, err := s.db.Q().SettleTransaction(ctx, row.ID, store.StatusFailed, failureCode(ledgerErr)); err != nil {
			logging.FromContext(ctx).Error("could not mark a movement as failed",
				"transaction_id", row.ID, "error", err)
		}
		return store.Transaction{}, ledgerErr
	}
}

// replay returns the movement a previous request with the same idempotency key
// produced.
//
// The stored movement's parameters are compared with the new request's, because
// a key reused for a *different* movement is a client bug, and answering it with
// an unrelated success would hide a real problem.
func (s *Service) replay(ctx context.Context, userID uuid.UUID, m movement) (store.Transaction, bool, error) {
	if m.idempotency == "" {
		return store.Transaction{}, false, nil
	}

	id, err := s.db.Q().FindIdempotent(ctx, userID, m.idempotency)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Transaction{}, false, nil
		}
		return store.Transaction{}, false, err
	}

	row, err := s.db.Q().TransactionByID(ctx, id)
	if err != nil {
		return store.Transaction{}, false, err
	}
	if row.Kind != m.kind || row.Amount != m.amount ||
		row.FromAccount != m.fromNumber || row.ToAccount != m.toNumber {
		return store.Transaction{}, false, fmt.Errorf("%w: %s", ErrIdempotencyMismatch, m.idempotency)
	}

	logging.FromContext(ctx).Info("idempotent replay served from the original movement",
		"transaction_id", row.ID, "key", m.idempotency)
	return row, true, nil
}

// failureCode reduces a ledger error to the short token stored on the row.
func failureCode(err error) string {
	switch {
	case errors.Is(err, ledger.ErrInsufficientFunds):
		return "insufficient_funds"
	case errors.Is(err, ledger.ErrSourceNotFound):
		return "source_account_not_found"
	case errors.Is(err, ledger.ErrDestinationNotFound):
		return "destination_account_not_found"
	case errors.Is(err, ledger.ErrSameAccount):
		return "same_account"
	case errors.Is(err, ledger.ErrAmountNotPositive):
		return "amount_not_positive"
	case errors.Is(err, ledger.ErrPreviouslyFailed):
		return "previously_failed"
	case err == nil:
		return ""
	default:
		return "ledger_error"
	}
}

func defaultDescription(given, fallback string) string {
	if given == "" {
		return fallback
	}
	return given
}

func originOrDefault(origin string) string {
	switch origin {
	case store.OriginChat, store.OriginIBKRSync:
		return origin
	default:
		return store.OriginAPI
	}
}
