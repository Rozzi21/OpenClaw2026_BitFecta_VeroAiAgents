# Google OAuth — Security Audit (READ-ONLY)

> Status audit: **Read-only**. Tidak ada kode yang diubah, tidak ada dependency
> ditambahkan, tidak ada skema DB yang diubah. Dokumen ini hanya menginventarisasi
> kelemahan pada implementasi "Continue with Google" (Authorization Code + PKCE +
> OIDC) beserta rekomendasi perbaikan yang **belum** dieksekusi.
>
> Tanggal audit: 31 Agu 2026. Commit yang diaudit: `7f2cc01`.

---

## 1. Ruang Lingkup

### 1.1 File yang diperiksa (backend)

| Area | File |
|---|---|
| OIDC client / verifikasi id_token | `backend/internal/auth/google.go` |
| JWT + cookie + audit | `backend/internal/auth/jwt.go`, `cookie.go`, `audit.go` |
| Service OAuth | `backend/internal/services/google_oauth_service.go` |
| Service sesi inti | `backend/internal/services/auth_service.go`, `services.go` |
| Handler OAuth | `backend/internal/handlers/google_auth_handlers.go`, `helpers.go` |
| Handler auth inti | `backend/internal/handlers/auth_handlers.go` |
| Middleware | `backend/internal/middlewares/middlewares.go`, `logging.go` |
| Model | `backend/internal/models/models.go` (`OAuthState`, `ExternalIdentity`, `User`, `AuthSession`) |
| Repository | `backend/internal/repositories/oauth_repository.go`, `user_repository.go`, `auth_sessions.go`, `guest_repository.go`, `booking_repository.go` |
| Config | `backend/internal/config/config.go` |
| Routes | `backend/internal/routes/routes.go` |
| Migrasi | `backend/migrations/20260818_google_oauth.sql`, `backend/internal/database/database.go` (`migrateGoogleOAuth`) |
| Main | `backend/cmd/server/main.go` |

### 1.2 File yang diperiksa (frontend customer)

- `frontend/src/lib/api.ts`
- `frontend/src/components/auth/AuthForm.tsx`, `GoogleButton.tsx`, `OAuthReceiver.tsx`
- `frontend/src/app/login/page.tsx`, `register/page.tsx`, `trip/[id]/page.tsx`, `order/[id]/page.tsx`
- `frontend/src/components/chat/ChatInterface.tsx`
- `frontend/next.config.mjs` (rewrite proxy + CSP)
- `frontend/src/app/api/v1/chat/route.ts` (SSE proxy)

### 1.3 Dokumen referensi

- `docs/GOOGLE_OAUTH.md`, `docs/GOOGLE_OAUTH_PLAN.md`
- `docs/ai/api.md`, `docs/ai/known-issues.md` (A.14, A.15, A.16), `docs/ai/deployment.md`

### 1.4 Khawatir yang menjadi fokus

Account takeover, CSRF, token leakage, open redirect, callback replay,
refresh-token reuse, race conditions, guest order ownership, IDOR,
privilege escalation.

---

## 2. Ringkasan Eksekutif

Implementasi Google OAuth **secara keseluruhan sangat solid** untuk lapisan
kriptografi dan validasi identitas: authorization code ditukar server-side
(confidential client) lewat `golang.org/x/oauth2`, id_token diverifikasi dengan
`coreos/go-oidc` (signature RS256 via JWKS dari discovery document, issuer di-pin
ke `https://accounts.google.com`, audience=clientID, expiry), `state` = 32-byte
CSPRNG single-use yang hanya disimpan sebagai hash SHA-256 dan dikonsumsi
atomik, `nonce` diikat ke id_token, PKCE S256 diterapkan penuh, dan hasil login
adalah **sesi Vero normal** (`issueSession`) sehingga rotasi/reuse-detection/
logout/revoke berlaku identik. Bagian tersebut diuji dengan automated tests
(`google_test.go`, `google_oauth_service_test.go`, `google_oauth_callback_test.go`,
`google_auth_handlers_test.go`).

Audit menemukan **0 temuan P0 (Critical)** dan **2 temuan P1 (High)**:

1. **P1-H1 — Bypass anti-merge (TOCTOU) di `resolveUser`.** Fallback setelah
   `CreateUserWithGoogleIdentity` gagal me-resolve user **berdasarkan email**
   tanpa memeriksa `google_sub`, sehingga guard anti-account-takeover
   (`ErrGoogleAccountExists`) bisa dilewati pada jendela lomba (race) antara
   `FindUserByEmail` dan `Create`.
2. **P1-H2 — Flow "Link Google Account" tidak bisa dijalankan dari browser.**
   `GET /auth/google/link` dilindungi `middlewares.Auth` (Bearer di header),
   sedangkan satu-satunya cara browser memulai flow adalah full-page navigation
   yang **tidak pernah** mengirim header Authorization; dan tidak ada satu pun
   komponen frontend yang memanggilnya. Akibatnya jalur linking eksplisit
   (satu-satunya cara pengguna ber-email untuk login Google) mati, dan ada risiko
   laten bila "diperbaiki" dengan sekadar menghapus middleware Auth
   (CSRF account-linking).

Temuan P2 meliputi: tabel `oauth_states` tidak pernah dibersihkan
(`DeleteExpiredOAuthStates` tanpa pemanggil), access token di `localStorage`
dengan CSP FE yang longgar (`'unsafe-inline' 'unsafe-eval'`), kontrak cookie
refresh host-only yang rapuh pada topologi production dwi-domain, dan
enumeration email via kode error `account_exists_link_required`.

Daftar lengkap + rekomendasi ada di Bagian 4; area yang sudah aman di Bagian 5;
urutan implementasi di Bagian 6.

## 3. Analisis per Kontrol

> Verdict: ✅ SECURE (terbukti dari kode + test) · ⚠️ PERLU PERHATIAN (ada catatan) ·
> ❌ WEAK (ada celah, dirinci di Bagian 4).

| # | Kontrol | Verdict | Bukti / catatan |
|---|---|---|---|
| 1 | Authorization Code flow | ✅ | `google.go:64-70,136-157` — exchange server-side via `x/oauth2`, confidential client, `AccessTypeOnline` (tanpa refresh token dari Google). |
| 2 | OpenID Connect validation | ✅ | `google.go:145-190` — `go-oidc` verifier (signature/iss/aud/exp) + issuer pinned eksplisit + `email_verified` wajib true. Library resmi, tanpa crypto manual. |
| 3 | OAuth `state` | ✅ | 32-byte CSPRNG (`google_oauth_service.go:136`), hanya hash SHA-256 disimpan (`:157,444`), single-use atomik (`oauth_repository.go:24-40`), TTL 10 mnt (`:73`). |
| 4 | `nonce` | ✅ | 32-byte CSPRNG (`:140`), disimpan server-side (`:157`), diverifikasi terhadap klaim nonce id_token (`google.go:174-176`). |
| 5 | PKCE | ✅ | `code_verifier` 64-byte CSPRNG (`:148`), challenge S256 (`:210-215`), diserahkan saat exchange `oauth2.VerifierOption` (`google.go:141`). |
| 6 | Token exchange | ✅ | Server-side, secret tidak pernah ke browser; redirect_uri di-replay persis (`callbackRedirectURI`). |
| 7 | Callback | ✅ | State divalidasi dulu; error generik ke klien (`SEC-15`), raw error hanya server. |
| 8 | JWT/session creation | ✅ | Sesi Vero normal via `AuthService.issueSession` — aud `access`/`refresh` dipisah, refresh punya `jti` + row `auth_sessions`. |
| 9 | Refresh token | ✅ | Rotasi atomik (`RotateSession`) + reuse detection (`auth_service.go:140-191`); window 60 detik untuk concurrent refresh (catatan L-5). |
| 10 | Logout | ✅ | `POST /auth/logout` revoke JTI + clear cookie; dipakai juga oleh sesi Google (`customerLogout`). |
| 11 | Token revocation | ✅ | Revoke-per-JTI + `RevokeAllActiveSessionsByUser`; Google access/id token dibuang setelah verifikasi (tidak dipakai). |
| 12 | Account linking | ⚠️ | Desain sudah benar (sub kunci, no auto-merge, `LinkAccount` diikat sesi autentik), **tapi** alur link tidak dapat dijalankan dari browser (P1-H2) dan ada bypass race (P1-H1). |
| 13 | Guest-order claiming | ✅ | `ClaimOrder` dibatasi cookie guest, row-lock + conditional update (`guest_repository.go:85-107`), direplikasi di callback (handler `GoogleCallback:117-121`). |
| 14 | Redirect handling | ✅ | `sanitizeReturnTo` (`:465-473`) — wajib `/`, tolak `//`, tolak CR/LF/backslash; origin FE dari env, bukan request. Edge fragment `#` hanya berdampak UX (P2-M5). |
| 15 | Cookie security | ✅ | HttpOnly, SameSite default Strict, Secure otomatis saat None / production, path scoped `/api/v1/auth` (`cookie.go:17-35`). Catatan topologi dwi-domain (P2-M3). |
| 16 | localStorage usage | ⚠️ | Token akses di `localStorage` (`api.ts:98-110`) + CSP longgar (P2-M2). Refresh token tetap HttpOnly. |
| 17 | Access token exposure | ✅ | Fragment URL, bukan query (`google_auth_handlers.go:127-135`); `OAuthReceiver` langsung membersihkan hash (`history.replaceState`). Catatan ini dihistory singkat (L-1). |
| 18 | Logging | ✅ | `redactSensitiveQuery` (`logging.go:71-99`) redaksi `code`/`state`/token; payload audit aman (`TestGoogleAuditEvents_SafePayloadsOnly`). Catatan raw provider error di server log (L-3). |
| 19 | Rate limiting | ✅ | Grup `/auth` = `AuthRateLimit` 5 rps/IP (`routes.go:40`, `middlewares.go:185`). |
| 20 | Database constraints | ✅ | `UNIQUE(provider, provider_user_id)` (`external_identities`), `idx_users_google_sub` parsial unik, `state_hash` unik. |
| 21 | Concurrency | ✅ | State consume, session rotate, guest claim semuanya pattern UPDATE-atomik single-winner (pola BUG-1). |
| 22 | Race conditions | ⚠️ | Identifikasi satu lubang TOCTOU di fallback create (P1-H1); sisanya sudah ditangani. |
| 23 | Error handling | ✅ | Envelope generik; kode `auth_error` maping terpusat (`OAuthReceiver.oauthErrorMessage`). Catatan enumeration (P2-M4). |
| 24 | Production configuration | ✅ | `Config.Validate()` menolak start: `GOOGLE_OAUTH_ENABLED=true` tanpa kredensial / redirect/FE masih localhost, JWT secret default, DB default. Catatan panjang secret (L-4). |

---
---
## 4. Temuan

### 4.1 P0 — Critical

Tidak ada temuan Critical pada lintasan yang diaudit. Lapisan validasi identitas
OIDC, anti-CSRF (`state`), anti-replay (`nonce` + PKCE + single-use state),
dan anti-account-takeover dasar (no email auto-merge) sudah terpasang dan diuji.

---

### 4.2 P1 — High

#### P1-H1: Bypass guard anti-merge (TOCTOU) di fallback `resolveUser`

- **File**: `backend/internal/services/google_oauth_service.go`
- **Function**: `resolveUser` — cabang fallback error `CreateUserWithGoogleIdentity` (±baris 364–381)
- **Problem**: Saat `CreateUserWithGoogleIdentity` gagal (constraint duplikat
  maupun error lain), fallback melakukan `FindUserByGoogleSub` lalu
  `FindUserByEmail` (baris 370) yang **mengembalikan user apa pun** dengan email
  tersebut tanpa memeriksa `google_sub` — melewati guard `ErrGoogleAccountExists`
  (baris 331–336). Jendela lomba ada di antara `FindUserByEmail` (langkah 2,
  "miss") dan `CreateUserWithGoogleIdentity` (langkah 3, gagal) saat `Register`
  paralel membuat akun password ber-email sama di tengah.
- **Security impact**: **Account takeover.** Google identity yang TIDAK pernah
  di-link eksplisit dapat memperoleh sesi Vero atas akun password ber-email sama
  yang dibuat pada jendela race. Sesi bertahan hingga TTL refresh (default 720
  jam). Melanggar kontrol account-takeover yang menjadi alasan desain
  "tanpa auto-merge".
- **Reproduction scenario**: (1) Attacker memegang akun Google ber-email `E`
  (mis. Workspace domain yang ia kuasai); (2) akun Vero password ber-email `E`
  dibuat berbarengan (konkurensi `Register`); (3) callback Google attacker:
  `FindUserByGoogleSub` → miss, `FindUserByEmail` → miss (akun korban belum
  ter-commit), `Create` → gagal constraint unique email; (4) fallback
  `FindUserByEmail` → hit → user korban dikembalikan → `issueSession` →
  attacker login sebagai korban tanpa link eksplisit.
- **Recommended fix**: Pada cabang fallback setelah `Create` gagal, jangan
  resolve berdasarkan email. Opsi: (a) hanya kembalikan `FindUserByGoogleSub`;
  bila miss, kembalikan `ErrGoogleAccountExists`; (b) bandingkan eksplisit
  `existing.GoogleSub == identity.Subject` sebelum mengembalikan; (c) bungkus
  create dalam transaksi dan bedakan error per constraint.
- **Implementation risk**: Rendah–sedang. Perubahan di satu fungsi + tambahan
  test race. Tidak mengubah skema DB.

#### P1-H2: Flow "Link Google Account" tidak dapat dijalankan dari browser

- **File**: `backend/internal/routes/routes.go:55`, `backend/internal/handlers/google_auth_handlers.go:48-65`
- **Function**: `GoogleLinkStart` + route guard `middlewares.Auth`
- **Problem**: Alur link didokumentasikan sebagai full-page navigation dari
  browser (`window.location.href = /api/v1/auth/google/link?...`). Navigasi
  top-level TIDAK pernah mengirim header `Authorization: Bearer`, sedangkan
  rutenya dilindungi `middlewares.Auth` yang mewajibkan header tersebut.
  Akibatnya browser yang sudah login tetap menerima 401 JSON (bukan 302 ke
  consent Google). Tidak ada komponen frontend customer yang memanggil alur ini
  (pencarian `google/link` tidak menemukan pemakai di `frontend/src`), sehingga
  jalur linking eksplisit **mati**.
- **Security impact**:
  - Availability: pengguna yang emailnya sudah punya akun password menerima
    `account_exists_link_required` dan **tidak pernah** bisa menyelesaikan
    login Google — satu-satunya jalan (link eksplisit) tidak dapat diakses.
  - Risiko laten: bila "diperbaiki" dengan menghapus `middlewares.Auth`, endpoint
    menjadi **CSRF account-linking**: attacker membuat state link miliknya,
    korban menyelesaikan consent Google di browser korban, callback mengikat
    `google_sub` korban ke akun attacker (`LinkAccount` di `:264-274`) —
    account-takeover parsial.
- **Reproduction scenario**: Di browser dengan sesi aktif, buka
  `https://api.vero.com/api/v1/auth/google/link?return_to=/` → middleware Auth
  menolak (tidak ada Bearer di navigasi) → respons 401, bukan redirect Google.
- **Recommended fix**: Pisahkan "intent" dari "navigasi": (1) endpoint JSON
  ber-auth (`POST /auth/google/link-intent`) yang memverifikasi sesi dan
  menerbitkan `link_intent` single-use (terikat user, TTL pendek); (2)
  `GET /auth/google/link?intent=...` → verifikasi intent, buat `OAuthState{LinkUserID}`,
  302 ke Google; (3) atau tambahkan UI customer (halaman akun)
  yang memanggil step (1) sebelum navigasi. **JANGAN** menghapus
  `middlewares.Auth` dari `GoogleLinkStart` sebagai "perbaikan cepat".
- **Implementation risk**: Sedang. Menyentuh desain alur (backend + frontend),
  perlu test baru jalur intent; tidak mengubah skema DB.

---

### 4.3 P2 — Medium

#### P2-M1: Tabel `oauth_states` tidak pernah dibersihkan

- **File**: `backend/internal/repositories/oauth_repository.go:42-49`;
  `interfaces.go:49` — **tidak ada pemanggil produksi** (hanya test)
- **Function**: `DeleteExpiredOAuthStates`
- **Problem**: Setiap `GET /auth/google` menyisipkan satu baris `oauth_states`
  (state hash + nonce + code_verifier plaintext). Baris tidak pernah dihapus:
  `ConsumeOAuthState` hanya menandai `consumed_at`; `DeleteExpiredOAuthStates`
  tidak dipanggil dari mana pun (background ticker `startChatSessionCleanup`
  hanya untuk chat session).
- **Security impact**: Pertumbuhan tabel tak terbatas (index/query melambat,
  bloat disk — DoS jangka panjang). Memperbanyak material rahasia at-rest:
  `code_verifier` dan `nonce` plaintext di DB.
- **Reproduction scenario**: `for i in $(seq 1 1000); do curl -s -o /dev/null
  http://localhost:8080/api/v1/auth/google; done` → `SELECT count(*)
  FROM oauth_states;` terus bertambah, baris expired tidak pernah terhapus.
- **Recommended fix**: Panggil `DeleteExpiredOAuthStates(ctx,
  time.Now().Add(-2*time.Hour))` dari goroutine periodik (pola
  `startChatSessionCleanup` di `main.go`) atau cron; sertakan juga baris
  `consumed_at IS NOT NULL` yang sudah tua.
- **Implementation risk**: Rendah. Penambahan scheduler saja, tanpa ubah skema.

#### P2-M2: Access token di localStorage + CSP frontend longgar

- **File**: `frontend/src/lib/api.ts:95-110` (`CUSTOMER_TOKEN_KEY`),
  `frontend/next.config.mjs:19` (CSP `script-src 'self' 'unsafe-inline' 'unsafe-eval'`)
- **Function**: `setCustomerAccessToken` / `getCustomerAccessToken` / `apiFetch`
- **Problem**: Access token disimpan di `localStorage` yang bisa dibaca
  JavaScript mana pun di origin FE; CSP mengizinkan `'unsafe-inline'` dan
  `'unsafe-eval'` sehingga tiap injeksi skrip langsung berjalan. XSS dapat
  mencuri token dan/atau memanggil `POST /auth/refresh` (cookie dikirim otomatis
  untuk permintaan same-site dari dalam origin) untuk memperpanjang akses tanpa
  batas selama refresh TTL (720 jam).
- **Security impact**: Session hijack bila ada satu titik XSS/skrip tak
  tepercaya di origin FE.
- **Reproduction scenario**: Dari konsol DevTools (setara skrip injected):
  `fetch('/api/v1/auth/refresh',{method:'POST'})` → respons berisi
  `access_token` baru → token valid 15 mnt untuk semua API.
- **Recommended fix**: (a) Pindah access token ke memory-only, boot ulang flow
  menarik ulang dari refresh cookie via `ensureCustomerSession`; (b) perketat
  CSP (hapus `'unsafe-inline'`/`'unsafe-eval'`, pakai nonce/hash); (c)
  pertimbangkan memperpendek TTL refresh untuk sesi consumer.
- **Implementation risk**: Sedang (perubahan pola penyimpanan FE; uji boot
  flow, SSE stream, tab paralel).

#### P2-M3: Kontrak cookie refresh host-only rapuh pada topologi dwi-domain

- **File**: `backend/internal/auth/cookie.go:17-35` (`SetRefreshCookie`),
  `backend/internal/handlers/google_auth_handlers.go:125`, `frontend/src/lib/api.ts:155-176`
- **Function**: `SetRefreshCookie` / `GoogleCallback` / `ensureCustomerSession`
- **Problem**: Cookie refresh tidak menetapkan atribut `Domain` (host-only).
  Callback Google adalah navigasi langsung ke origin backend
  (`GOOGLE_REDIRECT_URI`), sehingga `Set-Cookie` tersimpan untuk host backend;
  sementara seluruh panggilan API frontend dikirim lewat rewrite/route handler
  ber-origin FE dan browser hanya mengirim cookie milik origin FE → cookie
  refresh tidak ikut → `POST /auth/refresh` selalu 401. Di dev tertutup karena
  FE dan BE dianggap satu origin (`localhost` + rewrite `next.config.mjs`), jadi
  bug muncul di topologi terpisah.
- **Security impact**: Availability — setelah login Google, sesi mati dalam 15
  menit dan tidak bisa di-refresh; user jatuh ke mode guest/anonim (order
  berikutnya bisa tidak ter-attribusi ke akun; guard
  `GUEST_ORDER_LIMIT_REACHED` muncul).
- **Reproduction scenario**: Deploy FE `app.vero.com` + BE `api.vero.com`
  (rewrite proxy), `GOOGLE_REDIRECT_URI=https://api.vero.com/...`, `GOOGLE_OAUTH_FRONTEND_URL=https://app.vero.com`
  → login Google sukses; 15 menit kemudian `ensureCustomerSession()` selalu
  `"anonymous"` (cookie `refresh_token` hanya ada di `api.vero.com`).
- **Recommended fix**: Tetapkan kontrak topologi eksplisit di `deployment.md`:
  (a) SATU origin FE+BE (pola dev dipertahankan di produksi) — teraman; atau
  (b) bila BE subdomain terpisah, tetapkan `Domain` cookie (mis. `.vero.com`) +
  `JWT_COOKIE_SAME_SITE=None` + `Secure`.
- **Implementation risk**: Rendah–sedang; sebagian besar keputusan deployment.
  Opsi (b) menyentuh `cookie.go` (Domain configurable) dan perlu diuji.

#### P2-M4: Enumeration akun lewat kode error `account_exists_link_required`

- **File**: `backend/internal/handlers/google_auth_handlers.go:92-96`,
  `frontend/src/components/auth/OAuthReceiver.tsx:63-64`
- **Function**: `GoogleCallback` (pemetaan error) / `oauthErrorMessage`
- **Problem**: Callback mengembalikan kode error spesifik yang mengungkap bahwa
  sebuah email sudah punya akun Vero (`account_exists_link_required`), berbeda
  dari kode generik `authentication_failed`.
- **Security impact**: Enumeration akun (validasi keberadaan email di Vero),
  bahan spear-phishing / bruteforce terarah. Dampak rendah karena memicu kode
  ini mensyaratkan consent Google untuk email yang attacker kuasai.
- **Reproduction scenario**: Attacker dengan akun Google ber-email `X@domain`
  yang ia kuasai → login Google → redirect FE berisi
  `?auth_error=account_exists_link_required` → dipastikan `X@domain` pengguna Vero.
- **Recommended fix**: Pertahankan kategori detail di log/audit server-side;
  di URL gunakan kode generik (`authentication_failed`) dan tampilkan pesan
  link-akun hanya di UI setelah alur link tersedia.
- **Implementation risk**: Rendah (ubah pemetaan kode + test handler).

#### P2-M5: Allowlist `return_to` berbasis path; fragment `#` memecah penyampaian token

- **File**: `backend/internal/services/google_oauth_service.go:465-473`,
  `backend/internal/handlers/google_auth_handlers.go:127-135`
- **Function**: `sanitizeReturnTo` / `GoogleCallback` (pembentukan target)
- **Problem**: Guard sudah menolak open redirect (wajib `/`, tolak `//`, tolak
  `\`/CR/LF; origin FE dari env). Namun (1) semua path absolut diterima tanpa
  cek halaman yang benar-benar ada; (2) `return_to` berisi `#` menghasilkan URL
  final `frontendURL + "/x#y" + "#access_token=..."` — browser memperlakukan
  semua setelah `#` pertama sebagai fragment, `OAuthReceiver` tidak menemukan
  `access_token`, token asli hilang.
- **Security impact**: Tidak ada open redirect (sudah diuji). Dampak UX/
  availability (login sukses di backend tapi token tidak tersampaikan) dan
  marker `google_linked=1` bisa salah tempat saat `return_to` memuat `#`.
- **Reproduction scenario**: Buka `/api/v1/auth/google?return_to=%2Ftrip%2F1%23annex`
  → URL FE menjadi `/trip/1#annex#access_token=...` → token tidak tersimpan.
- **Recommended fix**: Pada `sanitizeReturnTo`, tolak/strip `#` dari input;
  opsional terapkan allowlist prefix halaman yang dikenal.
- **Implementation risk**: Rendah (fungsi murni + test unit sudah ada).

---

### 4.4 P3 — Low

- **L-1 — Token tampil singkat di address bar.** `google_auth_handlers.go:127-135`
  menaruh access token di fragment redirect final; `OAuthReceiver`
  (`OAuthReceiver.tsx:28`) membersihkannya via `history.replaceState`, tapi ada
  jendela singkat token terlihat di UI/riwayat sebelum JS berjalan. Risiko
  shoulder-surfing/screenshot; fragment tidak pernah dikirim ke server.
  Rekomendasi: praktik saat ini cukup untuk MVP; pertimbangkan halaman
  perantara yang langsung `history.replaceState` sebelum konten dirender.

- **L-2 — Tanpa verifikasi `azp`/`auth_time`/`max_age`.** `google.go:83-86`
  memakai `oidc.Config{ClientID}` (aud dijamin = clientID). Google access/id
  token tidak dipakai untuk API Google, maka `at_hash`/`azp` tidak diperlukan;
  `auth_time`/`prompt=login` bukan kebutuhan bisnis saat ini. Kategorikan
  sebagai keputusan desain, bukan bug.

- **L-3 — Log handler memuat detail error provider.** `google_auth_handlers.go:90`
  `log.Printf("[google-callback] failed: %v", err)`; error
  `*oauth2.RetrieveError` dapat membawa status + body respons provider. Tidak
  memuat token/secret, tapi hindari mendorong detail HTTP ke agregator log
  pihak ketiga — pakai reason kategori seperti `logGoogleLoginFailed`.

- **L-4 — Validasi kekuatan `JWT_SECRET` minim.** `config.go:181-183` hanya
  menolak default; secret lemah lolos saat `APP_ENV=production`. Rekomendasi:
  min. 32 karakter, dan pertimbangkan RS256 (asymmetric) untuk produksi besar.

- **L-5 — Window reuse-detection 60 detik.** `auth_service.go:31,179-185` —
  reuse dalam 1 menit setelah rotasi dianggap "concurrent refresh" dan tidak
  memicu `RevokeAllActiveSessionsByUser`. Trade-off wajar (tab paralel), tapi
  pertimbangkan binding device/IP/UA pada sesi untuk deteksi anomali.

- **L-6 — Tidak ada fitur unlink/revoke identitas Google.** `ExternalIdentity`
  hanya bisa ditautkan (`LinkUserGoogleSub`); tidak ada endpoint melepas
  tautan. Akun Google yang disusupi tidak bisa diputus koneksinya tanpa dukungan
  admin. Rekomendasi: `DELETE /auth/google/link` + audit event.

- **L-7 — Refresh cookie tidak diikat ke IP/device/UA.** Cookie HttpOnly
  berlaku hingga 720 jam; pencurian cookie (malware/non-TLS) bisa dipakai
  diam-diam. Rekomendasi: metadata sesi + deteksi anomali.

- **L-8 — `nonce` dan `code_verifier` plaintext di DB.** Kolom `nonce` dan
  `code_verifier` tersimpan apa adanya di `oauth_states`. Dapat diterima untuk
  state server-side; perkuat dengan hapus baris setelah consume (P2-M1) atau
  enkripsi kolom verifier.

- **L-9 — Dependensi `http` localhost di dev.** Default `GOOGLE_REDIRECT_URI`
  = `http://localhost:8080/...` wajar untuk lokal; produksi dicegah
  `Config.Validate()` (menolak `localhost`).

---

## 5. Area yang Sudah Aman (Already-Secure)

Berikut dikonfirmasi dari kode aktual (bukan klaim dokumen):

1. **Kriptografi OIDC bebas manual** — `google.go` memakai `x/oauth2` untuk
   exchange dan `go-oidc` untuk verifikasi RS256 via JWKS dari discovery
   document Google (issuer di-pin `https://accounts.google.com`), memvalidasi
   signature, `iss`, `aud=clientID`, `exp`, plus cek eksplisit issuer,
   nonce, dan `email_verified`.
2. **Anti-CSRF `state`**: 32-byte CSPRNG, hanya SHA-256 tersimpan, single-use
   atomik (`ConsumeOAuthState` dengan `UPDATE ... WHERE consumed_at IS NULL`),
   TTL 10 menit.
3. **Anti-replay `nonce`** diikat ke id_token; **PKCE S256** dengan
   `code_verifier` 64-byte CSPRNG server-side.
4. **Confidential client** — client secret hanya di server; scope minimal
   `openid email profile`; `AccessTypeOnline` (tidak ada refresh token Google
   yang bisa bocor ke sisi klien).
5. **Sesi Vero identik** — login Google melewati `AuthService.issueSession`;
   refresh rotation atomik, reuse detection, logout, dan revoke berlaku sama
   (dikunci test `TestGoogleSession_EquivalentToPasswordLogin`).
6. **No auto email-merge** — guard `ErrGoogleAccountExists` secara normal
   menolak merge; tautan identitas dikunci oleh `sub` via
   `UNIQUE(provider, provider_user_id)`.
7. **Account linking aman** — `LinkAccount` hanya dari state ber-
   `link_user_id` server-generated + guard `ErrGoogleIdentityTaken`.
8. **Guest order claim** — dibatasi cookie `vero_guest_session` (HttpOnly),
   row-lock + conditional update, sekali-claim.
9. **Open redirect** — `sanitizeReturnTo` menolak URL non-`/`, `//`, CR/LF,
   backslash; origin FE dari env.
10. **Token leakage** — access token di fragment (bukan query), refresh token
    hanya cookie HttpOnly; `redactSensitiveQuery` menutup `code`/`state`/token
    dari log; payload audit aman (test `TestGoogleAuditEvents_SafePayloadsOnly`).
11. **Cookie flags** — HttpOnly, SameSite default Strict, Secure (otomatis saat
    `None`/production), path scoped `/api/v1/auth`.
12. **IDOR** — `/bookings/:id` scope owner (`FindBookingForUser`),
    `/orders/:id` scope guest (`FindBookingForGuest`), staff-only untuk listing.
13. **Rate limit** — grup `/auth` 5 rps/IP; global 20 rps; write publik 5
    r/mnt; in-memory limiter dibatasi 10k entri + janitor.
14. **Production guard** — `Config.Validate()` menolak start tanpa kredensial,
    secret default, DB default, atau redirect/FE localhost saat
    `GOOGLE_OAUTH_ENABLED=true`.
15. **Fail-closed feature-flag** — `GOOGLE_OAUTH_ENABLED=false` → endpoint 404
    tanpa dependensi jaringan.
16. **Error disclosure** — kode `auth_error` generik ke klien; raw error hanya
    di server (SEC-15).
17. **Sesi tamper** — JWT HS256 `WithValidMethods` menolak `alg:none`, aud
    access/refresh dipisah (`RefreshTokenUsedAsAccess` /
    `AccessTokenUsedOnRefresh` di-audit).

---

## 6. Urutan Implementasi yang Disarankan

> Ini rekomendasi saja — fase implementasi yang sebenarnya harus menunggu
> instruksi eksplisit dan tidak termasuk audit ini.

| Urutan | Item | Alasan prioritas | Effort perkiraan |
|---|---|---|---|
| 1 | **P1-H1** — tutup fallback `resolveUser` agar tidak me-resolve by email | Perbaikan kontrol account-takeover yang sudah ada; murah, lokal, tidak mengubah skema | Kecil (1 fungsi + test) |
| 2 | **P1-H2** — rancang ulang mulai alur link (intent server-side) atau pasang UI + jalur bertoken; JANGAN menghapus Auth guard | Menghidupkan jalur linking yang aman; mencegah "perbaikan" berbahaya yang membuka CSRF linking | Sedang (backend + FE + test) |
| 3 | **P2-M1** — panggil `DeleteExpiredOAuthStates` periodik | Batasi pertumbuhan tabel + kurangi rahasia at-rest | Kecil (scheduler) |
| 4 | **P2-M3** — putuskan & dokumentasikan topologi production (satu origin atau Domain cookie) | Mencegah sesi Google mati 15 menit setelah deploy produksi | Konfigurasi/deploy + dokumentasi |
| 5 | **P2-M2** — pindahkan access token ke memory / perketat CSP | Kurangi dampak XSS terhadap sesi | Sedang (FE) |
| 6 | **P2-M4** — generikkan kode error enumerasi di URL | Tutup kebocoran keberadaan akun | Kecil |
| 7 | **P2-M5** — tolak `#` di `return_to` (+ allowlist opsional) | Perbaikan determinisme pengiriman token | Kecil |
| 8 | **P3** — L-4 (panjang secret), L-6 (unlink), L-7 (session meta), L-3 (log detail provider) | Hardening tambahan | Beragam |

Prioritas ulang bila ditemukan indikasi penyalahgunaan aktual: naikkan P1-H1
menjadi perbaikan darurat dan audit jejak `external_identities`/`auth_sessions`
untuk anomali.

---

## 7. Laporan Akhir

### Critical (P0)

- Tidak ada.

### High (P1)

- **P1-H1** — Bypass TOCTOU guard anti-merge di `resolveUser`
  (`google_oauth_service.go:364-381`): fallback `FindUserByEmail` mengembalikan
  user password tanpa verifikasi `google_sub` → potensi account takeover pada
  jendela race.
- **P1-H2** — Alur "Link Google Account" mati: `middlewares.Auth` (Bearer) vs
  full-page navigation yang tidak pernah mengirim Bearer (`routes.go:55`);
  tanpa UI pemanggil; dan ada risiko publish CSRF linking bila Auth dihapus.
  Perbaikan cepat yang salah = lubang baru.

### Medium (P2)

- **P2-M1** — `oauth_states` tidak pernah dibersihkan (pertumbuhan tak terbatas;
  `code_verifier`/nonce at-rest).
- **P2-M2** — Access token di `localStorage` + CSP `'unsafe-inline'/'unsafe-eval'`.
- **P2-M3** — Cookie refresh host-only vs topologi production dwi-domain
  (refresh pasca-login Google gagal di produksi bila tidak satu origin).
- **P2-M4** — Enumeration akun via kode error `account_exists_link_required`.
- **P2-M5** — `return_to` memuat `#` memecah penyampaian token; allowlist path
  longgar.

### Low (P3)

- L-1 token singkat di address bar; L-2 tanpa `azp`/`auth_time` (by design);
  L-3 detail provider di log server; L-4 validasi panjang JWT secret;
  L-5 window reuse-detection 60 detik; L-6 tidak ada unlink Google;
  L-7 cookie tidak terikat device/IP; L-8 nonce/verifier plaintext at-rest;
  L-9 http localhost untuk dev (sudah dicegah di produksi).

### Already-secure

Lihat Bagian 5: validasi OIDC library-resmi, state/nonce/PKCE, confidential
code exchange, sesi Vero identik dengan rotasi/reuse-detection, no auto-merge
(design), akun-linking di-start dari sesi terautentikasi, guest claim atomik,
open-redirect guard, redaksi query log, cookie flags, IDOR scoping, rate
limiting, fail-closed feature flag, dan production config validation.

### Catatan metodologi

- Audit bersifat **read-only**; tidak ada kode yang diubah. Satu-satunya
  artefak baru adalah dokumen ini.
- Klaim dibuat berdasarkan pembacaan kode aktual + automated tests yang ada;
  tidak ada verifikasi runtime terhadap environment produksi (tidak ada akses).
- Temuan P1-H1/H-2 membutuhkan keputusan desain sebelum diperbaiki; P2-M3
  membutuhkan keputusan deployment.

---
