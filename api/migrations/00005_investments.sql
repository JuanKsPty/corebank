-- Investments: a real portfolio backed by Interactive Brokers, replacing the
-- purely cosmetic "investment" account type with actual holdings and cash
-- flows synced from a Flex Query report.
--
-- The same rule 00004_categories.sql followed for metadata applies here to
-- money, in reverse: where a category is data with no bearing on the ledger,
-- an IBKR cash movement (a deposit, a dividend, a fee, a trade's net
-- settlement) *is* real money and becomes a real transactions/ledger row,
-- through the existing deposit/withdraw path — see internal/investments and
-- internal/transactions. Positions and trades, by contrast, describe shares
-- of an instrument, not dollars this system can hold or move; they live only
-- here, refreshed by each sync, and are never posted to TigerBeetle.

-- +goose Up

-- One IBKR account linked per corebank account. The token is the Flex Web
-- Service token, encrypted with the key in IBKR_TOKEN_ENCRYPTION_KEY before
-- it ever reaches this column — see internal/investments/crypto.go. Losing
-- that key makes every stored token unusable, not merely unreadable; there is
-- nothing this schema can do to soften that, so the application refuses to
-- store one when the key is unset rather than storing a token nothing can
-- later decrypt.
CREATE TABLE ibkr_links (
    id                    UUID        PRIMARY KEY,
    account_id            UUID        NOT NULL UNIQUE REFERENCES accounts (id) ON DELETE CASCADE,
    ibkr_account_id       TEXT        NOT NULL,
    flex_query_id         TEXT        NOT NULL,
    flex_token_ciphertext BYTEA       NOT NULL,
    last_synced_at        TIMESTAMPTZ,
    last_sync_status      TEXT        NOT NULL DEFAULT 'never' CHECK (last_sync_status IN ('never', 'ok', 'error')),
    last_sync_error       TEXT,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per (symbol, currency) across every customer, not per account: the
-- same instrument in two portfolios is the same instrument, and pricing or
-- naming it twice would only let the two copies drift.
CREATE TABLE instruments (
    id          UUID    PRIMARY KEY,
    symbol      TEXT    NOT NULL,
    currency    CHAR(3) NOT NULL DEFAULT 'USD',
    asset_class TEXT    NOT NULL DEFAULT '',
    UNIQUE (symbol, currency)
);

-- A snapshot, replaced wholesale by every sync — hence the plain UNIQUE on
-- (account_id, instrument_id) with an upsert, rather than a history of past
-- valuations nobody asked for.
CREATE TABLE investment_positions (
    id                 UUID        PRIMARY KEY,
    account_id         UUID        NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    instrument_id      UUID        NOT NULL REFERENCES instruments (id) ON DELETE RESTRICT,
    quantity           NUMERIC(20, 6) NOT NULL,
    mark_price_cents   BIGINT,
    market_value_cents BIGINT,
    cost_basis_cents   BIGINT,
    as_of              TIMESTAMPTZ NOT NULL,
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (account_id, instrument_id)
);

-- Unlike positions, trades accumulate: every sync re-fetches the query's
-- whole configured window, and external_ref (IBKR's tradeID) is what makes
-- inserting the same trade twice a no-op instead of a duplicate row.
CREATE TABLE investment_trades (
    id               UUID        PRIMARY KEY,
    account_id       UUID        NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    instrument_id    UUID        NOT NULL REFERENCES instruments (id) ON DELETE RESTRICT,
    external_ref     TEXT        NOT NULL,
    side             TEXT        NOT NULL CHECK (side IN ('buy', 'sell')),
    quantity         NUMERIC(20, 6) NOT NULL,
    price_cents      BIGINT      NOT NULL,
    commission_cents BIGINT      NOT NULL DEFAULT 0,
    net_cash_cents   BIGINT      NOT NULL DEFAULT 0,
    trade_date       TIMESTAMPTZ NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (account_id, external_ref)
);

CREATE INDEX investment_trades_account_idx ON investment_trades (account_id, trade_date DESC);

-- A cash movement synced from IBKR is posted through the ordinary
-- deposit/withdraw path (internal/transactions), which is what makes this
-- migration's only change to `transactions` a new allowed origin — dedup for
-- these rides on the idempotency_keys table every other movement already
-- uses, keyed as "ibkr_cash:<transactionID>", rather than a bespoke table.
ALTER TABLE transactions DROP CONSTRAINT transactions_origin_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_origin_check
    CHECK (origin IN ('api', 'chat', 'ibkr_sync'));

-- +goose Down
ALTER TABLE transactions DROP CONSTRAINT transactions_origin_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_origin_check
    CHECK (origin IN ('api', 'chat'));

DROP TABLE IF EXISTS investment_trades;
DROP TABLE IF EXISTS investment_positions;
DROP TABLE IF EXISTS instruments;
DROP TABLE IF EXISTS ibkr_links;
