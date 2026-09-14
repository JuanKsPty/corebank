package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// IBKRLink is one corebank account's connection to an IBKR Flex Query.
type IBKRLink struct {
	ID                  uuid.UUID
	AccountID           uuid.UUID
	IBKRAccountID       string
	FlexQueryID         string
	FlexTokenCiphertext []byte
	LastSyncedAt        *time.Time
	LastSyncStatus      string
	LastSyncError       string
}

// IBKRLinkOwned is a link together with the account and owner it belongs to,
// for a job that walks every configured link without being told which
// account to sync.
type IBKRLinkOwned struct {
	IBKRLink
	AccountNumber string
	UserID        uuid.UUID
}

// UpsertIBKRLink creates or replaces a corebank account's IBKR link. Replacing
// rather than requiring a separate update path is deliberate: relinking with a
// new token is indistinguishable from linking for the first time as far as
// this table is concerned, and the sync history in last_synced_at/status is
// intentionally reset with it, since a new token is a new connection.
func (q *Queries) UpsertIBKRLink(ctx context.Context, l IBKRLink) (IBKRLink, error) {
	const query = `
		INSERT INTO ibkr_links (id, account_id, ibkr_account_id, flex_query_id, flex_token_ciphertext)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (account_id) DO UPDATE SET
			ibkr_account_id       = EXCLUDED.ibkr_account_id,
			flex_query_id         = EXCLUDED.flex_query_id,
			flex_token_ciphertext = EXCLUDED.flex_token_ciphertext,
			last_synced_at        = NULL,
			last_sync_status      = 'never',
			last_sync_error       = NULL,
			updated_at            = now()
		RETURNING id`

	err := q.q.QueryRow(ctx, query, l.ID, l.AccountID, l.IBKRAccountID, l.FlexQueryID, l.FlexTokenCiphertext).
		Scan(&l.ID)
	if err != nil {
		return IBKRLink{}, wrap("store.UpsertIBKRLink", err)
	}
	return l, nil
}

// IBKRLinkByAccountID looks up the link for one account, if any.
func (q *Queries) IBKRLinkByAccountID(ctx context.Context, accountID uuid.UUID) (IBKRLink, error) {
	const query = `
		SELECT id, account_id, ibkr_account_id, flex_query_id, flex_token_ciphertext,
		       last_synced_at, last_sync_status, COALESCE(last_sync_error, '')
		FROM ibkr_links WHERE account_id = $1`

	l, err := scanIBKRLink(q.q.QueryRow(ctx, query, accountID))
	if err != nil {
		return IBKRLink{}, wrap("store.IBKRLinkByAccountID", err)
	}
	return l, nil
}

// AllIBKRLinks lists every configured link together with the account and
// owner it belongs to, for a sync job that has no account number of its own
// to start from.
func (q *Queries) AllIBKRLinks(ctx context.Context) ([]IBKRLinkOwned, error) {
	const query = `
		SELECT l.id, l.account_id, l.ibkr_account_id, l.flex_query_id, l.flex_token_ciphertext,
		       l.last_synced_at, l.last_sync_status, COALESCE(l.last_sync_error, ''),
		       a.account_number, a.user_id
		FROM ibkr_links l JOIN accounts a ON a.id = l.account_id
		ORDER BY a.account_number`

	rows, err := q.q.Query(ctx, query)
	if err != nil {
		return nil, wrap("store.AllIBKRLinks", err)
	}
	defer rows.Close()

	var out []IBKRLinkOwned
	for rows.Next() {
		var o IBKRLinkOwned
		var lastSyncedAt *time.Time
		var lastSyncError string
		if err := rows.Scan(&o.ID, &o.AccountID, &o.IBKRAccountID, &o.FlexQueryID, &o.FlexTokenCiphertext,
			&lastSyncedAt, &o.LastSyncStatus, &lastSyncError, &o.AccountNumber, &o.UserID); err != nil {
			return nil, wrap("store.AllIBKRLinks", err)
		}
		o.LastSyncedAt = lastSyncedAt
		o.LastSyncError = lastSyncError
		out = append(out, o)
	}
	return out, wrap("store.AllIBKRLinks", rows.Err())
}

// RecordSyncOutcome updates a link's last-sync bookkeeping. status is "ok" or
// "error"; syncErr is ignored (stored as NULL) when status is "ok".
func (q *Queries) RecordSyncOutcome(ctx context.Context, accountID uuid.UUID, status string, syncErr string, at time.Time) error {
	const query = `
		UPDATE ibkr_links
		SET last_synced_at = $2, last_sync_status = $3, last_sync_error = $4, updated_at = now()
		WHERE account_id = $1`

	errText := &syncErr
	if status == "ok" {
		errText = nil
	}
	tag, err := q.q.Exec(ctx, query, accountID, at, status, errText)
	if err != nil {
		return wrap("store.RecordSyncOutcome", err)
	}
	if tag.RowsAffected() == 0 {
		return wrap("store.RecordSyncOutcome", pgx.ErrNoRows)
	}
	return nil
}

func scanIBKRLink(s scanner) (IBKRLink, error) {
	var l IBKRLink
	if err := s.Scan(&l.ID, &l.AccountID, &l.IBKRAccountID, &l.FlexQueryID, &l.FlexTokenCiphertext,
		&l.LastSyncedAt, &l.LastSyncStatus, &l.LastSyncError); err != nil {
		return IBKRLink{}, err
	}
	return l, nil
}

// --- instruments -------------------------------------------------------------

// UpsertInstrument returns the id of the (symbol, currency) instrument,
// creating it if this is the first time any portfolio has referenced it.
// assetClass is updated on an existing row too, since the first sync to see
// a symbol may have caught it in a statement section that did not carry one.
func (q *Queries) UpsertInstrument(ctx context.Context, symbol, currency, assetClass string) (uuid.UUID, error) {
	const query = `
		INSERT INTO instruments (id, symbol, currency, asset_class)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (symbol, currency) DO UPDATE SET
			asset_class = CASE WHEN EXCLUDED.asset_class <> '' THEN EXCLUDED.asset_class ELSE instruments.asset_class END
		RETURNING id`

	var id uuid.UUID
	if err := q.q.QueryRow(ctx, query, uuid.New(), symbol, currency, assetClass).Scan(&id); err != nil {
		return uuid.Nil, wrap("store.UpsertInstrument", err)
	}
	return id, nil
}

// --- positions ----------------------------------------------------------------

// InvestmentPosition is one holding as of the last sync.
type InvestmentPosition struct {
	AccountID      uuid.UUID
	InstrumentID   uuid.UUID
	Symbol         string
	AssetClass     string
	Quantity       float64
	MarkPriceCents *int64
	MarketValue    *int64
	CostBasis      *int64
	AsOf           time.Time
}

// DeletePositionsByAccount clears every position corebank has recorded for an
// account. Positions are a snapshot, not a history, and every sync replaces
// the snapshot wholesale — delete then insert, in one transaction — rather
// than upserting per symbol, which is what a fully sold-down holding needs to
// disappear instead of being left showing a quantity that stopped being true.
func (q *Queries) DeletePositionsByAccount(ctx context.Context, accountID uuid.UUID) error {
	const query = `DELETE FROM investment_positions WHERE account_id = $1`
	_, err := q.q.Exec(ctx, query, accountID)
	return wrap("store.DeletePositionsByAccount", err)
}

// InsertPosition adds one row to an account's position snapshot. Callers
// clear the account's positions first (DeletePositionsByAccount, in the same
// transaction), so this never needs to reconcile against an existing row.
func (q *Queries) InsertPosition(ctx context.Context, p InvestmentPosition) error {
	const query = `
		INSERT INTO investment_positions
			(id, account_id, instrument_id, quantity, mark_price_cents, market_value_cents, cost_basis_cents, as_of)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	_, err := q.q.Exec(ctx, query, uuid.New(), p.AccountID, p.InstrumentID,
		p.Quantity, p.MarkPriceCents, p.MarketValue, p.CostBasis, p.AsOf)
	return wrap("store.InsertPosition", err)
}

// PositionView is a position joined with its instrument, for rendering.
type PositionView struct {
	Symbol         string
	AssetClass     string
	Quantity       float64
	MarkPriceCents *int64
	MarketValue    *int64
	CostBasis      *int64
	AsOf           time.Time
}

// PositionsByAccount lists an account's current holdings, largest first.
func (q *Queries) PositionsByAccount(ctx context.Context, accountID uuid.UUID) ([]PositionView, error) {
	const query = `
		SELECT i.symbol, i.asset_class, p.quantity, p.mark_price_cents, p.market_value_cents,
		       p.cost_basis_cents, p.as_of
		FROM investment_positions p JOIN instruments i ON i.id = p.instrument_id
		WHERE p.account_id = $1
		ORDER BY p.market_value_cents DESC NULLS LAST, i.symbol`

	rows, err := q.q.Query(ctx, query, accountID)
	if err != nil {
		return nil, wrap("store.PositionsByAccount", err)
	}
	defer rows.Close()

	var out []PositionView
	for rows.Next() {
		var v PositionView
		if err := rows.Scan(&v.Symbol, &v.AssetClass, &v.Quantity, &v.MarkPriceCents,
			&v.MarketValue, &v.CostBasis, &v.AsOf); err != nil {
			return nil, wrap("store.PositionsByAccount", err)
		}
		out = append(out, v)
	}
	return out, wrap("store.PositionsByAccount", rows.Err())
}

// --- trades -------------------------------------------------------------------

// InvestmentTrade is one execution.
type InvestmentTrade struct {
	AccountID       uuid.UUID
	InstrumentID    uuid.UUID
	ExternalRef     string
	Side            string
	Quantity        float64
	PriceCents      int64
	CommissionCents int64
	NetCashCents    int64
	TradeDate       time.Time
}

// InsertTradeIfNew records a trade, reporting false rather than an error when
// this exact (account, external_ref) pair was already recorded by an earlier
// sync — a Flex Query re-fetches its whole configured window every time, so
// re-seeing the same trade is the normal case, not a conflict to surface.
func (q *Queries) InsertTradeIfNew(ctx context.Context, t InvestmentTrade) (bool, error) {
	const query = `
		INSERT INTO investment_trades
			(id, account_id, instrument_id, external_ref, side, quantity, price_cents,
			 commission_cents, net_cash_cents, trade_date)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (account_id, external_ref) DO NOTHING`

	tag, err := q.q.Exec(ctx, query, uuid.New(), t.AccountID, t.InstrumentID, t.ExternalRef,
		t.Side, t.Quantity, t.PriceCents, t.CommissionCents, t.NetCashCents, t.TradeDate)
	if err != nil {
		return false, wrap("store.InsertTradeIfNew", err)
	}
	return tag.RowsAffected() > 0, nil
}

// TradeView is a trade joined with its instrument, for rendering.
type TradeView struct {
	Symbol          string
	AssetClass      string
	Side            string
	Quantity        float64
	PriceCents      int64
	CommissionCents int64
	NetCashCents    int64
	TradeDate       time.Time
}

// TradesByAccount lists an account's trades, most recent first, capped at
// limit — a personal account's lifetime trade count is small enough that
// cursor pagination would be solving a problem this table does not have.
func (q *Queries) TradesByAccount(ctx context.Context, accountID uuid.UUID, limit int) ([]TradeView, error) {
	const query = `
		SELECT i.symbol, i.asset_class, t.side, t.quantity, t.price_cents,
		       t.commission_cents, t.net_cash_cents, t.trade_date
		FROM investment_trades t JOIN instruments i ON i.id = t.instrument_id
		WHERE t.account_id = $1
		ORDER BY t.trade_date DESC
		LIMIT $2`

	rows, err := q.q.Query(ctx, query, accountID, limit)
	if err != nil {
		return nil, wrap("store.TradesByAccount", err)
	}
	defer rows.Close()

	var out []TradeView
	for rows.Next() {
		var v TradeView
		if err := rows.Scan(&v.Symbol, &v.AssetClass, &v.Side, &v.Quantity, &v.PriceCents,
			&v.CommissionCents, &v.NetCashCents, &v.TradeDate); err != nil {
			return nil, wrap("store.TradesByAccount", err)
		}
		out = append(out, v)
	}
	return out, wrap("store.TradesByAccount", rows.Err())
}
