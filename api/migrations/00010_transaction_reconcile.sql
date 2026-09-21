-- A customer can state the true balance a bank import got wrong (a parser
-- miscounted or misread a statement) and have corebank post one corrective
-- deposit or withdrawal for the difference — see internal/transactions'
-- Reconcile, posted through the ordinary deposit/withdraw path exactly like
-- an IBKR cash sync or a bank import already does (00005_investments.sql,
-- 00008_bank_import_linked_accounts.sql), hence the same shape of change
-- here: one more allowed origin, nothing else.

-- +goose Up

ALTER TABLE transactions DROP CONSTRAINT transactions_origin_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_origin_check
    CHECK (origin IN ('api', 'chat', 'ibkr_sync', 'bank_import', 'reconcile'));

-- +goose Down

ALTER TABLE transactions DROP CONSTRAINT transactions_origin_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_origin_check
    CHECK (origin IN ('api', 'chat', 'ibkr_sync', 'bank_import'));
