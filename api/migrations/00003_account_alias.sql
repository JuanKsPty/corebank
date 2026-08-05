-- A name a person can give an account.
--
-- An account number identifies an account; it does not help anybody recognise one.
-- Until now every label in the interface was derived from account_type, so two
-- savings accounts read identically and were told apart only by their last four
-- digits. That is not an edge case: a customer may hold six accounts and there are
-- three types, so duplicates are guaranteed for anybody who opens a fourth.
--
-- NOT NULL DEFAULT '' rather than nullable, deliberately. An empty alias and an
-- absent one mean the same thing to a person, so distinguishing them with NULL would
-- add null handling to the pgx scan, the Go struct, the JSON and the frontend in
-- exchange for a distinction nobody uses. The DEFAULT also settles the 1605 seeded
-- accounts without a backfill.
--
-- The length limit lives here and not only in the handler because the width of a card
-- in the accounts grid depends on it. Forty characters is "Gastos de la casa del mes"
-- and does not break the layout.
--
-- Aliases are deliberately not unique. Two accounts may share one, as at a real bank:
-- the number is still the identifier and the interface always shows it alongside, so a
-- duplicate is confusing to a person but never ambiguous to the system.

-- +goose Up
ALTER TABLE accounts
    ADD COLUMN alias TEXT NOT NULL DEFAULT ''
        CHECK (char_length(alias) <= 40);

-- +goose Down
ALTER TABLE accounts DROP COLUMN IF EXISTS alias;
