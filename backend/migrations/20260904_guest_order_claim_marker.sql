-- Guest order claim marker (GO-P1-3 / GO-P3-3).
--
-- Before this migration the claim state of a guest order could only be INFERRED
-- from bookings.guest_session_id turning NULL. That made a second claim an
-- ambiguous "record not found": "never claimed", "already yours" (an idempotent
-- retry) and "already someone else's" (a refusal that must never transfer)
-- looked identical. These two columns record the decision itself, written in the
-- same transaction that transfers the booking, so ownership is decided exactly
-- once and every later attempt is answered from the marker instead of racing for
-- the booking row again.
--
-- Additive + idempotent. Equivalent to the GuestSession model fields registered
-- in Database.AutoMigrate().
--
-- The final UPDATE backfills sessions whose order was already claimed before the
-- columns existed. It only fills NULL markers and only records what the booking
-- row already states (guest_session_id released, user_id = current owner), so it
-- cannot change any ownership; without it those rows would have to fall back to
-- reading the booking on every claim attempt.
BEGIN;
ALTER TABLE guest_sessions ADD COLUMN IF NOT EXISTS claimed_user_id uuid NULL;
ALTER TABLE guest_sessions ADD COLUMN IF NOT EXISTS claimed_at timestamptz NULL;
CREATE INDEX IF NOT EXISTS idx_guest_sessions_claimed_user_id ON guest_sessions(claimed_user_id);
UPDATE guest_sessions gs
SET claimed_user_id = b.user_id,
    claimed_at = COALESCE(b.updated_at, gs.updated_at)
FROM bookings b
WHERE gs.first_order_id = b.id
  AND b.guest_session_id IS NULL
  AND gs.claimed_user_id IS NULL;
COMMIT;
