# Guest Order Limit — Dokumentasi Implementasi

Status: **diimplementasikan** (18 Agustus 2026; jangkar kontak 4 September 2026)

Seorang pengunjung yang BELUM login boleh membuat **tepat SATU** order travel
yang sukses. Setelah itu guest tetap bisa melihat/tracking order-nya dan lanjut
memakai chat, tetapi TIDAK bisa membuat order kedua sebelum login/register.
Backend + database adalah otoritas penegak — bukan frontend, cookie,
localStorage, ChatSession, AI, MCP, atau IP. Sejak 4 Sep 2026 entitlement
berjangkar DUA hal di DB: guest session (cookie) DAN kontak order yang
dinormalisasi, sehingga menghapus cookie tidak lagi mereset allowance
(lihat §"Jangkar kontak").


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
   transaction PostgreSQL — lock row guest (`SELECT ... FOR UPDATE`), cek
   `order_count`, cek jangkar kontak (lihat §"Jangkar kontak"), validasi
   trip/pax/tanggal/kontak/harga, insert `bookings` (dengan
   `guest_session_id`), lalu konsumsi entitlement
   (`order_count=1`, `first_order_id=<booking>`, + baris
   `guest_order_entitlements` per channel kontak) — semuanya atomik.
4. Tracking order: `GET /api/v1/orders/:id` memverifikasi cookie guest dan
   `bookings.guest_session_id` cocok. UUID saja tidak cukup.
5. Setelah login/register (password maupun Google), handler meng-claim order
   guest ke akun lewat satu helper `claimGuestOrder` (terverifikasi cookie,
   transfer sekali, **idempoten**, non-fatal terhadap penerbitan sesi) lalu
   tracking lanjut lewat `GET /api/v1/bookings/:id` (owner user). Lihat
   §"Claim order guest".

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

Model kedua `GuestOrderEntitlement` (`guest_order_entitlements`, 4 Sep 2026):
UUID PK, `contact_key` (varchar 64, **uniqueIndex**), `channel`
(`email`|`phone`), `guest_session_id` (nullable, index — audit saja),
`booking_id` (index). Migrasi:
`backend/migrations/20260904_guest_order_contact_entitlement.sql`.

## Jangkar kontak (GO-P0-1, 4 Sep 2026)

Audit `docs/GUEST_ORDER_AUDIT.md` menemukan: penegakannya sudah atomik, tapi
**kuncinya dipilih klien**. Satu-satunya jangkar adalah cookie
`vero_guest_session`, dan `GuestService.Resolve` mencetak identitas baru
(allowance baru) setiap kali request datang tanpa cookie valid — devtools, mode
privat, atau `curl` tanpa cookie jar. Efektifnya "satu order per cookie yang mau
disimpan klien", bukan "satu order per pengunjung yang belum login".

Perbaikannya menambah jangkar kedua yang **tidak dipilih klien**: kontak yang
dipakai untuk order, dinormalisasi lalu di-hash.

- Normalisasi (`backend/internal/services/guest_entitlement.go`):
  - email → trim + lowercase + buang suffix `+tag`. Titik TIDAK dibuang (itu
    khas Gmail dan akan salah menggabungkan mailbox provider lain).
    `"  Guest.Order@Example.COM "`, `"guest.order@example.com"`, dan
    `"guest.order+order2@example.com"` → satu key.
  - telepon → digit saja, prefix `00` dibuang, prefix `0` dilipat ke `62`
    (pasar Indonesia). `"0812-3456-789"`, `"+62 812 3456 789"`,
    `"0062 812 3456 789"`, `"628123456789"` → satu key.
- `contact_key` = `sha256("<channel>:<nilai ternormalisasi>")`. Hash, bukan
  plaintext: tidak ada salinan kedua PII kontak (alasan sama dengan
  `token_hash`). Channel ikut jadi pre-image supaya email tak pernah berkolisi
  dengan nomor telepon.
- Satu order menulis satu baris per channel yang diisi (email + telepon = dua
  baris), sehingga pengunjung tidak bisa kembali dengan hanya salah satunya.
- Order guest WAJIB punya minimal satu kontak yang bisa dijadikan jangkar.
  Kontak tak terpakai (`"n/a"` sebagai telepon, string tanpa `@` sebagai email)
  ditolak `BOOKING_VALIDATION_FAILED` — kalau dibiarkan lolos, aturan akan
  kembali bergantung pada cookie saja.
- Hanya jalur guest yang membaca DAN menulis jangkar. `POST /bookings`
  (authenticated) tidak tersentuh: user login tetap memakai aturan booking biasa
  walau kontaknya sama dengan order guest yang sudah terpakai.
- Batas yang disengaja: pengunjung yang memakai email DAN telepon yang
  benar-benar berbeda tetap dapat satu order. Menutup itu butuh verifikasi
  kontak (OTP) — keputusan produk, bukan backend (`GUEST_ORDER_AUDIT.md`
  GO-P0-1 (d)). Kuota per-IP sengaja TIDAK dipakai sebagai business rule (IP
  hanya abuse control; NAT bersama akan salah menghukum pelanggan asli).

## Security model

- Token opaque random CSPRNG 256-bit, hash-only at rest; raw token tak pernah
  dilog/disimpan di DB/dikirim di body.
- Guest identity TIDAK pernah diterima dari request payload — hanya cookie.
  Karena cookie bisa dibuang klien, entitlement punya jangkar kedua di DB
  (`guest_order_entitlements.contact_key`, unique) — lihat §"Jangkar kontak".
- Authorization order guest = cookie token valid + `bookings.guest_session_id`
  match. Bukan UUID/email/phone/frontend state (anti-IDOR).
- Booking guest memakai user guest terisolasi (`guest-<uuid>@vero.local`) per
  guest session — tidak ada shared guest account antar order.
- Claim order: cookie token valid + order masih `guest_session_id` match +
  account valid + belum pernah di-claim (marker `claimed_user_id`); conditional
  UPDATE single-statement (transfer sekali). Claim ulang oleh akun yang sama =
  no-op sukses; order milik akun lain DITOLAK. TIDAK ada auto-claim berbasis
  email. Detail: §"Claim order guest".
- Rate limit abuse: `PublicWriteRateLimit` 5 req/menit per-IP di `/orders` dan
  `/chat` (SEC-13) + global 20 req/s. IP bukan business rule — hanya abuse
  control.
- Audit aman: `guest_order_created`, `guest_order_limit_reached`,
  `guest_order_linked`, `guest_order_claim_replayed`,
  `guest_order_claim_conflict`, `guest_order_claim_failed`,
  `guest_order_auth_required` — hanya safe IDs; tidak ada
  raw guest token/JWT/PII kontak. `guest_order_limit_reached` membawa `reason`
  kategori (`guest_session_spent` | `contact_already_used`) plus
  `matched_guest_session_id` bila jangkar kontak yang menahan — cukup untuk
  mendeteksi farming cookie tanpa pernah me-log kontak maupun hash-nya.
  `guest_order_claim_failed` membawa `reason` kategori
  (`no_authenticated_account` | `guest_identity_invalid` | `repository_error`).

## Claim order guest (GO-P1-3 / GO-P3-3, 4 Sep 2026)

`GuestService.ClaimOrder(ctx, token, userID) (GuestOrderClaimResult, error)`.
Satu-satunya bukti kepemilikan adalah cookie `vero_guest_session`: hash-nya
me-resolve baris `guest_sessions`, dan target order diambil dari
`first_order_id` baris itu. Yang **bukan** bukti: booking id (tidak pernah jadi
parameter), email/telepon yang sama dengan kontak order (tidak pernah dibaca di
jalur claim), dan status "sudah login" (user tanpa cookie tidak meng-claim
apa pun).

Transaksi `Repository.ClaimGuestOrder`:

1. `SELECT ... FOR UPDATE` baris guest (serialisasi dua claim bersamaan).
2. Baca marker `claimed_user_id` **sebelum menulis apa pun**. Sudah terisi ⇒
   kepemilikan tidak pernah dihitung ulang; pemilik pertama yang dilaporkan.
3. Conditional UPDATE booking `WHERE id = ? AND guest_session_id = ?` →
   `user_id = <akun>`, `guest_session_id = NULL` (`RowsAffected == 1`).
4. UPDATE marker `WHERE claimed_user_id IS NULL` → `claimed_user_id`,
   `claimed_at`. Gagal ⇒ transaksi rollback (tidak ada order ter-claim tanpa
   claimant tercatat).

Marker masih NULL tapi booking sudah lepas dari jalur guest (claim pra-migrasi):
pemilik AKTUAL dibaca dari baris booking dan dilaporkan, tidak ditimpa.

| Kondisi | Hasil |
|---|---|
| Cookie valid, order belum di-claim | `Transferred=true` (audit `guest_order_linked`) |
| Akun yang sama meng-claim ulang | sukses, `Transferred=false` (audit `guest_order_claim_replayed`) |
| Tanpa cookie / cookie tak dikenal / session kedaluwarsa / belum pernah order | `ErrGuestOrderNothingToClaim` |
| Order sudah milik AKUN LAIN | `ErrGuestOrderClaimConflict` — ditolak, tidak dipindah (audit `guest_order_claim_conflict`) |
| `userID == uuid.Nil` | `ErrGuestOrderClaimUnauthenticated` (ditolak sebelum sentuh DB) |
| Kegagalan DB | error asli + audit `guest_order_claim_failed` (`reason` kategori) |

Handler (`handlers/helpers.go: claimGuestOrder`, dipakai Register, Login,
GoogleCallback) tetap TIDAK memfatalkan penerbitan sesi — cookie guest yang
tidak ada adalah kasus normal, dan penolakan tidak boleh membatalkan login.

### Jalur retry eksplisit: `POST /api/v1/orders/claim` (4 Sep 2026)

Karena hook otomatis di atas best-effort, claim bisa ter-skip tanpa terasa
(cookie guest tidak terkirim pada callback Google lintas-situs bila `SameSite`
terlalu ketat — GO-P2-6, atau kegagalan DB sesaat) dan order tertinggal pada
identitas guest yang tidak bisa login. Endpoint
`POST /api/v1/orders/claim` (`handlers.ClaimOrderToAccount`, grup `protected`
⇒ `middlewares.Auth`) menjalankan ULANG transisi yang sama dengan bukti yang
sama — tidak ada pelemahan:

- Bearer access token menentukan AKUN mana yang meng-claim (tanpa akun → 401;
  handler juga fail-closed sendiri saat `user_id` kosong, jadi salah mount
  middleware tidak bisa menulis `uuid.Nil` ke `bookings.user_id`).
- Cookie `vero_guest_session` menentukan ORDER mana yang di-claim.
- Request tanpa body: order id maupun email tidak pernah diterima sebagai
  parameter, jadi tidak bisa diarahkan ke order orang lain.

| Outcome service | HTTP | Body |
|---|---|---|
| `Transferred=true` | 200 | `data.order_id`, `data.transferred=true` |
| `Transferred=false` (replay idempoten) | 200 | `data.transferred=false` |
| `ErrGuestOrderNothingToClaim` | 404 | `error.code=NO_GUEST_ORDER_TO_CLAIM` |
| `ErrGuestOrderClaimConflict` | 409 | `error.code=GUEST_ORDER_CLAIMED_BY_ANOTHER_ACCOUNT` |
| `ErrGuestOrderClaimUnauthenticated` / tanpa akun | 401 | envelope error generik |
| kegagalan repository | 500 | pesan generik (SEC-15), error asli di log |

Catatan: endpoint TIDAK menghapus cookie guest setelah sukses (identik dengan
hook otomatis). Membersihkannya akan mencetak identitas guest baru dengan
allowance segar; jangkar kontak (GO-P0-1) tetap menahan, tapi tidak perlu
mengendurkan satu lapis pun untuk fitur retry.

## Error codes

| Code | HTTP | Arti |
|---|---|---|
| `GUEST_ORDER_LIMIT_REACHED` | 403 | Guest sudah pakai satu order (jangkar guest session ATAU kontak); perlu login |
| `IDEMPOTENCY_KEY_REQUIRED` | 400 | Header idempotency hilang/tak valid |
| `BOOKING_VALIDATION_FAILED` | 400 | Kontak/tanggal invalid, termasuk kontak yang tidak bisa dijadikan jangkar (tidak konsumsi allowance) |
| `NO_GUEST_ORDER_TO_CLAIM` | 404 | `POST /orders/claim`: tanpa cookie guest, cookie tak dikenal/kedaluwarsa, atau session itu belum pernah order |
| `GUEST_ORDER_CLAIMED_BY_ANOTHER_ACCOUNT` | 409 | `POST /orders/claim`: order sudah dimiliki akun lain — ditolak, tidak pernah dipindah |

Semua transport memakai satu konstanta: `services.CodeGuestOrderLimitReached`
(dideklarasikan di `booking_service.go`, tepat di sebelah
`ErrGuestOrderLimitReached`) sehingga handler HTTP, tool MCP, dan gate chat tidak
bisa saling drift. Message boleh diubah/diterjemahkan kapan saja — code TIDAK.

## Integrasi MCP / AI / chat (4 Sep 2026)

Backend tetap satu-satunya otoritas: aturan hidup di `BookingService.create`
(transaksi + jangkar DB). Yang ditambahkan di sini hanya penerusan keputusan itu
ke jalur chat.

| Lapisan | Perilaku |
|---|---|
| MCP `create_booking` | Menerjemahkan `ErrGuestOrderLimitReached` → ToolResult `status=failed` + `{success:false, status:"requires_authentication", code:"GUEST_ORDER_LIMIT_REACHED", message}`. Sukses membawa `code:"ORDER_CREATED"` + `order_id`; guard duplikat sesi (AIW-8) membawa `code:"ORDER_ALREADY_EXISTS"` + `order_id` sesi itu. Teks error Go internal tidak pernah masuk payload. |
| AI tool loop | `executeToolCall` memanggil `blockedRetryAfterGuestOrderLimit(prior, tool)`: setelah code itu muncul di turn ini, `create_booking`/`create_order` berikutnya DITOLAK sebelum MCP dipanggil (`retry_blocked: true`, code sama). Prompt hanya saran — model bisa retry dengan argumen berbeda dan dedup AIW-3 tidak menangkapnya. Tool lain tetap boleh jalan. |
| Chat response | `ChatResult.OrderGate` = `{code, auth_required, order_id?}` (`order_gate`, `omitempty`), diturunkan dari tool result saja. `auth_required` hanya untuk `GUEST_ORDER_LIMIT_REACHED`; `order_id` hanya untuk order milik sesi ini (limit sengaja tanpa `order_id` — order pemblokir bisa milik identitas guest lain lewat jangkar kontak). |
| Frontend chat | `ChatInterface` menyimpan `order_gate` per pesan dan me-render `OrderGateBlock` via `orderGateView()` (`lib/orderGate.ts`): limit → Google/Login/Create Account; ada order → Continue Tracking ke `/order/{id}`. Hanya `code` yang dibaca, prosa LLM tidak pernah di-parse. |
| Proxy SSE | `lib/chatProxy.ts` meneruskan `Authorization` (fix GO-P1-1) sehingga user yang sudah login benar-benar memakai jalur authenticated dari chat dan boleh membuat order tambahan. |
| Kembali dari Google | `ChatInterface` me-mount `OAuthReceiver`, jadi token di fragment `#access_token=...` untuk `return_to=/` dikonsumsi; tanpa ini user kembali ke chat sebagai guest dan tetap terblokir. |

## Frontend behavior

- Cookie otomatis terkirim (`credentials: include`). Tidak ada entitlement di
  localStorage; hanya access token customer disimpan setelah login.
- **Kontak wajib (4 Sep 2026).** `trip/[id]` punya satu input "Email or phone
  number"; tombol "Confirm & Create Order" disabled selama kosong, dan nilainya
  dikirim sebagai `contact_email` (bila memuat `@`) atau `contact_phone`.
  Sebelumnya halaman ini mengirim placeholder `contact_phone:
  "provided-via-chat"` — tidak bisa dijadikan jangkar, jadi backend menolaknya.
  Ini satu-satunya perubahan frontend yang dituntut kontrak baru; kode error
  tidak berubah.
- Order sukses: tampilkan "Your order has been created successfully" + "You can
  continue tracking this order as a guest. To create another order, please sign
  in." + tombol Continue Tracking / Login / Register.
- Order kedua (`GUEST_ORDER_LIMIT_REACHED`): auth gate "Your guest order has
  already been used" + tombol Continue with Google (aktif sejak 19 Agu 2026),
  Login, Create Account. Order pertama tetap bisa diakses.
- **Chat (4 Sep 2026):** gate yang sama muncul di dalam percakapan lewat
  `order_gate` (lihat "Integrasi MCP / AI / chat"), bukan lewat pembacaan teks
  jawaban asisten. Order milik sesi chat itu tetap punya tombol Continue
  Tracking.
- Halaman `/login` & `/register` baru; setelah auth sukses, access token
  disimpan dan order guest di-claim backend. Halaman `/order/[id]` untuk
  tracking (guest cookie atau bearer token).

## Order ownership

- Guest booking: `bookings.guest_session_id` + user guest terisolasi di
  `bookings.user_id`. Kolom `UserID/TripID/BookingStatus/PaymentStatus/
  Contact*/TravelDate/TotalPrice/Payments` tidak berubah.
- Setelah claim: `guest_session_id` di-NULL-kan dan `user_id` diubah ke akun
  dalam satu conditional UPDATE — order pindah ke ownership user, path
  `FindBookingForUser` existing langsung berlaku. Marker
  `guest_sessions.claimed_user_id`/`claimed_at` ditulis di transaksi yang sama
  agar kepemilikan hanya diputuskan sekali (lihat §"Claim order guest").

## Concurrency handling

Satu transaction per create. Row guest dikunci `FOR UPDATE` sebelum cek
`order_count`, jadi dua request bersamaan terserialisasi di database: pemenang
insert + konsumsi, yang kalah membaca count=1 →
`ErrGuestOrderLimitReached`. Conditional `ConsumeGuestOrder`
(`WHERE order_count=0`, `RowsAffected==1`) adalah lapisan kedua. Tidak ada
mutex in-memory (aman multi-instance).

Untuk request konkuren yang masing-masing membawa **identitas guest berbeda**
(dua cookie, dua browser, `curl` paralel tanpa cookie jar) row lock tidak
membantu — di situ unique index `guest_order_entitlements.contact_key` yang
memutuskan: `INSERT ... ON CONFLICT DO NOTHING` menyisipkan satu baris
(`RowsAffected==1`) untuk pemenang, yang kalah mendapat 0 baris → dipetakan ke
`ErrGuestOrderLimitReached` → seluruh transaksinya (termasuk insert booking dan
`order_count`) di-rollback.

**Binding chat→guest (GO-P2-7, 4 Sep 2026).** `chat_sessions.guest_session_id`
adalah input otorisasi (cabang guest MCP `create_booking` menurunkan pemilik
order dari sana), jadi penulisannya juga single-winner:
`Repository.BindChatSessionGuest` = satu conditional UPDATE yang menang hanya
bila kolom `NULL`, sudah berisi guest yang sama, atau guest terikat sudah
kedaluwarsa/hilang. Yang kalah dapat `ErrChatSessionGuestMismatch` (audit
`guest_chat_bind_refused`) dan `GuestChat` mencetak chat session baru untuk
identitas pemanggil, bukan memakai sesi milik orang lain.

## Idempotency

Wajib `Idempotency-Key` header (16–200 char). Hash disimpan sebagai
SHA-256(`user:`|​`guest:` + ownerID + key) di `bookings.idempotency_key_hash`
(unique). Retry dengan key sama mengembalikan booking yang sama — termasuk
retry setelah allowance terpakai (lookup idempotency dijalankan sebelum dan
sesudah cek limit). Key TIDAK memakai booking ID. MCP `create_booking`
menurunkan key deterministik dari session+payload.

**Race key identik (GO-P2-3, 4 Sep 2026).** Dua request paralel dengan owner +
key yang sama bisa lolos lookup pra-insert bersamaan; unique index yang menolak
yang kalah, dan di Postgres penolakan itu **membatalkan transaksi** sehingga
baris pemenang hanya bisa dibaca setelah transaksi tersebut selesai. Karena itu
`BookingService.create` mengulang `FindBookingByIdempotency` **di luar**
transaksi dan mengembalikan booking pemenang sebagai replay (dulu: constraint
error → HTTP 500). Lookup tetap di-scope owner + key hash pemanggil, jadi replay
tidak bisa menyentuh order pemilik lain, dan allowance tidak pernah terpakai dua
kali karena transaksi yang kalah sudah rollback. Penolakan/kegagalan lain
(`ErrGuestOrderLimitReached`, kontak invalid, DB down) tetap diteruskan.

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
7. Claim: transfer ke akun sekali, claim kedua oleh akun yang sama = no-op
   sukses (`Transferred=false`), owner baru bisa akses (linking security).

MCP second-booking mengembalikan `code=GUEST_ORDER_LIMIT_REACHED` terstruktur
(18) dan enforcement tetap di BookingService (19). AI system prompt kini
melarang klaim sukses tanpa tool success dan melarang retry setelah code
limit (20,21). Chat baru tidak mereset allowance karena identity terpisah dari
chat session (5,6,7 by design).

Integrasi MCP/AI/chat (4 Sep 2026) ditutup dua file test baru:

`backend/internal/services/guest_order_chat_gate_test.go` (tanpa DB, tanpa LLM —
MCP + booking domain di-mock lewat interface SEC-27, jadi `OPENAI_API_KEY` tidak
dibutuhkan):

1. `TestCreateBookingGuestLimit_StructuredToolResult` — tool result membawa
   `code`/`status`/`success:false`, tanpa `order_id`, dan tanpa teks error Go
   internal.
2. `TestCreateBookingAuthenticated_NoGuestLimit` — panggilan yang sama oleh akun
   memakai `BookingService.Create` dan menghasilkan `ORDER_CREATED` + `order_id`.
3. `TestExecuteToolCall_NoRetryAfterGuestOrderLimit` — retry dengan argumen
   BERBEDA tidak pernah mencapai MCP (dispatch=0), model menerima code yang sama
   + `retry_blocked`, dan tool non-order tetap jalan.
4. `TestBlockedRetryAfterGuestOrderLimit_Scope` — tabel kapan guard menyala:
   order pertama tidak diblok, alias `create_order` diblok, kegagalan
   `create_booking` lain tidak diblok, code dari tool lain tidak diblok.
5. `TestChatOrderGateFromToolResults` — `order_gate` untuk limit (auth, tanpa
   `order_id`), created (`order_id`), duplikat (`order_id`), precedence created >
   limit, dan code tak dikenal → tanpa gate.

`backend/internal/handlers/guest_order_limit_handler_test.go` (SQLite in-memory,
memakai harness claim): order guest pertama `201` tanpa login, kedua `403` dengan
`error.code=GUEST_ORDER_LIMIT_REACHED` + `error.status=authentication_required`
dan **tidak ada baris booking tambahan**; penolakan berbasis jangkar kontak
(cookie baru, kontak sama) memakai kontrak yang identik.

Frontend (`cd frontend && npm test`, runner bawaan Node):
`chatProxy.test.ts` (`Authorization` diteruskan — regresi GO-P1-1, header di luar
allowlist dibuang), `orderGate.test.ts` (code → keputusan UI; `auth_required`
palsu pada `ORDER_CREATED` tidak bisa memaksa sign-in wall; code tak dikenal →
tidak render), `chatStream.test.ts` (`order_gate` sampai utuh lewat SSE `done`,
dan Bearer token terpasang pada request stream).

Jangkar kontak (4 Sep 2026) ditutup dua file test baru:

`backend/internal/services/guest_order_contact_entitlement_test.go`
(SQLite in-memory, memakai fixture yang sama):

1. `TestGuestOrderDeniedForFreshIdentityWithSameContact` — identitas guest baru
   (persis yang dihasilkan cookie dihapus / mode privat / `curl` tanpa cookie
   jar) dengan kontak sama → `ErrGuestOrderLimitReached`; email saja dan telepon
   saja masing-masing cukup untuk menahan; pengunjung lain tetap dapat order.
2. `TestGuestEntitlementSurvivesIdentityRotation` — identitas lama di-expire
   (rotasi TTL 30 hari), identitas baru dengan kontak sama tetap ditolak.
3. `TestNewChatSessionDoesNotResetGuestEntitlement` — chat session baru pada
   identitas sama, lalu chat baru + identitas baru: dua-duanya ditolak.
4. `TestFailedGuestOrderDoesNotConsumeContactEntitlement` — trip tak dikenal +
   tanggal invalid tidak menulis baris jangkar; kontak masih bisa dipakai satu
   kali, lalu terpakai permanen.
5. `TestGuestOrderRequiresAnchorableContact` — kontak tanpa jangkar ditolak
   `ErrBookingContactRequired` tanpa mengonsumsi apa pun.
6. `TestConcurrentGuestIdentitiesSameContactCreateOnlyOne` — 8 goroutine, 8
   identitas berbeda, kontak sama → tepat 1 booking + 2 baris jangkar
   (diverifikasi `go test -race`).
7. `TestAuthenticatedBookingIgnoresGuestContactAnchors` — user login boleh dua
   order dengan kontak yang jangkarnya sudah terpakai; jalur authenticated tidak
   menulis jangkar.
8. `TestConsumeGuestOrderEntitlementsRejectsTakenContactKey` — level repository:
   `contact_key` yang sudah terpakai dilaporkan sebagai konflik
   (`RowsAffected == 0` → `gorm.ErrDuplicatedKey`), bukan sukses senyap, dan
   INSERT yang kalah tidak menulis baris. Perlu diuji terpisah karena pada test
   SQLite (`SetMaxOpenConns(1)`) jalur konflik tidak pernah tercapai lewat
   service — pre-check sudah menahan lebih dulu.

`backend/internal/services/guest_entitlement_test.go` (unit, tanpa DB) mengunci
normalisasi: varian email (trim/case/`+tag`/titik dipertahankan/malformed) dan
telepon (`0`/`+62`/`00`/pemisah/tanpa digit), serta derivasi anchor
(email ≠ phone key, ejaan ekuivalen → key sama, kontak beda → key beda).

Claim pasca-autentikasi (4 Sep 2026) ditutup
`backend/internal/services/guest_order_claim_test.go` (SQLite in-memory, fixture
yang sama):

1. `TestGuestOrderClaimValidCookie` — cookie valid → transfer, marker terisi,
   akun bisa akses lewat `FindBookingForUser`, jalur guest tertutup.
2. `TestGuestOrderClaimInvalidGuestIdentity` — cookie kosong / token asing /
   hash disodorkan sebagai token / session kedaluwarsa → `ErrGuestOrderNothingToClaim`
   dan booking tidak bergerak; `uuid.Nil` → `ErrGuestOrderClaimUnauthenticated`.
3. `TestGuestOrderClaimWrongGuest` — guest B tidak bisa meng-claim order guest A,
   termasuk saat `first_order_id` B diarahkan ke booking A (kasus "saya tahu
   order id"); order B sendiri tetap bisa di-claim.
4. `TestGuestOrderClaimWrongAuthenticatedUser` — akun kedua di belakang cookie
   yang sama → `ErrGuestOrderClaimConflict`, order tetap milik akun pertama,
   marker tidak ditimpa, akun kedua tidak bisa membaca order.
5. `TestGuestOrderClaimDuplicateIsIdempotent` — tiga claim ulang oleh akun yang
   sama: sukses tanpa transfer, `claimed_at` tidak ditulis ulang, jumlah booking
   milik akun tetap 1.
6. `TestGuestOrderConcurrentClaimsTransferOnce` — 4 akun × 2 percobaan paralel:
   tepat satu transfer, sisanya replay (akun pemenang) atau konflik, owner
   tersimpan = pemenang (diverifikasi `go test -race`).
7. `TestGuestOrderClaimAlreadyClaimedWithoutMarker` — order yang di-claim
   sebelum kolom marker ada: pemilik sah dapat sukses idempoten, akun lain
   ditolak (backfill migrasi bukan batas keamanannya).
8. `TestGuestOrderClaimIgnoresMatchingEmail` — akun dengan email sama seperti
   kontak order tidak mendapat order maupun akses; cookie tetap satu-satunya
   bukti.

Lapisan HTTP-nya dikunci `backend/internal/handlers/guest_order_claim_handler_test.go`
(9 test, harness SQLite yang sama, router memakai stand-in `middlewares.Auth`):
delapan skenario di atas diulang lewat `POST /api/v1/orders/claim` dengan
pemetaan status (200 transfer, 200 replay `transferred=false`, 404
`NO_GUEST_ORDER_TO_CLAIM`, 409 `GUEST_ORDER_CLAIMED_BY_ANOTHER_ACCOUNT`), plus
`TestClaimOrderEndpoint_RequiresAuthenticatedAccount` — cookie guest valid tanpa
akun → 401 dan tidak ada yang bergerak.

TOCTOU/konkurensi (4 Sep 2026) dikunci tiga file tambahan:

- `internal/services/guest_concurrency_test.go` — order guest dengan
  `Idempotency-Key` sama secara paralel (satu booking, semua pemanggil menerima
  booking yang sama, allowance + jangkar kontak terpakai sekali);
  claim duplikat paralel oleh akun yang sama (tepat satu transfer, sisanya replay,
  tanpa error); resolusi identitas paralel dengan cookie hidup yang sama (semua
  resolve ke guest session yang sama, tidak ada identitas baru); binding
  chat→guest tidak bisa dicuri identitas lain, pemenang tunggal saat paralel, dan
  boleh diambil alih bila pemilik lama sudah kedaluwarsa.
- `internal/services/booking_idempotency_race_test.go` — stub repository yang
  mereproduksi transaksi ABORT karena unique index: loser membalas booking
  pemenang, lookup replay-nya di-scope owner + key hash pemanggil, dan kegagalan
  tanpa pemenang (limit guest, kontak invalid, DB down) tetap diteruskan. Jalur
  ini tidak bisa diuji lewat harness SQLite (satu koneksi ⇒ transaksi
  terserialisasi ⇒ lookup di dalam transaksi selalu menang).
- `internal/services/identity_resolution_race_test.go` — P1-H1: `resolveUser`
  yang kalah race TIDAK BOLEH merge by email (`ErrGoogleAccountExists`, tidak ada
  akun yang dikembalikan, tidak ada link/mapping baru, akun korban tidak
  disentuh), tetap sukses bila pemenangnya identitas `sub` yang sama, dan
  meneruskan kegagalan non-race; `LinkAccount` yang kalah constraint membalas
  `ErrGoogleIdentityTaken` (akun lain) atau no-op idempotent (akun sendiri).
  Pemulihan handler-nya di `internal/handlers/guest_chat_bind_handler_test.go`.
- `internal/services/guest_postgres_race_test.go` — **verifikasi mesin nyata
  (opsional)**. Empat skenario yang sama dijalankan di PostgreSQL dengan pool 8
  koneksi, jadi `FOR UPDATE` benar-benar berebut dan transaksi yang ditolak unique
  index benar-benar abort. Di-skip kecuali `VERO_TEST_POSTGRES_DSN` di-set:

  ```bash
  cd backend
  VERO_TEST_POSTGRES_DSN="host=127.0.0.1 port=5432 user=… password=… dbname=… \
    search_path=toctou_verify_tmp sslmode=disable" \
    go test -count=1 -run TestPostgres ./internal/services/
  ```

  Harness MENOLAK jalan bila `search_path` tidak menunjuk schema sekali-pakai yang
  namanya memuat `test`/`verify` — test ini melakukan `TRUNCATE`, jadi jangan
  pernah diarahkan ke schema aplikasi. Schema-nya dibuat otomatis
  (`CREATE SCHEMA IF NOT EXISTS`, nama divalidasi sebagai identifier polos);
  bersihkan setelah selesai dengan `DROP SCHEMA toctou_verify_tmp CASCADE`.

## Batasan yang tersisa

- Google OAuth tombol di frontend masih placeholder (disabled) — belum ada
  provider OAuth di backend.
- Order guest lama (dibuat sebelum fitur ini) tidak punya `guest_session_id`
  dan tidak bisa di-claim otomatis; tetap dapat diakses staff.
- Order guest yang dibuat SEBELUM jangkar kontak ada (4 Sep 2026) tidak punya
  baris `guest_order_entitlements`, jadi masih berjangkar cookie saja. Tidak ada
  backfill: normalisasi Go (strip `+tag`, lipat prefix telepon) tidak aman
  direplikasi di SQL.
- Pengunjung yang memakai email DAN telepon berbeda tetap dapat satu order —
  butuh verifikasi kontak (OTP) untuk menutupnya (keputusan produk).
- Token guest berlaku 30 hari; order guest yang belum di-claim setelah expiry
  token hanya bisa diakses staff (retention dapat diperpanjang via env).
- Rate limit masih per-IP in-memory single instance (lihat known-issues PRR-P1-3).

