-- Categories: how a customer groups their own transactions for spend tracking.
--
-- Two levels only (a category and, optionally, its parent) rather than
-- unbounded nesting: every category corebank ships as a default is a flat
-- list, and a deeper tree would buy nothing a customer actually uses while
-- making "which bucket does this fall under" a recursive question instead of
-- a one-hop lookup.
--
-- Assigning a category to a transaction is metadata, exactly like an
-- account's alias (00003_account_alias.sql): it never touches the ledger,
-- because no amount of relabelling a movement can change how much money
-- moved.

-- +goose Up

CREATE TYPE category_kind AS ENUM ('income', 'expense');

CREATE TABLE categories (
    id         UUID          PRIMARY KEY,
    user_id    UUID          NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- ON DELETE SET NULL: removing a parent promotes its children to
    -- top-level categories rather than cascading their deletion, so a
    -- customer tidying up one level never loses the categories underneath.
    parent_id  UUID          REFERENCES categories (id) ON DELETE SET NULL,
    name       TEXT          NOT NULL CHECK (char_length(name) <= 60),
    kind       category_kind NOT NULL,
    color      TEXT,
    icon       TEXT,
    -- A default category a customer did not create themselves. Kept apart so
    -- the interface can stop them renaming "Sin categorizar" into something
    -- that no longer means what every uncategorised transaction points at.
    is_system  BOOLEAN       NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ   NOT NULL DEFAULT now(),
    -- Only stops two *children* of the same parent sharing a name: PostgreSQL
    -- treats every NULL as distinct from every other NULL, so this constraint
    -- alone would let a customer create any number of top-level categories
    -- all named "Otros". The partial index below is what actually covers that
    -- case, which is also the more common one — every default category is
    -- top-level.
    UNIQUE (user_id, parent_id, name)
);

CREATE INDEX categories_user_id_idx ON categories (user_id);

CREATE UNIQUE INDEX categories_user_id_name_top_level_idx
    ON categories (user_id, name) WHERE parent_id IS NULL;

-- ON DELETE SET NULL, not RESTRICT: a customer deleting a category they no
-- longer want must not be blocked by every transaction it was ever attached
-- to, and "uncategorised" is a normal state for a transaction to be in.
ALTER TABLE transactions
    ADD COLUMN category_id UUID REFERENCES categories (id) ON DELETE SET NULL;

CREATE INDEX transactions_category_id_idx ON transactions (category_id) WHERE category_id IS NOT NULL;

-- +goose Down
ALTER TABLE transactions DROP COLUMN IF EXISTS category_id;
DROP TABLE IF EXISTS categories;
DROP TYPE IF EXISTS category_kind;
