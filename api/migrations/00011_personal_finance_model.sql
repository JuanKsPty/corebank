-- The money model, rebuilt around what the owner's banks actually say.
--
-- Balances used to live in a separate ledger, mirrored from imported files
-- through deposits and withdrawals against a synthetic counterparty. That
-- mirror could not hold a card's debt, could not be backdated, invented an
-- opening balance just large enough to stay non-negative, and lost any row it
-- rejected. This replaces it with one row per line a statement printed, signed
-- from the holder's point of view, anchored to the balances the bank itself
-- printed.
--
-- DESTRUCTIVE. Every financial table is dropped, and the data is re-imported
-- from the original statements and a fresh IBKR sync. Kept: users and their
-- sessions and trusted devices, categories, category rules (deduplicated), IBKR
-- links with their encrypted token, and the AI budget. store.Migrate refuses to
-- run this against an existing database unless COREBANK_CONFIRM_WIPE=00011, so
-- it cannot run by accident; the backup taken before it is the only way back.

-- +goose Up

-- 1. IBKR links belong to a user now, and their brokerage account is recreated
--    below with the same id, so the link and its sync button survive.
ALTER TABLE ibkr_links ADD COLUMN user_id UUID REFERENCES users (id) ON DELETE CASCADE;
UPDATE ibkr_links l SET user_id = a.user_id FROM accounts a WHERE a.id = l.account_id;
DELETE FROM ibkr_links WHERE user_id IS NULL;
-- The same IBKR account linked twice by one user would make two brokerage
-- accounts compete for one set of cash rows; the most recently updated wins.
DELETE FROM ibkr_links l USING ibkr_links k
 WHERE k.user_id = l.user_id AND k.ibkr_account_id = l.ibkr_account_id
   AND (k.updated_at, k.id) > (l.updated_at, l.id);
ALTER TABLE ibkr_links ALTER COLUMN user_id SET NOT NULL;
UPDATE ibkr_links SET last_synced_at = NULL, last_sync_status = 'never', last_sync_error = NULL;
CREATE TEMP TABLE kept_links ON COMMIT DROP AS
    SELECT l.account_id, l.user_id, l.ibkr_account_id FROM ibkr_links l;

-- 2. The old money model goes, all of it.
DROP TABLE idempotency_keys, external_transactions, import_batches, external_accounts,
           investment_trades, investment_positions, instruments, seed_state, transactions CASCADE;
DROP TABLE accounts CASCADE;
DROP TYPE tx_status;
DROP TYPE tx_kind;
-- The conversation talks about balances, holds and tools that no longer exist.
DELETE FROM chat_messages;

-- 3. Categories are referenced together with their owner, so no row can point
--    at another user's category.
ALTER TABLE categories ADD CONSTRAINT categories_id_user_key UNIQUE (id, user_id);

-- Rules were re-seeded on every card import; keep the oldest copy of each.
DELETE FROM category_rules r
 WHERE NOT EXISTS (SELECT 1 FROM categories c WHERE c.id = r.category_id AND c.user_id = r.user_id);
DELETE FROM category_rules r USING category_rules k
 WHERE k.user_id = r.user_id
   AND lower(btrim(k.match_text)) = lower(btrim(r.match_text))
   AND (k.created_at, k.id) < (r.created_at, r.id);
ALTER TABLE category_rules
    DROP CONSTRAINT category_rules_category_id_fkey,
    ALTER COLUMN category_id DROP NOT NULL,
    ADD COLUMN set_kind TEXT CHECK (set_kind IN ('transfer')),
    ADD CONSTRAINT category_rules_does_something CHECK (category_id IS NOT NULL OR set_kind IS NOT NULL),
    ADD CONSTRAINT category_rules_category_owner_fk FOREIGN KEY (category_id, user_id)
        REFERENCES categories (id, user_id) ON DELETE CASCADE;
CREATE UNIQUE INDEX category_rules_user_match_key ON category_rules (user_id, lower(btrim(match_text)));

-- 4. One row per real-world account, keyed the way its institution keys it.
CREATE TYPE account_class AS ENUM ('asset', 'liability');
CREATE TABLE accounts (
    id              UUID          PRIMARY KEY,
    user_id         UUID          NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    class           account_class NOT NULL,
    type            TEXT          NOT NULL CHECK (type IN ('checking', 'savings', 'credit_card', 'brokerage')),
    institution     TEXT          NOT NULL CHECK (institution IN ('banco_general', 'bac', 'ibkr')),
    external_number TEXT          NOT NULL CHECK (external_number <> ''),
    currency        CHAR(3)       NOT NULL DEFAULT 'USD' CHECK (currency = 'USD'),
    display_name    TEXT          NOT NULL DEFAULT '',
    alias           TEXT          NOT NULL DEFAULT '' CHECK (char_length(alias) <= 40),
    created_at      TIMESTAMPTZ   NOT NULL DEFAULT now(),
    UNIQUE (user_id, institution, external_number, currency),
    UNIQUE (id, user_id),
    CONSTRAINT accounts_card_is_liability CHECK ((class = 'liability') = (type = 'credit_card'))
);

INSERT INTO accounts (id, user_id, class, type, institution, external_number, display_name)
SELECT account_id, user_id, 'asset', 'brokerage', 'ibkr', ibkr_account_id, 'IBKR ' || ibkr_account_id
  FROM kept_links;
ALTER TABLE ibkr_links
    ADD CONSTRAINT ibkr_links_account_owner_fk FOREIGN KEY (account_id, user_id)
        REFERENCES accounts (id, user_id) ON DELETE CASCADE,
    ADD CONSTRAINT ibkr_links_user_ibkr_account_key UNIQUE (user_id, ibkr_account_id);

-- 5. Every ingestion — a file or an IBKR sync — and what it did.
CREATE TABLE import_runs (
    id              UUID        PRIMARY KEY,
    user_id         UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    account_id      UUID,
    source          TEXT        NOT NULL CHECK (source IN ('bg_account', 'bac_account', 'bg_card', 'ibkr_flex')),
    status          TEXT        NOT NULL CHECK (status IN ('ok', 'unchanged', 'error')),
    filename        TEXT        NOT NULL DEFAULT '',
    content_sha256  TEXT        NOT NULL DEFAULT '',
    period_start    DATE,
    period_end      DATE,
    lines_seen      INT         NOT NULL DEFAULT 0,
    lines_new       INT         NOT NULL DEFAULT 0,
    lines_duplicate INT         NOT NULL DEFAULT 0,
    lines_failed    INT         NOT NULL DEFAULT 0,
    details         JSONB       NOT NULL DEFAULT '{}'::jsonb,
    error           TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (account_id, user_id) REFERENCES accounts (id, user_id) ON DELETE CASCADE
);
CREATE INDEX import_runs_user_idx ON import_runs (user_id, created_at DESC);
CREATE INDEX import_runs_account_idx ON import_runs (account_id, created_at DESC);

-- 6. What one statement said about one account for one period.
CREATE TABLE statements (
    id              UUID        PRIMARY KEY,
    user_id         UUID        NOT NULL,
    account_id      UUID        NOT NULL,
    run_id          UUID        NOT NULL REFERENCES import_runs (id) ON DELETE CASCADE,
    period_start    DATE        NOT NULL,
    period_end      DATE        NOT NULL,
    opening_cents   BIGINT,
    closing_cents   BIGINT,
    available_cents BIGINT,
    held_cents      BIGINT,
    line_count      INT         NOT NULL,
    warnings        JSONB       NOT NULL DEFAULT '[]'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (account_id, user_id) REFERENCES accounts (id, user_id) ON DELETE CASCADE,
    CHECK (period_end >= period_start)
);
CREATE INDEX statements_account_idx ON statements (account_id, period_start);

-- 7. One row per movement a statement or IBKR printed, signed from the
--    holder's view: positive makes them richer. A card charge is negative.
CREATE TYPE entry_kind AS ENUM ('income', 'expense', 'refund', 'fee', 'interest', 'transfer', 'trade');
CREATE TABLE entries (
    id                 UUID        PRIMARY KEY,
    user_id            UUID        NOT NULL,
    account_id         UUID        NOT NULL,
    -- The statement the line was first imported from; later overlapping
    -- files find it by its dedup key instead.
    statement_id       UUID        REFERENCES statements (id) ON DELETE SET NULL,
    run_id             UUID        REFERENCES import_runs (id) ON DELETE SET NULL,
    booked_on          DATE        NOT NULL,
    booked_at          TIMESTAMPTZ,
    posted_on          DATE,
    balance_on         DATE        GENERATED ALWAYS AS (COALESCE(posted_on, booked_on)) STORED,
    -- Order within the day, as the source listed it chronologically.
    seq                INT         NOT NULL DEFAULT 0,
    amount_cents       BIGINT      NOT NULL CHECK (amount_cents <> 0),
    -- kind is the importer's classification; user_kind, when set, overrides
    -- it. Reports read COALESCE(user_kind, kind).
    kind               entry_kind  NOT NULL,
    user_kind          entry_kind,
    description        TEXT        NOT NULL DEFAULT '',
    bank_ref           TEXT        NOT NULL DEFAULT '',
    bank_category      TEXT        NOT NULL DEFAULT '',
    -- The balance the bank printed after this line, when it prints one.
    bank_balance_cents BIGINT,
    category_id        UUID,
    category_source    TEXT        CHECK (category_source IN ('rule', 'user', 'assistant')),
    note               TEXT        NOT NULL DEFAULT '' CHECK (char_length(note) <= 500),
    dedup_key          TEXT        NOT NULL,
    raw                JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (account_id, user_id) REFERENCES accounts (id, user_id) ON DELETE CASCADE,
    FOREIGN KEY (category_id, user_id) REFERENCES categories (id, user_id) ON DELETE SET NULL (category_id),
    UNIQUE (account_id, dedup_key),
    UNIQUE (id, user_id)
);
CREATE INDEX entries_account_order_idx ON entries (account_id, balance_on, booked_at, seq, id);
CREATE INDEX entries_user_booked_idx   ON entries (user_id, booked_on DESC, id DESC);
CREATE INDEX entries_user_category_idx ON entries (user_id, category_id) WHERE category_id IS NOT NULL;

-- What a statement printed cannot be edited: fix the parser and re-import.
-- +goose StatementBegin
CREATE FUNCTION entries_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.amount_cents IS DISTINCT FROM OLD.amount_cents
       OR NEW.booked_on IS DISTINCT FROM OLD.booked_on
       OR NEW.posted_on IS DISTINCT FROM OLD.posted_on
       OR NEW.account_id IS DISTINCT FROM OLD.account_id
       OR NEW.dedup_key IS DISTINCT FROM OLD.dedup_key
       OR NEW.kind IS DISTINCT FROM OLD.kind THEN
        RAISE EXCEPTION 'entries: % came from a document; its amount, dates, account and identity cannot change', OLD.id
            USING ERRCODE = 'check_violation';
    END IF;
    NEW.updated_at := now();
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER entries_guard BEFORE UPDATE ON entries FOR EACH ROW EXECUTE FUNCTION entries_guard();

-- 8. Balances somebody stated: a statement's opening and closing, IBKR's cash
--    report, or the owner reading their bank. Exactly one per account can be
--    pinned as the anchor the opening balance is derived from.
CREATE TABLE balance_checkpoints (
    id                    UUID        PRIMARY KEY,
    user_id               UUID        NOT NULL,
    account_id            UUID        NOT NULL,
    -- The balance at the end of this day...
    as_of                 DATE        NOT NULL,
    -- ...or, for a statement's opening, immediately before this entry — exact
    -- even when a file starts partway through a day.
    before_entry_id       UUID        REFERENCES entries (id) ON DELETE CASCADE,
    balance_cents         BIGINT      NOT NULL,
    source                TEXT        NOT NULL CHECK (source IN ('statement_opening', 'statement_closing', 'broker', 'manual')),
    statement_id          UUID        REFERENCES statements (id) ON DELETE CASCADE,
    run_id                UUID        REFERENCES import_runs (id) ON DELETE CASCADE,
    pinned                BOOLEAN     NOT NULL DEFAULT false,
    due_on                DATE,
    minimum_payment_cents BIGINT,
    note                  TEXT        NOT NULL DEFAULT '' CHECK (char_length(note) <= 200),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (account_id, user_id) REFERENCES accounts (id, user_id) ON DELETE CASCADE
);
CREATE INDEX balance_checkpoints_account_idx ON balance_checkpoints (account_id, as_of);
CREATE UNIQUE INDEX balance_checkpoints_one_pin_idx ON balance_checkpoints (account_id) WHERE pinned;
-- One broker checkpoint per day, replaced by each sync of that day.
CREATE UNIQUE INDEX balance_checkpoints_broker_day_idx ON balance_checkpoints (account_id, as_of) WHERE source = 'broker';

-- 9. Holdings: the latest snapshot per account, replaced by each sync.
CREATE TABLE investment_positions (
    id                 UUID           PRIMARY KEY,
    user_id            UUID           NOT NULL,
    account_id         UUID           NOT NULL,
    symbol             TEXT           NOT NULL,
    currency           CHAR(3)        NOT NULL DEFAULT 'USD',
    asset_class        TEXT           NOT NULL DEFAULT '',
    quantity           NUMERIC(20, 6) NOT NULL,
    mark_price_cents   BIGINT,
    market_value_cents BIGINT,
    cost_basis_cents   BIGINT,
    as_of              DATE           NOT NULL,
    FOREIGN KEY (account_id, user_id) REFERENCES accounts (id, user_id) ON DELETE CASCADE,
    UNIQUE (account_id, symbol, currency)
);

CREATE TABLE investment_trades (
    id               UUID           PRIMARY KEY,
    user_id          UUID           NOT NULL,
    account_id       UUID           NOT NULL,
    external_ref     TEXT           NOT NULL CHECK (external_ref <> ''),
    symbol           TEXT           NOT NULL,
    currency         CHAR(3)        NOT NULL DEFAULT 'USD',
    asset_class      TEXT           NOT NULL DEFAULT '',
    side             TEXT           NOT NULL CHECK (side IN ('buy', 'sell')),
    quantity         NUMERIC(20, 6) NOT NULL,
    price_cents      BIGINT         NOT NULL,
    commission_cents BIGINT         NOT NULL DEFAULT 0,
    net_cash_cents   BIGINT         NOT NULL DEFAULT 0,
    trade_date       DATE           NOT NULL,
    created_at       TIMESTAMPTZ    NOT NULL DEFAULT now(),
    FOREIGN KEY (account_id, user_id) REFERENCES accounts (id, user_id) ON DELETE CASCADE,
    UNIQUE (account_id, external_ref)
);
CREATE INDEX investment_trades_account_idx ON investment_trades (account_id, trade_date DESC);

-- +goose Down
-- There is no way down: the old tables' data is gone. Restore the backup
-- taken before this migration instead.
SELECT 1;
