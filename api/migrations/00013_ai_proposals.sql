-- Changes the assistant proposed and the owner has to confirm.
--
-- The assistant never writes to the owner's data directly. Each of its write
-- tools stores a proposal here and the chat shows it as a card; only the
-- owner's "Aplicar" makes the change. Everything a proposal can do leaves every
-- balance as the bank printed it: a category, a note, a rule, a transfer match,
-- or a stated balance.

-- +goose Up
CREATE TABLE ai_proposals (
    id          UUID        PRIMARY KEY,
    user_id     UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind        TEXT        NOT NULL CHECK (kind IN ('recategorize', 'note', 'rule', 'transfer_decision', 'checkpoint')),
    payload     JSONB       NOT NULL,
    summary     TEXT        NOT NULL CHECK (char_length(summary) <= 500),
    status      TEXT        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'applied', 'rejected')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    decided_at  TIMESTAMPTZ
);
CREATE INDEX ai_proposals_pending_idx ON ai_proposals (user_id, created_at) WHERE status = 'pending';

-- +goose Down
DROP TABLE IF EXISTS ai_proposals;
