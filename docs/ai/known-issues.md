# Known Issues & Technical Debt

Catatan jujur tentang keterbatasan, technical debt, dan area yang perlu diperhatikan di VeroAiTravelAgents. Ditujukan untuk agent AI berikutnya agar tidak salah asumsi tentang apa yang "sudah jalan" vs "masih placeholder".

> Prinsip: dokumen ini sengaja menyoroti yang BELUM beres. Untuk gambaran fitur yang sudah aktif, lihat `architecture.md` dan `api.md`.

> Audit terakhir: 23 Jul 2026 (audit keamanan + bug menyeluruh) menemukan 12 temuan (SEC-10..SEC-21). Semuanya telah diselesaikan.

---

## A.4 Temuan Audit Keamanan Baru (Belum Diperbaiki - 26 Jul 2026)

### SEC-22. KRITIS — DOKU Webhook Signature Bypass (Body Terkonsumsi)

- **Severity:** Critical
- **Root Cause:** Pada `handlers.go` (`PaymentWebhook`), fungsi `bind(c, &req)` (`c.ShouldBindJSON`) membaca habis `c.Request.Body`. Ketika `c.GetRawData()` dipanggil setelahnya, stream body sudah di-consume sehingga mengembalikan `[]byte{}` (kosong). Akibatnya, `rawBody` yang diteruskan ke `payment_service.go` selalu string kosong. HMAC signature diverifikasi hanya menggunakan `timestamp + "|"` tanpa isi body request sebenarnya.
- **Impact:** Attacker dapat mem-bypass autentikasi webhook. Dengan menangkap satu webhook valid dari log (signature + timestamp), attacker dapat mengirim ulang payload tersebut dengan mengubah isi body (misalnya merubah `"status": "paid"` atau memanipulasi `amount`), dan validasi signature server akan tetap `true` karena body manipulasi tersebut tidak pernah di-hash oleh server.
- **Exploit Scenario:** 
  1. Attacker mendapatkan satu payload webhook valid (timestamp + signature) hasil transaksi kecil.
  2. Attacker mengirim POST ke `/api/v1/payments/webhook` menggunakan signature yang sama namun memanipulasi JSON payload menjadi instruksi untuk membayar booking ID jutaan rupiah.
  3. Server memverifikasi body kosong dengan signature dan timestamp yang cocok, dan menyetujui transaksi tersebut.
- **Affected Files:** 
  - `backend/internal/handlers/handlers.go` (`PaymentWebhook`)
  - `backend/internal/services/payment_service.go` (`verifyDokuSignature`)
- **Recommendation:** Gunakan `c.GetRawData()` di awal handler untuk membaca body mentah, lalu lakukan `json.Unmarshal` secara manual ke dalam `req`, ATAU gunakan `c.ShouldBindBodyWith(&req, binding.JSON)` agar stream body disalin dan bisa dibaca ulang.
- **Implementation Complexity:** Low
- **OWASP Mapping:** API2:2023 Broken Authentication, API10:2023 Unsafe Consumption of APIs

### SEC-23. TINGGI — Race Condition (TOCTOU) pada Transisi Status Booking

- **Severity:** High
- **Root Cause:** Pada `BookingService.UpdateStatus`, status dibaca dari database dan ditampung ke memori (`current := booking.BookingStatus`). Validasi `allowedTransitions` dilakukan di memori. Setelah itu, status baru disimpan dengan `s.repo.UpdateBooking(&booking)`. Tidak ada atomicity di level query (optimistic locking atau atomic update constraint) yang menjamin bahwa status di database belum berubah ketika proses update dijalankan.
- **Impact:** Terjadinya *Time-of-Check to Time-of-Use* (TOCTOU) *race condition*. Dua instruksi bersamaan dapat menimpa data satu sama lain dan menghasilkan transisi state logistik yang dilarang.
- **Exploit Scenario:** 
  1. Dua administrator/request paralel mengakses status pesanan yang sama secara bersamaan (keduanya membaca status `pending`).
  2. Request pertama memerintahkan transisi `pending` -> `processing` dan lewat validasi.
  3. Request kedua memerintahkan `pending` -> `cancelled` dan lewat validasi.
  4. Keduanya melakukan update ke DB yang menyebabkan status saling bertumpuk (data inkonsisten / invalid workflow).
- **Affected Files:** 
  - `backend/internal/services/booking_service.go` (`UpdateStatus`)
- **Recommendation:** Lakukan atomic update berbasis kondisi pada database. Misalnya menggunakan *Optimistic Locking* dengan field `version`, atau menggunakan where clause pada `status`: `db.Model(&booking).Where("id = ? AND booking_status = ?", id, current).Update("booking_status", target)`. Pastikan mengecek nilai `RowsAffected`.
- **Implementation Complexity:** Low
- **OWASP Mapping:** API4:2023 Unrestricted Resource Consumption (Concurrency/Race Condition), API6:2023 Unrestricted Access to Sensitive Business Flows

### SEC-24. SEDANG — Risiko Kolisi UUID dan Weak Randomness pada Guest User

- **Severity:** Medium
- **Root Cause:** Fungsi pembuatan guest user (`AuthService.GuestUser`) memotong `uuid.NewString()` menjadi 8 karakter saja: `"guest-" + guestID[:8] + "@vero.local"`. Berdasarkan *Birthday Paradox*, probabilitas kolisi pada ruang 8 karakter hex sangat tinggi (kemungkinan bertabrakan setelah sekitar ~65.000 iterasi). Selain itu, password di-generate dari `uuid.NewString()` yang secara matematis tidak terdesain sebagai *Cryptographically Secure Pseudo-Random Number Generator* (CSPRNG).
- **Impact:** Begitu terjadi kolisi karakter `guestID`, `FirstOrCreateUser` akan mengasumsikan guest tersebut sudah ada di database. Alih-alih membuat user baru, sistem akan menetapkan booking ID ke user lama. Hal ini menghancurkan isolasi dan privasi pesanan guest.
- **Exploit Scenario:** 
  1. Sistem melayani ribuan guest order seiring waktu.
  2. *Collision* terjadi di 8 karakter hex UUID. Sistem mengembalikan user_id tamu sebelumnya.
  3. Pesanan tamu B terekam di akun tamu A, mengacaukan riwayat kepemilikan transaksi di database.
- **Affected Files:** 
  - `backend/internal/services/auth_service.go` (`GuestUser`)
- **Recommendation:** Jangan memotong UUID. Gunakan `uuid.NewString()` secara utuh (36 karakter) untuk pembuatan guest email. Gunakan library `crypto/rand` untuk mengenerate string password yang keamanannya terjamin secara kriptografi.
- **Implementation Complexity:** Low
- **OWASP Mapping:** API2:2023 Broken Authentication, API9:2023 Improper Inventory Management (Data Integrity)

---

## A.2 Celah Keamanan — SELESAI (Batch 21 Jul 2026)

Temuan batch audit 21 Jul 2026 yang sudah diperbaiki pada hari yang sama dan diverifikasi `go build`/`go vet`/`gofmt`.

### SEC-14. ✅ SEDANG — Rate Limiter `sync.Map` Tumbuh Tak Terbatas (Memory DoS) (FIXED 23 Jul 2026)

**Lokasi:** `backend/internal/middlewares/middlewares.go` → `ipRateLimiter`; `backend/cmd/server/main.go`; `backend/internal/config/config.go`; `backend/.env.example`.

Setiap IP baru membuat entry `*rate.Limiter` di `sync.Map` dan TIDAK PERNAH dihapus. Penyerang dengan banyak IP (botnet/spoof via header jika `TrustedProxies` salah konfigurasi) dapat mengisi memori server tanpa batas. Juga: `c.ClientIP()` memakai default Gin yang percaya `X-Forwarded-For` dari semua proxy — `router.SetTrustedProxies()` tidak dipanggil di `main.go`, sehingga rate limit per-IP mudah di-bypass dengan memutar header `X-Forwarded-For`.

Kini dua lapis pertahanan:

1. **Memory-bounded rate limiter**:
   - `maxRateLimiterEntries = 10_000` — ketika map sudah penuh, IP baru tetap mendapat limiter anonim sementara (tidak disimpan) sehingga attacker tidak bisa membanjiri memori.
   - **Janitor** berjalan tiap 30 detik, menghapus limiter yang idle ≥ 1 menit (tidak pernah kehabisan token = tidak ada request). Konsekuensinya jika prod attack: jumlah entry tidak akan melampaui ~10k.
2. **Trusted proxy explicit**:
   - `Config.TrustedProxies` di-load dari env `TRUSTED_PROXIES` (CSV CIDR/IP).
   - `main.go`: dev default `SetTrustedProxies(nil)` — server tidak percaya `X-Forwarded-For` sama sekali. Production wajib set `TRUSTED_PROXIES` ke CIDR reverse proxy (cloud load balancer, nginx, dll).
   - `.env.example` menambahkan contoh `TRUSTED_PROXIES`.

Verifikasi: `go build ./...` + `go vet` + `gofmt` bersih.

### SEC-11. ✅ TINGGI — Validasi Pax Negatif pada Booking (FIXED 21 Jul 2026)

**Lokasi:** `backend/internal/services/booking_service.go` → `Create()`, `dto.go` → `BookingRequest` + konstanta `MaxBookingPax`.

Dulu `AdultPax`/`ChildPax` tanpa batas: nilai negatif menghasilkan `TotalPrice` negatif/nol dan nilai raksasa berisiko overflow. Kini dua lapis pertahanan:

1. DTO binding `gte=0,lte=20` pada `AdultPax`/`ChildPax` — menolak request HTTP (`POST /bookings`, `POST /orders`) di luar rentang.
2. Guard server-side di `BookingService.Create()`: tolak `pax < 0` atau `pax > dto.MaxBookingPax` (20). Menutup jalur non-HTTP yang bypass binding (tool MCP `create_booking` di `mcp_service.go` — cast `int(v)` tanpa clamp kini tertahan guard ini dan mengembalikan error ke tool result).

Verifikasi: `go build ./...` + `go vet` + `gofmt` bersih.

### SEC-15. ✅ SEDANG — Kebocoran Detail Error Internal ke Client (FIXED 21 Jul 2026)

**Lokasi:** `backend/internal/utils/response.go` → `ServerError()`; `backend/internal/handlers/handlers.go`.

Dulu respons 500/400 membawa pesan error Go/GORM mentah (nama tabel, constraint, DSN fragment). Kini:

1. `ServerError()` membalas pesan generik `"Internal server error"` dengan `error: {}`; error asli di-`log.Printf` ke server bersama `request_id`, method, path.
2. `/health/database` (`DatabaseHealth`) tidak lagi mengirim `detail` — error DB di-log server-side, client hanya menerima `"Database disconnected"`.
3. BadRequest yang membawa error service internal disapukan ke pesan statis + log server: `Register`, `AdminCreateUser`, `UpdateBooking`, `PaymentWebhook`, `UploadTripMedia` (form file + read file).
4. Disengaja dipertahankan: `bind()` (error validasi JSON per-field) dan `parseID()` (error parse UUID) masih mengirim `detail` — itu error input klien, bukan internal; berguna untuk UX form. `Login` tetap membalas `err.Error()` via `Unauthorized` (pesan kredensial-salah yang memang ditujukan ke user, bukan error DB).

Verifikasi: `go build ./...` + `go vet` + `gofmt` bersih.

### SEC-13. ✅ SEDANG — Endpoint Publik `POST /orders` & `/chat` Tanpa Proteksi Abuse (FIXED 21 Jul 2026)

**Lokasi:** `backend/internal/middlewares/middlewares.go` → `PublicWriteRateLimit()`; `backend/internal/routes/routes.go`.

Dulu `POST /orders` (publik) dan `POST /chat` hanya dilindungi `RateLimit()` global 20 req/s per-IP — cukup untuk spam ribuan booking palsu dan membakar biaya LLM. Kini keduanya dilewati middleware baru `PublicWriteRateLimit()` per-route: **5 request/menit per-IP** (`rate.Every(12*time.Second)`, burst 5), memakai `ipRateLimiter` yang sama dengan `RateLimit()`/`AuthRateLimit()`. Dikombinasikan SEC-11 (pax divalidasi), nilai order tidak bisa lagi negatif/nol. Catatan: masing-masing route punya bucket limiter sendiri (5/menit per route, bukan gabungan). CAPTCHA/Turnstile belum ada — opsional bila abuse berlanjut.

Verifikasi: `go build ./...` + `go vet` + `gofmt` bersih.

### SEC-17. ✅ SEDANG — Session ID Asing Diterima di Chat (FIXED 21 Jul 2026)

**Lokasi:** `backend/internal/services/ai_service.go` → `Chat()`.

Dulu `session_id` dari body diterima mentah — pesan langsung ditulis ke sesi itu tanpa cek kepemilikan (lintas-sesi tamu: prompt injection + polusi memory summary). Kini `Chat()` memverifikasi dulu: `FindChatSession(*req.SessionID)` dan hanya memakai sesi itu bila `existing.UserID == userID`. Sesi asing atau tidak ditemukan **jatuh ke pembuatan sesi baru** milik caller (bukan error) — perilaku UX tidak berubah untuk alur normal, tapi injeksi lintas sesi tertutup.

Verifikasi: `go build ./...` + `go vet` + `gofmt` bersih.

### SEC-16. ✅ SEDANG — Prompt Chat Tanpa Batas Ukuran (FIXED 21 Jul 2026)

**Lokasi:** `backend/internal/dto/dto.go` → `ChatRequest`; `backend/internal/middlewares/middlewares.go` → `RequestBodyLimit()`; `backend/internal/routes/routes.go`.

Dulu prompt chat tidak memiliki batas panjang dan request publik tidak memiliki batas body khusus. Kini `ChatRequest.Prompt` dibatasi `2..4000` karakter. Endpoint publik `POST /chat` dan `POST /orders` memakai `RequestBodyLimit(64 << 10)` (64 KiB) sebelum binding JSON; rate limit SEC-13 tetap aktif. Ini membatasi payload besar, biaya token LLM, alokasi memory, dan write workload dari request tunggal.

Verifikasi: `go build ./...` + `go vet` + `gofmt` bersih.

### SEC-18. ✅ RENDAH — Event Bus Broadcast Data Sensitif ke Semua Subscriber SSE (FIXED 23 Jul 2026)

**Lokasi:** `backend/internal/routes/routes.go` (`/events/stream`), `backend/internal/services/ai_service.go`, `payment_service.go`, `booking_service.go`, `mcp_service.go`.

Dulu setiap subscriber `/events/stream` (cukup JWT apa pun, termasuk user biasa) menerima SEMUA event: prompt mentah user lain, session_id, struct booking lengkap (contact name/email/phone), dan struct payment lengkap (external_id, amount). Kini dua lapis pertahanan:

1. **Akses dibatasi ke staff**: route `/events/stream` kini diguard `middlewares.Role(models.RoleOperator, models.RoleAdmin)` di samping `Auth` — user biasa menerima 403. SSE memang belum dikonsumsi frontend mana pun, jadi tidak ada UX yang rusak.
2. **Payload disanitasi di sisi publish** (defense-in-depth bila nanti endpoint dibuka lebih luas):
   - `ai_service.go` — step workflow hanya mengirim `{session_id, tool}` (prompt mentah dihapus); `workflow_completed` hanya `{session_id}` (body pesan asisten dihapus).
   - `mcp_service.go` — `mcp_tool_executed` hanya `{tool, status}` (bukan seluruh `ToolResult.Data` yang bisa memuat PII booking).
   - `booking_service.go` — `booking_created`/`booking_updated` hanya `{booking_id, trip_id?, status}` (struct dengan contact PII tidak lagi di-broadcast).
   - `payment_service.go` — `payment_created`/`payment_updated` hanya `{payment_id, booking_id, status}` (external_id & amount tetap server-side). `trip_created` dibiarkan apa adanya (data katalog publik).

Catatan: kanal per-user belum ada — bila SSE nanti dipakai customer chat, rancang filter per-user/session sebelum membuka akses non-staff.

Verifikasi: `go build ./...` + `go vet` + `gofmt` bersih.

### SEC-19. ✅ RENDAH — Token Backoffice di `localStorage` + BroadcastChannel Tanpa Verifikasi Origin (FIXED 22 Jul 2026)

**Lokasi:** `backoffice-frontend/src/lib/api.ts` (`getAuthChannel().onmessage`), `backoffice-frontend/next.config.mjs`, `frontend/next.config.mjs`.

Dua lapis perbaikan:

1. `getAuthChannel().onmessage` kini memvalidasi pesan secara ketat sebelum mengadopsi token: pesan harus object, `type === "token_refreshed"`, `access_token` string non-kosong, dan `expires_at` number finite > 0. Pesan crafted dari tab terkompromosi ditolak, sehingga localStorage tab lain tidak bisa disuntik token palsu.
2. Kedua `next.config.mjs` kini mengirim header keamanan di semua route: `Content-Security-Policy` (default-src 'self'; script-src 'self' 'unsafe-inline' 'unsafe-eval' untuk kompatibilitas Next.js dev; style-src 'self' 'unsafe-inline'; img/connect-src mengizinkan backend `:8080` dan WebSocket localhost; object-src 'none'; frame-ancestors 'none'; tanpa `upgrade-insecure-requests` agar dev lokal HTTP tetap bisa memanggil `localhost:8080`), `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: strict-origin-when-cross-origin`, dan `Permissions-Policy` (camera/mic/geo off). CSP mempersempit permukaan XSS pencurian token dari `localStorage`. Untuk production dengan HTTPS, pertimbangkan menghapus `'unsafe-eval'`, mengganti `ws://` dengan `wss://`, dan menambahkan `upgrade-insecure-requests`.

Catatan: access token masih di `localStorage` (trade-off DX vs keamanan; refresh token tetap cookie HttpOnly). Migrasi penuh ke cookie HttpOnly + BFF tetap menjadi opsi hardening lanjutan.

Verifikasi: `tsc --noEmit` bersih di kedua frontend (`backoffice-frontend` exit 0, `frontend` exit 0).

### SEC-20. ✅ RENDAH — Docker/Deploy: Root User, `network_mode: host`, Credential Dev Ter-commit (FIXED 23 Jul 2026)

**Lokasi:** `backend/Dockerfile`, `backend/docker-compose.yml`, `backend/.dockerignore`, `.gitignore`, `backend/.env.example`, `backend/internal/config/config.go`.

Perbaikan:

1. `backend/Dockerfile` runtime sekarang memakai user non-root `app`; uploads dir dibuat dan dimiliki `app`.
2. `backend/docker-compose.yml` menghapus `network_mode: host`, memakai bridge network + `ports: "8080:8080"`, `host.docker.internal` untuk DB host lokal, named volume `uploads_data`, dan placeholder password via env.
3. `backend/.dockerignore` mencegah `.env`, uploads, git metadata, log/temp masuk build context; Dockerfile tidak lagi menyalin `.env.example` ke image.
4. `.gitignore` mengabaikan isi `backend/uploads/*` dan hanya mempertahankan `backend/uploads/.gitkeep`; file uploads lama dihapus dari index Git tanpa menghapus file lokal.
5. `backend/.env.example` mengganti password dev lama `password_aman` menjadi placeholder `change_me_dev_password` dan menghapus typo `ds`.
6. `backend/internal/config/config.go` menolak `DATABASE_PASSWORD` kosong/placeholder (termasuk bila placeholder ada di `DATABASE_URL`) saat `APP_ENV=production`.

Catatan: password/secret production tetap wajib dirotasi setelah deploy pertama.

Verifikasi: `gofmt`, `go build ./...`, dan `docker compose config` bersih.

---

## A.3 Celah Keamanan — SELESAI (Batch 25 Jun 2026)

Seluruh sembilan temuan di bawah sudah diperbaiki dan diverifikasi `go build`/`go vet`. Dicatat di sini sebagai jejak audit + acuan regresi (lihat juga `#3` soal kebutuhan automated test untuk mengunci perbaikan ini).

### SEC-1. ✅ KRITIS — Privilege Escalation lewat `/auth/register` (FIXED)

**Lokasi:** `backend/internal/services/auth_service.go` → `AuthService.Register()`.

`Register()` kini **selalu** memaksa `models.RoleUser` dan tidak lagi membaca field `role` dari body. Field `Role` dihapus dari `dto.RegisterRequest`. Pembuatan akun operator/admin dipindah ke jalur resmi terproteksi: `POST /api/v1/admin/users` (guard `Role(admin)`) → `dto.AdminCreateUserRequest` → `AuthService.CreateStaff()`. Verifikasi: register dengan `role:"admin"` tetap menghasilkan user biasa.

### SEC-2. ✅ TINGGI — IDOR pada `GET /bookings/:id` & `GET /payments/:id` (FIXED)

**Lokasi:** `booking_service.go`/`payment_service.go` (`Find(id, userID, isStaff)`), `repositories.go` (`FindBookingForUser`, `FindPaymentForUser`), `handlers.go` (`isStaff(c)`).

`Find` kini menerima `userID` + `isStaff`. Caller non-staff hanya bisa mengambil record miliknya (query difilter `user_id`; payment via join ke `bookings`). Staff (operator/admin) tetap bisa mengakses semua. Record milik user lain membalas not found.

> Verifikasi ulang 21 Jul 2026: fix utuh, tidak ada regresi. Rute `GET /bookings/:id` & `GET /payments/:id` tetap di grup protected (JWT); handler membalas 404 generik; `go build ./...` + `go vet` + `gofmt` bersih.

### SEC-3. ✅ TINGGI — Tampering Harga Booking & Jumlah Pembayaran (FIXED)

**Lokasi:** `dto.go` (`BookingRequest`, `PaymentCreateRequest`), `booking_service.go`, `payment_service.go`.

`BookingRequest.TotalPrice` dan `PaymentCreateRequest.Amount` **dihapus**. `BookingService.Create()` menghitung total server-side: `tripAdultPrice(trip)*adultPax + tripChildPrice(trip)*childPax` (menghormati diskon). `PaymentService.Create()` mengambil `Amount` dari `Booking.TotalPrice`. Body kini hanya menerima `trip_id`,`adult_pax`,`child_pax` (booking) dan `booking_id`,`payment_method` (payment).

### SEC-4. ✅ TINGGI — Webhook Pembayaran Bisa Dipalsukan (FIXED)

**Lokasi:** `payment_service.go` → `Webhook()`, `config.go` → `Validate()`.

Bila `PAYMENTS_ENABLED=true` dan `DOKU_SECRET` ter-set, webhook **wajib** signature valid (tolak bila kosong/salah). Bila secret kosong saat `APP_ENV=production` dan payments enabled, webhook ditolak; `Config.Validate()` juga mewajibkan `DOKU_SECRET` non-kosong di production hanya saat payments enabled. Ditambah validasi `amount` (jika dikirim) harus cocok dengan payment, dan idempotency: status yang sudah `paid`/`settlement` tidak bisa diturunkan dan tidak diproses ulang.

### SEC-5. ✅ SEDANG — Upload Media: Batas Ukuran & MIME Asli (FIXED)

**Lokasi:** `handlers.go` → `UploadTripMedia()` + `detectImageContentType()`, `cmd/server/main.go`.

`router.MaxMultipartMemory = 8<<20`. Upload dibatasi `maxUploadBytes = 5 MiB` (cek `file.Size`), dan content-type asli diverifikasi via `http.DetectContentType` pada 512 byte pertama — ditolak bila bukan `image/*`, meski ekstensi cocok.

### SEC-6. ✅ SEDANG — Recovery Tidak Bocorkan Detail Panic (FIXED)

**Lokasi:** `middlewares.go` → `Recovery()`.

Detail panic + `request_id` + path di-`log.Printf` ke server log; client hanya menerima pesan generik tanpa field `panic`.

### SEC-7. ✅ SEDANG — Rate Limiter Per-IP + Ketat untuk `/auth` (FIXED)

**Lokasi:** `middlewares.go` → `ipRateLimiter`, `RateLimit()`, `AuthRateLimit()`.

Rate limit kini per-IP via `sync.Map` of `*rate.Limiter` (`c.ClientIP()`). Global 20 req/detik per-IP; grup `/auth` memakai `AuthRateLimit()` lebih ketat (5 req/detik) untuk meredam brute force.

### SEC-8. ✅ SEDANG — CORS dari Env (FIXED)

**Lokasi:** `config.go` (`CORSAllowedOrigins`, `parseCSVEnv`), `middlewares.go` → `CORS(allowedOrigins)`, `main.go`.

Origins dibaca dari env `CORS_ALLOWED_ORIGINS` (CSV), fallback ke localhost dev. `CORS()` menerima daftar dari config.

### SEC-9. ✅ SEDANG — AI Client: Body Dibatasi (FIXED)

**Lokasi:** `ai/ai_client.go` → `Generate()`.

`res.Body` dibungkus `io.LimitReader(res.Body, maxAIResponseBytes)` (1 MiB) sebelum decode JSON.

---

## B. Placeholder & Integrasi Belum Selesai

### 0. Guest Chat Session Hardening (IMPLEMENTED)

Guest ChatSession kini anonymous (`user_id=NULL`) dan diikat ke HttpOnly cookie `vero_chat_session`, bukan shared `guest@vero.local`. Cookie memakai `SameSite=Lax` default yang dapat dikonfigurasi (`GUEST_COOKIE_SAME_SITE`) untuk kompatibilitas roadmap OAuth, Secure di production, dan sliding TTL default 7 hari. `GET /chat/history` tidak menerima atau mengembalikan session identifier. Cleanup MVP berjalan tiap jam dan menghapus session expired (catatan: child chat records TIDAK ikut terhapus — lihat #19).

Booking guest masih memakai legacy `GuestUser()` hanya untuk memenuhi kontrak `bookings.user_id` yang saat ini `NOT NULL`; ini tidak lagi dipakai sebagai ownership ChatSession. Saat login guest di masa depan, migrasi session cukup mengubah `chat_sessions.user_id` ke user baru.

### 1. MCP Tools Legacy Sudah Di-unify ke `search_trips` (Status Terkini 25 Jul 2026)

**Lokasi:** `backend/internal/services/mcp_service.go` → `MCPService.Execute()` + `mock()`

Tool rekomendasi legacy (`search_destination`, `search_hotels`, `calculate_budget`, `generate_itinerary`) sudah **dinonaktifkan dari katalog OpenAI** dan tidak lagi mengembalikan data dummy statis. `MCPService.Execute()` memetakan nama-nama itu ke `executeSearchTrips()` (scoring katalog published nyata dari DB) untuk kompatibilitas bila LLM lama tetap memanggilnya. Fungsi `mock()` kini hanya menangani `send_whatsapp` (juga disabled) dan mengembalikan `unknown tool` untuk sisanya.

Tool yang nyata saat ini: `search_trips` (scoring katalog DB), `select_package`, `collect_order_detail`, `create_booking` (via `BookingService.Create()`), `create_order` (alias `create_booking`).

**Dampak:** Tidak ada lagi dummy Tokyo/Kyoto/Bali statis di workflow chat. Rekomendasi paket sepenuhnya berasal dari katalog DB published.

**Yang perlu dilakukan (opsional):** hapus cabang legacy + `mock()` sepenuhnya bila yakin tidak ada LLM client lama yang masih memanggil nama tool lama.

---

### 2. `create_payment` Sengaja Dinonaktifkan

**Lokasi:** `backend/internal/services/ai_service.go` (workflow steps di `Chat()`), `backend/internal/mcp/tools.go` (`Enabled: false`)

Ini **keputusan desain, bukan bug**. Tool `create_payment` dikeluarkan dari pipeline chat dan diblok di `MCPService.Execute()` agar AI tidak menjanjikan/menyebut pembayaran (QRIS/DOKU) selama `PAYMENTS_ENABLED=false`. `send_whatsapp` juga `Enabled: false`.

**Jangan** mengaktifkan kembali tanpa lebih dulu menyambungkan alur booking end-to-end di frontend. Lihat komentar di `mcp/tools.go` `Catalog()`.

---

### 3. Automated Test Masih Minim

**Lokasi:** seluruh repo

Backend sudah memiliki test minimal untuk `internal/ai`, tetapi belum ada coverage memadai untuk service/repository dan belum ada test JS/TS. Verifikasi utama masih `go build`, `go test ./...`, `gofmt`, dan `tsc --noEmit`.

**Area paling berisiko tanpa test (prioritas bila menambah test):**
1. `AuthService.Register()`/`Login()`/`Refresh()`/`CreateStaff()` — rotasi token, reuse detection, revoke-all, **dan regresi SEC-1** (register tidak boleh bisa set role).
2. `PaymentService.Webhook()` — verifikasi HMAC signature + idempotency + amount mismatch (SEC-4).
3. `BookingService.Create()`/`PaymentService.Create()` — harga server-side (SEC-3), dan `Find()` ownership (SEC-2).
4. `AIService.Chat()` — orkestrasi workflow, function calling loop, guard agar AI tidak mengklaim order berhasil tanpa `create_booking` success.

---

### 4. Booking & Payment: Backend Siap, Frontend Belum

**Lokasi:** `frontend/src/app/trip/[id]/page.tsx`

Backend punya endpoint `POST /api/v1/bookings`, `POST /api/v1/payments/create`, dan webhook DOKU. Namun:

- Tombol customer sudah membuat order manual via `POST /api/v1/orders`, tanpa payment otomatis.
- Teks checkout sudah diganti menjadi manual admin processing.
- Tidak ada UI checkout/QRIS di mana pun.

**Dampak:** Order manual sudah bisa dibuat dari customer UI, tetapi revenue/payment DOKU belum tersambung end-to-end karena payment sengaja dinonaktifkan.

> Catatan kontrak (pasca SEC-3): `POST /bookings` kini menerima `{trip_id, adult_pax, child_pax}` (tanpa `total_price`); `POST /payments/create` menerima `{booking_id, payment_method}` (tanpa `amount`). Saat menyambungkan UI, ikuti kontrak baru ini — harga dihitung server-side.

---

### 5. Backoffice: Banyak Halaman Placeholder

**Lokasi:** `backoffice-frontend/src/app/`

- **Dashboard** (`on-development-panel.tsx`) → layar "On Development", tidak memanggil `analytics/dashboard`.
- **`/settings`, `/trips/[id]`** → masih me-render `CurrentTripsScreen` placeholder.
- **`/orders`** → sudah memiliki antarmuka lengkap (Order Management) sesuai desain Stitch.
- **Mock data** di `backoffice-frontend/src/lib/data.ts` (`travelCards`, `orders`, `payments`, `workflowSteps`) **tidak dipakai** komponen mana pun.

**Yang benar-benar jalan di backoffice:** auth + CRUD paket + upload media + list order manual. Selain itu placeholder.

---

### 6. Endpoint Backend yang Belum Dikonsumsi Frontend

- `GET /api/v1/events/stream` (SSE) — **tidak ada** EventSource di kedua frontend.
- `GET /api/v1/analytics/dashboard` — tidak dipanggil backoffice.
- `GET /api/v1/logs`, `/logs/workflows`, `/logs/tool-calls` — tidak dipanggil.
- `GET /api/v1/bookings/:id` — tidak dipanggil.
- `GET /api/v1/chat/sessions`, `/chat/:id/messages` — tidak dipanggil.

**Dampak:** Effort SSE realtime saat ini "terbuang" dari sisi UX. Peluang: sambungkan SSE ke customer chat untuk progress workflow realtime.

---

## C. Arsitektur & Skalabilitas

### 7. Event Bus In-Memory: Tidak Tahan Restart & Tidak Multi-Instance

**Lokasi:** `backend/internal/events/bus.go`

- Event **hilang saat restart** (tidak ada persistensi).
- **Tidak bisa multi-instance** — klien SSE di instance A tidak menerima event dari instance B.
- Publish **non-blocking** — jika buffer (32) penuh, event **di-drop diam-diam**.

**Yang perlu dilakukan bila scale:** ganti ke Redis Pub/Sub atau message broker. Untuk single instance cukup.

---

### 8. Guest Chat: Legacy User "Guest Traveler" Hanya untuk Booking (RESOLVED untuk Chat)

**Lokasi:** `backend/internal/services/auth_service.go` → `AuthService.GuestUser()`

Sejak guest session hardening (lihat #0), `ChatSession` tamu ber-`UserID=NULL` (anonymous) dan diikat cookie HttpOnly `vero_chat_session` — tamu **tidak lagi berbagi** satu user untuk chat. Masalah lama "`GET /chat/sessions` mengembalikan sesi semua tamu" sudah tidak relevan karena sesi guest tidak punya `user_id` dan endpoint itu hanya men-list sesi user authenticated.

Sisa penggunaan: `GuestUser()` (`guest@vero.local`) masih dipakai **hanya** untuk memenuhi kontrak `bookings.user_id NOT NULL` saat order manual dibuat (`GuestCreateOrder` + tool `create_booking`). Semua order tamu tetap tercatat di bawah satu user — ini berdampak ke administrasi order, bukan privasi chat. Pertimbangan lanjutan: jadikan `bookings.user_id` nullable atau buat user booking per-kontak bila perlu isolasi order antar-tamu.

---

### 9. Konfigurasi Secret di `.env.example` adalah Nilai Dev

**Lokasi:** `backend/.env.example`

`DATABASE_PASSWORD=change_me_dev_password`, `JWT_SECRET=super_secret_vero_travel` adalah nilai dev/placeholder. `Config.Validate()` menolak start bila `APP_ENV=production` dan `JWT_SECRET` kosong/default, `DATABASE_PASSWORD` kosong/placeholder (termasuk di `DATABASE_URL`), atau `DOKU_SECRET` kosong saat `PAYMENTS_ENABLED=true`.

**Catatan:** `.env` aktual developer berisi AI key nyata. Jangan commit `.env`.

---

### 10. AI Memory Summary: Masih Truncation (Bukan LLM Summarization)

**Lokasi:** `backend/internal/services/ai_service.go` → `refreshMemorySummary()`

Ringkasan memory bukan hasil summarization LLM — hanya **potong string** ambil `AI_MEMORY_MAX_CHARS` (1800) karakter terakhir. Konteks lama bisa terpotong di tengah kalimat.

**Sudah dioptimasi:** memakai `TailChatMessages()` untuk mengambil hanya pesan terakhir (estimasi `AIMemoryMaxChars / 200`) alih-alih memuat SEMUA pesan sesi.

**Yang bisa ditingkatkan:** panggil LLM untuk meringkas, bukan slice string.

---

## D. Kualitas Kode & Optimasi

### 11. ✅ `services.go` Monolitik — SUDAH DIPECAH (Batch 25 Jun 2026)

**Lokasi:** `backend/internal/services/`

Dulu semua service di satu file `services.go` (~970 baris). Kini sudah dipecah per-domain dalam package `services` yang sama (API publik tidak berubah):

- `services.go` → `Services` struct, `New()`, tipe bersama (`AuthRequestMeta`, `AuthIssueResult`, error vars).
- `auth_service.go`, `ai_service.go`, `mcp_service.go`, `trip_service.go`, `booking_service.go`, `payment_service.go`, `log_service.go`, `analytics_service.go`.
- `helpers.go` → util bersama (`slugify`, `normalize`, `firstNonEmpty`, `firstNonZero`, `parseDate`).

---

### 12. ✅ Duplikasi Prompt User di Konteks LLM — SUDAH DIPERBAIKI (Batch 25 Jun 2026)

**Lokasi:** `backend/internal/services/ai_service.go` → `generateWithAI()`

Dulu prompt user terkirim dua kali ke LLM (sekali via `ListRecentChatMessages`, sekali di-append manual). Kini urutan pesan: `system → catalog → memory → workflow_context → recent_messages`. Append manual prompt dihapus (hanya fallback bila `recent` kosong). Selain itu konteks workflow diringkas via `summarizeWorkflow()` (hanya `tool`+`status`, bukan seluruh data dummy) untuk menghemat token.

---

### 13. Uang Disimpan sebagai `float64`

**Lokasi:** `backend/internal/models/models.go` (`BasePrice`, `TotalPrice`, `Amount`, dll bertipe `float64`; kolom DB `numeric(14,2)`).

Aritmetika `float64` rawan galat presisi untuk nominal uang. DB sudah `numeric`, tapi nilai di Go tetap float. **Makin relevan** sejak SEC-3: kalkulasi harga booking kini dilakukan server-side (`tripAdultPrice*pax + tripChildPrice*pax`) memakai `float64`.

**Perbaikan yang disarankan:** pertimbangkan integer (satuan terkecil/sen) atau tipe decimal untuk kalkulasi harga server-side.

---

### 14. ✅ Frontend & Backoffice: Error Response HTML Saat JSON Diharapkan (FIXED)

**Lokasi:**
- `backoffice-frontend/src/lib/api.ts` → `parseJsonEnvelope()`, `request()`.
- `frontend/src/lib/api.ts` → `parseJsonEnvelope()`, `apiFetch()`.

Request kini memeriksa `Content-Type` dan membungkus pembacaan respons dalam try-catch. Jika backend/proxy membalas HTML (502/504/nginx timeout, Next.js error page, dll) atau JSON rusak, client mendapat pesan user-friendly: "Server merespons dengan format yang tidak dikenal" / "Gagal membaca respons dari server". Versi customer (`frontend`) juga menambahkan timeout 35 detik via `AbortController` agar workflow AI yang lambat tidak membuat UI menggantung.

### 15. ✅ Backoffice: Refresh Token Promise Tanpa Timeout (FIXED)

**Lokasi:** `backoffice-frontend/src/lib/api.ts` → `refreshAccessToken()`.

Refresh request kini menggunakan `AbortController` dengan timeout `10_000` ms. Jika backend hang, refresh akan abort dan request menunggu dapat reject, sehingga tidak menggantung seluruh antrean request.

---

## E. Production Readiness & Operasional Gaps

Berdasarkan Production Readiness Review (PRR), implementasi saat ini sudah layak menjadi fondasi production, namun ada beberapa hutang operasional yang perlu dibereskan:

### 16. Ketiadaan Metrik Observability (Prometheus)
Backend saat ini tidak mengekspor metrik operasional (latensi endpoint, QPS, tingkat error, penggunaan memori/goroutine).
**Dampak:** Buta visibilitas saat insiden production.
**Rekomendasi:** Tambahkan middleware `gin-prometheus` dan ekspos rute `/metrics`.

### 17. Duplikasi Kode Shared Frontend
Tipe data (seperti `TripPackage`) dan fungsi utilitas (`formatIDR`) disalin secara manual (duplikat) di `frontend/` dan `backoffice-frontend/`.
**Dampak:** Risiko inkonsistensi saat salah satu codebase diperbarui tanpa memperbarui yang lain.
**Rekomendasi:** Ekstrak kode bersama ke folder shared (monorepo workspace/lokal package).

### 18. Cleanup Job via Ticker Internal
Job `CleanupExpiredChatSessions` dipicu via `time.Ticker` internal di `main.go`. Pada skenario multi-instance (horizontal scaling), setiap instance akan menjalankan job yang sama dan saling berpacu (race condition), membebani database.
**Dampak:** Beban DB ganda dan potensi konflik transaksi.
**Rekomendasi:** Matikan ticker internal di mode prod, delegasikan eksekusi `CleanupExpiredChatSessions` ke Kubernetes CronJob atau scheduler eksternal lain.

### 19. Cleanup Session Meninggalkan Child Records Orphan (ditemukan 25 Jul 2026)

**Lokasi:** `backend/internal/repositories/repositories.go` → `DeleteExpiredChatSessions()`.

Method ini hanya menjalankan `Delete(&models.ChatSession{})` (soft delete karena `BaseModel.DeletedAt`). Child records `ChatMessage`, `ToolCall`, dan `AILog` milik session tersebut **tidak ikut dihapus** — menjadi orphan permanen di DB (soft-deleted session tidak pernah diquery lagi, tapi tabel anak terus tumbuh). Dokumentasi lama (`database.md`, bagian #0 di atas) mengklaim cleanup menghapus child dalam transaksi — klaim itu TIDAK sesuai implementasi.

**Dampak:** `chat_messages`, `tool_calls`, `ai_logs` tumbuh tak terbatas dari sesi tamu expired; boros storage dan memperlambat query berindeks `session_id` seiring waktu.

**Perbaikan yang disarankan:** dalam satu transaksi, ambil ID session expired, lalu `Unscoped().Delete` child (`ChatMessage`/`ToolCall`/`AILog` dengan `session_id IN (...)`) sebelum delete parent — atau pakai hard delete berkala. Catatan: `AILog.SessionID` nullable, jadi filter harus `session_id IN` bukan join.

---

## Ringkasan Prioritas

**Sisa pekerjaan (belum selesai):**

| Prioritas | Item | Alasan |
|---|---|---|
| 🟠 **Tinggi** | #7 Event bus in-memory | SSE realtime putus di arsitektur load balancer; ganti ke Redis Pub/Sub |
| 🟡 Sedang | #4 Re-enable payment UI saat siap | Alur revenue/payment belum jalan dari UI (ikuti kontrak baru pasca SEC-3 dan set `PAYMENTS_ENABLED=true`) |
| 🟡 Sedang | #16 Metrik Observability | Tambah metrik Prometheus untuk production visibilitas |
| Rendah | #13 Uang float64 | Presisi (makin relevan setelah harga server-side SEC-3) |
| Rendah | #10 LLM summarization memory | Masih truncation string (termasuk risiko patah byte UTF-8 dari SEC-21) |
| Rendah | #17 Duplikasi frontend shared | Ekstrak tipe dan utils ke shared package |
| Rendah | #18 Cleanup via internal ticker | Pindahkan job ticker ke Kubernetes CronJob |

**Sudah selesai (jejak audit):**

| Item | Status |
|---|---|
| SEC-12 Replay webhook | ✅ Signature (digest body + header timestamp) tervalidasi dgn toleransi 5mnt. |
| #3 Test auth/payment/AI | ✅ Test utk PaymentWebhookReplay + Idempotency ditambahkan |
| #19 Cleanup orphan records | ✅ Unscoped Delete `chat_messages`, `tool_calls`, `ai_logs` sblm hapus session |
| #8 Isolasi guest user | ✅ ID unik utk tiap `GuestUser()` |
| SEC-21 Bug Kecil | ✅ Diperbaiki (sentinel error Booking, clamp pax, safe rune slice, dll) |
| SEC-1 Privilege escalation `/auth/register` | ✅ Register paksa `RoleUser` + endpoint `admin/users` |
| SEC-2 IDOR booking/payment | ✅ `Find(id,userID,isStaff)` + repo scoped per-owner |
| SEC-3 Tampering harga/amount | ✅ Harga & amount dihitung server-side |
| SEC-4 Webhook dipalsukan | ✅ Signature wajib + `DOKU_SECRET` prod + idempotency |
| SEC-5 Upload tanpa batas + MIME ekstensi | ✅ Batas 5 MiB + sniff `DetectContentType` |
| SEC-6 Recovery info disclosure | ✅ Log ke server, pesan generik ke client |
| SEC-7 Rate limiter global | ✅ Per-IP + `AuthRateLimit` ketat untuk `/auth` |
| SEC-8 CORS hardcoded | ✅ Dari env `CORS_ALLOWED_ORIGINS` |
| SEC-9 AI body tanpa limit | ✅ `io.LimitReader` 1 MiB |
| SEC-11 Pax negatif booking | ✅ DTO `gte=0,lte=20` + guard `MaxBookingPax` di service |
| SEC-13 Spam order/chat publik | ✅ `PublicWriteRateLimit` 5 req/menit per-IP untuk `/orders` + `/chat` |
| SEC-14 Memory-bounded rate limiter | ✅ `maxRateLimiterEntries=10_000` + janitor + `TRUSTED_PROXIES` di production |
| SEC-15 Kebocoran error internal | ✅ `ServerError` generik + log; `/health/database` & BadRequest tanpa `detail` mentah |
| SEC-16 Prompt chat tanpa batas | ✅ Prompt `max=4000` + body limit 64 KiB untuk `/chat` dan `/orders` |
| SEC-17 Session ID asing di chat | ✅ Cek `UserID` di `Chat()`; sesi asing → sesi baru |
| SEC-18 SSE broadcast data sensitif | ✅ `/events/stream` dibatasi staff + payload event disanitasi (tanpa prompt/PII/amount) |
| SEC-19 Token backoffice + BroadcastChannel | ✅ Validasi pesan channel + CSP/security headers di kedua `next.config.mjs` |
| SEC-20 Docker/deploy hardening | ✅ Runtime non-root, no host network, uploads volume/gitignore, env placeholder guard |
| SEC-10 IDOR chat messages | ✅ `ChatMessages()` cek ownership session + tolak guest/expired (verifikasi 25 Jul 2026) |
| #11 Pecah services.go | ✅ Dipecah per-domain (satu package) |
| #12 Duplikasi prompt LLM | ✅ Urutan pesan dirapikan + workflow diringkas |
| #14 Error HTML Saat JSON | ✅ Cek `Content-Type` + try-catch di `api.ts` |
| #15 Refresh Promise Timeout | ✅ AbortController 10s di `refreshAccessToken` |

> Catatan: item lama (pagination list endpoint & async logging MCP + retry) sudah selesai lebih dulu: `dto.ListQuery.Normalize()` (default 50, maks 200) dan audit log + single retry di `MCPService.Execute()`.

---

## Lihat Juga
- `architecture.md` — gambaran sistem & fitur aktif
- `backend.md` — detail service layer & integrasi
- `coding-rules.md` — konvensi agar perubahan konsisten
