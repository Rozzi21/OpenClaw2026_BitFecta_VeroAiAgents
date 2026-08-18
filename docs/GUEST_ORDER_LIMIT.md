# Guest Order Limit — Dokumentasi Implementasi

Status: **diimplementasikan** (18 Agustus 2026)

Seorang pengunjung yang BELUM login boleh membuat **tepat SATU** order travel
yang sukses. Setelah itu guest tetap bisa melihat/tracking order-nya dan lanjut
memakai chat, tetapi TIDAK bisa membuat order kedua sebelum login/register.
Backend + database adalah otoritas penegak — bukan frontend, cookie,
localStorage, AI, atau MCP.

## Guest flow

1. Request pertama ke `POST /api/v1/chat` atau `POST /api/v1/orders` membuat
   **GuestSession** server-side. Browser menerima cookie HttpOnly
   `vero_guest_session` berisi token opaque random 256-bit (hex 64 char).
   Hanya SHA-256 hash token yang disimpan di `guest_sessions.token_hash`.
   Cookie: `HttpOnly`, `Secure` di production, `SameSite` dari
   `GUEST_COOKIE_SAME_SITE` (default `Lax`), path `/api/v1`, TTL dari
   `GUEST_IDENTITY_TTL_HOURS` (default 720 jam / 30 hari).
2. Chat session yang dibuat di-link ke guest session
   (`chat_sessions.guest_session_id`). Refresh browser, chat baru, dan hapus
   localStorage TIDAK mereset allowance karena allowance hidup di guest
   session (server-side), bukan di chat session atau browser.
3. Order guest pertama: `BookingService.CreateGuest` menjalankan satu
   transaction PostgreSQL — lock row guest (`SELECT ... FOR UPDATE`), validasi
   trip/pax/tanggal/kontak/harga, insert `bookings` (dengan
   `guest_session_id`), lalu konsumsi entitlement
   (`order_count=1`, `first_order_id=<booking>`) — semuanya atomik.
4. Tracking order: `GET /api/v1/orders/:id` memverifikasi cookie guest dan
   `bookings.guest_session_id` cocok. UUID saja tidak cukup.
5. Setelah login/register, handler meng-claim order guest ke akun (best-effort,
   single-use, terverifikasi cookie) lalu tracking lanjut lewat
   `GET /api/v1/bookings/:id` (owner user).

## Authenticated flow

`POST /api/v1/bookings` (Bearer access token) memakai jalur yang sama
(`BookingService.Create`) TANPA guest entitlement check — user authenticated
boleh banyak order sesuai aturan booking biasa. Idempotency tetap wajib.

## Perilaku API

| Endpoint | Akses | Perilaku |
|---|---|---|
| `POST /api/v1/orders` | publik | Buat order guest (maks 1 sukses). Wajib header `Idempotency-Key` (16–200 char) |
| `GET /api/v1/orders/:id` | cookie guest | Tracking order milik guest session |
| `POST /api/v1/bookings` | Bearer | Buat booking user; wajib `Idempotency-Key` |
| `GET /api/v1/bookings/:id` | Bearer | Detail booking milik user (owner/staff) |

Error contract untuk order guest kedua (HTTP 403, envelope standar):

```json
{
  "success": false,
  "message": "Please sign in to create another order.",
  "error": { "status": "authentication_required", "code": "GUEST_ORDER_LIMIT_REACHED" }
}
```

Frontend membaca `error.code`, BUKAN string pesan.

## Database design

Model baru `GuestSession` (`guest_sessions`): UUID PK, `token_hash`
(uniqueIndex), `user_id` (user guest terisolasi untuk memenuhi
`bookings.user_id NOT NULL`), `first_order_id` (nullable), `order_count`
(default 0), `expires_at` (index). Kolom additive baru:

- `chat_sessions.guest_session_id` (nullable, index) — link chat→guest identity.
- `bookings.guest_session_id` (nullable, index) — ownership + policy lookup.
- `bookings.idempotency_key_hash` (varchar 64, unique partial) — replay shield.

Migrasi: `backend/migrations/20260818_guest_order_limit.sql` (additive,
idempotent, tidak menyentuh data existing) + AutoMigrate untuk dev. Justifikasi
index: `token_hash` unique untuk lookup O(1) + mencegah duplikat hash;
`bookings.guest_session_id` untuk ownership query; partial unique pada
`idempotency_key_hash` mencegah duplikat logical request tanpa mengunci baris
lama yang kosong.

## Security model

- Token opaque random CSPRNG 256-bit, hash-only at rest; raw token tak pernah
  dilog/disimpan di DB/dikirim di body.
- Guest identity TIDAK pernah diterima dari request payload — hanya cookie.
- Authorization order guest = cookie token valid + `bookings.guest_session_id`
  match. Bukan UUID/email/phone/frontend state (anti-IDOR).
- Booking guest memakai user guest terisolasi (`guest-<uuid>@vero.local`) per
  guest session — tidak ada shared guest account antar order.
- Claim order: cookie token valid + order masih `guest_session_id` match +
  account valid + belum pernah di-claim; conditional UPDATE single-statement
  (single-use). TIDAK ada auto-claim berbasis email.
- Rate limit abuse: `PublicWriteRateLimit` 5 req/menit per-IP di `/orders` dan
  `/chat` (SEC-13) + global 20 req/s. IP bukan business rule — hanya abuse
  control.
- Audit aman: `guest_order_created`, `guest_order_limit_reached`,
  `guest_order_linked`, `guest_order_auth_required` — hanya safe IDs; tidak ada
  raw guest token/JWT/PII kontak.

## Error codes

| Code | HTTP | Arti |
|---|---|---|
| `GUEST_ORDER_LIMIT_REACHED` | 403 | Guest sudah pakai satu order; perlu login |
| `IDEMPOTENCY_KEY_REQUIRED` | 400 | Header idempotency hilang/tak valid |
| `BOOKING_VALIDATION_FAILED` | 400 | Kontak/tanggal invalid (tidak konsumsi allowance) |

## Frontend behavior

- Cookie otomatis terkirim (`credentials: include`). Tidak ada entitlement di
  localStorage; hanya access token customer disimpan setelah login.
- Order sukses: tampilkan "Your order has been created successfully" + "You can
  continue tracking this order as a guest. To create another order, please sign
  in." + tombol Continue Tracking / Login / Register.
- Order kedua (`GUEST_ORDER_LIMIT_REACHED`): auth gate "Your guest order has
  already been used" + tombol Continue with Google (placeholder disabled),
  Login, Create Account. Order pertama tetap bisa diakses.
- Halaman `/login` & `/register` baru; setelah auth sukses, access token
  disimpan dan order guest di-claim backend. Halaman `/order/[id]` untuk
  tracking (guest cookie atau bearer token).

## Order ownership

- Guest booking: `bookings.guest_session_id` + user guest terisolasi di
  `bookings.user_id`. Kolom `UserID/TripID/BookingStatus/PaymentStatus/
  Contact*/TravelDate/TotalPrice/Payments` tidak berubah.
- Setelah claim: `guest_session_id` di-NULL-kan dan `user_id` diubah ke akun
  dalam satu conditional UPDATE — order pindah ke ownership user, path
  `FindBookingForUser` existing langsung berlaku.

## Concurrency handling

Satu transaction per create. Row guest dikunci `FOR UPDATE` sebelum cek
`order_count`, jadi dua request bersamaan terserialisasi di database: pemenang
insert + konsumsi, yang kalah membaca count=1 →
`ErrGuestOrderLimitReached`. Conditional `ConsumeGuestOrder`
(`WHERE order_count=0`, `RowsAffected==1`) adalah lapisan kedua. Tidak ada
mutex in-memory (aman multi-instance).

## Idempotency

Wajib `Idempotency-Key` header (16–200 char). Hash disimpan sebagai
SHA-256(`user:`|​`guest:` + ownerID + key) di `bookings.idempotency_key_hash`
(unique). Retry dengan key sama mengembalikan booking yang sama — termasuk
retry setelah allowance terpakai (lookup idempotency dijalankan sebelum dan
sesudah cek limit). Key TIDAK memakai booking ID. MCP `create_booking`
menurunkan key deterministik dari session+payload.

## Pengujian

`backend/internal/services/guest_order_limit_test.go` (SQLite in-memory):

1. First guest order sukses; second ditolak `ErrGuestOrderLimitReached` (1,2).
2. Owner bisa akses order; guest lain tidak; UUID tebakan tidak memberi akses
   (3,4,22,23).
3. Failed attempts (invalid contact, invalid date, invalid trip) tidak
   mengonsumsi allowance; first order setelahnya tetap sukses (8,9 + rollback).
4. Race 8 goroutine → tepat 1 order (12,13) — diverifikasi `go test -race`.
5. Retry idempotency key sama → booking sama, tanpa duplikat (14).
6. Authenticated user tidak dibatasi guest limit; 2 order sukses (15,16).
7. Claim single-use: transfer ke akun, claim kedua gagal, owner baru bisa
   akses (linking security).

MCP second-booking mengembalikan `code=GUEST_ORDER_LIMIT_REACHED` terstruktur
(18) dan enforcement tetap di BookingService (19). AI system prompt kini
melarang klaim sukses tanpa tool success dan melarang retry setelah code
limit (20,21). Chat baru tidak mereset allowance karena identity terpisah dari
chat session (5,6,7 by design).

## Batasan yang tersisa

- Google OAuth tombol di frontend masih placeholder (disabled) — belum ada
  provider OAuth di backend.
- Order guest lama (dibuat sebelum fitur ini) tidak punya `guest_session_id`
  dan tidak bisa di-claim otomatis; tetap dapat diakses staff.
- Token guest berlaku 30 hari; order guest yang belum di-claim setelah expiry
  token hanya bisa diakses staff (retention dapat diperpanjang via env).
- Rate limit masih per-IP in-memory single instance (lihat known-issues PRR-P1-3).
