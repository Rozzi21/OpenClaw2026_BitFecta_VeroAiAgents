# Backend - Service Layer, Business Logic, dan Integrasi

Dokumen ini menjelaskan lapisan backend Go: service layer, logika bisnis inti, mekanisme realtime, dan integrasi eksternal. Untuk arsitektur umum lihat [architecture.md](architecture.md); untuk endpoint lihat [api.md](api.md); untuk skema data lihat [database.md](database.md).

## Lokasi Kode Inti

| Path | Isi |
|---|---|
| `backend/cmd/server/main.go` | Entry point, wiring dependency, graceful shutdown |
| `backend/internal/services/` | Service layer, dipecah per-domain (lihat di bawah) |
| `backend/internal/services/services.go` | `Services` struct, `New()` (wiring), tipe bersama |
| `backend/internal/ai/ai_client.go` | Klien AI OpenAI-compatible + fallback lokal |
| `backend/internal/mcp/tools.go` | Katalog definisi tool MCP |
| `backend/internal/events/bus.go` | Event bus in-memory untuk SSE |
| `backend/internal/auth/` | JWTService, cookie refresh, audit log |

## Service Layer

Service di-wiring di `services.New()` (`services.go`). Container `Services` berisi: `Auth`, `AI`, `MCP`, `Trips`, `Bookings`, `Payments`, `Logs`, `Analytics`.

Sejak refactor 25 Jun 2026, kode dipecah **per-domain dalam satu package `services`** (bukan lagi satu file monolitik). API publik tidak berubah:

| File | Isi |
|---|---|
| `services.go` | `Services` struct, `New()`, `ChatContext`, `AuthRequestMeta`, `AuthIssueResult`, error vars |
| `auth_service.go` | `AuthService` (Register, CreateStaff, Login, Refresh, Logout, Me, legacy booking GuestUser, issueSession) |
| `ai_service.go` | `AIService` (Chat via `ChatContext`, `generateWithToolLoop`, tool execution loop, katalog & rekomendasi paket, memory summary, expiry cleanup) |
| `mcp_service.go` | `MCPService` (`Execute`, `executeCreateBooking`, `mock`) + `ToolResult` |
| `trip_service.go` | `TripService` + `buildTripFromRequest`, `buildItineraries` |
| `booking_service.go` | `BookingService` + `tripAdultPrice`/`tripChildPrice` |
| `payment_service.go` | `PaymentService` (Create, Find, Webhook, verifySignature, triggerN8N) |
| `log_service.go` / `analytics_service.go` | `LogService` / `AnalyticsService` |
| `helpers.go` | util bersama: `slugify`, `normalize`, `firstNonEmpty`, `firstNonZero`, `parseDate` |

Pola umum: tiap service adalah struct dengan dependency `repo` (repository), dan opsional `bus` (event), `cfg` (config), `jwt`, `mcp`, `client` (AI). Dependency di-inject manual via `services.New()`.

```go
func New(cfg config.Config, repo *repositories.Repository, jwt *auth.JWTService, bus *events.Bus) *Services {
    s := &Services{Config: cfg, Repo: repo, JWT: jwt, Events: bus}
    s.Auth = &AuthService{repo: repo, jwt: jwt, cfg: cfg}
    s.Bookings = &BookingService{repo: repo, bus: bus}
    s.MCP = &MCPService{repo: repo, bus: bus, bookings: s.Bookings, auth: s.Auth}
    aiClient := ai.NewClient(cfg.AIAPIKey, cfg.AIBaseURL, cfg.AIModel, cfg.AITemperature, cfg.AITimeout)
    s.AI = &AIService{repo: repo, mcp: s.MCP, bus: bus, client: aiClient, cfg: cfg}
    // ...
}
```

### AuthService

Tanggung jawab: register, login, refresh, logout, profil, guest user.

Poin penting:
- `issueSession()` menghasilkan token pair (access + refresh) dan menyimpan refresh JTI sebagai `AuthSession` di DB.
- `Refresh()` mengimplementasikan **rotasi token atomik** (sejak BUG-1, 27 Jul 2026): `RotateSession()` me-revoke sesi lama dalam satu `UPDATE ... WHERE token_jti=? AND revoked_at IS NULL AND expires_at > now()`; hanya request pemenang (`RowsAffected==1`) yang menerbitkan token baru, sehingga concurrent refresh (dua tab auto-refresh) tidak menghasilkan sesi ganda. Yang kalah race ditolak tanpa eskalasi — **reuse detection** hanya memicu `RevokeAllActiveSessionsByUser()` + log `refresh_token_reuse_detected` bila sesi di-revoke LEBIH LAMA dari `refreshRotationConcurrentWindow` (1 menit); revokasi dalam window dianggap race sah (bukan pencurian).
- `GuestUser()` membuat/menemukan user "Guest Traveler" (`guest@vero.local`) via `FirstOrCreateUser` untuk guest chat.
- Semua aksi auth mencatat audit via `auth.LogSecurity()`.

### AIService - Inti Produk

`AIService.Chat()` adalah jantung platform. Alur (lihat `services.go`):

1. Buat/lanjutkan `ChatSession`, simpan pesan user.
2. Jalankan tool loop via `generateWithToolLoop()`. LLM memutuskan tool mana yang perlu dipanggil dari katalog minimal yang aktif (`search_trips`, `select_package`, `collect_order_detail`, `create_booking`).
3. `search_trips` adalah satu-satunya sumber rekomendasi paket. Tool ini mengambil katalog published dari DB, melakukan scoring lokal, dan mengembalikan hingga 3 paket ke LLM serta ke frontend.
4. `select_package(trip_id)` menyimpan `SelectedTripID` pada `ChatSession`, menandakan user sudah memilih paket.
5. Bila LLM mengembalikan `tool_calls`, backend parse arguments, eksekusi via `MCPService.Execute()`, append hasil sebagai role `tool`, lalu panggil LLM lagi sampai ada final text response atau `MaxToolCallRounds` tercapai.
6. `create_booking` hanya boleh dianggap berhasil bila tool result `status=success`; jika model mengklaim pesanan dibuat tanpa hasil tersebut, backend mengganti response dengan pesan gagal aman.
7. Bila AI gagal/empty -> fallback response lokal, log kegagalan.
8. Simpan pesan assistant, refresh memory summary, publish `workflow_completed`.
9. Response `ChatResult` mengandung `show_recommendations` dan `recommendation_reason` — diturunkan dari hasil tool `search_trips` dan keberadaan `SelectedTripID`. Tidak ada lagi `selectRecommendedPackages()` otomatis setelah LLM menjawab.

Guest ownership: handler membuat atau memvalidasi anonymous `ChatSession` berdasarkan cookie HttpOnly `vero_chat_session`, lalu meneruskan `ChatContext{SessionID, UserID:nil}`. `UserID` nullable membedakan guest dari session authenticated tanpa shared guest account. Aktivitas chat memperbarui `LastActivityAt` dan `ExpiresAt` secara sliding (default 7 hari). `GET /chat/history` memakai cookie yang sama dan tidak menerima/menampilkan session ID.

Sliding expiration kini konsisten (sejak BUG-6, 28 Jul 2026): `Chat()` selalu menghitung ulang `expires_at = now + GuestSessionTTL` **sebelum** tool loop — dulu hanya diisi saat `ExpiresAt==nil` sehingga session near-expiry mempertahankan deadline lama dan bisa terhapus cleanup di tengah proses. Atomik lewat `UpdateChatSessionActivity`, menyamakan perilaku `GuestHistory`/`resolveGuestSession`. Karena `GuestSessionTTL` (7 hari) `>> AITimeout` (35 dtk), deadline selalu jatuh setelah request selesai.

Memory management: `refreshMemorySummary()` membuat ringkasan percakapan setelah >= `AI_MEMORY_SUMMARY_AFTER` (default 12) pesan, dibatasi `AI_MEMORY_MAX_CHARS` (default 1800). Alih-alih memuat SEMUA pesan sesi, method ini memakai `TailChatMessages()` untuk mengambil hanya pesan terakhir (estimasi berdasarkan `AIMemoryMaxChars / 200`), lalu memotong string ke maksimum karakter. Ini menghindari loading ribuan row pada sesi panjang.

Cleanup session dijalankan sementara oleh ticker satu jam di `cmd/server/main.go`, tetapi memanggil `AIService.CleanupExpiredChatSessions()` sehingga scheduler eksternal (cron/systemd/Kubernetes CronJob) dapat menggantikan adapter tanpa memindahkan SQL. Sejak BUG-6 (28 Jul 2026), method ini memakai cutoff `now - (AITimeout + chatSessionCleanupGraceExtra 30 dtk)` — bukan `now` — sebagai fail-safe agar ticker tidak pernah menghapus session yang masih ditulis request in-flight (repo `DeleteExpiredChatSessions` tidak diubah; geseran cutoff dilakukan di service agar repo tetap generik).

### MCPService

`Execute()` menjalankan tool, mencatat log tool selected/called/arguments/execution/result, lalu publish event `mcp_tool_executed`. Persistensi `ToolCall` + `AILog` dilakukan **asinkron** via goroutine agar tidak memblokir workflow chat. Goroutine ini melakukan **error logging via audit log** (`auth.LogSecurity` dengan event `tool_call_persist_failed` / `ai_log_persist_failed`) dan **single retry** (500ms delay) bila persistensi gagal.

Tool status saat ini:
- `search_trips` nyata: satu-satunya sumber rekomendasi paket. Menerima `query` dan `alternative`. Jika user sudah memilih paket (`SelectedTripID` terisi) tetapi tidak meminta alternatif, backend menolak tool ini untuk menghindari spam rekomendasi.
- `select_package(trip_id)` nyata: menyimpan paket terpilih ke `ChatSession.SelectedTripID`.
- `collect_order_detail` nyata: menyimpan draft detail booking (pax, tanggal, kontak) tanpa membuat booking.
- `create_booking` nyata: memanggil `BookingService.Create()` dan menyimpan booking ke DB. Response sukses memuat `{success:true, order_id, status, booking_id, booking_status, payment_status, total_price}`.
- `create_order` aktif sebagai alias aman dari `create_booking`.
- Tool lama `search_destination`, `search_hotels`, `calculate_budget`, `generate_itinerary`, dan `update_order_draft` dinonaktifkan dari katalog OpenAI.
- `create_payment` diblok karena DOKU/payment disabled.

Katalog di `mcp/tools.go` punya field `Enabled` per-tool; `ActiveCatalog()` mengembalikan tool aktif, dan `OpenAITools()` mengubahnya menjadi schema OpenAI tool calling.

### TripService

CRUD trip + transformasi DTO. Pola penting:
- `buildTripFromRequest()` menormalkan field (slug auto, dual field destination/location, default category/status).
- Saat status `published` dan `PublishedAt` kosong, set timestamp.
- Itinerary di-replace via `ReplaceTripItineraries()` (hapus + insert ulang dalam transaksi).

### BookingService & PaymentService

- `BookingService.Create()`: booking/order baru selalu `booking_status=pending`, `payment_status=pending_admin_processing` selama DOKU dinonaktifkan sementara. **Harga dihitung server-side** (SEC-3): `tripAdultPrice(trip)*adult_pax + tripChildPrice(trip)*child_pax` (menghormati diskon), bukan dari body client.
- `BookingService.Find(id, userID, isStaff)` / `PaymentService.Find(...)`: cek kepemilikan (SEC-2). Non-staff hanya bisa akses miliknya (repo `FindBookingForUser`/`FindPaymentForUser`).
- `PaymentService.Create()`: payment intent dengan `ExternalID=DOKU-<uuid>`, expired 15 menit. `Amount` diambil dari `Booking.TotalPrice` (SEC-3), bukan dari body.
- `PaymentService.Webhook()`: bila `DOKU_SECRET` di-set, signature **wajib** valid (SEC-4); di production secret wajib ada. Validasi `amount` (bila dikirim) + idempotency (status `paid`/`settlement` tidak bisa turun/diproses ulang). Bila `paid`/`settlement` -> publish `booking_confirmed` + trigger N8N.

Temporary: `PAYMENTS_ENABLED=false` by default disables DOKU routes, `PaymentService.Create/Find/Webhook`, and MCP `create_payment`. Orders are saved for manual admin processing in Backoffice.

### AnalyticsService

`Dashboard()` mengagregasi metrik via query GORM langsung (`db.Model().Count()`, `Select("COALESCE(SUM...)")`): total bookings, revenue, active trips, AI usage, payment success rate. Untuk aktivitas customer terbaru, memakai `RecentBookings(10)` (tanpa preload Payments) alih-alih `ListBookings()` agar query dashboard ringan — tidak memuat seluruh tabel booking + 3 preloads.

## Mekanisme Realtime (Event Bus + SSE)

Bukan queue/message broker eksternal. Implementasi in-memory di `backend/internal/events/bus.go`:

- `Bus` menyimpan `map[chan Event]struct{}` dengan `sync.RWMutex`.
- `Subscribe()` membuat channel buffered (kapasitas 32). Signature `(chan Event, bool)` sejak BUG-4 — menolak registrasi bila jumlah subscriber sudah `>= MaxSubscribers (100)`; caller menangani `ok=false` (handler membalas 503). Mencegah map `clients` tumbuh tak terbatas dari akumulasi koneksi zombie.
- `Publish()` **non-blocking**: pakai `select` dengan `default`, jadi event di-drop bila channel penuh (tidak memblok publisher). Channel tidak pernah di-`close` (BUG-2), jadi `Publish` tidak bisa menyentuh channel tertutup.
- Handler `EventStream` (di `handlers.go`) men-stream via SSE dengan heartbeat 25 detik (`time.NewTicker`, bukan `time.After` — menutup timer leak SEC-31 dan BUG-4).
- **Koneksi zombie guard (BUG-4, 28 Jul 2026):** `WriteTimeout` server diubah menjadi `15 * time.Second` (demi proteksi global slow-write, lihat ARCH-3), dan dinonaktifkan dinamis di handler SSE (`rc.SetWriteDeadline(time.Time{})`). Tiga lapis pertahanan dari BUG-4 tetap aktif untuk mengelola deadline secara manual: (1) write-error detection per-tulis via `http.NewResponseController(c.Writer)` + `SetWriteDeadline(now+10s)` + `Flush()` — error pada koneksi setengah-putus (NAT timeout/laptop sleep) membuat handler keluar + subscriber dilepas; (2) max lifetime koneksi `sseMaxLifetime=30 menit` via `time.NewTimer`, lalu handler mengirim event `reconnect` dan return — `EventSource` browser reconnect otomatis; (3) cap subscriber `MaxSubscribers=100` di atas.
- **Akses (SEC-18):** route `/api/v1/events/stream` diguard `Role(operator, admin)` — hanya staff. Payload publish disanitasi: workflow step hanya `{session_id, tool}`, `workflow_completed` hanya `{session_id}`, `mcp_tool_executed` hanya `{tool, status}`, booking/payment event hanya ID + status (tanpa PII kontak, external_id, amount).

Implikasi penting untuk AI: karena in-memory, event TIDAK persisten dan TIDAK survive restart atau multi-instance. Untuk horizontal scaling perlu diganti Redis pub/sub atau sejenis (lihat known-issues.md #7 dan ARCH-3). Batas subscriber/lifetime saat ini memakai konstanta package (`MaxSubscribers`, `sseMaxLifetime`) — pindahkan ke `config.Config` bila perlu env-tunable saat scaling.

## Background Jobs, Queue, Cache, Scheduler

Saat ini **TIDAK ADA**:
- Tidak ada background worker / cron / scheduler di backend.
- Tidak ada queue (RabbitMQ/Kafka/dll).
- Tidak ada cache (Redis/Memcached).

Satu-satunya "asinkron" adalah goroutine di `Bus.Publish` (implisit lewat channel) dan goroutine health check di `database.Health()`. Semua proses lain sinkron dalam request lifecycle.

Catatan: N8N (eksternal) yang berperan sebagai automation/scheduler di luar aplikasi Go ini.

## Integrasi Eksternal

| Integrasi | Lokasi | Fungsi | Fallback |
|---|---|---|---|
| AI Provider (OpenAI-compatible) | `ai/ai_client.go` | Generasi respons chat via `POST {AI_BASE_URL}/chat/completions` | Bila `AI_API_KEY` kosong atau gagal -> respons lokal |
| DOKU (payment gateway) | `payment_service.go` PaymentService | Webhook pembayaran, verifikasi HMAC-SHA256 | Bila `DOKU_SECRET` kosong: ditolak di production, diterima di dev (SEC-4) |
| N8N (automation) | `payment_service.go` `triggerN8N()` | Webhook pasca-pembayaran (`payment_success`) | Bila `N8N_WEBHOOK` kosong, di-skip |

### Klien AI (`ai/ai_client.go`)

- `NewClient()` set default: baseURL `https://api.openai.com/v1`, model `gpt-4o-mini`, timeout 35s.
- `Generate()`: bila API key kosong -> langsung return fallback. Jika ada key -> POST ke `/chat/completions` dengan `messages`, `temperature`, dan optional `tools`.
- `extractToolCalls()` parsing `choices[0].message.tool_calls`; `AIService.generateWithToolLoop()` mengeksekusi tool via MCP dan mengirim balik hasil role `tool` sebelum final response.
- `extractText()` parsing fleksibel: coba `choices[0].message.content`, lalu `choices[0].message.reasoning_content`, `reasoning`, `thinking`, lalu `choices[0].text`, lalu top-level keys (`text`, `output`, `content`, `message`). Fallback ini menjaga agar model penalaran (Qwen/DeepSeek) yang mengembalikan jawaban di `reasoning_content` tidak terabaikan ketika `content` kosong.

## Pola Penting untuk Diingat

1. **Service dipecah per-domain** dalam package `services` (mis. `auth_service.go`, `payment_service.go`). Saat menambah fitur, taruh di file domain yang sesuai (atau buat file baru), bukan menumpuk di `services.go`. Ikuti pola struct + method yang ada.
2. **Event-driven via publish**: aksi penting (trip_created, booking_created, payment_updated, dll) selalu `bus.Publish()`.
3. **Fallback-first untuk integrasi eksternal**: tiap integrasi punya jalur degradasi supaya demo tetap jalan.
4. **Logging audit untuk auth**: tiap aksi auth memanggil `auth.LogSecurity()`.
5. **Retry untuk operasi tak stabil**: MCP tool (3x), koneksi DB (5x).
