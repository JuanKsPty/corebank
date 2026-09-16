-- A real account's own number has nothing to do with the account number
-- printed on a bank statement — they are two separate identifier spaces,
-- and picking the wrong real account to link an external one to would
-- silently merge two unrelated real-world accounts' histories together.
-- This makes that impossible: a real account can be linked_account_id on
-- at most one external_accounts row.

-- +goose Up

CREATE UNIQUE INDEX external_accounts_linked_account_id_idx
    ON external_accounts (linked_account_id) WHERE linked_account_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS external_accounts_linked_account_id_idx;
