# Laporan Audit Backend — Vero Ai Travel Agents

- **Tanggal audit**: 6 September 2026
- **Cakupan**: seluruh kode Go pada `backend/` (89 file `.go`, ~17.889 baris, non-test + test)
- **Komit yang diaudit**: `5a26240` (`master`)
- **Metode**: baca knowledge base `docs/ai/` → baca seluruh source code backend per package → verifikasi mekanis (`go build`, `go vet`, `go test`, `go test -race`) → periksa kepatuhan konvensi `docs/ai/coding-rules.md` + `known-issues.md` (BUG-*, SEC-*, GO-P*).

> Status keseluruhan: **arsitektur dan kualitas kode secara umum baik**. Tidak ditemukan bug yang aktif menyala di jalur produksi default (PAYMENTS_ENABLED=false). Ditemukan **1 temuan P1 (bug laten pada jalur payment yang dipreservasi)**, beberapa **P2** (kebersihan/observability/anti-pattern laten), dan sejumlah **P3** (dead code / minor). Semua verifikasi mekanis LOLOS.

---

## 1. Ringkasan Eksekutif

| Aspek | Status | Catatan |
|---|---|---|
| `go build ./...` | ✅ Lolos | exit 0 |
| `go vet ./...` | ✅ Lolos | exit 0 |
| `go test ./...` | ✅ Lolos | semua package hijau |
| `go test -race` (middlewares, auth, ai, services) | ✅ Lolos | race detector bersih |
| Layered architecture | ✅ Patuh | Handler → Service → Repository → GORM |
| Envelope respons seragam | ✅ Patuh | semua endpoint via `utils.*` |
| Context propagation (SEC-26) | ✅ Patuh | `ctx` param pertama; `context.Background()` hanya di 3 jalur detach sah |
| Dependency Inversion (SEC-27) | ✅ Patuh | interface narrow + compile-time assertion |
| Larangan `.Save()` (DB-2) | ⚠️ 1 sisa | `UpdateChatSession` di `chat_repository.go`, **tanpa caller produksi** (laten) |
| Anti-IDOR (SEC-2) | ✅ Patuh | scoping per-user di repo/service |
| Pola terlarang lain (c.JSON mentah, repo.DB di service, secret hardcode) | ✅ Bersih | tidak ditemukan |

### Temuan berdasarkan tingkat keparahan

| ID | Sev | Ringkas |
|---|---|---|
| [BSD-1](#bsd-1-p1-doku-webhook-verifikasi-hmac-selalu-gagal) | **P1** | Verifikasi HMAC DOKU webhook selalu memakai body kosong → webhook selalu ditolak saat payment diaktifkan |
| [BSD-2](#bsd-2-p2-kardinalitas-label-prometheus-unbounded) | P2 | Label `path` di metrics memakai raw URL (memuat UUID) → cardinality tak terbatas |
| [BSD-3](#bsd-3-p2-save-dibanned-masih-ada-di-updatechatsession) | P2 | `UpdateChatSession` memakai `.Save()` (DB-2) — laten, tanpa caller |
| [BSD-4](#bsd-4-p2-tripsliplistquery-tidak-dinormalisasi) | P2 | `TripListQuery` tanpa clamp limit → endpoint publik bisa minta page tak terbatas |
| [BSD-5](#bsd-5-p3-handler-chat-dead-code) | P3 | `Handler.Chat` tidak terdaftar di rute (dead code) |
| [BSD-6](#bsd-6-p3-findbookingbysession-query-kolom-tidak-ada) | P3 | `FindBookingBySession` query kolom `session_id` yang tidak ada di model (dead code + query rusak) |
| [BSD-7](#bsd-7-p3-strings-title-deprecated) | P3 | `strings.Title` (deprecated) di `parseDate` helpers |
| [BSD-8](#bsd-8-p3-coverage-rendah) | P3 | Coverage rendah di `ai` (10%), `handlers` (9%), `auth` (19%); 5 package 0% |

---

## 2. Metodologi & Cakupan

1. Baca `docs/ai/README.md`, `project-map.md`, `architecture.md`, `modules.md`, `database.md`, `api.md`, `backend.md`, `coding-rules.md`, `known-issues.md`.
2. Baca **seluruh** source code produksi backend:
   - Entry point & wiring: `cmd/server/main.go`
   - `config`, `database`, `models`, `dto`, `routes`, `events`, `utils`
   - `middlewares` (semua), `auth` (jwt, cookie, audit, google), `payments`
   - `handlers` (semua 12 file produksi + `docs.go`), `repositories` (semua 11 file produksi)
   - `services` (ai_service 1084 baris, mcp_service 886 baris, auth, booking, guest, google_oauth, payment, trip, log, analytics, audit_pool, guest_entitlement, helpers)
   - `ai/ai_client.go`, `mcp/tools.go`
3. Verifikasi mekanis: `go build ./...`, `go vet ./...`, `go test ./...`, `go test -race ./internal/middlewares/ ./internal/auth/ ./internal/ai/ ./internal/services/`, `go test -cover ./internal/...`.
4. Pemeriksaan pola: grep untuk `.Save(`, `repo.DB` di service, `c.JSON` di handler, `context.Background()`/`TODO()`, `strings.Title`, TODO/FIXME, caller dari method yang mencurigakan (dead code).

---

## 3. Temuan Detail

**Konteks penting sebelum menilai temuan**: beberapa temuan (BSD-1, BSD-5, BSD-6) hidup di jalur yang sengaja dipreservasi saat fitur sedang non-aktif (`PAYMENTS_ENABLED=false`, tool `create_payment` disabled, dll.). Karena itu tidak ada yang "menyala sekarang", tetapi semuanya akan menggigit begitu fitur itu diaktifkan kembali — itulah mengapa tetap dilaporkan.

### BSD-1 [P1] — DOKU webhook: verifikasi HMAC selalu memakai body kosong

- **File**: `backend/internal/handlers/payment_handlers.go` (baris 34–48) dan `backend/internal/services/payment_service.go` (`verifyDokuSignature`).
- **Masalah**: handler memanggil `bind(c, &req)` (yang memakai `c.ShouldBindJSON`) **sebelum** `c.GetRawData()`. `ShouldBindJSON` membaca habis `c.Request.Body`; saat `GetRawData()` dipanggil setelahnya, body sudah EOF sehingga `req.RawBody = []byte{}`. Akibatnya `verifyDokuSignature` menghitung `HMAC-SHA256(secret, timestamp + "|" + "")` — tidak pernah cocok dengan signature DOKU asli yang memakai body sebenarnya.
- **Dampak**: saat `PAYMENTS_ENABLED=true` dan `DOKU_SECRET` terisi (diwajibkan di production oleh `Config.Validate`), **semua webhook DOKU akan ditolak dengan `ErrInvalidPaymentSignature`** → pembayaran tidak pernah dikonfirmasi. Bug ini saat ini laten karena payment disabled, tetapi merupakan blocker mutlak untuk mengaktifkan kembali payment.
- **Rekomendasi (salah satu)**:
  1. Baca `c.GetRawData()` di paling awal handler, lalu regenerasi body atau gunakan `ShouldBindBodyWith` (Gin menyimpan cache body untuk binding ulang); atau
  2. Bind manual: decode JSON sendiri dari `rawBody` ke DTO, tanpa `ShouldBindJSON`; atau
  3. Pindahkan pembacaan body ke middleware khusus webhook yang menyimpan raw body ke context.
  - Tambahkan regression test: webhook dengan body nyata + signature yang benar harus lolos verifikasi.

### BSD-2 [P2] — Metric Prometheus: label `path` kardinalitas tak terbatas

- **File**: `backend/internal/middlewares/metrics.go` (baris 38).
- **Masalah**: middleware mencatat `c.Request.URL.Path` (path mentah yang memuat parameter, mis. `/api/v1/trips/9c0c…-uuid`, `/api/v1/bookings/<uuid>`, `/api/v1/orders/<uuid>`) sebagai label `path` pada `http_requests_total` dan `http_request_duration_seconds`. Ini menciptakan time-series baru untuk setiap UUID baru → **unbounded cardinality** → memori proses naik seiring lalu lintas.
- **Rekomendasi**: pakai `c.FullPath()` (pola rute, mis. `/api/v1/trips/:id`); fallback ke `path` bila `FullPath()` kosong. Catatan: `StructuredLogger` di `logging.go` memakai `URL.Path` juga, tapi untuk *log* itu dapat diterima (satu baris per request, bukan label series).
- **Referensi pola yang sudah benar**: handler `EventStream` / `streamChat` tidak kena; hanya metrics yang bermasalah.

### BSD-3 [P2] — `.Save()` (pola dilarang DB-2) masih ada di `UpdateChatSession`

- **File**: `backend/internal/repositories/chat_repository.go` (baris 21–23).
- **Masalah**: `UpdateChatSession` memakai `r.DB.WithContext(ctx).Save(session)` — persis pola yang dilarang di `coding-rules.md §4` / `database.md` (DB-2): menulis semua kolom + meng-upsert asosiasi ter-Preload → Lost Update + clobber asosiasi.
- **Status**: **belum ada caller produksi** (grep: hanya deklarasi + interface), sehingga tidak ada dampak aktif. Namun method ini ada di `ChatRepository` interface dan akan dipanggil oleh service di masa depan tanpa sadar.
- **Rekomendasi**: ganti dengan `.Model(&models.ChatSession{}).Where("id = ?", id).Select("*").Updates(session)` (full-edit aman), atau hapus dari interface bila memang tidak dibutuhkan. Tambahkan komentar "no current callers; preserve association-safe form" seperti `UpdateBooking`/`UpdatePayment`.

### BSD-4 [P2] — `TripListQuery` tidak dinormalisasi; `limit<=0` = tanpa batas

- **File**: `backend/internal/handlers/trip_handlers.go` (`ListTrips`/`PublicPackages`), `backend/internal/repositories/trip_repository.go` (`ListTrips`), `backend/internal/dto/dto.go` (`TripListQuery`).
- **Masalah**: endpoint publik `GET /api/v1/packages` (tanpa auth) menerima `limit`/`offset` tanpa clamp. Di repo, `if query.Limit > 0 { db = db.Limit(...) }` berarti **`limit=0` atau negatif justru memuat SEMUA baris**, dan `offset` positif tanpa batas. Katalog saat ini kecil jadi risikonya rendah, tetapi endpoint publik yang tak terbatas scan adalah jebakan saat katalog membesar.
- **Catatan**: `dto.ListQuery` (bookings/logs/tool-calls) sudah pakai `Normalize()` (`DefaultListLimit=50`, `MaxListLimit=200`); `TripListQuery` tidak ikut — sudah dicatat sebagai known issue di `database.md`.
- **Rekomendasi**: beri `Normalize()` untuk `TripListQuery` juga (clamp `limit` ke `MaxListLimit`), atau set default `Limit=50`. Jaga backward-compat frontend yang mengirim `limit=0` untuk "semua".

### BSD-5 [P3] — `Handler.Chat` (chat terautentikasi non-streaming) adalah dead code

- **File**: `backend/internal/handlers/chat_handlers.go` (baris 16–41).
- **Masalah**: `Handler.Chat` tidak terdaftar di `routes.go` (route `/chat` memakai `GuestChat`; `GuestChat` sudah menangani path streaming maupun non-streaming). Tidak ada referensi `h.Chat(` di seluruh repo.
- **Dampak**: kode ganda yang membingungkan — dua jalur chat (guest vs authenticated) padahal hanya satu yang aktif. Logika ownership di `Chat` usang (`UserID: &userID` tanpa guard cookie) dan bisa menyesatkan pembaca.
- **Rekomendasi**: dokumentasikan sebagai "reserved for future authenticated chat" atau hapus.

### BSD-6 [P3] — `FindBookingBySession` me-query kolom yang tidak ada di model

- **File**: `backend/internal/repositories/booking_repository.go` (baris 10–14).
- **Masalah**: query `WHERE session_id = ?` terhadap tabel bookings, padahal model `Booking` **tidak punya kolom `session_id`** (linimasa asosiasi order sekarang lewat `chat_messages` marker `__order_created__` di MCP). Jika method ini pernah dipanggil, query gagal di runtime (SQL error). Saat ini **tidak ada caller** (hanya deklarasi interface + komentar).
- **Rekomendasi**: hapus bersama interface-nya, atau perbaiki query jika memang kelak dibutuhkan (mis. lewat join ke marker — lebih baik pakai mekanisme `findSessionOrder` yang sudah ada).

### BSD-7 [P3] — `strings.Title` (deprecated) di `parseDate`

- **File**: `backend/internal/services/helpers.go` (baris 147).
- **Masalah**: `strings.Title` sudah deprecated sejak Go 1.18 (`golang.org/x/text/cases` adalah penggantinya) dan tidak menangani huruf kapital ber-aksara Unicode lain. Juga, fallback `strings.Title(val)` hanya diuji di dalam loop layout sehingga kurang deterministik.
- **Dampak**: minor; parsing tanggal tetap jalan untuk bahasa Latin. Hanya kebersihan kode.
- **Rekomendasi**: ganti dengan casing manual atau `cases.Title(language.Indonesian)` dari `golang.org/x/text` bila akurasi nama bulan Indonesia ingin dipertahankan.

### BSD-8 [P3] — Coverage test tidak merata

Hasil `go test -cover ./internal/...`:

| Package | Coverage | Keterangan |
|---|---|---|
| `utils` | 100% | ✅ |
| `routes` | 95.4% | ✅ |
| `mcp` | 93.5% | ✅ |
| `config` | 54.4% | ok |
| `services` | 49.4% | ok (fokus guest/booking/OAuth/race) |
| `middlewares` | 20.2% | rendah |
| `auth` | 19.1% | rendah |
| `ai` | 10.1% | rendah (klien AI stream/tool loop) |
| `handlers` | 9.3% | rendah |
| `database` `dto` `events` `models` `payments` `repositories` | 0.0% | tanpa unit test langsung |

- **Catatan**: beberapa perilaku `repositories` sudah teruji tidak langsung via suite `services` (SQLite in-memory untuk guest order). Namun jalur payment (`payments`, `UpdatePayment`, `UpdatePaymentStatusAtomic`, `verifyDokuSignature`) **tidak teruji sama sekali** — itulah salah satu alasan BSD-1 lolos ke produksi.
- **Rekomendasi (prioritas)**: tambahkan unit test untuk `verifyDokuSignature`/webhook (termasuk regresi body raw), dan setidaknya smoke-test `UpdateChatSession`/`ListTrips` (clamp).

---

## 4. Hal yang Sudah Berjalan Baik (verifikasi kepatuhan)

### 4.1 Arsitektur & layering
- Handler hanya parsing/format; service tidak menyentuh `*gin.Context`; repository satu-satunya yang menyentuh GORM. ✅
- Tidak ada `c.JSON` di handler selain `docs.go` (OpenAPI, memang wajar). ✅
- Tidak ada akses `repo.DB` dari service (escape hatch analytics sudah ditutup SEC-27). ✅
- DI manual via interface narrow + compile-time assertions (`interfaces.go` var block). ✅

### 4.2 Context & konkurensi
- `ctx context.Context` konsisten sebagai parameter pertama di service/repo. ✅
- `context.Background()` hanya muncul di 3 tempat yang sah (detached): `audit_pool.go` (worker audit), `google_oauth_service.go:98` (discovery OIDC saat startup wiring), `payment_service.go:238` (webhook N8N fire-and-forget). ✅
- Transisi status dipakai `*StatusAtomic` (conditional UPDATE + RowsAffected) → TOCTOU-safe. ✅
- `go test -race` bersih di `middlewares`, `auth`, `ai`, dan `services`. ✅

### 4.3 Keamanan
- JWT dua audience; access TTL 15m; refresh disimpan sebagai AuthSession DB (revocable, rotasi atomik, reuse detection → revoke-all). ✅
- Refresh token hanya via cookie HttpOnly (Path=/api/v1/auth); ada audit `refresh_token_used_as_access`. ✅
- Cookie guest: token opaque hash-only di DB, HttpOnly. ✅
- Guest one-order rule dikunci di DB (row lock `FOR UPDATE` + `guest_order_entitlements` contact-anchored). ✅
- Google OAuth: library resmi (`go-oidc` + `x/oauth2`), state hash-only + single-use, nonce, PKCE, issuer pinned, open-redirect guard, anti-account-takeover via explicit link. ✅
- Upload: clamp ukuran + whitelist ekstensi + sniff magic bytes. ✅
- CORS allow-list, `SecureHeaders`, `RequestID`, rate limit per-IP (map cap 10k + janitor), `RequestBodyLimit` 64KiB untuk /chat & /orders. ✅
- SSE dibatasi role staff (SEC-18), payload disanitasi, write-error detection (BUG-4), cap subscriber 100. ✅
- `Config.Validate()` menolak secret default di production (JWT, DB password, DOKU, Google). ✅
- Webhook timestamp freshness ±5 menit + idempotency + anti-downgrade `ErrPaymentAlreadySettled`. (Kecuali cacat body di BSD-1.)

### 4.4 Kebersihan anti-pattern
- Larangan `.Save()`: hanya 1 sisa yang laten (BSD-3). ✅ (dengan catatan)
- Tidak ada secret/hardcode di kode; `.env.example` memakai nilai dev namun divalidasi. ✅
- Tidak ada `TODO`/`FIXME`/`HACK` di kode produksi. ✅
- `strings.Title` satu-satunya API deprecated yang ditemukan. ✅ (dengan catatan)

---

## 5. Kesesuaian dengan Temuan Audit Sebelumnya (known-issues)

### Sudah masuk perkara & masih terbuka (bukan temuan baru, di-recap untuk konteks)
- Google OAuth security audit (31 Agu 2026): 0 P0, 2 P1, 5 P2, 9 P3 — **belum diperbaiki**. Rincian: `docs/GOOGLE_OAUTH_SECURITY_AUDIT.md`.
- Guest order audit (3 Sep 2026): GO-P0-1, GO-P1-3, GO-P3-3, GO-P1-1 sudah FIXED; sisa P1/P2/P3 lain masih terbuka. Rincian: `docs/GUEST_ORDER_AUDIT.md`.

### Yang dengan sengaja dipertahankan (bukan bug)
- `create_payment` MCP `Enabled: false` + di-blok di `MCPService.Execute` + dikomentari di workflow chat — sesuai instruksi. ✅
- Tool legacy (`search_destination`, dll.) dipetakan ke `search_trips`; `mock()` hanya untuk `send_whatsapp` + unknown. ✅
- `UpdateBooking`/`UpdatePayment` tanpa caller produksi — **didokumentasikan sengaja** ("retained on the interface"); bentuknya sudah benar (association-safe). ✅
- Payment routes ter-isolasi di balik `PAYMENTS_ENABLED` dan menjawab 503 saat disabled. ✅

---

## 6. Statistik & Struktur

- **Total file Go**: 89 (46 produksi, 43 test).
- **Baris**: ~17.889 total; file terbesar `ai_service.go` (1.084), `mcp_service.go` (886), `google_oauth_service.go` (513), `ai_client.go` (528), `booking_service.go` (448), `models.go` (422).
- **Endpoint terdaftar**: 30+ termasuk health, auth (+Google OAuth 4 rute), chat (guest + history + authed sessions/messages), trips/packages, orders, bookings, payments (disable-able), logs, analytics, SSE, uploads, OpenAPI/docs, metrics.
- **Service**: Auth, Google, Guests, AI, MCP, Trips, Bookings, Payments, Logs, Analytics + AuditPool.
- **Repository**: 10 file domain + `interfaces.go` (10 interface) + compile assertions.
- **Entitas**: 15 model (`User`, `AuthSession`, `OAuthState`, `ExternalIdentity`, `GuestSession`, `GuestOrderEntitlement`, `ChatSession`, `ChatMessage`, `Trip`, `TripMedia`, `Itinerary`, `Booking`, `Payment`, `AILog`, `ToolCall`).

---

## 7. Rekomendasi Prioritas

1. **BSD-1 → wajib** sebelum mengaktifkan kembali `PAYMENTS_ENABLED`: perbaiki urutan baca raw body vs bind, tambahkan regression test webhook.
2. **BSD-2**: ganti `URL.Path` → `FullPath()` di metrics middleware (5 menit, mencegah OOM lambat di instance Prometheus).
3. **BSD-3 / BSD-6**: bersihkan dua repository method laten (rewrite association-safe atau hapus + dokumentasikan).
4. **BSD-4**: pindahkan `TripListQuery` ke jalur `Normalize()`/clamp.
5. **BSD-5 / BSD-7**: hapus atau dokumentasikan dead code `Handler.Chat`; ganti `strings.Title`.
6. **BSD-8**: tingkatkan coverage pada `ai`, `handlers`, dan terutama jalur payment yang sama sekali tidak teruji.

---

## 8. Kesimpulan

Backend Vero Ai Travel Agents adalah codebase Go yang **terpelihara dengan baik**: layering dan konvensi ditegakkan secara konsisten, keamanan auth/guest/Google ditangani serius (race-safe, atomik, audit trail), dan seluruh verifikasi mekanis (`build`, `vet`, `test`, `race`) berjalan hijau.

Satu-satunya halangan nyata (P1) adalah **bug laten verifikasi DOKU webhook** yang baru akan berdampak saat payment diaktifkan kembali — menegaskan pentingnya menutupnya sebelum fitur itu dibuka. Sisanya adalah kebersihan kode (P2/P3) yang bisa dikerjakan bertahap tanpa risiko.

---

*Laporan disusun dari hasil audit read-only terhadap commit `5a26240`. Tidak ada perubahan kode yang dilakukan selama audit.*