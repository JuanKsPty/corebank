package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/civil"
	"github.com/JuanKsPty/corebank/api/internal/money"
)

// IBKRLink is a brokerage account's connection to an IBKR Flex Query.
type IBKRLink struct {
	ID                  uuid.UUID
	UserID              uuid.UUID
	AccountID           uuid.UUID
	IBKRAccountID       string
	FlexQueryID         string
	FlexTokenCiphertext []byte
	LastSyncedAt        *time.Time
	LastSyncStatus      string
	LastSyncError       string
}

const linkColumns = `id, user_id, account_id, ibkr_account_id, flex_query_id, flex_token_ciphertext,
	last_synced_at, last_sync_status, COALESCE(last_sync_error, '')`

// UpsertIBKRLink creates or replaces an account's link. A new token is a new
// connection, so the sync history resets with it.
func (q *Queries) UpsertIBKRLink(ctx context.Context, l IBKRLink) error {
	_, err := q.q.Exec(ctx, `
		INSERT INTO ibkr_links (id, user_id, account_id, ibkr_account_id, flex_query_id, flex_token_ciphertext)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (account_id) DO UPDATE SET
			flex_query_id         = EXCLUDED.flex_query_id,
			flex_token_ciphertext = EXCLUDED.flex_token_ciphertext,
			last_synced_at        = NULL,
			last_sync_status      = 'never',
			last_sync_error       = NULL,
			updated_at            = now()`,
		l.ID, l.UserID, l.AccountID, l.IBKRAccountID, l.FlexQueryID, l.FlexTokenCiphertext)
	return wrap("store.UpsertIBKRLink", err)
}

// IBKRLinkByAccount returns one of the user's links.
func (q *Queries) IBKRLinkByAccount(ctx context.Context, userID, accountID uuid.UUID) (IBKRLink, error) {
	l, err := scanLink(q.q.QueryRow(ctx,
		`SELECT `+linkColumns+` FROM ibkr_links WHERE account_id = $1 AND user_id = $2`, accountID, userID))
	return l, wrap("store.IBKRLinkByAccount", err)
}

// IBKRLinksByUser lists the user's links.
func (q *Queries) IBKRLinksByUser(ctx context.Context, userID uuid.UUID) ([]IBKRLink, error) {
	return q.links(ctx, "store.IBKRLinksByUser", `WHERE user_id = $1 ORDER BY created_at`, userID)
}

// AllIBKRLinks lists every link, for the scheduled sync.
func (q *Queries) AllIBKRLinks(ctx context.Context) ([]IBKRLink, error) {
	return q.links(ctx, "store.AllIBKRLinks", `ORDER BY user_id, created_at`)
}

func (q *Queries) links(ctx context.Context, op, where string, args ...any) ([]IBKRLink, error) {
	rows, err := q.q.Query(ctx, `SELECT `+linkColumns+` FROM ibkr_links `+where, args...)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()
	var out []IBKRLink
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, wrap(op, err)
		}
		out = append(out, l)
	}
	return out, wrap(op, rows.Err())
}

// RecordSyncOutcome updates a link's last-sync bookkeeping.
func (q *Queries) RecordSyncOutcome(ctx context.Context, accountID uuid.UUID, status, syncErr string, at time.Time) error {
	var errText *string
	if status != "ok" {
		errText = &syncErr
	}
	return q.execOne(ctx, "store.RecordSyncOutcome", `
		UPDATE ibkr_links SET last_synced_at = $2, last_sync_status = $3, last_sync_error = $4, updated_at = now()
		WHERE account_id = $1`, accountID, at, status, errText)
}

func scanLink(s scanner) (IBKRLink, error) {
	var l IBKRLink
	err := s.Scan(&l.ID, &l.UserID, &l.AccountID, &l.IBKRAccountID, &l.FlexQueryID, &l.FlexTokenCiphertext,
		&l.LastSyncedAt, &l.LastSyncStatus, &l.LastSyncError)
	return l, err
}

// Position is one holding in a brokerage account's latest snapshot.
type Position struct {
	Symbol      string
	Currency    string
	AssetClass  string
	Quantity    float64
	MarkPrice   *money.Cents
	MarketValue *money.Cents
	CostBasis   *money.Cents
	AsOf        civil.Date
}

// ReplacePositions swaps an account's holdings snapshot for a new one.
func (q *Queries) ReplacePositions(ctx context.Context, userID, accountID uuid.UUID, positions []Position) error {
	if _, err := q.q.Exec(ctx, `DELETE FROM investment_positions WHERE account_id = $1 AND user_id = $2`,
		accountID, userID); err != nil {
		return wrap("store.ReplacePositions", err)
	}
	for _, p := range positions {
		if _, err := q.q.Exec(ctx, `
			INSERT INTO investment_positions (id, user_id, account_id, symbol, currency, asset_class, quantity,
			                                  mark_price_cents, market_value_cents, cost_basis_cents, as_of)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			uuid.New(), userID, accountID, p.Symbol, p.Currency, p.AssetClass, p.Quantity,
			centsArg(p.MarkPrice), centsArg(p.MarketValue), centsArg(p.CostBasis), p.AsOf); err != nil {
			return wrap("store.ReplacePositions", err)
		}
	}
	return nil
}

// PositionsByAccount returns an account's latest snapshot, largest first.
func (q *Queries) PositionsByAccount(ctx context.Context, userID, accountID uuid.UUID) ([]Position, error) {
	return q.positions(ctx, "store.PositionsByAccount", `WHERE user_id = $1 AND account_id = $2`, userID, accountID)
}

// HoldingsByUser totals the market value of each of the user's brokerage
// accounts' snapshots.
func (q *Queries) HoldingsByUser(ctx context.Context, userID uuid.UUID) (map[uuid.UUID]money.Cents, error) {
	rows, err := q.q.Query(ctx, `SELECT account_id, COALESCE(SUM(market_value_cents), 0)
		FROM investment_positions WHERE user_id = $1 GROUP BY account_id`, userID)
	if err != nil {
		return nil, wrap("store.HoldingsByUser", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]money.Cents{}
	for rows.Next() {
		var id uuid.UUID
		var v int64
		if err := rows.Scan(&id, &v); err != nil {
			return nil, wrap("store.HoldingsByUser", err)
		}
		out[id] = money.Cents(v)
	}
	return out, wrap("store.HoldingsByUser", rows.Err())
}

func (q *Queries) positions(ctx context.Context, op, where string, args ...any) ([]Position, error) {
	rows, err := q.q.Query(ctx, `
		SELECT symbol, currency, asset_class, quantity::float8, mark_price_cents, market_value_cents,
		       cost_basis_cents, as_of
		FROM investment_positions `+where+` ORDER BY market_value_cents DESC NULLS LAST, symbol`, args...)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()
	var out []Position
	for rows.Next() {
		var p Position
		var mark, value, cost *int64
		if err := rows.Scan(&p.Symbol, &p.Currency, &p.AssetClass, &p.Quantity, &mark, &value, &cost, &p.AsOf); err != nil {
			return nil, wrap(op, err)
		}
		p.MarkPrice, p.MarketValue, p.CostBasis = centsPtr(mark), centsPtr(value), centsPtr(cost)
		out = append(out, p)
	}
	return out, wrap(op, rows.Err())
}

// Trade is one IBKR execution.
type Trade struct {
	ExternalRef string
	Symbol      string
	Currency    string
	AssetClass  string
	Side        string
	Quantity    float64
	Price       money.Cents
	Commission  money.Cents
	NetCash     money.Cents
	TradeDate   civil.Date
}

// InsertTradeIfNew stores a trade unless the account already holds it.
func (q *Queries) InsertTradeIfNew(ctx context.Context, userID, accountID uuid.UUID, t Trade) (bool, error) {
	tag, err := q.q.Exec(ctx, `
		INSERT INTO investment_trades (id, user_id, account_id, external_ref, symbol, currency, asset_class, side,
		                               quantity, price_cents, commission_cents, net_cash_cents, trade_date)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (account_id, external_ref) DO NOTHING`,
		uuid.New(), userID, accountID, t.ExternalRef, t.Symbol, t.Currency, t.AssetClass, t.Side, t.Quantity,
		int64(t.Price), int64(t.Commission), int64(t.NetCash), t.TradeDate)
	if err != nil {
		return false, wrap("store.InsertTradeIfNew", err)
	}
	return tag.RowsAffected() == 1, nil
}

// TradesByAccount lists an account's trades, newest first.
func (q *Queries) TradesByAccount(ctx context.Context, userID, accountID uuid.UUID, limit int) ([]Trade, error) {
	rows, err := q.q.Query(ctx, `
		SELECT external_ref, symbol, currency, asset_class, side, quantity::float8, price_cents,
		       commission_cents, net_cash_cents, trade_date
		FROM investment_trades WHERE user_id = $1 AND account_id = $2
		ORDER BY trade_date DESC, created_at DESC LIMIT $3`, userID, accountID, limit)
	if err != nil {
		return nil, wrap("store.TradesByAccount", err)
	}
	defer rows.Close()
	var out []Trade
	for rows.Next() {
		var t Trade
		var price, commission, net int64
		if err := rows.Scan(&t.ExternalRef, &t.Symbol, &t.Currency, &t.AssetClass, &t.Side, &t.Quantity,
			&price, &commission, &net, &t.TradeDate); err != nil {
			return nil, wrap("store.TradesByAccount", err)
		}
		t.Price, t.Commission, t.NetCash = money.Cents(price), money.Cents(commission), money.Cents(net)
		out = append(out, t)
	}
	return out, wrap("store.TradesByAccount", rows.Err())
}
