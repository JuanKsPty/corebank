-- PIN login: a device-bound quick-unlock alternative to email + password.
--
-- A 6-digit PIN alone has only a million combinations, nowhere near enough
-- entropy to stand as a credential by itself. So it never works on its own:
-- it only unlocks a session on a device that already proved itself with a
-- full email+password login and was explicitly trusted from the new
-- Seguridad screen. trusted_devices is that trust record, storing only the
-- hash of a 256-bit device token — the same shape as refresh_tokens, and for
-- the same reason: a database dump must not be replayable as a live device.
--
-- pin_hash lives on users directly, nullable, because most accounts will
-- never set one — it is opt-in, not a replacement for the password.

-- +goose Up
ALTER TABLE users ADD COLUMN pin_hash TEXT;

CREATE TABLE trusted_devices (
    id                UUID        PRIMARY KEY,
    user_id           UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    device_token_hash TEXT        NOT NULL UNIQUE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Consecutive wrong PINs on this device. Reset on success, and the device
    -- is revoked outright once it crosses the limit enforced in Go — a
    -- stolen device plus shoulder-surfed digits stops being useful quickly.
    failed_attempts   SMALLINT    NOT NULL DEFAULT 0,
    expires_at        TIMESTAMPTZ NOT NULL
);

CREATE INDEX trusted_devices_user_id_idx ON trusted_devices (user_id);

-- +goose Down
DROP TABLE IF EXISTS trusted_devices;
ALTER TABLE users DROP COLUMN IF EXISTS pin_hash;
