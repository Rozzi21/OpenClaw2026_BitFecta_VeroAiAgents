# Guest Order System — Security Audit (READ-ONLY)

> Status audit: **Read-only**. Tidak ada kode, dependency, skema DB, atau
> migrasi yang diubah. Dokumen ini hanya menginventarisasi cara kerja aktual
> sistem guest order beserta kelemahannya. Semua rekomendasi **belum**
> dieksekusi.
>
> Tanggal audit: 3 Sep 2026. Commit yang diaudit: `5b46a32`.
> Verifikasi: `go build ./...` OK; `go test ./internal/services -run
> 'Guest|Idempotent|Concurrent|Authenticated' -race -count=1` OK.

> **Status perbaikan (diperbarui 4 Sep 2026 — dokumen audit tetap apa adanya):**
> **GO-P0-1 FIXED** (jangkar kontak `guest_order_entitlements`).
> **GO-P3-3 FIXED** + **GO-P1-3 SEBAGIAN**: `guest_sessions.claimed_user_id` /
> `claimed_at` ditulis dalam transaksi claim, `GuestService.ClaimOrder`
> mengembalikan `(GuestOrderClaimResult, error)` dengan sentinel
> `ErrGuestOrderNothingToClaim` / `ErrGuestOrderClaimConflict` /
> `ErrGuestOrderClaimUnauthenticated` plus audit event terpisah
> (`guest_order_claim_replayed` / `_conflict` / `_failed`); claim ulang oleh akun
> yang sama = no-op sukses, akun lain = penolakan eksplisit. Sifat "kepemilikan
> diputuskan sekali" TIDAK dilonggarkan (§7 aturan sekuensing tetap dipatuhi):
> yang jadi idempoten adalah HASIL panggilan, bukan transfernya. **Belum
> dikerjakan** dari GO-P1-3: jalur retry claim (endpoint/hook re-claim). Detail:
> `docs/GUEST_ORDER_LIMIT.md` §"Claim order guest",
> `docs/ai/known-issues.md` A.18.

---

## 1. Ruang Lingkup

### 1.1 Backend

| Area | File |
|---|---|
| Guest identity (resolve / authenticate / claim) | `backend/internal/services/guest_service.go` |
| Persistensi guest + booking transaksional | `backend/internal/repositories/guest_repository.go` |
| Policy order + pricing + idempotency | `backend/internal/services/booking_service.go` |
| Handler order guest | `backend/internal/handlers/booking_handlers.go` |
| Handler chat guest + resolve chat session | `backend/internal/handlers/chat_handlers.go`, `chat_stream_handlers.go` |
| Claim pasca-auth | `backend/internal/handlers/auth_handlers.go`, `google_auth_handlers.go` |
| MCP `create_booking` | `backend/internal/services/mcp_service.go` |
| Ownership sesi chat | `backend/internal/services/ai_service.go` |
| Guest user terisolasi | `backend/internal/services/auth_service.go` |
| Google OAuth callback / `resolveUser` | `backend/internal/services/google_oauth_service.go` |
| Cookie | `backend/internal/auth/cookie.go` |
| Model | `backend/internal/models/models.go` (`GuestSession`, `ChatSession`, `Booking`) |
| Rute + middleware | `backend/internal/routes/routes.go`, `internal/middlewares/middlewares.go` |
| Skema/migrasi | `backend/internal/database/database.go`, `backend/migrations/20260818_guest_order_limit.sql` |
| Config | `backend/internal/config/config.go` |
| Test | `backend/internal/services/guest_order_limit_test.go` |

### 1.2 Frontend customer

- `frontend/src/app/api/v1/chat/route.ts` (proxy SSE)
- `frontend/src/lib/api.ts`, `frontend/src/lib/authToken.ts`
- `frontend/src/app/trip/[id]/page.tsx`, `frontend/src/app/order/[id]/page.tsx`
- `frontend/src/components/chat/ChatInterface.tsx`
- `frontend/next.config.mjs`

### 1.3 Dokumen referensi

- `docs/GUEST_ORDER_LIMIT.md`, `docs/GUEST_ORDER_LIMIT_PLAN.md`
- `docs/GOOGLE_OAUTH_SECURITY_AUDIT.md` (khusus **P1-H1**, TOCTOU `resolveUser`)
- `docs/ai/database.md`, `docs/ai/api.md`, `docs/ai/backend.md`, `docs/ai/known-issues.md`

---

## 2. Ringkasan Eksekutif

Lapisan **penegakan** (enforcement) sudah benar dan atomik: satu transaksi
PostgreSQL per create, `SELECT ... FOR UPDATE` pada baris guest, lalu
`UPDATE ... WHERE order_count = 0` bersyarat dengan `RowsAffected == 1`.
Harga dihitung server-side, pax dibatasi di service (bukan hanya DTO), ownership
order diverifikasi lewat `bookings.guest_session_id`, dan claim pasca-login
bersifat atomik + sekali pakai. Untuk **satu identitas guest**, limit satu order
tidak dapat dilewati oleh refresh browser, localStorage, tab ganda, chat session
baru, maupun request konkuren.

Masalahnya bukan pada penegakan, tapi pada **identitas**: satu-satunya jangkar
identitas guest adalah cookie `vero_guest_session`, dan `GuestService.Resolve`
akan **mencetak identitas baru (allowance baru) secara diam-diam** setiap kali
request datang tanpa cookie yang valid. Membuang cookie itu — devtools, mode
privat, atau `curl` tanpa cookie jar — memberi allowance segar tanpa batas.
Jadi kontrolnya adalah "satu order per cookie", bukan "satu order per pengunjung
yang belum login".

Audit menemukan **1 temuan P0 (Critical)** dan **3 temuan P1 (High)**:

1. **GO-P0-1 — Identitas guest sepenuhnya dapat dibuang klien; limit bisa
   direset tanpa batas.** Tidak ada jangkar sekunder (IP, kontak, device) dan
   tidak ada dedup pada tingkat order. Satu perintah `curl` tanpa cookie
   menghasilkan order guest baru berikut baris `users` + `guest_sessions` baru.
2. **GO-P1-1 — Proxy SSE Next.js membuang header `Authorization`,** sehingga
   pelanggan yang SUDAH login tetap diperlakukan sebagai guest pada satu-satunya
   jalur chat yang dipakai UI. Upgrade eligibility yang didokumentasikan di
   `docs/ai/known-issues.md` (27 Agu 2026) mati di praktiknya.
3. **GO-P1-2 — TTL cookie dan TTL baris DB tidak sinkron:** cookie diperbarui
   (sliding) setiap request, `guest_sessions.expires_at` tidak → setelah 30 hari
   identitas berotasi diam-diam, allowance kembali segar, dan order lama
   kehilangan jalur akses guest.
4. **GO-P1-3 — Kegagalan `ClaimOrder` ditelan (hanya di-log)** di ketiga call
   site, tanpa jalur retry, sehingga order bisa tertinggal permanen pada guest
   user sekali-pakai — pelanggan kehilangan akses order dan tetap terblokir.

Temuan P2 mencakup pertumbuhan tabel tanpa batas (satu `users` + satu
`guest_sessions` per identitas, tanpa job cleanup), migrasi SQL guest order yang
**tidak** terhubung ke `AutoMigrate` (partial unique index + `CHECK` tidak
pernah ada di DB baru), idempotency yang tidak concurrency-safe (HTTP 500 alih
alih replay), scope idempotency yang bergeser melewati batas claim, guard
duplikat MCP yang terikat chat session dan window 200 pesan, footgun
`GUEST_COOKIE_SAME_SITE`, rebinding `chat_sessions.guest_session_id` tanpa cek
kepemilikan, audit log tanpa IP/UA, dan rate limit per-IP yang masih in-memory
single-instance.

Interaksi dengan **P1-H1** (TOCTOU `resolveUser`) dibahas terpisah di Bagian 6:
lubang itu bukan hanya soal account takeover — ia juga merupakan jalur suntik
order ke akun korban dan jalur bypass limit guest.

---
## 3. Jawaban Langsung atas 12 Pertanyaan

| # | Pertanyaan | Jawaban ringkas |
|---|---|---|
| 1 | Bagaimana guest diidentifikasi? | Cookie HttpOnly `vero_guest_session` = token opaque 256-bit; DB menyimpan `sha256` saja (`guest_sessions.token_hash`). **Tidak ada** jangkar lain. |
| 2 | Bagaimana order guest disimpan? | Baris `bookings` normal + `guest_session_id` (nullable) + `user_id` = user guest sekali-pakai yang di-generate saat identitas dibuat. |
| 3 | Bagaimana limit ditegakkan sekarang? | `guest_sessions.order_count` di dalam SATU transaksi: `LockGuestSession` (`FOR UPDATE`) → cek `OrderCount >= 1` → insert booking → `ConsumeGuestOrder` (`WHERE order_count = 0`, `RowsAffected == 1`). |
| 4 | Apakah limit backend-authoritative? | **Penegakannya ya, identitasnya tidak.** Keputusan 100% di DB/service; tapi identitas yang jadi kunci penegakan dipilih klien dan bisa dibuang (GO-P0-1). |
| 5 | Apakah ChatSession baru mereset limit? | **Tidak.** Allowance ada di `guest_sessions`, bukan `chat_sessions`. Tapi chat session baru **mereset guard duplikat MCP dan scope idempotency** (GO-P2-5). |
| 6 | Apakah refresh browser bisa bypass? | **Tidak.** Cookie persisten (Max-Age 720 jam) + state server-side. |
| 7 | Apakah localStorage bisa bypass? | **Tidak.** Tidak ada entitlement di localStorage; hanya access token. Token palsu ditolak karena verifikasi tanda tangan di `OptionalAuth`. |
| 8 | Apakah multi-tab bisa bypass? | **Tidak.** Semua tab berbagi cookie yang sama; row lock + conditional update menyerialisasi request. |
| 9 | Bagaimana ownership order guest diverifikasi? | `FindBookingForGuest`: `WHERE id = ? AND guest_session_id = ?` setelah cookie dicocokkan ke baris guest. UUID saja tidak cukup. |
| 10 | Bagaimana order guest di-claim setelah auth? | `ClaimGuestOrder` dalam transaksi: lock baris guest → `UPDATE bookings SET user_id=?, guest_session_id=NULL WHERE id=? AND guest_session_id=?` → sekali pakai. Dipanggil dari Register, Login, dan Google callback. |
| 11 | Apakah request konkuren bisa bypass? | **Tidak untuk satu identitas guest** (conditional update menang/kalah bersih; yang kalah di-rollback). Konkurensi tidak dibutuhkan untuk bypass — cukup buang cookie. |
| 12 | Apakah idempotency ada? | Ada dan wajib (`Idempotency-Key`, 16–200 char, hash `sha256(prefix+ownerID+key)`, unique index). Tapi **tidak concurrency-safe** dan **scope-nya bergeser saat claim** (GO-P2-3, GO-P2-4). |


### 3.1 Detail per pertanyaan

**(1) Identifikasi guest.** `auth.SetGuestIdentityCookie` (`cookie.go:105-110`)
menulis cookie `vero_guest_session` pada path `/api/v1`, `HttpOnly=true`,
`Secure` dari `GUEST_COOKIE_SECURE` (default = `APP_ENV == "production"`),
`SameSite` dari `GUEST_COOKIE_SAME_SITE` (default `Lax`), Max-Age =
`GUEST_IDENTITY_TTL_HOURS` (default 720). Token dibuat di
`GuestService.Resolve` (`guest_service.go:52-73`): 32 byte `crypto/rand` →
hex 64 char; hanya `HashGuestToken(token)` = SHA-256 hex yang disimpan
(`models.go:216`, uniqueIndex). Kebocoran DB tidak menghasilkan kredensial
bearer. Tidak ada pengikatan ke IP, User-Agent, device, atau kontak.

**(2) Penyimpanan order guest.** `BookingService.create`
(`booking_service.go:133-155`) menulis satu baris `bookings` dengan
`GuestSessionID = &guestID`, `UserID` = user guest, `BookingStatus = pending`,
`PaymentStatus = pending_admin_processing`, `TotalPrice` dihitung server-side
via `priceBreakdown`, plus `IdempotencyKeyHash`. `AuthService.GuestUser`
(`auth_service.go:237-258`) membuat **user baru per identitas guest** —
`guest-<uuid v4>@vero.local`, password bcrypt acak dari `crypto/rand` — semata
agar `bookings.user_id NOT NULL` terpenuhi. User itu tidak pernah bisa login.

**(3) Penegakan limit.** Semua di dalam `repo.WithBookingTransaction`
(`booking_service.go:73-162`): `FindBookingByIdempotency` → `LockGuestSession`
(gagal ⇒ `ErrGuestSessionInvalid`) → `if guest.OrderCount >= 1` ⇒ lookup
idempotency sekali lagi, lalu `ErrGuestOrderLimitReached` → validasi
trip/pax/kontak/tanggal/kapasitas → `CreateBooking` → `ConsumeGuestOrder`.
Kegagalan `ConsumeGuestOrder` dipetakan ke `ErrGuestOrderLimitReached` dan
mengembalikan error dari callback transaksi, sehingga **insert booking ikut
di-rollback**. Handler memetakan ke HTTP 403 + `error.code =
GUEST_ORDER_LIMIT_REACHED` (`booking_handlers.go:50-53`); MCP mengembalikan
`code` yang sama secara terstruktur (`mcp_service.go:809-813`).

**(4) Otoritas.** Tidak ada satu pun keputusan limit yang bergantung pada
frontend, AI, atau MCP: `mcp_service.go:790-806` hanya memilih *identitas*
(`Create` untuk userID non-nil, `CreateGuest` untuk nil); policy tetap di
`BookingService`. Yang tidak authoritative adalah pemilihan identitas guest
itu sendiri — lihat GO-P0-1.

**(5) ChatSession baru.** `chat_sessions.guest_session_id` (`models.go:230`)
hanya link; allowance ada di `guest_sessions`. Membuat chat baru
(`ResolveGuestSession`, `ai_service.go:941-954`) lalu `AttachChat` mengikat chat
baru ke identitas guest yang sama, jadi limit tetap. Yang ter-reset:
`findSessionOrder` (guard duplikat, `mcp_service.go:742`) dan kunci idempotency
MCP (`"mcp:"+sessionID+...`, `mcp_service.go:787`) — keduanya per-chat-session.

**(6) Refresh browser.** Tidak ada state entitlement di klien. Cookie punya
Max-Age (bukan session cookie), jadi bertahan melewati refresh dan restart
browser.

**(7) localStorage.** `authToken.ts` hanya menyimpan access token (dengan batas
panjang). Entitlement tidak pernah disimpan di sana. Menanam token palsu tidak
membantu: `middlewares.OptionalAuth` (`middlewares.go:252-269`) memverifikasi
tanda tangan + audience `access`; refresh token yang disodorkan di sini
diabaikan, bukan diterima. Token **valid milik akun mana pun** memang melewati
limit guest — itu perilaku yang disengaja.

**(8) Multi-tab.** Cookie di-share per-origin. Dua tab menembak `POST /orders`
bersamaan menabrak baris guest yang sama; `UPDATE ... WHERE order_count = 0`
mengunci baris dan mengevaluasi ulang predikat setelah menunggu, jadi tepat satu
yang mendapat `RowsAffected == 1`.

**(9) Ownership.** `GuestGetOrder` (`booking_handlers.go:68-84`) memanggil
`Guests.Authenticate` (fail-closed: token kosong/tak dikenal ⇒
`ErrGuestSessionInvalid`) lalu `FindBookingForGuest`
(`guest_repository.go:73-77`) yang menyaring `id = ? AND guest_session_id = ?`.
Guest lain / UUID tebakan mendapat 404. Setelah claim, `guest_session_id`
menjadi NULL sehingga jalur guest berhenti berlaku dan
`GET /api/v1/bookings/:id` (Bearer, `FindBookingForUser`) yang berlaku.

**(10) Claim.** `GuestService.ClaimOrder` (`guest_service.go:93-104`):
`Authenticate(cookie)` → jika gagal atau `FirstOrderID == nil` ⇒ no-op nil →
`ClaimGuestOrder` (`guest_repository.go:85-107`) lock baris guest, conditional
UPDATE, `RowsAffected != 1` ⇒ `gorm.ErrRecordNotFound`. Dipanggil di
`auth_handlers.go:29-33` (Register), `:47-51` (Login), dan
`google_auth_handlers.go:117-121` (callback Google). `order_count` **tidak**
di-reset setelah claim — sengaja fail-closed.

**(11) Konkurensi.** Untuk satu identitas: aman (lihat 8). `-race` pada
`TestConcurrentGuestOrdersCreateOnlyOne` lulus, tapi test memakai SQLite
in-memory dengan `SetMaxOpenConns(1)` (`guest_order_limit_test.go:23-39`),
sehingga serialisasi datang dari koneksi tunggal dan `FOR UPDATE` tidak pernah
benar-benar diuji (GO-P3-6). Jaminannya tetap berdiri di Postgres karena
conditional `UPDATE` sudah cukup pada isolasi READ COMMITTED.

**(12) Idempotency.** Wajib untuk kedua jalur (`booking_service.go:63-65`).
Hash = `sha256("guest:"|"user:" + ownerID + ":" + key)` (`:45-52`), disimpan di
`bookings.idempotency_key_hash` dengan unique index. Retry dengan key sama
mengembalikan booking yang sama, termasuk setelah allowance habis (lookup
dijalankan sebelum DAN sesudah cek limit). MCP menurunkan key deterministik
dari `sessionID` + SHA-256 payload JSON (`json.Marshal` pada map mengurutkan
key, jadi deterministik). Batasannya: race dua request identik menghasilkan 500,
dan hash berubah begitu order pindah kepemilikan.

---

## 4. Temuan

### 4.1 P0 — Critical

#### GO-P0-1: Identitas guest sepenuhnya dapat dibuang klien — limit satu order dapat direset tanpa batas

> **Status 4 Sep 2026: DIPERBAIKI** (opsi (a) — jangkar kedua berbasis kontak).
> `guest_order_entitlements` (`contact_key` unique = `sha256("<channel>:<kontak
> ternormalisasi>")`) dikonsumsi di transaksi booking yang sama, sehingga cookie
> yang dibuang tidak lagi mencetak allowance baru. Opsi (b)/(c)/(d) TIDAK
> dikerjakan: `POST /orders` tetap boleh mencetak identitas (agar pengguna baru
> yang sah tidak terputus), `guest_identity_created` belum di-emit (GO-P2-8), dan
> verifikasi kontak (OTP) tetap keputusan produk. Sisa celah: pengunjung dengan
> email DAN telepon yang benar-benar berbeda tetap dapat satu order.
> Detail + test: `docs/GUEST_ORDER_LIMIT.md` §"Jangkar kontak".

- **File**: `backend/internal/services/guest_service.go:37-76` (`Resolve`);
  `backend/internal/handlers/booking_handlers.go:36-48` (`GuestCreateOrder`);
  `backend/internal/handlers/chat_handlers.go:53-62` (`GuestChat`)
- **Function**: `GuestService.Resolve` — cabang "cetak identitas baru"
- **Problem**: `Resolve` mencoba menemukan baris guest dari token cookie; jika
  token kosong, tidak dikenal, atau kedaluwarsa, ia **langsung membuat
  `GuestSession` baru** (`order_count = 0`) tanpa sinyal lain apa pun dan tanpa
  audit event. Karena `guest_sessions` adalah satu-satunya tempat allowance
  hidup, "satu order per guest" secara efektif berarti "satu order per cookie
  yang klien mau simpan". Tidak ada jangkar sekunder (IP, fingerprint,
  device), tidak ada deduplikasi pada tingkat order (`contact_email` /
  `contact_phone` / `trip_id` + `travel_date` tidak pernah dicek terhadap order
  guest lain), dan `POST /api/v1/orders` sama sekali tidak memerlukan chat
  session atau bukti interaksi sebelumnya.
- **Security impact**: **Bypass total atas kontrol bisnis.** Konsekuensi:
  - Antrean pemrosesan manual (`payment_status = pending_admin_processing`,
    pembayaran memang dinonaktifkan) dapat dibanjiri order palsu yang tampak
    sah; operator tidak punya sinyal untuk membedakannya.
  - Setiap identitas baru mencetak satu baris `users` + satu baris
    `guest_sessions` yang tidak pernah dibersihkan, plus satu hash bcrypt
    (cost 10) di jalur request — beban CPU dan pertumbuhan tabel (lihat
    GO-P2-1).
  - Insentif untuk mendaftar/login (alasan keberadaan fitur ini) hilang.
- **Reproduction scenario**:
  1. `curl -X POST http://host/api/v1/orders -H 'Content-Type: application/json'
     -H 'Idempotency-Key: aaaaaaaaaaaaaaaa1' -d '{...}'` → HTTP 201.
  2. Ulangi perintah yang **sama persis** tanpa menyimpan cookie apa pun
     (tanpa `-b/-c`), dengan `Idempotency-Key` baru → HTTP 201 lagi.
  3. Ulangi N kali. Batas praktis satu-satunya adalah `PublicWriteRateLimit`
     (5 req/menit per-IP, in-memory, single-instance — `middlewares.go:196-198`),
     yang dilewati dengan rotasi IP atau restart instance.
  4. Setara di browser: hapus cookie situs / buka jendela privat baru.
- **Recommended fix** (butuh keputusan produk lebih dulu — seberapa keras limit
  ini harus mengikat):
  - (a) Tambahkan lapisan kedua di luar cookie yang *tidak* dipilih klien:
    dedup order guest per `contact_email`/`contact_phone` yang dinormalisasi
    (dengan tabel/kolom terindeks), atau kuota per-IP + per-subnet yang
    dipersistensi ke DB (bukan in-memory), atau keduanya. Ini menutup jalur
    "cookie baru = allowance baru" tanpa mengubah arsitektur penegakan yang
    sudah benar.
  - (b) Pisahkan "membuat identitas guest" dari "membuat order": izinkan
    `Resolve` mencetak identitas baru hanya pada jalur chat, dan wajibkan
    `POST /api/v1/orders` memakai identitas yang **sudah ada** (cookie tidak
    valid ⇒ 400/403, bukan identitas baru diam-diam).
  - (c) Emit audit event saat identitas guest baru dicetak (`guest_identity_created`
    dengan IP/UA/request_id) sehingga farming terdeteksi walau belum diblokir
    (lihat GO-P2-8).
  - (d) Verifikasi kontak (OTP e-mail/WA) sebelum order guest pertama —
    perubahan produk, bukan hanya perubahan teknis.
  - **Jangan** "memperbaiki" ini dengan memindahkan state ke localStorage,
    fingerprint browser sebagai satu-satunya kunci, atau menolak request tanpa
    cookie di semua endpoint (memutus pengguna baru yang sah).
- **Implementation risk**: Sedang–tinggi. (a) dan (b) memerlukan skema tambahan
  atau perubahan kontrak endpoint publik plus test baru; (d) menyentuh alur
  produk. Tidak ada opsi yang sekadar tempelan satu baris.

---

### 4.2 P1 — High

#### GO-P1-1: Proxy SSE Next.js membuang `Authorization` — pelanggan yang sudah login tetap dibatasi limit guest

- **File**: `frontend/src/app/api/v1/chat/route.ts:28-42`;
  `frontend/src/lib/api.ts:286-312` (`streamChat`);
  `frontend/src/components/chat/ChatInterface.tsx:238-241`;
  `backend/internal/handlers/chat_handlers.go:64-71`
- **Function**: route handler `POST /api/v1/chat` (Next.js) — konstruksi
  `headers` yang diteruskan ke backend
- **Problem**: `streamChat` memasang `Authorization: Bearer <access_token>`
  di browser (`api.ts:300-301`), tapi request itu menuju `/api/v1/chat`
  **same-origin** (`resolveApiBase()` mengembalikan `""` di browser), sehingga
  ditangani oleh App Router route handler — yang menang atas `rewrites()` di
  `next.config.mjs`. Handler tersebut meneruskan **hanya** `Content-Type`,
  `Cookie`, dan `X-Request-ID` (`route.ts:30-42`). Header `Authorization`
  dibuang. Akibatnya `middlewares.OptionalAuth` di backend tidak melihat token,
  `currentUserID(c)` mengembalikan `uuid.Nil`, `chatCtx.UserID` tetap nil, dan
  `MCPService.executeCreateBooking` mengambil cabang
  `s.bookings.CreateGuest(...)` (`mcp_service.go:796-805`).
- **Security impact**: Kontrol yang seharusnya aktif justru salah arah:
  - Pengguna yang **sudah login** (password maupun Google) dan sudah memakai
    allowance guest-nya menerima `GUEST_ORDER_LIMIT_REACHED` dari chat —
    padahal desainnya membolehkan (lihat `docs/ai/known-issues.md`, 27 Agu 2026,
    dan `docs/ai/backend.md:117`).
  - Order yang berhasil dari chat diatribusikan ke user guest sekali-pakai,
    bukan ke akun pelanggan, sehingga tidak muncul di
    `GET /api/v1/bookings/:id` dan hanya bisa di-claim lewat cookie guest.
  - Hanya jalur trip page (`trip/[id]/page.tsx:50-55`, `apiFetch` langsung ke
    `/api/v1/bookings`) yang benar; jalur chat — jalur utama produk — tidak.
  - `ChatInterface` memanggil `ensureCustomerSession()` sebelum `streamChat`,
    jadi token sudah segar; kerugiannya murni karena header dibuang di proxy.
- **Reproduction scenario**: Login (password atau Google), buat satu order guest
  sebelumnya, lalu minta order kedua lewat chat. Backend akan mencatat
  `guest_order_limit_reached` dengan `guest_session_id`, bukan menerbitkan order
  atas akun. Perbandingan langsung: `POST /api/v1/chat` via `curl` dengan
  `Authorization` **langsung ke backend :8080** berhasil memakai jalur akun.
- **Recommended fix**: Teruskan `Authorization` di `route.ts` (allowlist header,
  bukan blocklist), dan tambahkan test yang mengunci bahwa header ini ikut
  diteruskan. Pertimbangkan juga memindahkan penentuan identitas order ke
  respons `done` agar UI dapat menampilkan atribusi yang benar.
- **Implementation risk**: Rendah. Satu file frontend + satu test. Tidak
  mengubah backend, skema, atau kontrak API.


#### GO-P1-2: TTL cookie di-slide, TTL baris DB tidak — identitas guest berotasi diam-diam dan allowance kembali segar

- **File**: `backend/internal/handlers/booking_handlers.go:47`;
  `backend/internal/handlers/chat_handlers.go:62`;
  `backend/internal/services/guest_service.go:45-48`;
  `backend/internal/repositories/guest_repository.go:17-21`
- **Function**: `SetGuestIdentityCookie` (dipanggil setiap request) vs
  `FindGuestSessionByTokenHash` (`WHERE ... expires_at > now`)
- **Problem**: Setiap `POST /api/v1/chat` dan `POST /api/v1/orders` menulis
  ulang cookie dengan Max-Age penuh (`GuestIdentityTTL`), sehingga cookie
  praktis tidak pernah kedaluwarsa bagi pengguna aktif. Sebaliknya
  `guest_sessions.expires_at` **hanya diisi sekali** saat pembuatan dan tidak
  pernah di-slide. Begitu 720 jam terlampaui, lookup token gagal walaupun cookie
  masih ada, dan `Resolve` mencetak identitas baru.
- **Security impact**:
  - Reset allowance periodik yang tidak disengaja: satu order guest baru per
    30 hari per browser, tanpa jejak (tidak ada event saat identitas berotasi).
  - Kehilangan akses order: order guest lama tetap memegang `guest_session_id`
    yang lama, sehingga `GET /api/v1/orders/:id` gagal (`Authenticate` kini
    me-resolve identitas berbeda) dan order hanya bisa dilihat staf. Ini persis
    batasan yang disebut `docs/GUEST_ORDER_LIMIT.md` §"Batasan yang tersisa",
    tapi terjadi **lebih cepat** dari yang diasumsikan karena cookie terus
    diperpanjang sementara barisnya tidak.
  - Baris `guest_sessions` kedaluwarsa tidak pernah dihapus (GO-P2-1), jadi
    identitas lama menumpuk tanpa bisa dipakai.
- **Reproduction scenario**: Set `GUEST_IDENTITY_TTL_HOURS=1`, buat order guest,
  tunggu 1 jam sambil tetap mengirim request chat (cookie diperbarui terus),
  lalu `POST /api/v1/orders` lagi → HTTP 201 (order kedua) dan
  `GET /api/v1/orders/<id-lama>` → 404.
- **Recommended fix**: Slide `guest_sessions.expires_at` pada setiap `Resolve`
  yang berhasil (satu `UPDATE` atomik, pola yang sama dengan
  `UpdateChatSessionActivity`), sehingga cookie dan baris DB kedaluwarsa
  bersama-sama. Alternatif yang lebih ketat: jangan slide cookie melebihi
  `expires_at` baris.
- **Implementation risk**: Rendah. Satu method repository + satu pemanggilan.
  Tidak mengubah skema.

#### GO-P1-3: Kegagalan `ClaimOrder` ditelan dan tidak punya jalur retry

- **File**: `backend/internal/handlers/auth_handlers.go:29-33` (Register),
  `:47-51` (Login); `backend/internal/handlers/google_auth_handlers.go:117-121`;
  `backend/internal/services/guest_service.go:93-104`
- **Function**: `GuestService.ClaimOrder` + ketiga call site-nya
- **Problem**: Ketiga call site hanya `log.Printf` saat claim gagal, lalu
  melanjutkan penerbitan sesi. `ClaimOrder` juga mengembalikan `nil` (bukan
  error) ketika cookie tidak ada atau `FirstOrderID == nil`, jadi "tidak ada
  yang perlu di-claim" dan "claim gagal" tidak terdistingsi di call site.
  Tidak ada mekanisme retry: claim tidak dijalankan pada
  `POST /api/v1/auth/refresh` maupun `GET /api/v1/auth/me`, dan tidak ada
  endpoint claim eksplisit. Sekali gagal, permanen gagal.
- **Security impact**: Kepemilikan data, bukan kerahasiaan.
  - Order tertinggal pada user guest sekali-pakai yang tidak bisa login,
    sementara `guest_sessions.order_count` tetap `1` (sengaja tidak di-reset),
    sehingga pelanggan **tidak bisa melihat order-nya dan tetap terblokir**
    membuat order guest baru.
  - `frontend/src/app/order/[id]/page.tsx:18-19` memilih endpoint berdasarkan
    status sesi: begitu sesi aktif, ia hanya mencoba
    `GET /api/v1/bookings/:id` dan tidak pernah jatuh kembali ke
    `GET /api/v1/orders/:id` — jadi kegagalan claim langsung tampak sebagai
    "order tidak ditemukan".
  - Pada jalur Google, cookie guest hanya terkirim jika `SameSite` mengizinkan
    navigasi top-level lintas-situs; konfigurasi yang salah membuat claim
    ter-skip **tanpa error apa pun** (lihat GO-P2-6).
- **Reproduction scenario**: Buat order guest, klaim sekali (login), lalu login
  lagi dari browser yang sama — call kedua mengembalikan
  `gorm.ErrRecordNotFound` dan hanya muncul di log. Untuk kegagalan asli:
  jalankan dua login paralel; satu memenangkan conditional UPDATE, yang lain
  gagal dan pemiliknya diam-diam berbeda dari yang diharapkan pemanggil.
- **Recommended fix**: (1) Bedakan "nothing to claim" dari "claim failed" di
  `ClaimOrder` (sentinel error terpisah). (2) Emit audit event
  `guest_order_claim_failed` dengan `guest_session_id` + `user_id`. (3) Sediakan
  jalur retry idempoten — claim ulang pada `/auth/me` atau `/auth/refresh`, atau
  endpoint `POST /api/v1/orders/claim` ber-auth yang membaca cookie guest.
  (4) Tambahkan kolom `claimed_at`/`claimed_user_id` di `guest_sessions` agar
  status claim dapat diaudit (lihat GO-P3-3).
- **Implementation risk**: Rendah–sedang. Butuh kolom baru bila (4) diambil;
  (1)–(3) murni service/handler + test.

---

### 4.3 P2 — Medium

#### GO-P2-1: Satu baris `users` + satu baris `guest_sessions` per identitas, tanpa cleanup apa pun

- **File**: `backend/internal/services/auth_service.go:237-258` (`GuestUser`);
  `backend/internal/services/guest_service.go:58-63`;
  `backend/cmd/server/main.go:123-143` (hanya cleanup chat session)
- **Problem**: `Resolve` memanggil `GuestUser` yang **selalu** membuat user baru
  (`guest-<uuid>@vero.local`, `FirstOrCreateUser` dengan email acak ⇒ selalu
  insert) plus satu hash bcrypt cost 10 di jalur request. Tidak ada
  `DeleteExpiredGuestSessions` di repository maupun interface, tidak ada job
  cleanup untuk `guest_sessions`, dan user guest yang sudah tidak dirujuk
  (pasca-claim) tidak pernah dihapus. Satu-satunya scheduler yang berjalan
  adalah `startChatSessionCleanup`.
- **Impact**: Pertumbuhan tabel tak terbatas dan biaya CPU per identitas baru;
  memperkuat GO-P0-1 menjadi vektor resource-exhaustion. `users` juga tercemar
  sehingga metrik/analitik "jumlah pengguna" tidak bermakna.
- **Recommended fix**: Tambahkan `DeleteExpiredGuestSessions` + job periodik
  (pola `migrateGoogleOAuth`/`startChatSessionCleanup`), pertimbangkan
  menghapus user guest tanpa booking, atau — lebih baik — hilangkan kebutuhan
  user guest dengan membuat `bookings.user_id` nullable dan mengandalkan
  `guest_session_id` sebagai pemilik. Yang terakhir mengubah skema, jadi butuh
  keputusan desain.
- **Implementation risk**: Rendah untuk cleanup; sedang untuk `user_id` nullable.

#### GO-P2-2: `migrations/20260818_guest_order_limit.sql` tidak terhubung ke `AutoMigrate`

- **File**: `backend/internal/database/database.go:52-78`;
  `backend/migrations/20260818_guest_order_limit.sql`
- **Problem**: `AutoMigrate` memanggil `migrateLegacySlots`, `migrateGoogleOAuth`,
  dan `migrateTripSearchIndexes`, tapi **tidak** ada `migrateGuestOrderLimit`.
  Dua objek yang tidak bisa diekspresikan lewat tag GORM karena itu tidak pernah
  ada pada DB yang hanya menjalankan `AutoMigrate`:
  `CREATE UNIQUE INDEX ... ON bookings(idempotency_key_hash) WHERE
  idempotency_key_hash IS NOT NULL AND idempotency_key_hash <> ''` dan
  `CHECK (order_count >= 0)`. GORM menggantinya dengan unique index **penuh**
  dari tag `uniqueIndex` (`models.go:312`).
- **Impact**: Drift skema antar-environment. Unique index penuh menolak baris
  kedua dengan `idempotency_key_hash = ''`; saat ini tidak terjangkau karena
  panjang key divalidasi ≥16, tapi setiap jalur create baru yang lupa mengisi
  key akan gagal dengan pelanggaran constraint, bukan error validasi yang jelas.
  `CHECK` yang hilang menghapus jaring pengaman terakhir pada `order_count`.
- **Recommended fix**: Tambahkan `migrateGuestOrderLimit()` idempoten ke
  `AutoMigrate` yang mencerminkan file SQL, sama seperti `migrateGoogleOAuth`.
- **Implementation risk**: Rendah.

#### GO-P2-3: Idempotency tidak concurrency-safe — race identik menghasilkan HTTP 500

- **File**: `backend/internal/services/booking_service.go:74-77, 153-155`;
  `backend/internal/repositories/guest_repository.go:61-71`
- **Problem**: Pola-nya read-then-insert: `FindBookingByIdempotency` miss ⇒
  `CreateBooking`. Dua request dengan key identik yang datang bersamaan
  dua-duanya miss; satu insert berhasil, satu ditolak unique index dan error
  mentahnya menggelembung keluar transaksi. Handler tidak mengenalinya
  (`booking_handlers.go:62`, `utils.ServerError`) ⇒ HTTP 500.
- **Impact**: Idempotency bekerja untuk retry sekuensial (yang diuji
  `TestIdempotentRetryReturnsSameBooking`) tapi tidak untuk klien yang mengirim
  ulang secara paralel — kasus paling umum di lapangan (double-click, retry
  otomatis, dua tab). Klien menerima 500 dan tidak tahu apakah order terbentuk.
- **Recommended fix**: Deteksi pelanggaran unique pada `idempotency_key_hash`,
  lalu baca ulang dan kembalikan booking yang menang (retry-then-read), atau
  gunakan `ON CONFLICT DO NOTHING` + `RETURNING`. Petakan sisa error ke 409,
  bukan 500.
- **Implementation risk**: Rendah–sedang; perlu test konkurensi khusus.


#### GO-P2-4: Scope idempotency bergeser melewati batas claim

- **File**: `backend/internal/services/booking_service.go:45-52`;
  `backend/internal/repositories/guest_repository.go:61-71`
- **Problem**: Hash memuat prefix + owner: `guest:<guestSessionID>` sebelum
  claim, `user:<userID>` sesudahnya. Setelah `ClaimGuestOrder` meng-NULL-kan
  `guest_session_id`, `FindBookingByIdempotency(..., guest=false, ...)`
  mencari `user_id = ? AND guest_session_id IS NULL` dengan hash yang
  **berbeda**, jadi replay `Idempotency-Key` yang sama dari akun tidak menemukan
  order lama.
- **Impact**: Order duplikat yang bisa dicegah tetap lolos tepat setelah
  login/register — momen dengan probabilitas retry tertinggi (klien mengulang
  request yang tadi ditolak 403). Jalur akun tidak dibatasi limit, jadi tidak
  ada rem lain.
- **Recommended fix**: Jadikan hash tidak bergantung pada owner (mis.
  `sha256(key)` + kolom owner terpisah pada unique index majemuk), atau simpan
  hash guest asli pada booking dan sertakan dalam lookup jalur akun.
- **Implementation risk**: Sedang (menyentuh index unik yang sudah ada).

#### GO-P2-5: Guard duplikat MCP terikat chat session dan window 200 pesan

- **File**: `backend/internal/services/mcp_service.go:610-628` (`findSessionOrder`),
  `:742-752` (guard), `:786-787` (kunci idempotency)
- **Problem**: Bukti "order sudah ada" disimpan sebagai pesan sistem
  ber-prefix di riwayat chat dan dicari hanya di **200 pesan terakhir**
  (`ListRecentChatMessages(ctx, sessionID, 200)`). Chat session baru menghapus
  guard sekaligus scope idempotency (`"mcp:"+sessionID+...`). Percakapan panjang
  mendorong marker keluar window sehingga guard kedaluwarsa diam-diam.
- **Impact**: Untuk guest, entitlement tetap menahan order kedua, jadi dampaknya
  terbatas. Untuk **user terautentikasi** (yang tidak punya limit) guard ini
  adalah satu-satunya pencegah order ganda dari chat — dan ia bisa hilang.
- **Recommended fix**: Turunkan status order dari DB (query `bookings` per
  chat session / per pemilik + rentang waktu) alih-alih memindai riwayat chat,
  dan buat kunci idempotency MCP tidak bergantung pada `sessionID` (mis. dari
  identitas pemilik + payload ternormalisasi).
- **Implementation risk**: Sedang.

#### GO-P2-6: `GUEST_COOKIE_SAME_SITE` yang salah tulis jatuh ke `Strict` dan mematikan claim Google secara senyap

- **File**: `backend/internal/auth/cookie.go:127-136` (`parseSameSite`);
  `backend/internal/config/config.go:108`;
  `backend/internal/handlers/google_auth_handlers.go:117-121`
- **Problem**: `parseSameSite` hanya mengenali `lax` dan `none`; **semua nilai
  lain — termasuk salah tulis — jatuh ke `SameSiteStrictMode`**. Default config
  `Lax` sudah benar, tapi `Config.Validate()` tidak memvalidasi nilai ini.
  Dengan `Strict`, cookie `vero_guest_session` tidak dikirim pada navigasi
  top-level lintas-situs, yaitu **tepat bentuk request callback Google**
  (`GET /api/v1/auth/google/callback` dari `accounts.google.com`).
- **Impact**: `ClaimOrder` menerima token kosong ⇒ `Authenticate` gagal ⇒
  `ClaimOrder` mengembalikan `nil` (dianggap "tidak ada yang di-claim") ⇒ order
  guest tidak pernah pindah ke akun, **tanpa error dan tanpa log**. Bergabung
  dengan GO-P1-3 menjadi kehilangan akses order yang permanen.
- **Recommended fix**: Validasi `GUEST_COOKIE_SAME_SITE` (dan
  `JWT_COOKIE_SAME_SITE`) di `Config.Validate()` — tolak nilai yang tidak
  dikenal alih-alih diam-diam mengetatkan. Dokumentasikan bahwa jalur claim
  Google membutuhkan `Lax` atau `None`.
- **Implementation risk**: Rendah.


#### GO-P2-7: `AttachChat` menimpa `chat_sessions.guest_session_id` tanpa cek kepemilikan

- **File**: `backend/internal/repositories/guest_repository.go:29-31`;
  `backend/internal/services/guest_service.go:89-91`;
  `backend/internal/handlers/chat_handlers.go:48-61, 156-173`
- **Problem**: `UpdateChatSessionGuest` melakukan
  `UPDATE chat_sessions SET guest_session_id = ? WHERE id = ?` — tanpa syarat
  bahwa kolomnya masih NULL atau sudah milik guest yang sama. Chat session
  di-resolve dari nilai mentah cookie `vero_chat_session` (UUID chat session apa
  adanya, `resolveGuestSession`), dan siapa pun bisa menaruh UUID di cookie
  request-nya sendiri (devtools/`curl`; HttpOnly hanya menghalangi JavaScript).
- **Impact**: Bukan bypass limit — allowance tetap dikonsumsi dari identitas
  guest si pemanggil. Namun atribusi order sebuah chat session dapat dipindahkan
  oleh pihak ketiga yang mengetahui UUID-nya, dan penulisannya last-writer-wins
  (bolak-balik saat korban melanjutkan chat). `ResolveGuestSession`
  (`ai_service.go:941-946`) dan `sessionOwnedByContext` (`:156-167`) sudah
  membatasi jalur ini ke sesi **anonim** yang belum kedaluwarsa, sehingga sesi
  milik user terautentikasi tidak bisa disentuh.
- **Recommended fix**: Jadikan penulisan bersyarat
  (`WHERE id = ? AND (guest_session_id IS NULL OR guest_session_id = ?)`) dan
  perlakukan `RowsAffected == 0` sebagai konflik yang di-log, bukan sukses.
- **Implementation risk**: Rendah.

#### GO-P2-8: Audit trail guest order tidak cukup untuk mendeteksi penyalahgunaan

- **File**: `backend/internal/services/booking_service.go:164-174`;
  `backend/internal/services/guest_service.go:52-73, 102`
- **Problem**: `guest_order_created` dan `guest_order_limit_reached` hanya
  membawa `guest_session_id` (+ `booking_id`) — tanpa `ip`, `user_agent`, atau
  `request_id`, berbeda dari kontrak payload event Google yang sudah rapi
  (`docs/ai/known-issues.md`, `auth/audit.go`). Dan **tidak ada event sama
  sekali** saat identitas guest baru dicetak.
- **Impact**: Farming cookie (GO-P0-1) dan rotasi identitas (GO-P1-2) tidak
  meninggalkan jejak yang bisa dikorelasikan. Investigasi pasca-insiden harus
  bersandar pada log request generik.
- **Recommended fix**: Alirkan `AuthRequestMeta` (pola yang sudah dipakai
  `AuthService`) ke event guest, dan emit `guest_identity_created` /
  `guest_identity_rotated`. Tetap patuhi aturan payload aman: jangan pernah
  me-log token guest mentah maupun hash-nya.
- **Implementation risk**: Rendah.

#### GO-P2-9: Satu-satunya rem kuantitas masih in-memory single-instance

- **File**: `backend/internal/middlewares/middlewares.go:79-107, 196-198`;
  `backend/internal/routes/routes.go:31, 36`
- **Problem**: `PublicWriteRateLimit` (5 req/menit per-IP) memakai `sync.Map`
  in-process dengan cap 10.000 entri. Budget hilang saat restart dan tidak
  dibagi antar-replika.
- **Impact**: Karena GO-P0-1 menghapus batas per-identitas, rate limit inilah
  satu-satunya rem yang tersisa — dan ia paling lemah tepat ketika paling
  dibutuhkan (deploy/scale-out). Sejalan dengan known-issues PRR-P1-3.
- **Recommended fix**: Pindahkan ke penyimpanan bersama (Redis/Postgres) atau
  ke edge (WAF/ingress) sebelum menaikkan trafik atau menambah replika.
- **Implementation risk**: Sedang (infrastruktur).

#### GO-P2-10: Kontrak cookie guest rapuh bila FE dan API tidak satu origin

- **File**: `frontend/src/lib/api.ts:14-19` (`resolveApiBase`);
  `frontend/next.config.mjs` (`rewrites`); `backend/internal/auth/cookie.go:105-110`
- **Problem**: Semua panggilan API dari browser memakai path relatif, sehingga
  cookie guest selalu same-origin dan diteruskan oleh proxy Next.js. Kontrak ini
  tidak dinyatakan eksplisit di mana pun dan tidak dilindungi test. Jika suatu
  saat FE memanggil origin API secara langsung (mengubah `resolveApiBase`, atau
  menghapus rewrite), cookie `SameSite=Lax` **tidak** akan terkirim pada
  `fetch` lintas-situs.
- **Impact**: `POST /api/v1/orders` akan tiba tanpa cookie ⇒ identitas guest baru
  per request ⇒ limit hilang total dan tracking order rusak — kegagalan senyap,
  bukan error. Sejenis dengan P2-M3 pada audit Google OAuth, tapi dengan akibat
  bypass kontrol, bukan sekadar sesi mati.
- **Recommended fix**: Dokumentasikan "FE harus satu origin dengan API (via
  rewrite)" sebagai kontrak deployment di `docs/ai/deployment.md`, atau siapkan
  `SameSite=None; Secure` + `Domain` bersama bila topologi dwi-domain dipilih.
- **Implementation risk**: Konfigurasi/deployment + dokumentasi.

---

### 4.4 P3 — Low

- **GO-P3-1 — `handlers.Chat` adalah dead code yang berbahaya bila dihidupkan.**
  `backend/internal/handlers/chat_handlers.go:16-41` tidak terdaftar di
  `routes.go` (hanya `h.GuestChat` yang dipakai), tapi ia membangun
  `ChatContext{UserID: &userID}` dari `currentUserID(c)` yang mengembalikan
  `uuid.Nil` bila tidak terautentikasi. Jika rute ini dipasang tanpa
  `middlewares.Auth`, MCP akan mengambil `BookingService.Create(uuid.Nil, ...)`:
  **tanpa limit guest** dan booking dimiliki UUID nil. Perbaikan: hapus, atau
  set `UserID` hanya bila `!= uuid.Nil` seperti `GuestChat` (`:69-71`).
- **GO-P3-2 — Tidak ada foreign key pada relasi guest↔booking.**
  `bookings.guest_session_id` dan `guest_sessions.first_order_id` hanya kolom
  uuid ber-index (`migrations/20260818_guest_order_limit.sql`; GORM juga tidak
  membuat relasi). Aman sekarang karena tidak ada penghapusan, tapi begitu job
  cleanup GO-P2-1 ditambahkan, orphan dan dangling pointer menjadi mungkin.
- **GO-P3-3 — Status claim tidak dapat diaudit.** Setelah claim,
  `guest_sessions.order_count` tetap `1` dan `first_order_id` tetap menunjuk
  booking yang sudah pindah pemilik (`guest_repository.go:85-107`) — fail-closed
  yang benar, tapi tidak ada `claimed_at`/`claimed_user_id`. Akibatnya claim
  kedua menghasilkan `record not found` yang ambigu, dan tidak ada cara
  membedakan "belum pernah di-claim" dari "sudah di-claim".
- **GO-P3-4 — `GET /api/v1/orders/:id` tanpa rate limit khusus.** Hanya dilindungi
  limiter global (`routes.go:37`). Perlu cookie guest yang valid **dan** UUID
  booking, jadi risikonya rendah, tapi tidak ada backoff atas kegagalan berulang.
- **GO-P3-5 — Respons order guest membocorkan `user_id` internal.**
  `GuestCreateOrder`/`GuestGetOrder` mengembalikan `models.Booking` utuh
  (`booking_handlers.go:65, 83`), termasuk UUID user guest sekali-pakai.
  `guest_session_id` sudah benar `json:"-"` (`models.go:298`). Tidak ada jalur
  eksploitasi yang ditemukan (user guest tidak bisa login), tapi identifier
  internal tidak perlu keluar. Pertimbangkan DTO respons khusus.
- **GO-P3-6 — Test race tidak menguji `FOR UPDATE`.**
  `guest_order_limit_test.go:23-39` memakai SQLite in-memory dengan
  `SetMaxOpenConns(1)`; serialisasi datang dari koneksi tunggal dan SQLite
  mengabaikan `SELECT ... FOR UPDATE`. `TestConcurrentGuestOrdersCreateOnlyOne`
  karena itu memvalidasi `ConsumeGuestOrder` (yang memang cukup di Postgres),
  bukan `LockGuestSession`. Jaminan konkurensinya tetap benar, tapi tidak
  terverifikasi pada engine produksi.
- **GO-P3-7 — Perbedaan pesan sukses order guest vs order akun.**
  `"Order created for manual admin processing"` vs `"Booking created"`
  (`booking_handlers.go:33, 65`) mengungkap ke klien jalur mana yang dipakai.
  Dampak minimal, tapi menjadi oracle untuk mengetahui apakah token diterima —
  relevan saat mendiagnosis GO-P1-1.

---

## 5. Sudah Aman (verified, jangan diregresi)

- **Atomicity penegakan limit**: satu transaksi, `FOR UPDATE` + conditional
  `UPDATE ... WHERE order_count = 0` dengan `RowsAffected == 1`; kegagalan
  konsumsi me-rollback insert booking (`booking_service.go:73-162`).
- **Attempt yang gagal tidak menghabiskan allowance**: validasi trip/pax/kontak/
  tanggal/kapasitas terjadi **di dalam** transaksi sebelum konsumsi
  (`TestFailedAttemptsDoNotConsumeGuestAllowance`).
- **Token guest tidak pernah disimpan mentah**: hanya SHA-256
  (`guest_service.go:32-35`, `models.go:216`); kebocoran DB bukan kredensial.
- **Cookie hygiene**: `HttpOnly` selalu, `Secure` otomatis saat `SameSite=None`
  atau `APP_ENV=production`, path di-scope (`/api/v1` untuk identitas guest,
  `/api/v1/chat` untuk chat, `/api/v1/auth` untuk refresh).
- **Anti-IDOR**: `FindBookingForGuest` dan `FindBookingForUser` selalu menyaring
  pemilik; UUID tebakan tidak memberi akses
  (`TestGuestOrderAccessOwnership`). `GET /orders/:id` fail-closed saat cookie
  tidak valid.
- **Harga tidak pernah dari klien**: `dto.BookingRequest` tidak punya field harga
  (SEC-3); total dihitung `priceBreakdown` yang sama dengan tool
  `calculate_trip_price`.
- **Batas pax ditegakkan di service**, bukan hanya binding DTO, sehingga MCP
  (non-HTTP) tidak bisa melewatinya (`booking_service.go:98-109`).
- **Claim atomik + sekali pakai**: lock baris guest, conditional UPDATE,
  `guest_session_id` di-NULL-kan dalam statement yang sama
  (`TestGuestOrderClaimTransitionsToAccountOnce`).
- **Policy tidak pernah di klien/AI/MCP**: MCP hanya memilih identitas; error
  limit dikembalikan sebagai `code` terstruktur, dan system prompt melarang
  klaim sukses tanpa tool success.
- **`OptionalAuth` fail-open tapi tidak fail-unsafe**: token tidak valid membuat
  request jadi anonim (bukan diterima), refresh token yang disodorkan sebagai
  access ditolak, dan `sessionOwnedByContext` menahan Bearer token agar tidak
  membuka sesi anonim milik user lain.
- **Tabrakan token pada `Resolve` di-resolve lewat kunci identitas yang benar**:
  fallback setelah kegagalan `CreateGuestSession` mencari ulang **berdasarkan
  token hash yang sama** (`guest_service.go:63-72`) — kontras dengan pola
  berbahaya di `resolveUser` (lihat Bagian 6).
- **Rate limit + batas body pada endpoint guest**: `PublicWriteRateLimit()` +
  `RequestBodyLimit(64<<10)` pada `POST /chat` dan `POST /orders`.
- **`create_payment` tetap nonaktif** dan rute payment terisolasi di belakang
  `PAYMENTS_ENABLED`.

---

## 6. Perhatian Khusus: TOCTOU `resolveUser` (P1-H1) dari Sudut Guest Order

Referensi: `docs/GOOGLE_OAUTH_SECURITY_AUDIT.md` §4.2 **P1-H1**;
`backend/internal/services/google_oauth_service.go:319-384`.

### 6.1 Status: masih ada, belum diperbaiki

Diverifikasi pada commit `5b46a32`. Cabang fallback setelah
`CreateUserWithGoogleIdentity` gagal masih:

```
if existing, findErr := s.repo.FindUserByGoogleSub(ctx, identity.Subject); findErr == nil { return existing, nil }
if existing, findErr := s.repo.FindUserByEmail(ctx, identity.Email); findErr == nil { return existing, nil }   // :370-372
```

Baris kedua mengembalikan user **apa pun** dengan email tersebut tanpa
membandingkan `google_sub`, sehingga melewati guard `ErrGoogleAccountExists` di
`:331-336`. Jendela lomba ada antara `FindUserByEmail` (miss, langkah 2) dan
`CreateUserWithGoogleIdentity` (gagal constraint, langkah 3) — mis. karena
`POST /auth/register` paralel dengan email yang sama.

### 6.2 Dampak tambahan pada domain guest order (tidak tercakup audit OAuth)

Callback Google memanggil `Guests.ClaimOrder(cookie, user.ID)` **setelah**
`resolveUser` (`google_auth_handlers.go:117-121`), memakai cookie guest dari
**browser pemanggil**. Konsekuensi yang belum didokumentasikan:

1. **Injeksi order ke akun korban.** Attacker yang memenangkan jendela race
   memperoleh sesi atas akun korban; pada request yang sama cookie guest
   *attacker* ikut terkirim, sehingga `ClaimGuestOrder` memindahkan order guest
   **milik attacker** ke `user_id` korban. Karena pembayaran dinonaktifkan dan
   order masuk antrean `pending_admin_processing`, operator akan memproses
   pesanan yang atas nama korban tapi dibuat attacker. Tanggung jawab finansial
   dan PII kontak bercampur, dan `guest_order_linked`
   (`guest_service.go:102`) mencatatnya sebagai linking yang sah.
2. **Bypass limit guest lewat account takeover.** Attacker yang allowance
   guest-nya sudah habis dapat memakai P1-H1 untuk masuk ke akun ber-email sama,
   lalu memesan tanpa batas lewat `POST /api/v1/bookings` (jalur akun tidak
   memiliki limit sama sekali). Ini menjadikan P1-H1 bukan hanya masalah
   identitas, tapi juga jalur pintas atas kontrol bisnis guest order.
3. **Order korban ikut tersapu bila arah serangan dibalik.** Bila korban
   menyelesaikan login Google di browser yang memegang cookie guest dengan order
   yang sudah dibuat, order itu di-claim ke akun yang **di-resolve** oleh
   `resolveUser` — dan justru cabang fallback inilah yang menentukan akun
   tersebut. Resolusi akun yang salah = order pindah ke akun yang salah, dan
   claim bersifat sekali pakai sehingga tidak dapat dikoreksi lewat login ulang
   (`RowsAffected != 1` pada percobaan berikutnya).

### 6.3 Kontras pola: `GuestService.Resolve` melakukannya dengan benar

`Resolve` juga punya fallback pasca-kegagalan-create, tapi ia mencari ulang
**berdasarkan kunci identitas yang sama** (`FindGuestSessionByTokenHash(HashGuestToken(token))`,
`guest_service.go:63-72`) — bukan berdasarkan atribut sekunder yang bisa
dikuasai penyerang. Ini adalah bentuk yang benar dan bisa dipakai sebagai
rujukan saat memperbaiki `resolveUser`: **fallback hanya boleh resolve lewat
kunci yang sama dengan kunci yang dipakai pada lookup utama.**

### 6.4 Rekomendasi (tidak dieksekusi di task ini)

Naikkan prioritas P1-H1 karena dampaknya melampaui autentikasi. Perbaikan
minimalnya sama seperti pada audit OAuth: pada cabang fallback, resolve **hanya**
lewat `FindUserByGoogleSub`; bila miss, kembalikan `ErrGoogleAccountExists`.
Sebagai pengerasan tambahan yang spesifik guest order, jalankan `ClaimOrder`
hanya untuk user yang benar-benar baru dibuat atau yang sub-nya sudah ter-link
(bukan hasil resolve fallback), dan catat `claimed_user_id` agar linking yang
salah dapat dilacak.

---

## 7. Urutan Implementasi yang Direkomendasikan

Prinsip urutannya: **perbaiki yang murah + tidak berisiko dulu** agar kontrol
yang sudah ada bekerja sebagaimana dirancang, **baru** ambil keputusan desain
untuk lubang struktural (GO-P0-1) — karena setiap opsi perbaikan P0 menyentuh
kontrak endpoint publik atau alur produk, dan sebagian bergantung pada
observability yang belum ada.

| # | Item | Alasan urutan ini | Ukuran |
|---|---|---|---|
| 1 | **GO-P1-1** — teruskan `Authorization` di `frontend/src/app/api/v1/chat/route.ts` | Satu-satunya temuan yang saat ini merugikan pelanggan sah tiap hari, dan perbaikannya satu file + satu test. Tidak menyentuh backend/skema. | Kecil |
| 2 | **P1-H1** (audit OAuth) — tutup fallback `resolveUser` agar tidak resolve by email | Account takeover **dan** jalur injeksi order ke akun korban (§6.2). Sudah ada rekomendasi konkret; satu fungsi + test race. Kerjakan sebelum menyentuh alur claim apa pun. | Kecil |
| 3 | **GO-P2-6** — validasi `GUEST_COOKIE_SAME_SITE`/`JWT_COOKIE_SAME_SITE` di `Config.Validate()` | Prasyarat agar diagnosis claim yang gagal tidak menyesatkan; mencegah `Strict` senyap sebelum kita mengeraskan jalur claim. | Kecil |
| 4 | **GO-P1-3** — bedakan "nothing to claim" vs "claim failed", emit audit event, sediakan retry idempoten | Menghentikan kehilangan akses order permanen. Bergantung pada #3 (agar kegagalan bukan karena cookie tidak terkirim) dan #2 (agar retry tidak memperbanyak linking salah). | Sedang |
| 5 | **GO-P1-2** — slide `guest_sessions.expires_at` saat `Resolve` berhasil | Menghapus rotasi identitas periodik yang mereset allowance dan memutus tracking. Satu method repository. | Kecil |
| 6 | **GO-P2-8** — alirkan `AuthRequestMeta` ke event guest + emit `guest_identity_created` | **Prasyarat GO-P0-1**: tanpa ini kita tidak bisa mengukur besar penyalahgunaan, memilih ambang kuota, atau memverifikasi bahwa perbaikan P0 bekerja. | Kecil |
| 7 | **GO-P2-2** — tambahkan `migrateGuestOrderLimit()` ke `AutoMigrate` | Menyamakan skema semua environment sebelum menyentuh index/constraint pada langkah berikutnya. | Kecil |
| 8 | **GO-P2-3** — idempotency retry-then-read + 409, bukan 500 | Prasyarat untuk kuota apa pun di P0: begitu request ditolak, klien akan me-retry, dan retry paralel saat ini menghasilkan 500 yang ambigu. | Kecil–sedang |
| 9 | **GO-P2-4** — buat hash idempotency tidak bergantung owner | Menutup duplikat pasca-claim. Kerjakan setelah #7 karena menyentuh unique index. | Sedang |
| 10 | **GO-P0-1** — keputusan desain + implementasi jangkar kedua (dedup kontak dan/atau kuota persisten per-IP/subnet; `POST /orders` tidak lagi mencetak identitas) | Temuan paling berat, tapi **paling mahal dan paling butuh data**. Dengan #6 kita punya telemetri, dengan #8 retry-nya bersih, dengan #1/#4/#5 jalur pengguna sah sudah tidak rusak — sehingga pengetatan tidak salah menghukum pelanggan asli. | Besar (butuh keputusan produk) |
| 11 | **GO-P2-1** — `DeleteExpiredGuestSessions` + job periodik; evaluasi `bookings.user_id` nullable | Mengurangi dampak sisa GO-P0-1 dan menghentikan pertumbuhan tabel. Setelah #10 agar keputusan skema (butuh user guest atau tidak) sudah pasti. | Sedang |
| 12 | **GO-P2-9** — pindahkan rate limit ke shared store / edge | Wajib sebelum multi-replika; setelah #10 karena ambangnya ditentukan oleh desain kuota yang dipilih. | Sedang (infra) |
| 13 | **GO-P2-5** — turunkan status order dari DB, bukan riwayat chat; kunci MCP lepas dari `sessionID` | Terutama melindungi user terautentikasi dari order ganda. Tidak memblokir apa pun di atasnya. | Sedang |
| 14 | **GO-P2-7** — `UpdateChatSessionGuest` bersyarat | Pengerasan; tidak ada bypass limit yang bergantung padanya. | Kecil |
| 15 | **GO-P2-10** — putuskan & dokumentasikan kontrak satu-origin FE↔API | Keputusan deployment; jadikan eksplisit agar perbaikan P0 tidak dibatalkan oleh perubahan topologi. | Dokumentasi/deploy |
| 16 | **GO-P3-1 … GO-P3-7** — hapus `handlers.Chat`, FK, `claimed_at`, DTO respons, test `FOR UPDATE` di Postgres, pesan seragam | Hardening + kebersihan. GO-P3-2 (FK) sebaiknya menyertai #11 karena job cleanup yang memunculkan risiko orphan. | Beragam |

**Aturan sekuensing yang penting**

- Jangan mengerjakan GO-P0-1 lebih dulu. Tanpa #6 tidak ada data untuk memilih
  ambang, dan tanpa #1/#4/#5 pengetatan akan menabrak pengguna sah yang saat ini
  sudah salah diblokir — sinyal palsu akan mengubur sinyal nyata.
- Jangan mengerjakan GO-P2-4 sebelum GO-P2-2; keduanya menyentuh unique index
  `idempotency_key_hash` yang bentuknya saat ini berbeda antar-environment.
- Jangan "memperbaiki" GO-P1-3 dengan menghapus kondisi pada `ClaimGuestOrder`
  atau membuat claim bisa diulang — sifat sekali-pakai itu yang menahan
  double-linking.
- Naikkan #2 (P1-H1) menjadi perbaikan darurat bila ditemukan indikasi
  penyalahgunaan nyata; audit `external_identities`, `auth_sessions`, dan event
  `guest_order_linked` untuk anomali.

---

## 8. Catatan Metodologi

- Audit **read-only**: tidak ada kode, migrasi, dependency, atau konfigurasi
  yang diubah. Satu-satunya artefak baru adalah dokumen ini.
- Semua klaim berasal dari pembacaan file sumber pada commit `5b46a32` plus
  eksekusi test yang sudah ada (`go build ./...`,
  `go test ./internal/services -run 'Guest|Idempotent|Concurrent|Authenticated'
  -race -count=1`). Tidak ada verifikasi runtime terhadap environment produksi
  (tidak ada akses), dan tidak ada eksploitasi yang benar-benar dijalankan
  terhadap sistem hidup.
- Perilaku Postgres (`SELECT ... FOR UPDATE`, evaluasi ulang predikat pada
  `UPDATE` bersyarat di READ COMMITTED) disimpulkan dari semantik engine, bukan
  dari test yang berjalan di Postgres — test yang ada memakai SQLite
  (GO-P3-6).
- GO-P0-1 memerlukan keputusan produk sebelum diperbaiki; GO-P2-9 dan GO-P2-10
  memerlukan keputusan deployment.

