BEGIN;
CREATE TABLE IF NOT EXISTS guest_sessions (
    id uuid PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    deleted_at timestamptz NULL,
    token_hash varchar(64) NOT NULL UNIQUE,
    user_id uuid NOT NULL REFERENCES users(id),
    first_order_id uuid NULL,
    order_count integer NOT NULL DEFAULT 0 CHECK (order_count >= 0),
    expires_at timestamptz NOT NULL
);
ALTER TABLE chat_sessions ADD COLUMN IF NOT EXISTS guest_session_id uuid NULL;
ALTER TABLE bookings ADD COLUMN IF NOT EXISTS guest_session_id uuid NULL;
ALTER TABLE bookings ADD COLUMN IF NOT EXISTS idempotency_key_hash varchar(64) NULL;
CREATE INDEX IF NOT EXISTS idx_guest_sessions_expires_at ON guest_sessions(expires_at);
CREATE INDEX IF NOT EXISTS idx_chat_sessions_guest_session_id ON chat_sessions(guest_session_id);
CREATE INDEX IF NOT EXISTS idx_bookings_guest_session_id ON bookings(guest_session_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_bookings_idempotency_key_hash ON bookings(idempotency_key_hash) WHERE idempotency_key_hash IS NOT NULL AND idempotency_key_hash <> '';
COMMIT;