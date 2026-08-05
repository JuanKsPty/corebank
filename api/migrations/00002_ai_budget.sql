-- The assistant's spend, and the ceiling it cannot cross.
--
-- This deployment is public, registration is open and the test credentials are
-- published in the README, so the API key behind the assistant is reachable by
-- anybody who opens the page. A per-address rate limit slows that down; it does not
-- bound it. Nothing here is about performance — it is about the fact that an
-- unbounded spend on a borrowed key is not a configuration detail.
--
-- The mechanism is the one this system already uses for money. A transfer reserves
-- funds before it moves them, so two movements cannot both spend the same balance;
-- a call reserves its estimated cost before it is made, so twenty concurrent
-- requests cannot all pass the same check and collectively overshoot. Reserve,
-- then settle with what it actually cost.
--
-- Amounts are micro-dollars (1/1,000,000 of a dollar) as BIGINT, because a single
-- call can cost a fraction of a cent and a counter that cannot represent what it
-- counts is not a counter. Integers end to end, like every other amount here: no
-- float ever touches a value in this schema.

-- +goose Up

-- The ceilings, one row per scope. A composite key rather than three tables
-- because the guard runs the same two statements against each of them, and three
-- shapes would mean three copies of that logic.
--
--   scope = 'total'     key = ''            the lifetime ceiling
--   scope = 'day'       key = '2026-08-04'  one calendar day, UTC
--   scope = 'user_day'  key = '<uuid>'||':'||'<date>'
--
-- Rows are created on demand with the ceiling from the environment, so a new day
-- or a new customer needs no migration and no scheduled job. Yesterday's rows stay
-- as the record of what yesterday cost.
CREATE TABLE ai_budget (
    scope           TEXT        NOT NULL CHECK (scope IN ('total', 'day', 'user_day')),
    key             TEXT        NOT NULL,
    cap_micros      BIGINT      NOT NULL CHECK (cap_micros >= 0),
    -- Settled spend: what calls actually cost, once known.
    spent_micros    BIGINT      NOT NULL DEFAULT 0 CHECK (spent_micros >= 0),
    -- In-flight spend: reserved before a call, released when it settles. A crash
    -- between the two leaves this high, which errs toward refusing to spend — the
    -- safe direction. `total` is reconcilable from ai_usage if it ever matters.
    reserved_micros BIGINT      NOT NULL DEFAULT 0 CHECK (reserved_micros >= 0),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (scope, key)
);

-- One row per call to the model: the audit trail, and the only honest source for
-- "what does a message actually cost?".
--
-- The four token counts are kept apart rather than summed because they are billed
-- at four different rates — writing the cache costs more than sending the tokens
-- plainly, reading it costs a tenth — so a single input_tokens column would
-- misprice every call after the first and quietly hide whether caching works at all.
CREATE TABLE ai_usage (
    id                 BIGSERIAL   PRIMARY KEY,
    -- Nullable and ON DELETE SET NULL: deleting a customer must not erase the
    -- record of money already spent, and must not be blocked by it either.
    user_id            UUID        REFERENCES users (id) ON DELETE SET NULL,
    model              TEXT        NOT NULL,
    input_tokens       INT         NOT NULL DEFAULT 0,
    output_tokens      INT         NOT NULL DEFAULT 0,
    cache_write_tokens INT         NOT NULL DEFAULT 0,
    cache_read_tokens  INT         NOT NULL DEFAULT 0,
    cost_micros        BIGINT      NOT NULL DEFAULT 0,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Supports the two questions actually asked of this table: what has been spent
-- lately, and what has one customer spent.
CREATE INDEX ai_usage_created_at_idx ON ai_usage (created_at DESC);
CREATE INDEX ai_usage_user_id_idx ON ai_usage (user_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS ai_usage;
DROP TABLE IF EXISTS ai_budget;
