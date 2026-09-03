-- Guest one-order enforcement: contact-anchored entitlement (GO-P0-1).
--
-- guest_sessions.order_count is anchored on the vero_guest_session cookie, which
-- the client can drop to mint a fresh identity (and a fresh allowance). This
-- table adds the second anchor the client does not choose: the normalized
-- contact channel the guest order was placed with, stored as
-- sha256("<channel>:<normalized value>"). The UNIQUE index on contact_key is the
-- authoritative gate — the service consumes it inside the same transaction that
-- inserts the booking, so a rejected attempt rolls back both.
--
-- Additive + idempotent; touches no existing row. Equivalent to the
-- GuestOrderEntitlement model registered in Database.AutoMigrate().
--
-- NOTE: guest orders created BEFORE this migration have no anchor row (their
-- normalized contacts were never recorded), so they stay cookie-anchored only.
-- No backfill is attempted here on purpose: reproducing the Go normalization
-- (plus-tag stripping, trunk-prefix folding) in SQL risks writing keys that do
-- not match the ones the service computes.
BEGIN;
CREATE TABLE IF NOT EXISTS guest_order_entitlements (
    id uuid PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    deleted_at timestamptz NULL,
    contact_key varchar(64) NOT NULL,
    channel varchar(10) NOT NULL,
    guest_session_id uuid NULL,
    booking_id uuid NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_guest_order_entitlements_contact_key ON guest_order_entitlements(contact_key);
CREATE INDEX IF NOT EXISTS idx_guest_order_entitlements_guest_session_id ON guest_order_entitlements(guest_session_id);
CREATE INDEX IF NOT EXISTS idx_guest_order_entitlements_booking_id ON guest_order_entitlements(booking_id);
CREATE INDEX IF NOT EXISTS idx_guest_order_entitlements_deleted_at ON guest_order_entitlements(deleted_at);
COMMIT;
