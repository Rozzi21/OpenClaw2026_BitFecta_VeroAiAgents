# Coding Rules & Conventions

Aturan ini diturunkan dari pola yang konsisten diamati di seluruh codebase VeroAiTravelAgents. Ikuti aturan ini agar kode baru selaras dengan yang sudah ada. Setiap bagian menyebut lokasi referensi.

---

## 1. Backend Go

### 1.1 Arsitektur Berlapis (WAJIB)

Alur dependensi selalu satu arah:

```
Handler -> Service -> Repository -> GORM/PostgreSQL
```

- **Handler** (`backend/internal/handlers/`): hanya parsing request, panggil service, format response. TIDAK boleh akses DB langsung atau berisi logika bisnis. Handler dipecah per-domain dalam file `*_handlers.go` (`auth_handlers.go`, `chat_handlers.go`, `trip_handlers.go`, `booking_handlers.go`, `payment_handlers.go`, `logs_handlers.go`, `upload_handlers.go`, `sse_handlers.go`, `health_handlers.go`); `handlers.go` hanya berisi `Handler` struct + `New()`, `helpers.go` helper bersama. Saat menambah handler, taruh di file domain yang sesuai (ARCH-5).
- **Service** (`backend/internal/services/`): semua logika bisnis, dipecah per-domain (`auth_service.go`, `ai_service.go`, `payment_service.go`, dst — satu package `services`). TIDAK boleh menyentuh `*gin.Context`. Saat menambah logika, taruh di file domain yang sesuai; jangan menumpuk semuanya di `services.go` (yang kini hanya berisi `Services` struct + `New()` + tipe bersama).
- **Repository** (`backend/internal/repositories/`): satu-satunya lapisan yang menyentuh `r.DB` (GORM).

DILARANG: handler memanggil repository langsung, atau service menerima `*gin.Context`.

### 1.1a Context Propagation (WAJIB, sejak SEC-26 — 31 Jul 2026)

Setiap method Service dan Repository yang menyentuh DB/network WAJIB menerima `ctx context.Context` sebagai **parameter pertama** dan meneruskannya ke bawah.

- Handler meneruskan `c.Request.Context()` ke service.
- Service meneruskan `ctx` ke repo dan ke integrasi eksternal.
- Repository menjalankan query lewat `r.DB.WithContext(ctx)` (termasuk transaksi: `r.DB.WithContext(ctx).Begin()` / `.Transaction(...)`).
- HTTP call keluar memakai `http.NewRequestWithContext(ctx, ...)` — contoh `ai/ai_client.go` (`Generate`) dan `payment_service.go` (`triggerN8N` — detached `context.Background()`+timeout karena webhook dikirim setelah respons request selesai).

DILARANG memakai `context.Background()`/`context.TODO()` di dalam jalur request HTTP (service/repo/HTTP client). `context.Background()` hanya boleh untuk background yang memang detach dari request: ticker/scheduler (mis. `startChatSessionCleanup` di `main.go` memakai `context.Background()`+timeout 30s), atau goroutine yang sengaja hidup setelah respons.

> Pengecualian yang sudah ada: `AnalyticsService.Dashboard()` mengakses `s.repo.DB` langsung untuk query agregat. Ini pengecualian sadar, bukan pola yang dianjurkan untuk ditiru.

### 1.1b Sentinel Errors (WAJIB, sejak SEC-28 — 1 Agu 2026)

Error domain yang mempengaruhi alur logika (bukan sekadar pesan ke user) WAJIB dideklarasikan sebagai sentinel error package-level, bukan `errors.New` inline.

```go
var ErrPaymentNotFound = errors.New("payment not found")
```

Saat mengecek jenis error di caller, WAJIB pakai `errors.Is(err, pkg.ErrName)`. DILARANG membandingkan `err.Error() == "..."` (string matching) karena rapuh terhadap perubahan teks.

Sentinel yang sudah ada:
- `services.go`: `ErrRefreshTokenRevoked`, `ErrInvalidRefreshToken`, `ErrPaymentsDisabled`, `ErrChatSessionNotFound`, `ErrChatSessionExpired`
- `payment_service.go`: `ErrPaymentNotFound`, `ErrBookingNotFoundForPayment`, `ErrMissingSignature`, `ErrInvalidTimestampFormat`, `ErrWebhookTimestampExpired`, `ErrInvalidPaymentSignature`, `ErrWebhookSecretMissing`, `ErrPaymentAmountMismatch`, `ErrPaymentAlreadySettled`
- `booking_service.go`: `ErrBookingNotFound`

### 1.2 Dependency Injection Manual

Semua dependency di-wire di `services.New()` (`backend/internal/services/services.go`) dan `handlers.New()`. TIDAK ada framework DI. Tambah service baru dengan menambahkan field ke struct `Services` lalu inisialisasi di `New()`.

### 1.3 Envelope Response Seragam (WAJIB)

Setiap response HTTP memakai helper di `backend/internal/utils/response.go`. JANGAN panggil `c.JSON()` langsung di handler.

```go
utils.Success(c, http.StatusOK, "Trips", trips)
utils.BadRequest(c, "Validation failed", gin.H{"detail": err.Error()})
utils.NotFound(c, "Trip not found")
utils.ServerError(c, err)
```

Bentuk envelope: `{ success, message, data, error }`. Frontend bergantung pada bentuk ini (lihat `Envelope<T>` di kedua `lib/api.ts`).

**Kebocoran error (SEC-15):** JANGAN mengirim `err.Error()` dari error internal (DB/GORM/service) ke client — baik via `ServerError` (sudah generik + log server) maupun `gin.H{"detail": err.Error()}` di `BadRequest` untuk endpoint publik/service. Log detail ke server (`log.Printf` + `request_id`), balas pesan statis ke client. Pengecualian yang disengaja: `bind()` (error validasi JSON per-field) dan `parseID()` (parse UUID) boleh mengirim `detail` karena itu error input klien, berguna untuk UX form.

**Batas request publik (SEC-16):** Endpoint publik yang memicu LLM atau write ke DB wajib memakai body limit middleware dan DTO validation. `ChatRequest.Prompt` maksimum 4000 karakter; `POST /chat` dan `POST /orders` maksimum 64 KiB sebelum `ShouldBindJSON`.

### 1.4 Validasi Input via DTO

Request divalidasi lewat struct tag binding di `backend/internal/dto/dto.go`, bukan manual di handler.

```go
type PaymentCreateRequest struct {
    BookingID     uuid.UUID `json:"booking_id" binding:"required"`
    PaymentMethod string    `json:"payment_method" binding:"required,oneof=QRIS VA VIRTUAL_ACCOUNT"`
    Amount        float64   `json:"amount" binding:"required"`
}
```

Handler memakai helper `bind(c, &req)` yang otomatis membalas `BadRequest` bila gagal.

### 1.5 Model GORM

- Semua model embed `BaseModel` (`backend/internal/models/models.go`): UUID primary key, timestamps, soft delete.
- UUID di-generate di hook `BeforeCreate` (jangan set ID manual).
- Field array/object disimpan sebagai JSONB via `gorm:"serializer:json;type:jsonb"`.
- Password user pakai tag `json:"-"` agar tidak pernah ter-serialize.

### 1.6 Keamanan

- Password selalu di-hash dengan `bcrypt` (`golang.org/x/crypto/bcrypt`). JANGAN simpan plaintext.
- Token JWT dipisah by audience (`access` vs `refresh`) - lihat `backend/internal/auth/jwt.go`.
- Event keamanan dicatat lewat `auth.LogSecurity()` (`backend/internal/auth/audit.go`), bukan `log.Print` biasa.
- Secret/kredensial selalu dari env (`os.Getenv`), JANGAN hardcode. `Config.Validate()` menolak secret default di production.
- **JANGAN pernah menerima `role` (atau field privilege lain) dari body request pada endpoint publik.** Role hanya boleh ditetapkan server-side atau lewat endpoint terproteksi `Role(admin)`. Lihat SEC-1 di `known-issues.md`.
- **Endpoint yang mengembalikan resource milik user (booking, payment, chat) WAJIB cek kepemilikan** (`resource.UserID == currentUserID(c)`) kecuali pemanggil operator/admin. Hindari IDOR (SEC-2).
- **Nilai uang/harga JANGAN dipercaya dari client.** Hitung `total_price`/`amount` di server dari data paket; body client hanya referensi (SEC-3).

### 1.7 Penamaan & Format

- `gofmt` wajib bersih (tab indentation). Jalankan `gofmt -w .` sebelum commit.
- Method exported pakai PascalCase, unexported camelCase.
- Error dikembalikan, bukan di-panic (kecuali `middlewares.Recovery()` yang menangkap panic global).

---

## 2. Frontend (Next.js - kedua app)

### 2.1 Pemanggilan API lewat `apiFetch`

Semua request ke backend lewat `apiFetch()` di `lib/api.ts`. JANGAN panggil `fetch()` mentah di komponen.

- Client memakai path relatif `/api/...` (di-proxy oleh `next.config.mjs`).
- `apiFetch` sudah envelope-aware: otomatis unwrap `payload.data` dan throw bila `!success`.

### 2.2 Pemisahan Client/Server Component

- Komponen interaktif diawali `"use client"` (lihat `ChatInterface.tsx`, `app-shell.tsx`).
- Resolusi base URL membedakan browser vs server lewat `resolveApiBase()` (browser pakai string kosong).

### 2.3 Tipe Data Selaras Backend

`TripPackage` type di `lib/api.ts` mencerminkan JSON model `Trip` backend. Saat menambah field model di backend, perbarui type ini agar tetap sinkron.

### 2.4 Konvensi Penamaan File

- **Customer frontend**: komponen PascalCase (`ChatInterface.tsx`, `RecommendationCard.tsx`).
- **Backoffice**: komponen kebab-case (`trips-list-screen.tsx`, `use-trip-form.ts`).

Ikuti konvensi yang sudah ada di masing-masing app; JANGAN campur.

### 2.5 Logika Reusable via Hook

Backoffice mengekstrak logika kompleks ke hook (`use-trip-form.ts`, `use-trips-list.ts`). Komponen fokus rendering, hook menangani state + efek + pemanggilan API.

### 2.6 Styling

TailwindCSS utility classes. Helper `cn()`/`clsx` + `tailwind-merge` di `lib/utils.ts` untuk menggabung class kondisional. Ikon via `lucide-react`. JANGAN tulis CSS file terpisah.

### 2.7 Dependencies npm (Frontend)

Jangan tambahkan dependency npm ke `frontend/` atau `backoffice-frontend/` kecuali benar-benar di-import di kode. Stack saat ini: `clsx`, `lucide-react`, `next`, `react`, `react-dom`, `tailwind-merge`. Library animasi eksternal (mis. `framer-motion`) tidak dipakai — animasi chat memakai state React + CSS.

### 2.8 Format Harga (`formatIDR`)

Kedua app punya salinan `lib/format.ts` terpisah (tidak di-share). Konvensi:
- Angka valid termasuk `0` → diformat `Intl` IDR (`Rp0`, `Rp 1.000.000`, dll).
- Hanya `null`, `undefined`, atau `NaN` → tampilkan `"TBD"`.
- JANGAN gunakan truthiness (`if (!amount)`) — itu salah menangani harga nol.

Saat mengubah logika harga, perbarui **kedua** `frontend/src/lib/format.ts` dan `backoffice-frontend/src/lib/format.ts`.

---

## 3. Pola Lintas-Tumpukan

### 3.1 Realtime via Event Bus

Untuk efek samping yang perlu disiarkan (workflow AI, pembayaran, booking), publish ke `events.Bus` (`backend/internal/events/bus.go`):

```go
s.bus.Publish("booking_created", booking)
```

Publish bersifat non-blocking (channel buffered 32, drop bila penuh). JANGAN andalkan event bus untuk data kritis yang tidak boleh hilang.

### 3.2 Fitur Dinonaktifkan Sengaja

Bila menonaktifkan fitur sementara, JANGAN hapus kodenya. Beri komentar alasan + cara mengaktifkan kembali, dan tandai status secara eksplisit. Contoh: `create_payment` di `mcp/tools.go` (`Enabled: false`) dan `services.go` (langkah workflow dikomentari dengan penjelasan).

### 3.3 Graceful Degradation

Integrasi eksternal harus punya fallback. Contoh: klien AI (`ai/ai_client.go`) mengembalikan respons lokal bila `AI_API_KEY` kosong atau provider gagal, sehingga demo tetap jalan. Tiru pola ini untuk integrasi baru.

---

## 4. Yang DILARANG

- Menyimpan refresh token di `localStorage` (hanya cookie HttpOnly).
- `c.JSON()` langsung di handler (pakai `utils.*`).
- Akses `r.DB` di luar lapisan repository (kecuali pengecualian analytics yang sudah ada).
- Hardcode secret/kredensial di kode.
- Menghapus kode fitur yang dinonaktifkan tanpa dokumentasi.
- `fetch()` mentah di komponen frontend (pakai `apiFetch`).
- Membuat file test tanpa menjalankan dan memverifikasinya (saat ini belum ada test sama sekali - lihat `known-issues.md`).

---

## 5. Verifikasi Sebelum Selesai

- **Backend**: `cd backend && gofmt -w . && go build ./...` harus exit 0.
- **Frontend/Backoffice**: `npx tsc --noEmit` harus lolos (atau `npm run lint`).
- Hindari `npx tsc` tanpa binary lokal karena bisa memicu prompt instalasi yang hang; pakai `./node_modules/.bin/tsc --noEmit`.
