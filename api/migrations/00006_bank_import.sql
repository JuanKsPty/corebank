-- Bank statement import: a bank account corebank has never verified, kept
-- explicitly separate from the accounts the ledger is authoritative for.
--
-- An external account's "balance" is only ever a sum over
-- external_transactions.amount_cents, shown labelled as declared by the
-- import, not verified by corebank — never blended into a TigerBeetle-
-- derived total. See internal/bankimport's package doc.

-- +goose Up

CREATE TABLE external_accounts (
    id             UUID        PRIMARY KEY,
    user_id        UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    institution    TEXT        NOT NULL CHECK (institution IN ('banco_general', 'bac')),
    account_number TEXT        NOT NULL,
    display_name   TEXT        NOT NULL DEFAULT '',
    currency       CHAR(3)     NOT NULL DEFAULT 'USD',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, institution, account_number)
);

CREATE TABLE import_batches (
    id                  UUID        PRIMARY KEY,
    external_account_id UUID        NOT NULL REFERENCES external_accounts (id) ON DELETE CASCADE,
    filename            TEXT        NOT NULL DEFAULT '',
    format              TEXT        NOT NULL,
    row_count           INT         NOT NULL DEFAULT 0,
    imported_count      INT         NOT NULL DEFAULT 0,
    duplicate_count     INT         NOT NULL DEFAULT 0,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX import_batches_account_idx ON import_batches (external_account_id, created_at DESC);

-- A user's own "if this text appears, use this category" rules for
-- auto-categorising imported movements. Seeded with defaults when a Banco
-- General credit card account is first linked (see bankimport.categorize.go)
-- and otherwise built up by hand.
CREATE TABLE category_rules (
    id          UUID        PRIMARY KEY,
    user_id     UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    match_text  TEXT        NOT NULL,
    category_id UUID        NOT NULL REFERENCES categories (id) ON DELETE CASCADE,
    priority    INT         NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX category_rules_user_id_idx ON category_rules (user_id);

-- dedup_key is a SHA-256 hex digest combining every signal a row has,
-- including the bank's own reference when there is one but never trusting
-- it alone — see bankimport.dedupKey's doc for the real Banco General
-- statement that showed why. raw keeps the original row exactly as the bank
-- sent it, independent of how this schema chose to interpret it.
CREATE TABLE external_transactions (
    id                  UUID        PRIMARY KEY,
    external_account_id UUID        NOT NULL REFERENCES external_accounts (id) ON DELETE CASCADE,
    import_batch_id     UUID        REFERENCES import_batches (id) ON DELETE SET NULL,
    dedup_key           TEXT        NOT NULL,
    external_ref        TEXT        NOT NULL DEFAULT '',
    occurred_at         TIMESTAMPTZ NOT NULL,
    amount_cents        BIGINT      NOT NULL,
    description         TEXT        NOT NULL DEFAULT '',
    category_id         UUID        REFERENCES categories (id) ON DELETE SET NULL,
    raw                 JSONB,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (external_account_id, dedup_key)
);

CREATE INDEX external_transactions_account_idx ON external_transactions (external_account_id, occurred_at DESC);

-- +goose Down
DROP TABLE IF EXISTS external_transactions;
DROP TABLE IF EXISTS category_rules;
DROP TABLE IF EXISTS import_batches;
DROP TABLE IF EXISTS external_accounts;
