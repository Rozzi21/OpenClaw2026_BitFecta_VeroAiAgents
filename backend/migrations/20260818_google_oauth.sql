-- Google OAuth (Continue with Google) — 18 Agu 2026
-- Additive + idempotent. Setara dengan AutoMigrate GORM untuk:
--   1. kolom users.google_sub (Google `sub` claim, nullable, unik parsial)
--   2. tabel oauth_states (state CSRF single-use; hanya hash SHA-256 disimpan)
-- Aman dijalankan berulang. Tidak ada kolom dihapus/diubah.

ALTER TABLE users ADD COLUMN IF NOT EXISTS google_sub VARCHAR(64);

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_google_sub
    ON users (google_sub)
    WHERE google_sub IS NOT NULL;

CREATE TABLE IF NOT EXISTS oauth_states (
    id          UUID PRIMARY KEY,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ,
    state_hash  VARCHAR(64) NOT NULL,
    nonce       VARCHAR(64) NOT NULL,
    return_to   VARCHAR(255) NOT NULL DEFAULT '/',
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_oauth_states_state_hash ON oauth_states (state_hash);
CREATE INDEX IF NOT EXISTS idx_oauth_states_expires_at ON oauth_states (expires_at);
CREATE INDEX IF NOT EXISTS idx_oauth_states_consumed_at ON oauth_states (consumed_at);
CREATE INDEX IF NOT EXISTS idx_oauth_states_deleted_at ON oauth_states (deleted_at);

-- Canonical external-identity link (ExternalIdentity model). Identity is keyed
-- by the provider's immutable subject (Google `sub`), NOT by email alone.
-- UNIQUE(provider, provider_user_id) enforces "one Google account → one Vero
-- account". users.google_sub above is kept only as a denormalized fast-path
-- mirror; this table is the source of truth.
CREATE TABLE IF NOT EXISTS external_identities (
    id               UUID PRIMARY KEY,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at       TIMESTAMPTZ,
    user_id          UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    provider         VARCHAR(30) NOT NULL,
    provider_user_id VARCHAR(128) NOT NULL,
    email            VARCHAR(180),
    picture          TEXT
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_ext_ident_provider_user
    ON external_identities (provider, provider_user_id);
CREATE INDEX IF NOT EXISTS idx_external_identities_user_id ON external_identities (user_id);
CREATE INDEX IF NOT EXISTS idx_external_identities_deleted_at ON external_identities (deleted_at);
