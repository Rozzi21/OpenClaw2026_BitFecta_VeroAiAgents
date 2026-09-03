# Guest Order System — Security Audit (READ-ONLY)

> Status audit: **Read-only**. Tidak ada kode, dependency, skema DB, atau
> migrasi yang diubah. Dokumen ini hanya menginventarisasi cara kerja aktual
> sistem guest order beserta kelemahannya. Semua rekomendasi **belum**
> dieksekusi.
>
> Tanggal audit: 3 Sep 2026. Commit yang diaudit: `5b46a32`.
> Verifikasi: `go build ./...` OK; `go test ./internal/services -run
> 'Guest|Idempotent|Concurrent|Authenticated' -race -count=1` OK.

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

