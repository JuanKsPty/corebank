-- Initial schema.
--
-- PostgreSQL holds identity, credentials and the descriptive metadata that a
-- financial ledger cannot: e-mail addresses, account numbers, free-text
-- descriptions, statuses. It deliberately has NO balance column anywhere —
-- balances are derived from TigerBeetle, which is the single source of truth
-- for money. Two stores can never disagree about how much money exists if only
-- one of them is allowed to answer the question.

-- +goose Up

-- Case-insensitive e-mail comparison at the database level, so "Ana@x.com" and
-- "ana@x.com" cannot both be registered no matter which code path inserts them.
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE users (
    id            UUID        PRIMARY KEY,
    email         CITEXT      NOT NULL UNIQUE,
    password_hash TEXT        NOT NULL,
    full_name     TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- A customer account. `ledger_id` is the same account's identity inside
-- TigerBeetle, derived from account_number by removing the separators
-- ("4001-6588-5247-0001" -> 4001658852470001). Deriving rather than generating
-- it is what makes seeding and retries idempotent, and it always fits in 64
-- bits, so BIGINT is exact here — no NUMERIC needed.
CREATE TABLE accounts (
    id             UUID        PRIMARY KEY,
    user_id        UUID        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    account_number TEXT        NOT NULL UNIQUE,
    ledger_id      BIGINT      NOT NULL UNIQUE CHECK (ledger_id > 1),
    account_type   TEXT        NOT NULL CHECK (account_type IN ('savings', 'checking', 'investment')),
    currency       CHAR(3)     NOT NULL DEFAULT 'USD',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX accounts_user_id_idx ON accounts (user_id);

CREATE TYPE tx_kind AS ENUM ('deposit', 'withdrawal', 'transfer', 'internal_transfer');

-- pending   funds are reserved in the ledger, awaiting the user's confirmation
-- completed the money moved
-- failed    the ledger rejected it (see failure_code); no money moved
-- voided    the user cancelled a pending movement; the reservation was released
-- expired   nobody answered and the ledger released the reservation on timeout
CREATE TYPE tx_status AS ENUM ('pending', 'completed', 'failed', 'voided', 'expired');

-- A movement's metadata. `id` is reused verbatim as the TigerBeetle transfer id
-- (a UUID is exactly the 128 bits TigerBeetle wants), which is what makes the
-- dual write safe: replaying a movement after a crash is reported by the ledger
-- as "already exists" instead of moving the money a second time.
CREATE TABLE transactions (
    id              UUID        PRIMARY KEY,
    kind            tx_kind     NOT NULL,
    status          tx_status   NOT NULL,
    amount_cents    BIGINT      NOT NULL CHECK (amount_cents > 0),
    currency        CHAR(3)     NOT NULL DEFAULT 'USD',

    -- Account numbers rather than foreign keys: the seed data references
    -- counterparties outside this bank, carried as the sentinel 'EXTERNAL'.
    from_account    TEXT,
    to_account      TEXT,

    description     TEXT        NOT NULL DEFAULT '',

    -- seed rows are read-only audit history; live rows have a matching ledger
    -- transfer. See the README for why the imported history is not replayed
    -- into TigerBeetle.
    source          TEXT        NOT NULL DEFAULT 'live' CHECK (source IN ('seed', 'live')),
    origin          TEXT        NOT NULL DEFAULT 'api' CHECK (origin IN ('api', 'chat')),

    initiated_by    UUID        REFERENCES users (id) ON DELETE SET NULL,

    -- Two-phase confirmation. hold_id is the pending ledger transfer that
    -- reserves the funds; `id` above is reserved for the transfer that will
    -- settle or void it. Both ids are minted up front so a crash mid-flight
    -- leaves an unambiguous row to reconcile.
    hold_id         UUID,
    hold_expires_at TIMESTAMPTZ,

    -- The ledger's rejection reason when status = 'failed', kept verbatim so a
    -- failure can be diagnosed without correlating logs.
    failure_code    TEXT,

    occurred_at     TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT transactions_endpoints_differ CHECK (from_account IS DISTINCT FROM to_account),
    CONSTRAINT transactions_hold_consistent  CHECK ((hold_id IS NULL) = (hold_expires_at IS NULL))
);

-- History is always read newest-first for one account at a time.
CREATE INDEX transactions_from_account_idx ON transactions (from_account, occurred_at DESC);
CREATE INDEX transactions_to_account_idx   ON transactions (to_account, occurred_at DESC);

-- The reconciliation sweeper's query: unresolved movements, oldest first.
CREATE INDEX transactions_pending_idx ON transactions (created_at) WHERE status = 'pending';

CREATE TABLE refresh_tokens (
    -- Only the hash is stored. A database dump therefore cannot be replayed as
    -- a set of live sessions.
    token_hash TEXT        PRIMARY KEY,
    user_id    UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    issued_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ
);

CREATE INDEX refresh_tokens_user_id_idx ON refresh_tokens (user_id);

CREATE TABLE chat_messages (
    id         BIGSERIAL   PRIMARY KEY,
    user_id    UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role       TEXT        NOT NULL CHECK (role IN ('user', 'assistant', 'tool')),
    -- JSONB rather than TEXT: an assistant turn carries tool calls and results
    -- alongside its prose, and the shape differs per role.
    content    JSONB       NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX chat_messages_user_id_idx ON chat_messages (user_id, id);

-- Idempotency keys are scoped per user: two customers independently choosing
-- the same key must not collide.
CREATE TABLE idempotency_keys (
    user_id        UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    key            TEXT        NOT NULL,
    transaction_id UUID        NOT NULL REFERENCES transactions (id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, key)
);

-- Single-row table guarding the seeder, so restarting the stack does not
-- re-import the dataset.
CREATE TABLE seed_state (
    id        INT         PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    seeded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    users     INT         NOT NULL,
    accounts  INT         NOT NULL,
    movements INT         NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS seed_state;
DROP TABLE IF EXISTS idempotency_keys;
DROP TABLE IF EXISTS chat_messages;
DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS transactions;
DROP TYPE  IF EXISTS tx_status;
DROP TYPE  IF EXISTS tx_kind;
DROP TABLE IF EXISTS accounts;
DROP TABLE IF EXISTS users;
