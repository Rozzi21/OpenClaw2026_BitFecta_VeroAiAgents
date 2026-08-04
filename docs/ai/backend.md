# Backend - Service Layer, Business Logic, dan Integrasi

Dokumen ini menjelaskan lapisan backend Go: service layer, logika bisnis inti, mekanisme realtime, dan integrasi eksternal. Untuk arsitektur umum lihat [architecture.md](architecture.md); untuk endpoint lihat [api.md](api.md); untuk skema data lihat [database.md](database.md).

## Lokasi Kode Inti

| Path | Isi |
|---|---|
| `backend/cmd/server/main.go` | Entry point, wiring dependency, graceful shutdown |
| `backend/internal/services/` | Service layer, dipecah per-domain (lihat di bawah) |
| `backend/internal/services/services.go` | `Services` struct, `New()` (wiring), tipe bersama |
| `backend/internal/handlers/` | HTTP handler, dipecah per-domain (`*_handlers.go`); `handlers.go` hanya wiring `Handler` struct + `New()` (ARCH-5, 31 Jul 2026) |
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
| `ai_service.go` | `AIService` (Chat via `ChatContext`, `generateWithToolLoop` — orkestrasi round LLM; blok single tool-call diekstrak ke helper `executeToolCall` + `toolResultMessage` (SEC-30), katalog & rekomendasi paket, memory summary, expiry cleanup; **streaming variant `ChatStream`/`generateWithToolLoopStream`/`finalizeChat` (PERF-1, 3 Agu 2026)** — final text round di-stream via `GenerateStream`, post-LLM logic shared via `finalizeChat` agar tak drift antar path) |

| `mcp_service.go` | `MCPService` (`Execute`, `executeCreateBooking`, `mock`, `persistAuditSync`, `clonePayload`) + `ToolResult` |
| `audit_pool.go` | `AuditPool` — bounded worker pool persistensi audit MCP (PERF-3); `AuditWriter` interface, `auditJob`, `Submit`/`Start`/`Stop` |
| `trip_service.go` | `TripService` + `buildTripFromRequest`, `buildItineraries` |

| `booking_service.go` | `BookingService` + `tripAdultPrice`/`tripChildPrice` |
| `payment_service.go` | `PaymentService` (Create, Find, Webhook, verifySignature, triggerN8N) |
| `log_service.go` / `analytics_service.go` | `LogService` / `AnalyticsService` |
| `helpers.go` | util bersama: `slugify`, `normalize`, `firstNonEmpty`, `firstNonZero`, `parseDate` |

Pola umum: tiap service adalah struct dengan dependency `repo` (repository), dan opsional `bus` (event), `cfg` (config), `jwt`, `mcp`, `client` (AI). Dependency di-inject manual via `services.New()`.

**Context propagation (SEC-26, FIXED 31 Jul 2026):** Semua method service dan repository menerima `ctx context.Context` sebagai parameter pertama dan meneruskannya ke bawah. Handler meneruskan `c.Request.Context()` ke service; service meneruskan ke repository; repository menjalankan query via `r.DB.WithContext(ctx)` (termasuk transaksi `Begin`/`Transaction`). `generateWithToolLoop` me-derive `context.WithTimeout(ctx, cfg.AITimeout)` dari **request ctx** (bukan `context.Background()`), sehingga klien yang putus akan me-cancel panggilan LLM (via `ai_client.Generate` yang memakai `http.NewRequestWithContext`) dan tool/DB. Aturan: jalur HTTP WAJIB pass request ctx; jangan pakai `context.Background()` kecuali untuk background terpisah (contoh: ticker cleanup di `main.go` memakai `context.Background()`+timeout 30s; `triggerN8N` detaches ke `context.Background()`+5s karena webhook dikirim setelah respons). Integrasi eksternal HTTP keluar memakai `http.NewRequestWithContext` + tutup body.

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

**Streaming respons (PERF-1, 3 Agu 2026):** `AIService.ChatStream(ctx, chatCtx, req, onDelta)` adalah pasangan streaming `Chat()` — identik untuk session prep + tool loop, tetapi **final text round di-stream** via `ai.Client.GenerateStream` (bukan `Generate`). Tool-selection rounds tetap non-streaming karena butuh `tool_calls` utuh sebelum dispatch MCP; hanya round teks akhir (saat `len(ToolCalls)==0` atau setelah `MaxToolCallRounds`) yang di-stream, setiap delta diteruskan ke `onDelta` agar handler SSE mem-flush-nya ke klien segera (TTFT rendah). Logika post-LLM (order-claim guard, fail-closed recommendation BUG-5, persist message, memory summary, `workflow_completed`) diekstrak ke `finalizeChat` yang dipakai bersama `Chat` dan `ChatStream` — tidak ada drift aturan antar path. Handler `streamChat` (`chat_stream_handlers.go`) menulis event SSE `delta`/{content} per token + event terminal `done` berisi `ChatResult` utuh; memakai pola BUG-4 (ResponseController + deadline per-tulis + flush) agar koneksi zombie terdeteksi. Guest cookie di-set via callback SEBELUM body SSE ditulis. Non-stream path (`stream:false`) tetap ada via `apiFetch` envelope JSON.

### MCPService

`Execute()` menjalankan tool, mencatat log tool selected/called/arguments/execution/result, lalu publish event `mcp_tool_executed`. Persistensi `ToolCall` + `AILog` dilakukan **asinkron via bounded worker pool** (`AuditPool`, PERF-3 — 4 Agu 2026) agar tidak memblokir workflow chat. `Execute` membangun `auditJob` (payload + result + meta, payload di-shallow-copy via `clonePayload` agar aman dari mutasi caller) lalu `s.audit.Submit(job)` — non-blocking, drop + log bila buffer penuh. Worker pool (2 worker, buffer 64) memproses job di goroutine terpisah: `json.Marshal` + `CreateToolCall` + `CreateAILog` dijalankan dengan `context.WithTimeout(context.Background(), 10s)` (detached, SEC-26). Error persistensi dicatat via `auth.LogSecurity` (event `tool_call_persist_failed` / `ai_log_persist_failed`); tidak ada retry (audit best-effort). Pool di-drain saat graceful shutdown via `Services.StopAudit()` (dipanggil `main.go` sebelum `server.Shutdown`). Saat `audit == nil` (unit test), `Execute` fallback ke `persistAuditSync` agar audit trail tetap terekam. Retry single (500ms) masih ada di `Execute` default branch (tool `mock`), terpisah dari audit pool.


Tool status saat ini:
- `search_trips` nyata: satu-satunya sumber rekomendasi paket. Menerima `query` dan `alternative`. Jika user sudah memilih paket (`SelectedTripID` terisi) tetapi tidak meminta alternatif, backend menolak tool ini untuk menghindari spam rekomendasi. Payload result tiap paket berisi: `id`, `slug`, `title`, `destination`, `location`, `category`, `duration`, `summary` (≤150 char), `price`, `highlights` (≤3), `image_url`. `scoreTrips` mengurutkan paket by score desc (stable) dan mengembalikan hingga 3 paket — paket dengan score 0 tetap disertakan (setelah yang match) agar customer melihat semua opsi saat katalog kecil (BUG-11, 5 Agu 2026).
- `select_package(trip_id)` nyata: menyimpan paket terpilih ke `ChatSession.SelectedTripID`.
- `collect_order_detail` nyata: menyimpan draft detail booking (pax, tanggal, kontak) tanpa membuat booking.
- `create_booking` nyata: memanggil `BookingService.Create()` dan menyimpan booking ke DB. Response sukses memuat `{success:true, order_id, status, booking_id, booking_status, payment_status, total_price}`.
- `create_order` aktif sebagai alias aman dari `create_booking`.
- Tool lama `search_destination`, `search_hotels`, `calculate_budget`, `generate_itinerary`, dan `update_order_draft` dinonaktifkan dari katalog OpenAI.
- `create_payment` diblok karena DOKU/payment disabled.

Katalog di `mcp/tools.go` punya field `Enabled` per-tool; `ActiveCatalog()` mengembalikan tool aktif, dan `OpenAITools()` mengubahnya menjadi schema OpenAI tool calling. Sejak AI-2 (3 Agu 2026), setiap `InputDefinition` membawa tipe JSON Schema eksplisit (`ParamTypeString`/`ParamTypeInteger`/`ParamTypeBoolean`/`ParamTypeNumber`) — `OpenAITools()` memetakan tipe akurat per-parameter (mis. `adult_pax`/`child_pax` = integer, `alternative` = boolean) sehingga Structured Outputs LLM tidak mengira semua argumen string. Parsing konsumsi di `mcp_service.go` tetap defensif (toleran `float64`/`string`/`bool`). Regresi dikunci oleh `tools_test.go`.


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

`Dashboard()` mengagregasi metrik (total bookings, revenue, active trips, AI usage, payment success rate) lewat method repository `CountBookings`, `SumBookingRevenue`, `CountTrips`, `CountAILogs`, `CountPayments`, `CountSuccessfulPayments` ([analytics_repository.go](../../backend/internal/repositories/analytics_repository.go)). Sebelum SEC-27 (1 Agu 2026) service ini mengakses `s.repo.DB` GORM langsung — escape hatch itu **sudah ditutup**; SQL agregat kini hidup di layer repository dan `AnalyticsService` depend hanya pada interface `AnalyticsRepository`. Untuk aktivitas customer terbaru, memakai `RecentBookings(10)` (tanpa preload Payments) alih-alih `ListBookings()` agar query dashboard ringan — tidak memuat seluruh tabel booking + 3 preloads.

## Dependency Inversion (SEC-27, 1 Agu 2026)

Layer service tidak lagi depend pada concrete `*repositories.Repository` maupun concrete service lain. Tiap service struct memakai interface narrow (interface segregation principle):

- Interface domain repo di [repositories/interfaces.go](../../backend/internal/repositories/interfaces.go) (`UserRepository`, `AuthSessionRepository`, `ChatRepository`, `TripRepository`, `BookingRepository`, `PaymentRepository`, `LogRepository`, `AnalyticsRepository`); compile-time assertion `var _ XRepository = (*Repository)(nil)` mengunci concrete.
- Interface lokal per-service memuat hanya method yang dipakai: `AuthRepository` (di `auth_service.go`), `BookingRepository` (di `booking_service.go`, memperluas repo Booking + `FindTrip`), `PaymentRepository` (di `payment_service.go`, Payment + `FindBooking`), `AIRepository` + `MCPToolExecutor` (di `ai_service.go`), `MCPRepository` + `BookingCreator` + `GuestUserProvider` (di `mcp_service.go`). `TripService`/`LogService` memakai interface domain langsung.
- Coupling antar-service di-invert: `MCPService`→`*BookingService`/`*AuthService` dan `AIService`→`*MCPService` diganti interface (`BookingCreator`, `GuestUserProvider`, `MCPToolExecutor`).

Concrete `*Repository` memenuhi semua interface secara implisit (structural typing Go), sehingga wiring `services.New()` TIDAK berubah — konstruktor tetap menerima `repo *repositories.Repository` lalu mengassign ke field interface. Implikasi untuk testing: tiap service bisa di-unit-test dengan mock interface, tanpa DB nyata.


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
- **`GenerateStream()` (PERF-1, 3 Agu 2026):** varian streaming — request memuat `stream: true`, body provider dibaca sebagai SSE via `bufio.Scanner`, setiap `choices[0].delta.content` diteruskan ke callback `onDelta` segera (TTFT rendah), `tool_calls` delta di-akumulasi (`accumulateToolCallDeltas`/`finalizeToolCalls`) agar kontrak `CompletionResponse` tetap konsisten. Konteks request (SEC-26) mengalir ke `http.NewRequestWithContext` sehingga disconnect klien membatalkan stream di hulu. Dipakai `AIService.ChatStream`/`generateWithToolLoopStream` untuk final text round; tool-selection rounds tetap non-streaming (butuh `tool_calls` utuh).

## Pola Penting untuk Diingat

1. **Service dipecah per-domain** dalam package `services` (mis. `auth_service.go`, `payment_service.go`). Saat menambah fitur, taruh di file domain yang sesuai (atau buat file baru), bukan menumpuk di `services.go`. Ikuti pola struct + method yang ada.
2. **Event-driven via publish**: aksi penting (trip_created, booking_created, payment_updated, dll) selalu `bus.Publish()`.
3. **Fallback-first untuk integrasi eksternal**: tiap integrasi punya jalur degradasi supaya demo tetap jalan.
4. **Logging audit untuk auth**: tiap aksi auth memanggil `auth.LogSecurity()`.
5. **Retry untuk operasi tak stabil**: MCP tool (3x), koneksi DB (5x).
