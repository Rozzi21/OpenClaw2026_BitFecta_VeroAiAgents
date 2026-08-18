# Guest Order Limit — Rencana Implementasi

Status: **rencana sebelum implementasi** (18 Agustus 2026)

## 1. Arsitektur guest saat ini

`POST /api/v1/chat` publik membuat `ChatSession` anonymous (`UserID=NULL`) dan
mengikatnya ke cookie HttpOnly `vero_chat_session`. Cookie berisi UUID
`ChatSession`, path `/api/v1/chat`, sliding TTL default 7 hari. Chat baru atau
cookie yang hilang membuat session baru. Identitas ini hanya identitas chat,
bukan identitas guest global.

Order manual publik memakai `POST /api/v1/orders`. Handler membuat user guest
acak lewat `AuthService.GuestUser()`, lalu memanggil `BookingService.Create()`.
Order AI memakai `MCPService.executeCreateBooking()`, juga membuat user guest
acak lalu memanggil service yang sama. Karena tidak ada relasi persisted antara
guest dan order, backend belum bisa menerapkan limit lintas chat/browser.

## 2. Arsitektur booking saat ini

`Booking` wajib memiliki `UserID`, `TripID`, status booking/payment, pax,
contact, tanggal, dan total. Harga dihitung server-side dari `Trip` melalui
`priceBreakdown()`. `BookingService.Create()` saat ini melakukan lookup trip lalu
insert booking tunggal. Tidak ada transaction wrapper untuk lookup + insert,
locking entitlement, atau idempotency key.

Ownership order authenticated memakai `FindBookingForUser(id, userID)`. Staff
memakai lookup global. Guest belum memiliki jalur ownership untuk
`GET /bookings/:id`; marker order di `ChatMessage` hanya berlaku per chat
session dan bukan authorization proof.

## 3. Arsitektur authentication saat ini

Access JWT memakai audience `access`; refresh JWT memakai audience `refresh` dan
disimpan sebagai row `AuthSession` plus cookie HttpOnly. Customer frontend belum
memiliki login/register UI; auth flow backend sudah tersedia dan dipakai
backoffice. Middleware `Auth` hanya mengisi context untuk rute protected.

Perubahan ini tidak mengubah JWT, refresh rotation, role, atau session auth.

## 4. Identitas guest yang diusulkan

Tambah entity `GuestSession` dengan UUID primary key, `TokenHash` unique,
`FirstOrderID` nullable, `OrderCount`, `CreatedAt`, `UpdatedAt`, `ExpiresAt`.
Browser menerima opaque random token 32 byte dari cookie HttpOnly
`vero_guest_session`; hanya SHA-256 token disimpan server-side. Cookie berlaku
di `/api/v1` agar bisa dipakai chat, order, tracking, dan transisi login.

Guest token tidak berisi UUID/order/PII, tidak dipercaya dari body, dan tidak
pernah dilog raw. Expiry guest identity lebih panjang dari chat TTL (default
30 hari) agar chat baru, refresh, dan penghapusan localStorage tidak mereset
entitlement. Setelah expiry, guest harus login untuk akses order lama; policy
retensi dapat diperpanjang setelah data production ditinjau.

`Booking` mendapat nullable `GuestSessionID` dan guest order menyimpan nilai
tersebut. `UserID` tetap dipertahankan untuk backward compatibility: guest order
memakai isolated guest user legacy saat ini, tetapi `GuestSessionID` menjadi
otoritas ownership/limit. Tidak ada fake user baru pada setiap retry.

## 5. Kebijakan satu order

- Authenticated request: jalur booking existing, tanpa guest entitlement check.
- Guest request dengan guest session baru/order count 0: boleh membuat satu order.
- Guest request dengan count >= 1: ditolak dengan error code
  `GUEST_ORDER_LIMIT_REACHED` dan status `authentication_required`.

Policy berada di `BookingService` pada authoritative create path. Handler dan
MCP hanya meneruskan identity/context dan menampilkan UX.

## 6. Perubahan database

AutoMigrate menambah tabel `guest_sessions` dan kolom nullable
`bookings.guest_session_id`, plus idempotency metadata pada booking atau tabel
dedicated `booking_idempotency_keys`. Pilihan awal: tabel dedicated dengan
unique `(scope_owner, idempotency_key_hash)` agar retry tidak bergantung pada
booking lifecycle dan tidak menerima raw key sebagai persisted secret.

Index yang dibenarkan:

- unique `guest_sessions.token_hash`: lookup token O(1), mencegah token hash
  duplicate;
- unique `bookings.guest_session_id` **tidak** dipakai karena relasi guest lama
  dan future migration/linking perlu fleksibel; query ownership memakai index
  biasa bila diperlukan;
- unique idempotency scope + hash: mencegah duplicate logical request;
- `bookings.guest_session_id`: guest order lookup/authorization.

Migration idempotent, additive, mempertahankan User, Booking, Payment, dan
ChatSession. Existing bookings tetap authenticated/legacy user-owned dan tidak
dianggap guest entitlement tanpa mapping yang dapat dibuktikan.

## 7. Strategi transaksi

Guest create memakai satu PostgreSQL transaction:

1. resolve guest session dari token hash sebelum service call;
2. lock guest row `SELECT ... FOR UPDATE`;
3. lock/check idempotency key;
4. validate trip, pax, date, contact, price;
5. insert Booking dengan `GuestSessionID`;
6. update `OrderCount=1`, `FirstOrderID=booking.ID`;
7. persist idempotency result;
8. commit.

Authenticated create tetap memakai transaction untuk booking/idempotency, tanpa
guest row. Semua error rollback; entitlement hanya berubah setelah insert
booking berhasil dalam transaction yang sama.

## 8. Concurrency

`FOR UPDATE` pada satu `guest_sessions` row serializes simultaneous guest
requests. Request pertama mengunci count 0, membuat booking, consume count, lalu
commit. Request kedua membaca count 1 dan gagal. PostgreSQL unique constraint
pada idempotency key menjadi lapisan kedua untuk retry race. Tidak memakai mutex
in-memory karena deployment dapat memiliki lebih dari satu process.

## 9. API changes

- Guest cookie dibuat/refresh pada public chat/order flow.
- `POST /orders` menerima identity dari cookie, bukan payload.
- Error second order memakai envelope existing:
  `success=false`, `message`, `error={status,code}` dengan HTTP 403/409 yang
  konsisten; frontend membaca code, bukan message.
- Guest tracking memakai `GET /bookings/:id` dengan guest cookie ownership.
- Login/register mempertahankan guest cookie lalu menjalankan explicit claim
  flow. Claim hanya jika token valid, guest session memiliki order, order masih
  belum claimed, dan account valid. Tidak ada email-only auto-claim.

## 10. MCP changes

`executeCreateBooking` meneruskan guest identity/session ke
`BookingService.Create`. MCP boleh melakukan pre-check untuk UX, tetapi
BookingService tetap final authority. Error limit dikonversi ke structured
`ToolResult.Data`:

```json
{"status":"requires_authentication","code":"GUEST_ORDER_LIMIT_REACHED","message":"Please sign in to create another order."}
```

MCP tidak retry error code tersebut. `create_order` alias mengikuti policy
yang sama.

## 11. AI behavior

System prompt menjelaskan: guest boleh satu order tanpa login; order guest kedua
memerlukan login; backend response authoritative; jangan retry saat code
`GUEST_ORDER_LIMIT_REACHED`; jangan klaim sukses tanpa successful tool result.
`finalizeChat` mempertahankan booking claim guard yang sudah ada.

## 12. Frontend behavior

Customer frontend tidak menyimpan entitlement di localStorage. Cookie otomatis
dikirim dengan `credentials: include`. Setelah first success, tampilkan order
success, tracking, login/register, dan penjelasan bahwa order kedua butuh auth.
Saat error code limit, tampilkan authentication gate tanpa menghilangkan akses
tracking order pertama. Login/Register memakai backend auth dan membawa guest
cookie untuk explicit linking.

## 13. Migration strategy

Implement additive AutoMigrate + data-safe startup migration. Jangan mengubah
`UserID NOT NULL`, status booking, payment, atau chat history. Existing guest
orders yang tidak punya verifiable guest identity tidak dapat diklaim otomatis;
tetap bisa diakses melalui existing owner path/staff dan perlu support flow.

## 14. Security considerations

- Token 256-bit random, opaque, hash-only at rest, HttpOnly, Secure production,
  SameSite configurable, no raw-token logs.
- Guest booking authorization memeriksa cookie-derived session + booking
  `GuestSessionID`; UUID saja/email/phone tidak cukup.
- Token forged/unknown/expired tidak mendapat order access.
- Rate limit existing per-IP dipertahankan; tambah abuse limit khusus guest
  booking bila diperlukan, tetapi IP bukan business identity.
- Transaction + row lock + unique constraints menutup double-order race.
- Idempotency key dibatasi format/panjang, di-hash, dan tidak memakai booking ID.
- Claim guest order explicit, authorized, single-use; tidak ada email-only claim.
- Audit events: `guest_order_created`, `guest_order_limit_reached`,
  `guest_order_linked`, `guest_order_auth_required`; log hanya safe IDs/hash.
