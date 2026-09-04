# Project Map - VeroAiTravelAgents

Peta navigasi keseluruhan project untuk agent AI. Mulai dari sini untuk orientasi cepat, lalu lompat ke dokumen spesifik sesuai kebutuhan.

## Ringkasan Project

VeroAiTravelAgents ("Vero Travel" / "TravelOS") adalah platform travel berbasis AI dengan tiga aplikasi independen dalam satu monorepo:

| Aplikasi | Stack | Peran | Port dev |
|---|---|---|---|
| `backend/` | Go 1.25.5, Gin, GORM, PostgreSQL 16 | REST API + orkestrasi AI + realtime SSE | 8080 |
| `frontend/` | Next.js 14, React 18, TypeScript, Tailwind | Chat AI untuk pelanggan/tamu | 3000 |
| `backoffice-frontend/` | Next.js 14, React 18, TypeScript, Tailwind | Dashboard admin/operator kelola paket | 3001 (konvensi) |

Backend adalah inti sistem. Kedua frontend memanggilnya lewat proxy `/api/*` -> `localhost:8080`.

## Struktur Folder Penting

```
VeroAiTravelAgents/
├── backend/
│   ├── cmd/server/main.go         → entry point: wiring + graceful shutdown
│   ├── internal/
│   │   ├── config/                → load env + Config.Validate()
│   │   ├── database/              → koneksi GORM, retry, AutoMigrate, health
│   │   ├── models/models.go       → semua skema GORM (10 entity)
│   │   ├── repositories/          → akses data (CRUD)
│   │   ├── services/              → business logic, dipecah per-domain (*_service.go) + services.go (wiring)
│   │   ├── handlers/              → HTTP handler, dipecah per-domain (*_handlers.go) + handlers.go (wiring) + docs.go (OpenAPI)
│   │   ├── routes/routes.go       → registrasi rute + middleware
│   │   ├── middlewares/           → Auth, Role, CORS, RateLimit, dll
│   │   ├── auth/                  → JWTService, cookie, audit log
│   │   ├── ai/                    → klien AI OpenAI-compatible + fallback
│   │   ├── mcp/tools.go           → katalog tool MCP (+ Enabled flag)
│   │   ├── events/bus.go          → event bus in-memory untuk SSE
│   │   ├── utils/response.go      → envelope respons API standar
│   │   └── dto/                   → request/response + validasi
│   ├── .env.example               → template environment variables
│   ├── docker-compose.yml         → Postgres 16 + API
│   └── docs/server-deploy.md      → panduan deploy systemd
│
├── frontend/ (customer)
│   └── src/
│       ├── app/                   → page.tsx (chat), trip/[id], layout.tsx
│       ├── components/chat/ChatInterface.tsx  → komponen inti
│       └── lib/api.ts             → apiFetch + assetURL (tanpa auth)
│
├── backoffice-frontend/ (admin)
│   └── src/
│       ├── app/                   → page, login, trips, orders, settings
│       ├── components/
│       │   ├── app-shell.tsx      → guard auth + routing
│       │   └── trips/             → form/, list/, shared/, ui/ (CRUD paket)
│       └── lib/api.ts             → apiFetch + auth + refresh proaktif
│
└── docs/ai/                       → knowledge base ini
```

## Entry Point Utama

- **Backend**: `backend/cmd/server/main.go` - `main()` memuat config, validasi, connect DB, AutoMigrate, wiring DI, daftar rute, jalankan server di `:8080` dengan graceful shutdown.
- **Frontend customer**: `frontend/src/app/page.tsx` - render `ChatInterface`.
- **Backoffice**: `backoffice-frontend/src/app/page.tsx` dibungkus `app-shell.tsx` (guard auth).

## File yang Sering Digunakan / Dimodifikasi

| File | Kapan disentuh |
|---|---|
| `backend/internal/services/*_service.go` | Business logic per-domain (auth, AI, trip, booking, payment, analytics); `services.go` untuk wiring/tipe bersama |
| `backend/internal/handlers/*_handlers.go` | Menambah/ubah HTTP handler (per-domain; `handlers.go` hanya wiring) |
| `backend/internal/routes/routes.go` | Menambah endpoint baru atau ubah middleware |
| `backend/internal/handlers/docs.go` | WAJIB diperbarui saat rute berubah (OpenAPI manual) |
| `backend/internal/models/models.go` | Ubah skema database |
| `backend/internal/dto/dto.go` | Ubah bentuk request/response + validasi |
| `backoffice-frontend/src/lib/api.ts` | Logika auth/refresh token backoffice |
| `backoffice-frontend/src/components/trips/form/use-trip-form.ts` | Logika form paket |
| `frontend/src/components/chat/ChatInterface.tsx` | UI chat pelanggan |

## Peta Navigasi untuk AI (Berdasarkan Tugas)

| Tugas | Baca dokumen ini | File kunci |
|---|---|---|
| Memahami arsitektur umum | `architecture.md` | `main.go`, `services.go` |
| Menambah/ubah endpoint | `api.md` | `routes.go`, `handlers.go`, `docs.go` |
| Ubah skema/query DB | `database.md` | `models.go`, `repositories/` |
| Kerja di UI pelanggan | `frontend.md` | `ChatInterface.tsx`, `frontend/src/lib/api.ts` |
| Kerja di UI admin | `frontend.md` | `app-shell.tsx`, `trips/`, `backoffice api.ts` |
| Business logic / integrasi | `backend.md` | `services.go`, `ai/`, `mcp/` |
| Deploy / env vars | `deployment.md` | `.env.example`, `docker-compose.yml` |
| Hindari jebakan | `known-issues.md` | - |
| Ikuti konvensi | `coding-rules.md` | - |
| Daftar modul lengkap | `modules.md` | - |

## Konsep Inti yang Harus Dipahami

1. **Envelope respons seragam**: semua endpoint mengembalikan `{ success, message, data, error }` via `utils.Success/Error`.
2. **Layered architecture**: Handler -> Service -> Repository -> GORM. Jangan lompati lapisan.
3. **DI manual**: semua service dirakit di `services.New()`, handler di `handlers.New()`.
4. **Event-driven SSE**: `events.Bus` in-memory mem-publish event, di-stream lewat `/api/v1/events/stream`.
5. **JWT dua audience**: access (15m) vs refresh (720h), refresh disimpan sebagai session DB yang bisa di-revoke + dirotasi.
6. **Guest chat**: `POST /api/v1/chat` tidak butuh login (session anonymous via cookie).
7. **AI workflow tool-driven**: LLM memilih tool aktif (`search_trips`, `select_package`, `collect_order_detail`, `create_booking`) via function calling; tool rekomendasi legacy (`search_destination`, dll) dinonaktifkan dan dipetakan ke `search_trips`. Integrasi LLM nyata dengan fallback lokal.
8. **`create_payment` sengaja dinonaktifkan** di workflow chat (lihat `mcp/tools.go` `Enabled: false`).
9. **Guest chat anonymous**: `ChatSession` ber-`UserID=NULL`, diikat cookie HttpOnly `vero_chat_session` (sliding 7 hari); bukan lagi user bersama `guest@vero.local` (user itu hanya dipakai untuk `bookings.user_id`).
10. **Guest order limit (18 Agu 2026)**: guest boleh membuat tepat SATU order; identitas = `GuestSession` server-side via cookie HttpOnly `vero_guest_session` (opaque token 256-bit, hash SHA-256 di DB). Enforcement di `BookingService` dalam satu transaction (row lock `FOR UPDATE` + konsumsi entitlement atomik). Detail: `docs/GUEST_ORDER_LIMIT.md`.

## Fakta Penting (Status Saat Ini)

- **Automated test backend aktif** di `internal/ai`, `internal/auth`, `internal/mcp`, `internal/middlewares`, `internal/handlers` (Google OAuth guard + `guest_order_claim_handler_test.go` untuk `POST /orders/claim`), `internal/services` (incl. `guest_order_limit_test.go` + `guest_order_claim_test.go` — guest policy, race, idempotency, ownership, claim), `internal/utils`. Frontend belum punya test.
- **Tool MCP legacy masih simulasi/mock** (`mcp_service.go` method `mock` — hanya `send_whatsapp` + fallback unknown; tool rekomendasi lama di-unify ke `search_trips`).
- **Frontend customer**: hanya 2 endpoint aktif (chat + detail paket), tanpa auth.
- **Backoffice**: auth + CRUD paket + upload media aktif; dashboard/orders/settings masih placeholder.
- **Dependencies frontend**: kedua app Next.js memakai `lucide-react` ^1.18; `framer-motion` sudah dihapus (tidak pernah dipakai). Animasi chat = client-side murni.
- **Secret di `.env.example`** adalah nilai dev; `Config.Validate()` menolak secret default saat `APP_ENV=production`.

Detail lengkap tiap poin ada di `known-issues.md`.
