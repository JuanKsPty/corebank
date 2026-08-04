// Package tigerbeetle adapts the TigerBeetle client to the ledger.Ledger
// contract.
//
// Everything TigerBeetle-specific is confined here: the u128 conversions, the
// flag layout, and the translation of result statuses into domain errors. The
// rest of the application talks to ledger.Ledger and stays unaware of which
// backend is behind it.
package tigerbeetle

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	tb "github.com/tigerbeetle/tigerbeetle-go"

	"github.com/JuanKsPty/corebank/api/internal/ledger"
	"github.com/JuanKsPty/corebank/api/internal/money"
)

// maxBatch is the number of events TigerBeetle accepts in one request. The
// seeder relies on this to submit thousands of accounts and transfers without
// tripping the limit.
const maxBatch = 8189

// Adapter implements ledger.Ledger on top of TigerBeetle.
type Adapter struct {
	client tb.Client
}

var _ ledger.Ledger = (*Adapter)(nil)

// Connect opens a client against the given replica addresses.
//
// clusterID must match the id the data file was formatted with; a mismatch is
// reported by the server rather than here.
func Connect(clusterID uint64, addresses []string) (*Adapter, error) {
	if len(addresses) == 0 {
		return nil, errors.New("tigerbeetle: at least one address is required")
	}
	client, err := tb.NewClient(tb.ToUint128(clusterID), addresses)
	if err != nil {
		return nil, fmt.Errorf("tigerbeetle: connect to %v: %w", addresses, err)
	}
	return &Adapter{client: client}, nil
}

func (a *Adapter) Close() error {
	a.client.Close()
	return nil
}

// Ping verifies the connection is usable, for the health endpoint.
func (a *Adapter) Ping(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.client.Nop(); err != nil {
		return fmt.Errorf("tigerbeetle: ping: %w", err)
	}
	return nil
}

// --- accounts ---------------------------------------------------------------

func (a *Adapter) EnsureAccounts(ctx context.Context, accounts []ledger.NewAccount) error {
	if len(accounts) == 0 {
		return nil
	}
	for start := 0; start < len(accounts); start += maxBatch {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(start+maxBatch, len(accounts))
		if err := a.ensureBatch(accounts[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func (a *Adapter) ensureBatch(accounts []ledger.NewAccount) error {
	batch := make([]tb.Account, len(accounts))
	for i, acc := range accounts {
		batch[i] = tb.Account{
			ID:          tb.ToUint128(uint64(acc.ID)),
			UserData128: uuidToUint128(acc.Owner),
			Ledger:      ledger.LedgerUSD,
			Code:        uint16(acc.Kind),
			Flags:       accountFlags(acc.Kind),
		}
	}

	results, err := a.client.CreateAccounts(batch)
	if err != nil {
		return fmt.Errorf("tigerbeetle: create accounts: %w", err)
	}
	if len(results) != len(batch) {
		return fmt.Errorf("tigerbeetle: expected %d results, got %d", len(batch), len(results))
	}

	// Results are positional: one per submitted account, in order.
	for i, r := range results {
		switch r.Status {
		case tb.AccountCreated, tb.AccountExists:
			// Both are success. An account that already exists with the same
			// configuration means a previous run got here first, which is
			// exactly what idempotent seeding should produce.
		default:
			return fmt.Errorf("%w: account %d (id %d): %s",
				ledger.ErrAccountConflict, i, accounts[i].ID, r.Status)
		}
	}
	return nil
}

// accountFlags encodes the rule that makes overdrafts impossible.
//
// Customer accounts carry debits_must_not_exceed_credits, so the ledger itself
// rejects any posting that would take the balance negative. The world account
// must be free to run an unbounded debit balance as money enters the bank, so
// it carries no balance-limiting flag.
func accountFlags(kind ledger.AccountKind) uint16 {
	if kind == ledger.KindWorld {
		return tb.AccountFlags{History: true}.ToUint16()
	}
	return tb.AccountFlags{
		DebitsMustNotExceedCredits: true,
		History:                    true,
	}.ToUint16()
}

func (a *Adapter) Balances(ctx context.Context, ids []ledger.AccountID) (map[ledger.AccountID]ledger.Balance, error) {
	out := make(map[ledger.AccountID]ledger.Balance, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	for start := 0; start < len(ids); start += maxBatch {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := min(start+maxBatch, len(ids))

		lookup := make([]tb.Uint128, 0, end-start)
		for _, id := range ids[start:end] {
			lookup = append(lookup, tb.ToUint128(uint64(id)))
		}
		accounts, err := a.client.LookupAccounts(lookup)
		if err != nil {
			return nil, fmt.Errorf("tigerbeetle: lookup accounts: %w", err)
		}
		// Unlike CreateAccounts, LookupAccounts omits accounts that do not
		// exist, so the result is not positional and callers learn about a
		// missing account by its absence from the map.
		for _, acc := range accounts {
			id := ledger.AccountID(u128ToInt64(acc.ID))
			posted := money.Cents(u128ToInt64(acc.CreditsPosted) - u128ToInt64(acc.DebitsPosted))
			held := money.Cents(u128ToInt64(acc.DebitsPending))
			out[id] = ledger.Balance{
				AccountID: id,
				Posted:    posted,
				Held:      held,
				Available: posted - held,
			}
		}
	}
	return out, nil
}

// --- movements --------------------------------------------------------------

func (a *Adapter) Post(ctx context.Context, m ledger.Movement) error {
	if err := m.Validate(); err != nil {
		return err
	}
	amount, err := m.Amount.Uint64()
	if err != nil {
		return err
	}
	return a.submit(ctx, tb.Transfer{
		ID:              uuidToUint128(m.ID),
		DebitAccountID:  tb.ToUint128(uint64(m.From)),
		CreditAccountID: tb.ToUint128(uint64(m.To)),
		Amount:          tb.ToUint128(amount),
		UserData128:     uuidToUint128(m.Actor),
		Ledger:          ledger.LedgerUSD,
		Code:            uint16(m.Kind),
	})
}

// PostMany submits movements in batches of maxBatch.
//
// The per-movement errors are returned rather than raised because that is what a
// bulk import needs: with a batch of sixteen hundred openings, "one of these was
// rejected" is not actionable, and stopping at the first would leave the rest
// unattempted. Results come back positionally, one per event sent, successes
// included — which is what makes lining them back up with the input reliable.
func (a *Adapter) PostMany(ctx context.Context, movements []ledger.Movement) ([]error, error) {
	out := make([]error, len(movements))

	for start := 0; start < len(movements); start += maxBatch {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		end := min(start+maxBatch, len(movements))

		transfers := make([]tb.Transfer, 0, end-start)
		indexes := make([]int, 0, end-start)

		for i := start; i < end; i++ {
			m := movements[i]
			if err := m.Validate(); err != nil {
				out[i] = err
				continue
			}
			amount, err := m.Amount.Uint64()
			if err != nil {
				out[i] = err
				continue
			}
			transfers = append(transfers, tb.Transfer{
				ID:              uuidToUint128(m.ID),
				DebitAccountID:  tb.ToUint128(uint64(m.From)),
				CreditAccountID: tb.ToUint128(uint64(m.To)),
				Amount:          tb.ToUint128(amount),
				UserData128:     uuidToUint128(m.Actor),
				Ledger:          ledger.LedgerUSD,
				Code:            uint16(m.Kind),
			})
			// Locally invalid movements are never sent, so the results cannot be
			// indexed by position in the original slice. This maps them back.
			indexes = append(indexes, i)
		}

		if len(transfers) == 0 {
			continue
		}
		results, err := a.client.CreateTransfers(transfers)
		if err != nil {
			return out, fmt.Errorf("tigerbeetle: create transfers: %w", err)
		}
		if len(results) != len(transfers) {
			return out, fmt.Errorf("tigerbeetle: sent %d transfers, got %d results",
				len(transfers), len(results))
		}
		for j, result := range results {
			out[indexes[j]] = translateTransferStatus(result.Status)
		}
	}
	return out, nil
}

func (a *Adapter) Hold(ctx context.Context, m ledger.Movement, ttl time.Duration) error {
	if err := m.Validate(); err != nil {
		return err
	}
	amount, err := m.Amount.Uint64()
	if err != nil {
		return err
	}
	seconds := uint32(ttl.Seconds())
	if seconds == 0 {
		return errors.New("tigerbeetle: hold ttl must be at least one second")
	}
	return a.submit(ctx, tb.Transfer{
		ID:              uuidToUint128(m.ID),
		DebitAccountID:  tb.ToUint128(uint64(m.From)),
		CreditAccountID: tb.ToUint128(uint64(m.To)),
		Amount:          tb.ToUint128(amount),
		UserData128:     uuidToUint128(m.Actor),
		Ledger:          ledger.LedgerUSD,
		Code:            uint16(m.Kind),
		// The timeout is the safety net behind the assistant's confirmation
		// prompt: if the user never answers, the ledger releases the funds on
		// its own and no cleanup job is needed.
		Timeout: seconds,
		Flags:   tb.TransferFlags{Pending: true}.ToUint16(),
	})
}

func (a *Adapter) Settle(ctx context.Context, holdID, entryID uuid.UUID, amount money.Cents, kind ledger.MovementKind) error {
	return a.resolve(ctx, holdID, entryID, amount, kind,
		tb.TransferFlags{PostPendingTransfer: true}.ToUint16())
}

func (a *Adapter) Void(ctx context.Context, holdID, entryID uuid.UUID, amount money.Cents, kind ledger.MovementKind) error {
	return a.resolve(ctx, holdID, entryID, amount, kind,
		tb.TransferFlags{VoidPendingTransfer: true}.ToUint16())
}

func (a *Adapter) resolve(ctx context.Context, holdID, entryID uuid.UUID, amount money.Cents, kind ledger.MovementKind, flags uint16) error {
	if holdID == uuid.Nil || entryID == uuid.Nil {
		return errors.New("tigerbeetle: hold id and entry id are required")
	}
	cents, err := amount.Uint64()
	if err != nil {
		return err
	}
	// Resolving a hold names the pending transfer instead of the accounts:
	// TigerBeetle takes the debit and credit sides from the original.
	return a.submit(ctx, tb.Transfer{
		ID:        uuidToUint128(entryID),
		PendingID: uuidToUint128(holdID),
		Amount:    tb.ToUint128(cents),
		Ledger:    ledger.LedgerUSD,
		Code:      uint16(kind),
		Flags:     flags,
	})
}

func (a *Adapter) submit(ctx context.Context, t tb.Transfer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	results, err := a.client.CreateTransfers([]tb.Transfer{t})
	if err != nil {
		return fmt.Errorf("tigerbeetle: create transfer: %w", err)
	}
	if len(results) != 1 {
		return fmt.Errorf("tigerbeetle: expected 1 result, got %d", len(results))
	}
	return translateTransferStatus(results[0].Status)
}

// translateTransferStatus converts a backend status into a domain error, so
// callers never match on TigerBeetle constants and the HTTP layer has one
// place to decide what each failure means to a user.
func translateTransferStatus(status tb.CreateTransferStatus) error {
	switch status {
	case tb.TransferCreated:
		return nil

	// The money is already where it should be. An earlier attempt succeeded
	// and the caller simply did not find out, so this is success.
	case tb.TransferExists:
		return ledger.ErrAlreadyApplied
	case tb.TransferIDAlreadyFailed:
		return ledger.ErrPreviouslyFailed

	case tb.TransferExceedsCredits, tb.TransferExceedsDebits:
		return ledger.ErrInsufficientFunds
	case tb.TransferDebitAccountNotFound:
		return ledger.ErrSourceNotFound
	case tb.TransferCreditAccountNotFound:
		return ledger.ErrDestinationNotFound
	case tb.TransferAccountsMustBeDifferent:
		return ledger.ErrSameAccount

	case tb.TransferPendingTransferNotFound:
		return ledger.ErrHoldNotFound
	case tb.TransferPendingTransferAlreadyPosted:
		return ledger.ErrHoldAlreadySettled
	case tb.TransferPendingTransferAlreadyVoided:
		return ledger.ErrHoldAlreadyVoided
	case tb.TransferPendingTransferExpired:
		return ledger.ErrHoldExpired

	default:
		// Anything else is a programming error in this adapter rather than
		// something a user did, so it surfaces with the raw status attached.
		return fmt.Errorf("tigerbeetle: transfer rejected: %s", status)
	}
}

// --- history ----------------------------------------------------------------

func (a *Adapter) Entries(ctx context.Context, id ledger.AccountID, q ledger.EntryQuery) ([]ledger.Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit == 0 || limit > maxBatch {
		limit = 100
	}
	transfers, err := a.client.GetAccountTransfers(tb.AccountFilter{
		AccountID: tb.ToUint128(uint64(id)),
		Limit:     limit,
		Flags: tb.AccountFilterFlags{
			Debits:   true,
			Credits:  true,
			Reversed: q.Newest,
		}.ToUint32(),
	})
	if err != nil {
		return nil, fmt.Errorf("tigerbeetle: account transfers: %w", err)
	}
	return toEntries(transfers), nil
}

func (a *Adapter) Lookup(ctx context.Context, ids []uuid.UUID) ([]ledger.Entry, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []ledger.Entry
	for start := 0; start < len(ids); start += maxBatch {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := min(start+maxBatch, len(ids))

		lookup := make([]tb.Uint128, 0, end-start)
		for _, id := range ids[start:end] {
			lookup = append(lookup, uuidToUint128(id))
		}
		transfers, err := a.client.LookupTransfers(lookup)
		if err != nil {
			return nil, fmt.Errorf("tigerbeetle: lookup transfers: %w", err)
		}
		out = append(out, toEntries(transfers)...)
	}
	return out, nil
}

func toEntries(transfers []tb.Transfer) []ledger.Entry {
	out := make([]ledger.Entry, 0, len(transfers))
	for _, t := range transfers {
		flags := t.TransferFlags()
		out = append(out, ledger.Entry{
			ID:      uint128ToUUID(t.ID),
			From:    ledger.AccountID(u128ToInt64(t.DebitAccountID)),
			To:      ledger.AccountID(u128ToInt64(t.CreditAccountID)),
			Amount:  money.Cents(u128ToInt64(t.Amount)),
			Kind:    ledger.MovementKind(t.Code),
			At:      time.Unix(0, int64(t.Timestamp)).UTC(),
			Pending: flags.Pending,
			Settles: flags.PostPendingTransfer,
			Voids:   flags.VoidPendingTransfer,
			HoldID:  uint128ToUUID(t.PendingID),
		})
	}
	return out
}

// --- conversions ------------------------------------------------------------

// uuidToUint128 reinterprets a UUID's 16 bytes as a u128. A UUID is exactly
// 128 bits, so a transaction row's primary key can serve directly as the
// ledger's transfer id with no mapping table and no ambiguity.
func uuidToUint128(id uuid.UUID) tb.Uint128 {
	return tb.BytesToUint128([16]byte(id))
}

func uint128ToUUID(v tb.Uint128) uuid.UUID {
	return uuid.UUID(v.Bytes())
}

// u128ToInt64 narrows a ledger value to int64.
//
// Every amount in this system is cents on a single USD ledger, so values stay
// many orders of magnitude below the u128 range. Saturating rather than
// wrapping means an impossible value shows up as an obviously wrong number
// instead of a silently negative balance.
func u128ToInt64(v tb.Uint128) int64 {
	b := v.BigInt()
	if !b.IsInt64() {
		return maxInt64
	}
	return b.Int64()
}

const maxInt64 = int64(^uint64(0) >> 1)
