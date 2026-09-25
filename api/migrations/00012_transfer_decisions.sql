-- Decisions about pairs of movements that look like one transfer between the
-- owner's own accounts: the card's "Pagos" line and the bank's "PAGO TC", say.
--
-- A confirmed pair marks both movements as a transfer, so paying the card is
-- neither spending nor income. A rejected pair is remembered so it is never
-- suggested again.

-- +goose Up
CREATE TABLE transfer_decisions (
    id           UUID        PRIMARY KEY,
    user_id      UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    out_entry_id UUID        NOT NULL,
    in_entry_id  UUID        NOT NULL,
    decision     TEXT        NOT NULL CHECK (decision IN ('confirmed', 'rejected')),
    decided_by   TEXT        NOT NULL CHECK (decided_by IN ('auto', 'user', 'assistant')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (out_entry_id, user_id) REFERENCES entries (id, user_id) ON DELETE CASCADE,
    FOREIGN KEY (in_entry_id, user_id) REFERENCES entries (id, user_id) ON DELETE CASCADE,
    UNIQUE (out_entry_id, in_entry_id),
    CHECK (out_entry_id <> in_entry_id)
);
CREATE INDEX transfer_decisions_user_idx ON transfer_decisions (user_id);

-- +goose Down
DROP TABLE IF EXISTS transfer_decisions;
