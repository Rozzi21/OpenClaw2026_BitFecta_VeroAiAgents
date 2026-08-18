# Google OAuth Plan — "Continue with Google"

Tanggal: 18 Agu 2026. Status: Phase 1 (audit) selesai; Phase 2 (implementasi) mengikuti dokumen ini.

Tujuan: menambahkan login Google sebagai **provider tambahan**. Auth yang ada
(email/password, JWT access+refresh, AuthSession, revokasi, logout, role,
middleware, guest order claim, frontend flow) TIDAK berubah perilaku. Google
hanya memverifikasi identitas; Vero tetap pemilik User, AuthSession, JWT, role,
authorization, logout, dan revocation.

---

## 1. Arsitektur Auth Saat Ini (hasil audit)

Sumber: `backend/internal/services/auth_service.go`, `backend/internal/auth/jwt.go`,
`backend/internal/handlers/auth_handlers.go`, `backend/internal/routes/routes.go`.

- Layered: Handler → Service → Repository → GORM. DI manual via
  `services.New()` + `handlers.New()`.
- `AuthService.Register/Login` → `issueSession(ctx, user)`:
  1. `jwt.Generate(user)` → token pair. Access: HS256, `aud=access`, TTL
     `JWT_ACCESS_TTL_MINUTES` (default 15 mnt). Refresh: `aud=refresh`, TTL
     `JWT_REFRESH_TTL_HOURS` (default 720 jam), punya `jti` UUID.
  2. `CreateAuthSession(userID, refreshJTI, expiresAt)` → baris `auth_sessions`.
  3. Return `AuthIssueResult{Response: dto.AuthResponse{access_token, token_type,
     expires_in, user}, RefreshToken, RefreshJTI}`.
- Handler `respondAuthIssue` (`handlers/helpers.go`): set cookie refresh
  HttpOnly (`auth.SetRefreshCookie`, path `/api/v1/auth`, SameSite dari
  `JWT_COOKIE_SAME_SITE` default `Strict`) + balas envelope JSON berisi
  `AuthResponse`.
- Setelah `Register`/`Login` sukses, handler memanggil
  `Guests.ClaimOrder(ctx, GetGuestIdentityCookie(c), user.ID)` — order guest
  (cookie `vero_guest_session`) di-claim ke akun. **Wajib direplikasi di jalur
  OAuth.**
- Password user: bcrypt, kolom `users.password` `not null`, `json:"-"`.
  Register publik selalu `RoleUser` (SEC-1).
- Audit: tiap aksi auth `auth.LogSecurity(...)` dengan `AuthRequestMeta{IP,
  UserAgent, RequestID}`.
- Rate limit: grup `/api/v1/auth` memakai `AuthRateLimit()` 5 req/dtk per-IP.

## 2. Arsitektur Sesi Saat Ini

- `AuthSession` (`models.go`): `UserID`, `TokenJTI` (uniqueIndex), `ExpiresAt`,
  `RevokedAt` nullable. Aktif = `revoked_at IS NULL AND expires_at > now()`.
- Refresh = rotasi atomik via `RotateSession` (BUG-1): hanya pemenang race yang
  menerbitkan token baru; reuse detection (>1 mnt setelah rotasi) me-revoke
  SEMUA sesi user (`RevokeAllActiveSessionsByUser`).
- Logout = `RevokeSessionByJTIIfExists` + clear cookie.
- Middleware `Auth(s.JWT)` memvalidasi access token (aud `access`), set
  `user_id`/`role`/`email` ke gin context. `Role(...)` untuk RBAC.
- **Implikasi OAuth:** sesi Google harus melalui `issueSession` yang sama agar
  rotasi/reuse-detection/logout/revoke bekerja identik. Tidak ada jalur sesi
  khusus Google.

## 3. Arsitektur Google OAuth (target)

Authorization Code Flow (server-side, confidential client):

```
Browser → FE (/login, /register, trip page) klik "Continue with Google"
  → window.location = /api/v1/auth/google/login?return_to=<path>
Backend (GET /auth/google/login):
  → generate state = random 32 byte (CSPRNG) + nonce
  → simpan OAuthState{StateHash, Nonce, ReturnTo, ExpiresAt: now+10m} ke DB
  → 302 redirect ke accounts.google.com/o/oauth2/v2/auth
    (client_id, redirect_uri, scope=openid email profile, state, nonce)
Google → user login → 302 ke GET /auth/google/callback?code&state
Backend (callback):
  → validasi state: cari by hash, belum dipakai, belum expired → tandai terpakai (single-use)
  → tukar code → POST https://oauth2.googleapis.com/token
    (code, client_id, client_secret, redirect_uri, grant_type=authorization_code)
  → dapat id_token (JWT RS256) → verifikasi signature via Google JWKS
    (https://www.googleapis.com/oauth2/v3/certs, di-cache), iss, aud=client_id,
    exp, nonce → extract sub, email, email_verified, name
  → email_verified wajib true
  → find/create User (lihat §7)
  → issueSession(ctx, user) → AuthSession + token pair Vero normal
  → ClaimOrder(cookie guest) seperti login biasa
  → set refresh cookie (Set-Cookie pada respons 302)
  → 302 redirect ke FE: {FE origin}{return_to}?provider=google#access_token=...&expires_in=...
Frontend halaman penerima (login/register/trip + /auth/callback):
  → baca hash fragment → setCustomerAccessToken(token) → bersihkan hash →
    redirect return_to
```

Kenapa redirect + fragment, bukan JSON: OAuth adalah full-page navigation;
SPA fetch tidak bisa mengikuti consent screen Google. Access token ditaruh di
**URL fragment** (`#...`) agar tidak masuk access log server/proxy dan tidak
terkirim ke backend sebagai query param. Refresh token tetap cookie HttpOnly
yang di-set pada respons 302 callback — SameSite cookie default `Strict` AMAN
di sini karena Set-Cookie terjadi pada respons backend (same-site terhadap FE
yang satu origin via proxy Next.js), bukan pada respons Google.

## 4. Perubahan Database

Dua perubahan additive (AutoMigrate + SQL versioned idempoten, mengikuti pola
`20260818_guest_order_limit.sql`):

1. **`users.google_sub`** — `VARCHAR(64) NULL` + `UNIQUE` (partial index
   `WHERE google_sub IS NOT NULL`). Menyimpan claim `sub` Google (immutable,
   stabil per akun Google). Nullable: user email/password tidak berubah.
2. **Tabel baru `oauth_states`** (model `OAuthState`):
   - `id` uuid PK (BaseModel), `state_hash` varchar(64) uniqueIndex (SHA-256
     dari state raw — raw state tidak disimpan), `nonce` varchar(64),
     `return_to` varchar(255), `expires_at` timestamptz index,
     `consumed_at` timestamptz NULL.
   - Single-use ditegakkan via consume atomik (`UPDATE ... SET consumed_at=now()
     WHERE state_hash=? AND consumed_at IS NULL AND expires_at>now()` →
     `RowsAffected==1`), pola sama dengan `RotateSession` (BUG-1).

Tidak ada kolom dihapus/diubah; migrasi aman untuk data existing.

## 5. Strategi OAuth State

- `state` = 32 byte `crypto/rand`, base64url. Hanya **SHA-256 hash** yang
  disimpan di DB (bocornya tabel tidak memberi state yang bisa dipakai).
- DB-backed (bukan cookie): state tidak bisa dipalsukan client, single-use
  enforceable, dan immune terhadap SameSite cookie quirks pada cross-site
  redirect kembali dari Google.
- TTL 10 menit (`GOOGLE_OAUTH_STATE_TTL_MINUTES` kalau perlu di-tune; default
  konstan 10 menit di service).
- `nonce` acak terpisah disimpan bareng state dan diverifikasi terhadap claim
  `nonce` di id_token → anti token-replay / mix-up.
- `return_to` disimpan di server (bukan query param callback) → path redirect
  pasca-login tidak bisa diubah attacker; divalidasi allowlist (lihat §10).

## 6. Alur Callback (detail)

`GET /api/v1/auth/google/callback`:

1. Baca `code`, `state` dari query. Bila `error` dari Google (mis.
   `access_denied`) → redirect FE dengan `?auth_error=...`.
2. `ConsumeOAuthState(state)` atomik → gagal (tidak ada/terpakai/expired) =
   400, audit `google_oauth_state_invalid`. Ini menutup replay + CSRF.
3. Tukar code via HTTP POST (ctx request, timeout 10 dtk,
   `http.NewRequestWithContext` + tutup body — pola SEC-26).
4. Parse + verifikasi `id_token`:
   - Signature RS256 terhadap JWKS Google (cache in-memory per `kid`, refresh
     bila `kid` tak dikenal).
   - `iss` ∈ {`https://accounts.google.com`, `accounts.google.com`}.
   - `aud` == `GOOGLE_CLIENT_ID`. `exp` belum lewat.
   - `nonce` == nonce dari state.
5. `email_verified` harus `true` (claim Google). Tolak bila false.
6. Resolve user (§7) → `issueSession` → `ClaimOrder` → set refresh cookie →
   302 ke FE (fragment berisi access token).
7. Audit: `google_login_success` / `google_login_failed` via
   `auth.LogSecurity` (event baru di `auth/audit.go`).

## 7. Kebijakan Account Linking

Urutan resolusi user di callback:

1. **`google_sub` match** → user ditemukan → login. Paling kuat; `sub` immutable.
2. **Email match + `email_verified=true`** → **link**: set
   `users.google_sub = sub` pada akun existing (Update kolom tunggal, bukan
   `Save` — DB-2), lalu login. Aman karena Google menjamin kepemilikan email
   terverifikasi; password lama TETAP bisa dipakai (akun tidak dikunci ke satu
   provider).
3. **Tidak ada match** → **create** user baru: `Name` dari claim `name`
   (fallback prefix email), `Email` lowercase, `Role=RoleUser` (SEC-1: role
   tidak pernah dari luar), `GoogleSub=sub`, dan password = bcrypt(random 16
   byte CSPRNG) — placeholder tak-tertebak yang memenuhi constraint `not null`
   (pola sama dengan `GuestUser`, SEC-24).
4. Guest user (`guest-*@vero.local`) tidak akan pernah match email Google —
   aman by construction.

Race dua callback paralel untuk email baru: unique index `users.email` +
`users.google_sub` menjamin satu pemenang; yang kalah di-retry sebagai "match
by sub/email" (fallback find setelah `CreateUser` gagal constraint).

## 8. Integrasi JWT/Sesi

- OAuth callback memanggil `AuthService.issueSession` YANG SAMA (diekspos via
  method baru `AuthService.GoogleLogin` di `auth_service.go` atau file domain
  baru `google_oauth_service.go` dalam package `services`). Hasilnya sesi Vero
  normal: access JWT aud `access`, refresh JWT aud `refresh` + baris
  `auth_sessions` + cookie HttpOnly.
- Refresh/rotasi/reuse-detection/logout/revoke/role/middleware: tidak tersentuh.
- User Google bisa juga set password nanti (alur reset di luar scope); login
  email/password untuk akun hasil Google secara praktis tidak mungkin karena
  password random — dapat diterima.

## 9. Alur Frontend

- Tombol "Continue with Google" (baru, komponen bersama
  `frontend/src/components/auth/GoogleButton.tsx`) dipakai di `/login`,
  `/register`, dan menggantikan placeholder disabled di
  `frontend/src/app/trip/[id]/page.tsx` (guest order limit gate).
- Aksi: `window.location.href = "/api/v1/auth/google/login?return_to=" +
  encodeURIComponent(pathSaatIni)`. Bukan `apiFetch` — full navigation.
- Handler penerima token: komponen client kecil
  `frontend/src/components/auth/OAuthCallback.tsx` (dipasang di `/login`,
  `/register`, dan halaman trip, atau halaman khusus `/auth/callback`) yang
  membaca `location.hash`, memanggil `setCustomerAccessToken`,
  `history.replaceState` membersihkan hash, lalu redirect ke `return_to`.
- Setelah itu flow existing berjalan normal: `apiFetch` menyertakan Bearer dari
  localStorage (`vero_customer_access_token`), order guest sudah di-claim
  backend saat callback.
- Backoffice TIDAK berubah (staff tetap email/password; Google hanya untuk
  customer — role operator/admin tetap hanya via `POST /admin/users`).

## 10. Pertimbangan Keamanan

- **CSRF/login-CSRF**: state DB-backed single-use + nonce id_token. Callback
  tanpa state valid → 400.
- **Token leakage**: access token di URL fragment (bukan query) pada redirect
  final; refresh token selalu cookie HttpOnly. `state` mentah tidak disimpan
  (hash saja).
- **Open redirect**: `return_to` divalidasi — hanya path relatif yang diawali
  `/` dan tidak diawali `//`; selain itu fallback `/`. Origin FE berasal dari
  env `GOOGLE_OAUTH_FRONTEND_URL` (default `http://localhost:3000`), bukan dari
  request.
- **Email tidak terverifikasi**: ditolak (`email_verified` wajib true) sebelum
  linking by email.
- **Scope minimal**: `openid email profile`. Tidak ada akses API Google lain.
- **Secret**: `GOOGLE_CLIENT_SECRET` hanya di server; `Config.Validate()`
  menolak start di production bila OAuth diaktifkan tapi secret/client kosong
  (pola SEC-4/DOKU).
- **Rate limit**: kedua endpoint baru masuk grup `/auth` → `AuthRateLimit()`
  5 req/dtk per-IP otomatis berlaku.
- **SEC-15**: error internal (gagal tukar code, JWKS) di-log server-side;
  client hanya menerima redirect `auth_error` generik.
- **Feature flag**: `GOOGLE_OAUTH_ENABLED` (default false). Bila false,
  endpoint membalas 404/503 dan tidak ada ketergantungan jaringan — konsisten
  dengan pola graceful degradation (coding-rules §3.3) dan `PAYMENTS_ENABLED`.

## 11. Environment Variables

| Variabel | Default | Keterangan |
|---|---|---|
| `GOOGLE_OAUTH_ENABLED` | `false` | Feature flag. Production: wajib true + kredensial |
| `GOOGLE_CLIENT_ID` | _(kosong)_ | Client ID dari Google Cloud Console |
| `GOOGLE_CLIENT_SECRET` | _(kosong)_ | Rahasia server. Wajib saat enabled di production |
| `GOOGLE_REDIRECT_URI` | `http://localhost:8080/api/v1/auth/google/callback` | Harus terdaftar persis di Google Console |
| `GOOGLE_OAUTH_FRONTEND_URL` | `http://localhost:3000` | Origin FE untuk redirect final + validasi return_to |

`.env.example` diperbarui; `deployment.md` mendapat tabel baru ini.

## 12. Strategi Testing

- **Unit (backend, package services)**: tabel `oauth_state_test.go` —
  generate/consume state (single-use, expiry, tamper), verifikasi id_token
  memakai JWKS palsu (httptest server + kunci RSA test): iss/aud/exp/nonce
  salah ditolak, email unverified ditolak, linking by email menset
  `google_sub`, create user baru role `user` + password random, race create
  (unique constraint fallback). Mock repo via interface narrow (SEC-27).
- **Build/vet/fmt**: `go build ./...`, `go vet ./...`, `gofmt -l .` kosong,
  `go test ./...`.
- **Frontend**: `tsc --noEmit` di `frontend/`.
- **Manual E2E (dev)**: kredensial Google dev → klik tombol di /login →
  consent → kembali dengan sesi aktif → `GET /auth/me` 200 → order guest
  ter-claim → refresh & logout normal.
- **Regresi**: login/register email-password, refresh rotation, reuse
  detection, guest order limit tidak berubah (test existing
  `guest_order_limit_test.go` tetap hijau).
