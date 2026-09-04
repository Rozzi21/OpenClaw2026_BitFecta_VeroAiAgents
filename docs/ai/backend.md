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
| `backend/internal/auth/` | JWTService, cookie refresh, audit log, Google OIDC client (`google.go`) |

## Service Layer

Service di-wiring di `services.New()` (`services.go`). Container `Services` berisi: `Auth`, `Google`, `AI`, `MCP`, `Trips`, `Bookings`, `Payments`, `Logs`, `Analytics`, `Guests`.

Sejak refactor 25 Jun 2026, kode dipecah **per-domain dalam satu package `services`** (bukan lagi satu file monolitik). API publik tidak berubah:

| File | Isi |
|---|---|
| `services.go` | `Services` struct, `New()`, `ChatContext`, `AuthRequestMeta`, `AuthIssueResult`, error vars |
| `auth_service.go` | `AuthService` (Register, CreateStaff, Login, Refresh, Logout, Me, legacy booking GuestUser, issueSession) |
| `google_oauth_service.go` | `GoogleOAuthService` (Google OAuth, 19 Agu 2026): `StartLogin` (state+nonce DB-backed), `Callback` (state atomik → tukar code → verifikasi id_token → resolve/link/create user → `AuthService.issueSession`), `resolveUser` (account linking), `sanitizeReturnTo` (open-redirect guard), helper `randomURLToken`/`hashOAuthState` |
| `ai_service.go` | `AIService` (Chat via `ChatContext`, `generateWithToolLoop` — orkestrasi round LLM; blok single tool-call diekstrak ke helper `executeToolCall` + `toolResultMessage` (SEC-30), katalog & rekomendasi paket, memory summary, expiry cleanup; **streaming variant `ChatStream`/`generateWithToolLoopStream`/`finalizeChat` (PERF-1, 3 Agu 2026)** — final text round di-stream via `GenerateStream`, post-LLM logic shared via `finalizeChat` agar tak drift antar path; **PERF-4 (11 Agu 2026)** — `generateWithToolLoop`/`generateWithToolLoopStream`/`buildMessages` terima `session models.ChatSession` dari caller, eliminasi `FindChatSession` redundan di `buildMessages` hemat 1 DB round-trip per request; **PERF-5 (11 Agu 2026)** — pre-LLM writes (`UpdateChatSessionActivity` + `AddChatMessage` user) dijalankan concurrent via `errgroup` di `prepareChatPreLLM`, hemat ~20-40ms di critical path sebelum LLM dipanggil) |

| `mcp_service.go` | `MCPService` (`Execute`, `executeSearchTrips`, `executeSelectPackage`, `executeCollectOrderDetail`, `executeCreateBooking`, `executeGetTripDetail`, `executeCalculateTripPrice`, `executeCheckTripAvailability`, `resolveAITrip`, `sanitizeStringSlice`, `mock`, `persistAuditSync`, `clonePayload`) + `ToolResult` |
| `audit_pool.go` | `AuditPool` — bounded worker pool persistensi audit MCP (PERF-3); `AuditWriter` interface, `auditJob`, `Submit`/`Start`/`Stop` |
| `trip_service.go` | `TripService` + `buildTripFromRequest`, `buildItineraries` |

| `booking_service.go` | `BookingService` + `tripAdultPrice`/`tripChildPrice` + shared `priceBreakdown`/`TripPriceBreakdown` (AIW-5 — source of truth pricing, dipakai booking + tool MCP) |
| `payment_service.go` | `PaymentService` (Create, Find, Webhook, verifySignature, triggerN8N) |
| `log_service.go` / `analytics_service.go` | `LogService` / `AnalyticsService` |
| `helpers.go` | util bersama: `slugify`, `normalize`, `firstNonEmpty`, `firstNonZero`, `parseDate`, `ParseIntFromString`, `sanitizePromptInjection`, `limitString`, `limitSlice` |

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

### GoogleOAuthService (19 Agu 2026)

`google_oauth_service.go` — "Continue with Google" sebagai provider tambahan. **Tidak mengganti auth yang ada**; hasil akhirnya sesi Vero normal via `AuthService.issueSession` (dipanggil internal karena satu package). Alur:

1. `StartLogin(ctx, returnTo, linkUserID)` — generate `state`+`nonce` (CSPRNG 32 byte), simpan hanya **SHA-256 hash** state ke `oauth_states` (TTL 10 mnt), validasi `returnTo` via `sanitizeReturnTo` (open-redirect guard: hanya path `/...`, tolak `//`/CRLF/backslash — browser menormalisasi `\` menjadi `/` saat parse header Location, jadi `/\evil.com` dinavigasi sebagai protocol-relative `//evil.com`). Balas URL consent Google (`AuthCodeURLForRedirect`). Redirect URI dipilih per-flow via `callbackRedirectURI(linkFlow)`: login → `GOOGLE_REDIRECT_URI` (`/google/callback`); link (`linkUserID != nil`) → `GOOGLE_LINK_REDIRECT_URI` (`/google/link/callback`, 24 Agu 2026).
2. `Callback(ctx, code, state, meta)` — `ConsumeOAuthState` atomik (single-use, pola `RotateSession` BUG-1) → `GoogleClient.ExchangeForRedirect` dengan redirect URI yang sama seperti saat start (token endpoint menolak redirect_uri yang beda) → tukar code + verifikasi id_token (signature JWKS RS256, iss/aud/exp, **nonce** binding) → `email_verified` wajib true → state ber-`link_user_id` → `LinkAccount` (tanpa sesi baru); selain itu `resolveUser` → `issueSession` → audit `login_success` (provider=google).
3. `resolveUser` (account resolution — **identitas by `sub`, BUKAN email; TANPA auto-merge**): (a) `FindUserByGoogleSub` membaca tabel kanonik `external_identities` (`UNIQUE(provider, provider_user_id)`) → login; (b) `FindUserByEmail` match tapi sub belum ter-link → **TOLAK** `ErrGoogleAccountExists` + audit `google_link_required` (link hanya via `LinkAccount` pada alur `/google/link` yang ter-auth — anti account-takeover); (c) tidak ada → `CreateUserWithGoogleIdentity` (user baru + ExternalIdentity atomik). Field user dari claim Google: verified email, `name`, `picture` (opsional → `ExternalIdentity.Picture`, bukan identity key). **Role = `RoleUser` hardcoded server-side; Google tidak pernah bisa pilih admin/operator (SEC-1)** — dikunci test `TestResolveUser_NeverPrivilegedRole`. Password bcrypt acak CSPRNG (pola SEC-24) + audit `google_account_created`. `users.google_sub` hanya denormalized fast-path mirror; ExternalIdentity sumber kebenaran (keputusan: `docs/GOOGLE_OAUTH_PLAN.md` §7). Race create (unique constraint) → fallback **hanya** resolve lewat kunci identitas yang sama seperti lookup utama (`FindUserByGoogleSub`); bila `sub` masih belum ter-link, jawabannya identik dengan guard (b) yaitu `ErrGoogleAccountExists` (audit `google_link_required`, `reason=create_race_email_taken`), dan kegagalan lain diteruskan apa adanya. Fallback by email DIHAPUS 4 Sep 2026 (P1-H1 TOCTOU: ia mengembalikan akun password yang belum pernah menautkan `sub` → guard anti-merge terlewati, dan claim guest order setelah callback memindahkan order pemanggil ke akun korban). `LinkAccount` memakai pola sama: loser constraint resolve ulang lewat `sub` → `ErrGoogleIdentityTaken` (akun lain) atau no-op idempotent (akun sendiri). Dikunci `internal/services/identity_resolution_race_test.go`.
4. Handler `google_auth_handlers.go` (redirect-based, bukan envelope JSON): set refresh cookie pada 302 + redirect ke FE dgn access token di **URL fragment**. Claim order guest direplikasi seperti login/register. Rute: `GET /auth/google` (login start), `GET /auth/google/callback`, `GET /auth/google/link` (guard Auth), `GET /auth/google/link/callback` (handler `GoogleCallback` yang sama; cabang link dipilih oleh `link_user_id` pada state).

`internal/auth/google.go` — `GoogleClient` OIDC di atas **library resmi**: `golang.org/x/oauth2` (code exchange `oauthConfig.Exchange`) + `github.com/coreos/go-oidc/v3` (verifikasi id_token: `verifier.Verify` memvalidasi signature RS256 via JWKS dari OIDC discovery, issuer pinned `https://accounts.google.com`, audience=clientID, expiry). **TIDAK ada crypto/JWT manual.** Nonce + `email_verified` dicek di atas verifikasi library. `NewGoogleClient` menjalankan OIDC discovery saat startup (hanya bila `GOOGLE_OAUTH_ENABLED=true`); gagal discovery → fail-closed (service disabled + endpoint 404, log `[google-oauth]`). `NewGoogleClientOfflineForTest` untuk unit test non-jaringan (verifier nil). Sentinel errors `ErrGoogle*` (SEC-28). Feature-flag `GOOGLE_OAUTH_ENABLED` (default false). Detail + keputusan: `docs/GOOGLE_OAUTH_PLAN.md`; batasan: `known-issues.md` A.14.

### AIService - Inti Produk


`AIService.Chat()` adalah jantung platform. Alur (lihat `services.go`):

1. Buat/lanjutkan `ChatSession`, simpan pesan user.
2. Jalankan tool loop via `generateWithToolLoop()`. LLM memutuskan tool mana yang perlu dipanggil dari katalog aktif (`search_trips`, `get_trip_detail`, `calculate_trip_price`, `check_trip_availability`, `select_package`, `collect_order_detail`, `create_booking` — AIW-5, 14 Agu 2026).
3. `search_trips` adalah satu-satunya sumber rekomendasi paket. Tool ini mengambil katalog published dari DB, melakukan scoring lokal, dan mengembalikan hingga 3 paket ke LLM serta ke frontend.
4. `select_package(trip_id)` menyimpan `SelectedTripID` pada `ChatSession`, menandakan user sudah memilih paket.
5. Bila LLM mengembalikan `tool_calls`, backend parse arguments, eksekusi via `MCPService.Execute()`, append hasil sebagai role `tool`, lalu panggil LLM lagi sampai ada final text response atau `MaxToolCallRounds` tercapai.
6. `create_booking` hanya boleh dianggap berhasil bila tool result `status=success`; jika model mengklaim pesanan dibuat tanpa hasil tersebut, backend memblok klaim, persist `AILog` (workflow `booking_claim_guard`), dan mengganti response dgn pesan gagal aman + kode pelacakan `AILog-xxxxxxxx` (AIW-6, 5 Agu 2026).
7. **Tool-failure surfacing (AIW-6, 5 Agu 2026):** bila `search_trips` mengembalikan `status=failed` dgn alasan bisnis `"a package is already selected"` dan model TIDAK menyebut konflik/opsi di responsnya, backend mengganti response dgn pesan konteks + opsi (lanjutkan / alternatif / batalkan). Title paket terpilih diambil dari tool result yang di-enrich (`selected_trip_title`). Backstop — respons LLM wajar yang sudah menyebut opsi diawetkan. **AIW-7 (14 Agu 2026):** backstop ini TIDAK menimpa respons bila tool informasi (`get_trip_detail`/`calculate_trip_price`/`check_trip_availability`) SUKSES di round yang sama (`hasSuccessfulInfoTool`) — pertanyaan detail/harga/ketersediaan tentang paket terpilih bukan pencarian baru; model kadang salah panggil `search_trips`, dan jawaban informatif dari tool info harus diawetkan, bukan diganti pesan konflik.
8. Bila AI gagal (genErr) -> persist `AILog` dgn ID unik, surface ke user: (a) penyataan singkat gangguan, (b) saran tindakan (coba lagi / minta alternatif), (c) kode pelacakan `AILog-xxxxxxxx`. Raw error tetap server-side (di `AILog.response` + log line); user tak lihat detail sensitif (AIW-6, 5 Agu 2026).
9. Simpan pesan assistant, refresh memory summary, publish `workflow_completed`.
10. Response `ChatResult` mengandung `show_recommendations` dan `recommendation_reason` — diturunkan dari hasil tool `search_trips` dan keberadaan `SelectedTripID`. Tidak ada lagi `selectRecommendedPackages()` otomatis setelah LLM menjawab.

Guest ownership: handler membuat atau memvalidasi anonymous `ChatSession` berdasarkan cookie HttpOnly `vero_chat_session`, lalu meneruskan `ChatContext{SessionID, UserID:nil}`. `UserID` nullable membedakan guest dari session authenticated tanpa shared guest account. Aktivitas chat memperbarui `LastActivityAt` dan `ExpiresAt` secara sliding (default 7 hari). `GET /chat/history` memakai cookie yang sama dan tidak menerima/menampilkan session ID.

Sliding expiration kini konsisten (sejak BUG-6, 28 Jul 2026): `Chat()` selalu menghitung ulang `expires_at = now + GuestSessionTTL` **sebelum** tool loop — dulu hanya diisi saat `ExpiresAt==nil` sehingga session near-expiry mempertahankan deadline lama dan bisa terhapus cleanup di tengah proses. Atomik lewat `UpdateChatSessionActivity`, menyamakan perilaku `GuestHistory`/`resolveGuestSession`. Karena `GuestSessionTTL` (7 hari) `>> AITimeout` (35 dtk), deadline selalu jatuh setelah request selesai.

Memory management: `refreshMemorySummary()` membuat ringkasan percakapan setelah >= `AI_MEMORY_SUMMARY_AFTER` (default 12) pesan, dibatasi `AI_MEMORY_MAX_CHARS` (default 1800). Alih-alih memuat SEMUA pesan sesi, method ini memakai `TailChatMessages()` untuk mengambil hanya pesan terakhir (estimasi berdasarkan `AIMemoryMaxChars / 200`), lalu memotong string ke maksimum karakter. Ini menghindari loading ribuan row pada sesi panjang.

Cleanup session dijalankan sementara oleh ticker satu jam di `cmd/server/main.go`, tetapi memanggil `AIService.CleanupExpiredChatSessions()` sehingga scheduler eksternal (cron/systemd/Kubernetes CronJob) dapat menggantikan adapter tanpa memindahkan SQL. Sejak BUG-6 (28 Jul 2026), method ini memakai cutoff `now - (AITimeout + chatSessionCleanupGraceExtra 30 dtk)` — bukan `now` — sebagai fail-safe agar ticker tidak pernah menghapus session yang masih ditulis request in-flight (repo `DeleteExpiredChatSessions` tidak diubah; geseran cutoff dilakukan di service agar repo tetap generik).

**Streaming respons (PERF-1, 3 Agu 2026):** `AIService.ChatStream(ctx, chatCtx, req, onDelta)` adalah pasangan streaming `Chat()` — identik untuk session prep + tool loop, tetapi **final text round di-stream** via `ai.Client.GenerateStream` (bukan `Generate`). Tool-selection rounds tetap non-streaming karena butuh `tool_calls` utuh sebelum dispatch MCP; hanya round teks akhir (saat `len(ToolCalls)==0` atau setelah `MaxToolCallRounds`) yang di-stream, setiap delta diteruskan ke `onDelta` agar handler SSE mem-flush-nya ke klien segera (TTFT rendah). Logika post-LLM (order-claim guard, fail-closed recommendation BUG-5, persist message, memory summary, `workflow_completed`) diekstrak ke `finalizeChat` yang dipakai bersama `Chat` dan `ChatStream` — tidak ada drift aturan antar path. Handler `streamChat` (`chat_stream_handlers.go`) menulis event SSE `delta`/{content} per token + event terminal `done` berisi `ChatResult` utuh; memakai pola BUG-4 (ResponseController + deadline per-tulis + flush) agar koneksi zombie terdeteksi. Guest cookie di-set via callback SEBELUM body SSE ditulis. Non-stream path (`stream:false`) tetap ada via `apiFetch` envelope JSON.

### MCPService

`Execute()` menjalankan tool, mencatat log tool selected/called/arguments/execution/result, lalu publish event `mcp_tool_executed`. Persistensi `ToolCall` + `AILog` dilakukan **asinkron via bounded worker pool** (`AuditPool`, PERF-3 — 4 Agu 2026) agar tidak memblokir workflow chat. `Execute` membangun `auditJob` (payload + result + meta, payload di-shallow-copy via `clonePayload` agar aman dari mutasi caller) lalu `s.audit.Submit(job)` — non-blocking, drop + log bila buffer penuh. Worker pool (2 worker, buffer 64) memproses job di goroutine terpisah: `json.Marshal` + `CreateToolCall` + `CreateAILog` dijalankan dengan `context.WithTimeout(context.Background(), 10s)` (detached, SEC-26). Error persistensi dicatat via `auth.LogSecurity` (event `tool_call_persist_failed` / `ai_log_persist_failed`); tidak ada retry (audit best-effort). Pool di-drain saat graceful shutdown via `Services.StopAudit()` (dipanggil `main.go` sebelum `server.Shutdown`). Saat `audit == nil` (unit test), `Execute` fallback ke `persistAuditSync` agar audit trail tetap terekam. Retry single (500ms) masih ada di `Execute` default branch (tool `mock`), terpisah dari audit pool.


Tool status saat ini:
- `search_trips` nyata: satu-satunya sumber rekomendasi paket (discovery/katalog). Menerima `query` dan `alternative`. Jika user sudah memilih paket (`SelectedTripID` terisi) tetapi tidak meminta alternatif, backend menolak tool ini untuk menghindari spam rekomendasi. Result gagal membawa `selected_trip_id` + `selected_trip_title` (di-enrich via `FindTrip`) supaya `finalizeChat` bisa memberitahu user PAKET MANA yang sudah dipilih + opsi (AIW-6, 5 Agu 2026). Payload result tiap paket berisi: `id`, `slug`, `title`, `destination`, `location`, `category`, `duration`, `summary` (≤150 char), `price`, `highlights` (≤3), `image_url`, **plus pricing aman (AIW-5, 14 Agu 2026): `adult_price`, `adult_effective_price`, `child_price`, `discount_enabled`, `discount_price`, `child_discount_enabled`, `child_discount`**. `price` dipertahankan untuk backward-compat kartu rekomendasi frontend. `scoreTrips` mengurutkan paket by score desc (stable) dan mengembalikan hingga 3 paket — paket dengan score 0 tetap disertakan (setelah yang match) agar customer melihat semua opsi saat katalog kecil (BUG-11, 5 Agu 2026).
- `get_trip_detail(trip_id)` nyata (AIW-5, 14 Agu 2026): detail penuh SATU paket saat user minta detail. Mengembalikan info dasar, harga dewasa/anak (normal + efektif), info diskon, `itinerary` (harian), `amenities_included`/`amenities_excluded`, `references`, `media`, kuota pax default (`adult_pax_quota`/`child_pax_quota`), dan `package_start_date`/`package_end_date` bila diset. HANYA field yang diperlukan AI — tanpa bookkeeping internal DB (CreatedAt/UpdatedAt/DeletedAt, publish scheduling). Semua free-text di-sanitize (AIW-1); lookup via `resolveAITrip` mengembalikan error AI-safe (`invalid trip_id`/`trip not found`), tidak pernah raw DB error.
- `calculate_trip_price(trip_id, adult_pax, child_pax)` nyata (AIW-5, 14 Agu 2026): satu-satunya sumber total harga untuk AI. LLM DILARANG menjumlahkan sendiri. Memakai shared helper `priceBreakdown()` — helper yang SAMA dipakai `BookingService.Create()` — sehingga total quote identik dengan total yang ditagih saat booking. Mengembalikan breakdown lengkap: harga normal/efektif per-unit dewasa & anak, flag + nominal diskon, kuantitas, subtotal per-baris, dan `total`. Pax di-bound sama seperti booking (0..`dto.MaxBookingPax`).
- `check_trip_availability(trip_id, travel_date, adult_pax, child_pax)` nyata (AIW-5, 14 Agu 2026): verifikasi ketersediaan dari data katalog backend (status published, window `PackageStartDate`/`PackageEndDate`, kuota default pax). Platform TIDAK punya tabel inventory per-tanggal, jadi ini best-effort dari katalog (bukan tebakan): mengembalikan `available` + `availability_confirmed` + `reasons` (bila diblok) atau `note` (bila tersedia). AI diinstruksikan tidak menjamin ketersediaan tanpa tool ini dan menyebut ketersediaan final dikonfirmasi saat booking.
- `check_order_status()` nyata (AIW-8, 14 Agu 2026): cek apakah pesanan sudah dibuat PADA SESI INI (session-scoped, tanpa parameter) dan kembalikan `order_id`/`booking_status`/`payment_status`/`total_price`. Link order↔session disimpan sebagai marker `ChatMessage` role `system` dengan prefix `__order_created__:` + JSON (`orderMarker`) yang ditulis setelah `create_booking` sukses — tabel `bookings` TIDAK punya kolom `session_id`, jadi penandaan memakai `chat_messages.session_id` yang sudah ter-index (tanpa migrasi skema). Marker hanya menyimpan identifier + total + nama kontak (email/phone TIDAK disimpan karena riwayat chat di-replay ke LLM). **Anti double-order:** `create_booking` memanggil `findSessionOrder` dulu; bila marker ada, tolak duplikat (status=failed, `order_exists=true`, sertakan `order_id` existing) tanpa memanggil `BookingService.Create` lagi.
- **Catatan (AIW-8):** `Repository.FindBookingBySession` adalah DEAD CODE yang query kolom `bookings.session_id` yang TIDAK ADA di model/DB — akan error bila dipanggil. Jangan dipakai; pakai `check_order_status`/marker. Hapus atau perbaiki (tambah kolom `session_id` + migrasi) bila suatu saat butuh query order-by-session di level DB.
- `select_package(trip_id)` nyata: menyimpan paket terpilih ke `ChatSession.SelectedTripID`.
- `collect_order_detail` nyata: menyimpan draft detail booking (pax, tanggal, kontak) tanpa membuat booking.
- `create_booking` nyata: menyimpan booking ke DB. Response sukses memuat `{success:true, order_id, status, booking_id, booking_status, payment_status, total_price}`. **Pemilihan identitas order (27 Agu 2026):** `MCPService.Execute` menerima `userID *uuid.UUID` dari `ChatContext.UserID` (diisi `GuestChat` bila `OptionalAuth` memvalidasi Bearer token). Bila `userID` non-nil → `BookingService.Create` (order milik akun, TANPA guest limit — inilah yang membuat user Google/password eligible membuat order tambahan dari chat); bila nil → `BookingService.CreateGuest` (jalur guest, limit satu order tetap berlaku). `ChatContext.GuestCookieBound` menjamin Bearer token hanya meng-upgrade atribusi pada sesi anonim yang ownership-nya sudah dibuktikan cookie — tidak membuka akses ke sesi milik user lain (`sessionOwnedByContext`). Interface `BookingCreator` kini memuat `Create` + `CreateGuest`; regresi dikunci `TestCreateBookingAuthenticatedUsesAccountPath`.
- `create_order` aktif sebagai alias aman dari `create_booking`.
- Tool lama `search_destination`, `search_hotels`, `calculate_budget`, `generate_itinerary`, dan `update_order_draft` dinonaktifkan dari katalog OpenAI.
- `create_payment` diblok karena DOKU/payment disabled.

Katalog di `mcp/tools.go` punya field `Enabled` per-tool; `ActiveCatalog()` mengembalikan tool aktif, dan `OpenAITools()` mengubahnya menjadi schema OpenAI tool calling. Sejak AI-2 (3 Agu 2026), setiap `InputDefinition` membawa tipe JSON Schema eksplisit (`ParamTypeString`/`ParamTypeInteger`/`ParamTypeBoolean`/`ParamTypeNumber`) — `OpenAITools()` memetakan tipe akurat per-parameter (mis. `adult_pax`/`child_pax` = integer, `alternative` = boolean) sehingga Structured Outputs LLM tidak mengira semua argumen string. Parsing konsumsi di `mcp_service.go` tetap defensif (toleran `float64`/`string`/`bool`). Regresi dikunci oleh `tools_test.go`.


### TripService

CRUD trip + transformasi DTO. Pola penting:
- `buildTripFromRequest()` menormalkan field (slug auto, dual field destination/location, default category/status).
- Saat status `published` dan `PublishedAt` kosong, set timestamp.
- Itinerary di-replace via `ReplaceTripItineraries()` (hapus + insert ulang dalam transaksi).

### GuestService (GUEST ORDER LIMIT, 18 Agu 2026)

`guest_service.go` mengelola identitas tamu server-side untuk kebijakan satu
order per guest. `Resolve()` membaca cookie `vero_guest_session` (opaque random
256-bit, hash SHA-256 di `guest_sessions.token_hash`) atau membuat session baru
+ user guest terisolasi; `Authenticate()` memvalidasi token untuk tracking;
`AttachChat()` mengikat chat ke guest identity (conditional bind, lihat di bawah);
`ClaimOrder()` mentransfer
order guest ke akun setelah login/register (cookie-diverifikasi, single-use,
audit `guest_order_linked`). Audit tambahan: `guest_order_created`,
`guest_order_limit_reached`, `guest_order_auth_required`, `guest_chat_bind_refused`
— hanya safe IDs, tanpa raw token. Detail: [GUEST_ORDER_LIMIT.md](../GUEST_ORDER_LIMIT.md).

**Binding chat→guest adalah input otorisasi (GO-P2-7, 4 Sep 2026).** Cabang guest
MCP `create_booking` menurunkan PEMILIK order dari
`chat_sessions.guest_session_id`, jadi siapa pun yang terakhir menulis kolom itu
menentukan allowance siapa yang terpakai dan order itu milik siapa. Karena itu
`AttachChat()` memakai `Repository.BindChatSessionGuest` — SATU conditional UPDATE
yang hanya menang bila kolom masih `NULL`, sudah berisi guest yang sama
(re-bind idempoten tiap request), atau guest yang terikat sudah
kedaluwarsa/hilang (identitas mati tidak memiliki apa pun, dan takeover-nya tidak
memberi akses ke order lama karena semua jalur order me-resolve cookie hash
terhadap session yang HIDUP). Loser dapat `ErrChatSessionGuestMismatch` + audit
`guest_chat_bind_refused`, dan `GuestChat` mencetak chat session BARU untuk
identitas pemanggil (`rebindGuestChatSession`, pola SEC-17) alih-alih memakai
sesi milik orang lain. Dikunci `internal/services/guest_concurrency_test.go` +
`internal/handlers/guest_chat_bind_handler_test.go`.

**Claim aman + idempoten (GO-P1-3 / GO-P3-3, 4 Sep 2026).**
`ClaimOrder(ctx, token, userID) (GuestOrderClaimResult, error)` kini melaporkan
hasil, bukan hanya error. Bukti kepemilikan HANYA cookie `vero_guest_session`
(hash-nya me-resolve baris guest session yang order-nya di-anchor); booking id
tidak pernah jadi input, dan email/kontak tidak pernah dibaca — tidak ada
auto-claim by email. Outcome yang bisa dibedakan pemanggil:

| Hasil | Arti |
|---|---|
| `Transferred=true`, err nil | kepemilikan berpindah di call ini (audit `guest_order_linked`) |
| `Transferred=false`, err nil | replay idempoten: akun ini SUDAH pemiliknya, tidak ada tulisan (audit `guest_order_claim_replayed`) |
| `ErrGuestOrderNothingToClaim` | no-op normal: tanpa cookie, session tak dikenal/kedaluwarsa, atau belum pernah order |
| `ErrGuestOrderClaimConflict` | order milik AKUN LAIN → ditolak, tidak pernah dipindah diam-diam (audit `guest_order_claim_conflict`) |
| `ErrGuestOrderClaimUnauthenticated` | `userID == uuid.Nil` → ditolak sebelum menyentuh DB |
| error lain | kegagalan repository (audit `guest_order_claim_failed` + `reason` kategori) |

Repository `ClaimGuestOrder` mengembalikan `repositories.GuestOrderClaim{BookingID,
OwnerID, Transferred}` — fakta DB; policy (replay vs penolakan) tetap di service.
Satu transaksi: lock baris guest `FOR UPDATE` → baca marker `claimed_user_id`
SEBELUM menulis apa pun (kalau sudah ada, kepemilikan tidak pernah dihitung
ulang) → conditional UPDATE booking (`WHERE id = ? AND guest_session_id = ?`,
`RowsAffected == 1`) → UPDATE marker (`WHERE claimed_user_id IS NULL`); marker
gagal ⇒ transaksi rollback, jadi tidak ada booking ter-claim tanpa claimant
tercatat. Bila marker masih NULL tapi booking sudah keluar dari jalur guest
(claim pra-migrasi), pemilik AKTUAL dibaca dari baris booking dan dilaporkan —
bukan ditimpa. Handler (`handlers/helpers.go: claimGuestOrder`) memakai satu jalur
untuk Register/Login/Google callback dan tetap non-fatal terhadap penerbitan
sesi; yang berubah: kegagalan dan penolakan tidak lagi menyatu jadi satu baris
log generik. Regresi dikunci `internal/services/guest_order_claim_test.go`
(valid claim, identitas guest invalid, guest salah, user salah, duplikat,
konkuren, order sudah di-claim tanpa marker, serangan email-only).

**Jalur retry claim: `POST /api/v1/orders/claim` (4 Sep 2026).** Hook otomatis di
Register/Login/GoogleCallback best-effort, jadi claim bisa ter-skip senyap
(cookie guest tidak terkirim pada callback Google lintas-situs bila `SameSite`
ketat — GO-P2-6) dan order tertinggal di identitas guest yang tak bisa login.
`handlers.ClaimOrderToAccount` (grup `protected` ⇒ `middlewares.Auth`) memanggil
`Guests.ClaimOrder` yang SAMA dengan bukti yang sama: Bearer token = akun,
cookie `vero_guest_session` = order, tanpa body (order id/email bukan input).
Pemetaan outcome → HTTP: transfer/replay `200` (`data.transferred`),
`ErrGuestOrderNothingToClaim` → `404 NO_GUEST_ORDER_TO_CLAIM`,
`ErrGuestOrderClaimConflict` → `409 GUEST_ORDER_CLAIMED_BY_ANOTHER_ACCOUNT`,
tanpa akun → `401`, kegagalan repository → `500` generik (SEC-15). Handler
fail-closed sendiri saat `user_id` kosong. Dikunci
`internal/handlers/guest_order_claim_handler_test.go` (9 test HTTP-level).
Konsumen: `frontend/src/app/order/[id]/page.tsx` memanggilnya sekali bila
`GET /bookings/:id` gagal untuk sesi aktif, lalu mencoba ulang.


**Jangkar kontak (GO-P0-1, 4 Sep 2026).** `Resolve()` tetap boleh mencetak
identitas baru saat cookie hilang (memutus itu akan mematahkan pengunjung baru
yang sah), jadi `guest_sessions.order_count` sendirian berarti "satu order per
cookie yang mau disimpan klien". `guest_entitlement.go` menambah jangkar kedua
yang tidak dipilih klien: kontak order yang dinormalisasi (email di-lowercase +
buang `+tag`; telepon jadi digit + lipat prefix `00`/`0` → `62`), di-hash jadi
`sha256("<channel>:<nilai>")` dan disimpan di `guest_order_entitlements`
(unique index `contact_key`). Penegakan tetap di `BookingService` — bukan di
handler, cookie, frontend, ChatSession, AI/MCP, atau IP. `GuestService` sengaja
TIDAK tahu soal jangkar ini: pemisahan tanggung jawab sama seperti Google OAuth
(identitas) vs `BookingService` (policy).

### BookingService & PaymentService

- `BookingService.Create()`: booking/order baru selalu `booking_status=pending`, `payment_status=pending_admin_processing` selama DOKU dinonaktifkan sementara. **Harga dihitung server-side** (SEC-3), bukan dari body client. Sejak AIW-5 (14 Agu 2026) total dihitung via shared helper `priceBreakdown(trip, adultPax, childPax).Total` — helper yang sama dipakai tool MCP `calculate_trip_price`, sehingga quote AI identik dengan tagihan booking. `priceBreakdown` internal memakai `tripAdultPrice`/`tripChildPrice` (menghormati diskon) — logic pricing tidak diduplikasi.
- `BookingService.Find(id, userID, isStaff)` / `PaymentService.Find(...)`: cek kepemilikan (SEC-2). Non-staff hanya bisa akses miliknya (repo `FindBookingForUser`/`FindPaymentForUser`).
- **Idempotency race (GO-P2-3, 4 Sep 2026)**: lookup `FindBookingByIdempotency` di dalam transaksi bisa miss di dua request paralel dengan owner + key yang sama; `bookings.idempotency_key_hash` (UNIQUE) menolak yang kalah dan **membatalkan transaksinya**, jadi baris pemenang hanya bisa dibaca SETELAH transaksi itu selesai. `create()` karena itu mengulang lookup yang sama **di luar** transaksi dan mengembalikan booking pemenang sebagai replay, bukan constraint error (dulu HTTP 500). Scope lookup tetap owner (`guest_session_id` untuk guest, `user_id` untuk akun) + key hash pemanggil, jadi replay tidak bisa menyentuh order pemilik lain; error nyata (limit guest, kontak invalid, DB down) tetap diteruskan. Dikunci `internal/services/booking_idempotency_race_test.go` + `guest_concurrency_test.go`.
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

### Production hardening (18 Agu 2026)

- `internal/middlewares/middlewares.go`: rate limiter per-IP memakai counter atomik
  untuk batas map O(1), metadata `lastUsed` untuk eviction, dan janitor tidak lagi
  memanggil `AllowN` sehingga proses cleanup tidak mengonsumsi token request. Perilaku
  quota tetap sama; perubahan mengurangi CPU amplification dari rotating-IP traffic.
- `internal/services/audit_pool.go`: `Submit` diserialisasi terhadap `Stop` memakai
  read/write lock. Submit setelah pool berhenti ditolak, sehingga graceful shutdown
  tidak membuka race `send on closed channel`. Drain timeout memakai `time.Timer`.
- Regresi dikunci oleh `middlewares_test.go` dan `audit_pool_test.go`; diverifikasi
  dengan `go test ./...`, `go test -race ./internal/middlewares ./internal/services`,
  `go vet ./...`, dan `go build ./...`.

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
