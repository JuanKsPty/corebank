-- A bank-account import (Banco General or BAC checking/savings) now posts
-- real ledger movements into one of the customer's own accounts, instead of
-- only ever landing in external_transactions — see internal/bankimport's
-- package doc for the updated split. A card import is unaffected:
-- linked_account_id stays null for it, and its rows still never touch
-- anything but this schema.

-- +goose Up

ALTER TABLE external_accounts
    ADD COLUMN linked_account_id UUID NULL REFERENCES accounts (id) ON DELETE SET NULL;

-- The real transaction a row produced, for a linked bank account; null for a
-- card row, which never produces one.
ALTER TABLE external_transactions
    ADD COLUMN transaction_id UUID NULL REFERENCES transactions (id) ON DELETE SET NULL;

-- A bank-account import posts through the ordinary deposit/withdraw path
-- (internal/transactions), exactly like an IBKR cash sync already does — see
-- 00005_investments.sql for the same change applied there.
ALTER TABLE transactions DROP CONSTRAINT transactions_origin_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_origin_check
    CHECK (origin IN ('api', 'chat', 'ibkr_sync', 'bank_import'));

-- +goose Down
ALTER TABLE transactions DROP CONSTRAINT transactions_origin_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_origin_check
    CHECK (origin IN ('api', 'chat', 'ibkr_sync'));

ALTER TABLE external_transactions DROP COLUMN IF EXISTS transaction_id;
ALTER TABLE external_accounts DROP COLUMN IF EXISTS linked_account_id;
