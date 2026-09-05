# Deployment

Panduan deployment, environment variables, infrastruktur, dan integrasi pihak ketiga untuk VeroAiTravelAgents. Hanya backend yang punya pipeline deploy formal; kedua frontend Next.js di-deploy terpisah (belum ada konfigurasi deploy khusus di repo).

## Komponen yang Di-deploy

| Komponen | Artefak | Status deploy |
|---|---|---|
| `backend/` | Binary Go statis (`vero-travel-api`) | Punya Dockerfile + docker-compose + panduan systemd (`backend/docs/server-deploy.md`) |
| `frontend/` | Next.js build (`next build` → `next start`) | Belum ada konfigurasi deploy di repo |
| `backoffice-frontend/` | Next.js build | Belum ada konfigurasi deploy di repo |
| PostgreSQL 16 | Container / instance server | Via docker-compose atau instance eksternal |

## Environment Variables (Backend)

Sumber kebenaran: `backend/internal/config/config.go` (fungsi `Load()`), contoh di `backend/.env.example`.

### Aplikasi
| Variabel | Default | Keterangan |
|---|---|---|
| `APP_ENV` | `development` | `production` mengaktifkan gin release mode, cookie secure default, dan guard `Config.Validate()` |
| `PORT` | `8080` | Port HTTP |

### Database
| Variabel | Default | Keterangan |
|---|---|---|
| `DATABASE_HOST` | `localhost` | Host PostgreSQL |
| `DATABASE_PORT` | `5432` | Port |
| `DATABASE_USER` | `vero_user` | User |
| `DATABASE_PASSWORD` | _(kosong)_ | Wajib diisi; production menolak kosong/placeholder |
| `DATABASE_NAME` | `vero_travel` | Nama DB |
| `DATABASE_SSLMODE` | `disable` | Mode SSL |
| `DATABASE_URL` | _(kosong)_ | DSN penuh; jika kosong dirakit dari field di atas. Jika mengandung `YOUR_PASSWORD` juga dirakit ulang |

### JWT / Auth
| Variabel | Default | Keterangan |
|---|---|---|
| `JWT_SECRET` | `super_secret_vero_travel` | **Wajib diganti di production.** `Config.Validate()` menolak start jika kosong/default saat `APP_ENV=production` |
| `JWT_ACCESS_TTL_MINUTES` | `15` | Masa hidup access token |
| `JWT_REFRESH_TTL_HOURS` | `720` | Masa hidup refresh token (30 hari) |
| `JWT_COOKIE_NAME` | `refresh_token` | Nama cookie refresh HttpOnly |
| `JWT_COOKIE_SECURE` | `APP_ENV==production` | Cookie hanya via HTTPS |
| `JWT_COOKIE_SAME_SITE` | `Strict` | `Lax`/`None`/`Strict` (case-insensitive). `None` otomatis memaksa `Secure=true`. Nilai lain **menggagalkan `Config.Validate()`** saat start (GO-P2-6) |

### Guest Identity (Guest Order Limit)
| Variabel | Default | Keterangan |
|---|---|---|
| `GUEST_COOKIE_SECURE` | `APP_ENV==production` | Cookie guest hanya via HTTPS |
| `GUEST_COOKIE_SAME_SITE` | `Lax` | SameSite cookie `vero_chat_session` dan `vero_guest_session`. Hanya `Strict`/`Lax`/`None` (case-insensitive) — nilai lain/salah tulis **menggagalkan `Config.Validate()`** saat start, tidak lagi jatuh senyap ke `Strict` (GO-P2-6). Pakai `Lax` (atau `None` + `GUEST_COOKIE_SECURE=true`): `Strict` valid tapi mematikan claim order guest pada callback Google |
| `GUEST_SESSION_TTL_HOURS` | `168` | Sliding TTL `ChatSession` tamu (7 hari) |
| `GUEST_IDENTITY_TTL_HOURS` | `720` | TTL `GuestSession` server-side untuk guest order limit (30 hari) |

### AI (OpenAI-compatible)
| Variabel | Default | Keterangan |
|---|---|---|
| `AI_API_KEY` | _(kosong)_ | Kosong → fallback lokal (demo tetap jalan) |
| `AI_BASE_URL` | `https://api.openai.com/v1` | Endpoint provider; klien memanggil `{AI_BASE_URL}/chat/completions` |
| `AI_MODEL` | `gpt-4o-mini` | Nama model |
| `AI_TEMPERATURE` | `0.4` | Temperature |
| `AI_TIMEOUT_SECONDS` | `35` | Timeout request AI |
| `AI_CONTEXT_RECENT_MESSAGES` | `8` | Jumlah pesan terakhir sebagai konteks |
| `AI_MEMORY_SUMMARY_AFTER` | `12` | Ambang pesan sebelum ringkasan memory dibuat |
| `AI_MEMORY_MAX_CHARS` | `1800` | Batas panjang ringkasan memory |

### Integrasi Eksternal
| Variabel | Default | Keterangan |
|---|---|---|
| `DOKU_CLIENT_ID` | _(kosong)_ | Client ID DOKU |
| `PAYMENTS_ENABLED` | `false` | Feature flag DOKU/payment flow. Default `false` keeps manual admin order processing and makes payment endpoints return 503. Set `true` only when re-enabling DOKU. |
| `DOKU_SECRET` | _(kosong)_ | Secret verifikasi signature webhook HMAC-SHA256. **Wajib di production hanya saat `PAYMENTS_ENABLED=true`** — `Config.Validate()` menolak start bila kosong saat DOKU diaktifkan (SEC-4) |
| `N8N_WEBHOOK` | _(kosong)_ | URL webhook N8N untuk otomasi pasca-pembayaran |
| `CORS_ALLOWED_ORIGINS` | _(localhost dev)_ | Daftar origin diizinkan CORS, dipisah koma (mis. `https://app.vero.com,https://admin.vero.com`). Fallback ke `http://localhost:3000,:3001,:5173` bila kosong (SEC-8) |

### Google OAuth ("Continue with Google", 19 Agu 2026)
| Variabel | Default | Keterangan |
|---|---|---|
| `GOOGLE_OAUTH_ENABLED` | `false` | Feature flag. Endpoint `/auth/google/*` membalas 404 saat false |
| `GOOGLE_CLIENT_ID` | _(kosong)_ | Client ID OAuth dari Google Cloud Console |
| `GOOGLE_CLIENT_SECRET` | _(kosong)_ | Secret server. **Wajib di production saat `GOOGLE_OAUTH_ENABLED=true`** — `Config.Validate()` menolak start bila kosong (pola SEC-4) |
| `GOOGLE_REDIRECT_URI` | `http://localhost:8080/api/v1/auth/google/callback` | Harus terdaftar PERSIS di Google Cloud Console (Authorized redirect URIs). Production: tolak start bila masih localhost saat enabled. Alias `GOOGLE_REDIRECT_URL` diterima sebagai fallback (23 Agu 2026); `GOOGLE_REDIRECT_URI` menang bila keduanya di-set |
| `GOOGLE_LINK_REDIRECT_URI` | derive dari `GOOGLE_REDIRECT_URI` (`…/auth/google/callback` → `…/auth/google/link/callback`) | Redirect URI alur "Link Google Account" (24 Agu 2026). Harus terdaftar PERSIS di Google Cloud Console juga. Production: ikut ditolak bila localhost saat enabled |
| `GOOGLE_OAUTH_FRONTEND_URL` | `http://localhost:3000` | Origin FE untuk redirect final + allowlist `return_to`. Jangan ambil dari request (open-redirect guard). Production: tolak localhost saat enabled |
| `TRUSTED_PROXIES` | _(kosong)_ | Daftar CIDR reverse proxy tepercaya untuk resolusi IP klien via `X-Forwarded-For`. Kosong berarti tidak mempercayai proxy sama sekali (default dev). Wajib diisi CIDR load balancer/nginx di production agar rate limit per-IP tidak di-bypass (SEC-14) |


### Environment Variables (Frontend)
Kedua Next.js app memakai satu variabel publik opsional:

| Variabel | Default | Dipakai untuk |
|---|---|---|
| `NEXT_PUBLIC_API_BASE_URL` | `http://localhost:8080` | Base URL aset gambar + pemanggilan sisi server. Request browser tetap relatif (`/api/...`) lewat proxy `next.config.mjs` |

## Build & Release Flow

### Backend (lokal)
```bash
cd backend
cp .env.example .env       # isi DATABASE_PASSWORD, JWT_SECRET, AI_*
go mod tidy
go run ./cmd/server        # atau: go build -o vero-travel-api ./cmd/server
```

### Backend (Docker)
`backend/Dockerfile` adalah multi-stage build (CGO_ENABLED=0, binary statis) dengan runtime non-root user `app`. `backend/docker-compose.yml` menjalankan API saja; **default dev memakai PostgreSQL lokal di host `:5432`** (via `host.docker.internal`):

```bash
cd backend
docker compose up --build
```

Pastikan Postgres lokal sudah jalan di `:5432` dan kredensial di `.env` cocok. API container memakai bridge network dan memetakan `8080:8080`; compose mengatur default `DATABASE_HOST=host.docker.internal`. Jangan gunakan `network_mode: host`.

Jika ingin Postgres bawaan Docker (port host `:5433`), jalankan dengan profile:

```bash
docker compose --profile docker-db up --build
```

Lalu jalankan dengan `DOCKER_DATABASE_HOST=postgres` dan `DATABASE_PASSWORD` yang sama dengan password Postgres. Compose meneruskan `DOCKER_DATABASE_HOST` ke API. API menyimpan uploads di named volume `uploads_data`.

### Backend (server / systemd)
Panduan lengkap di `backend/docs/server-deploy.md`:
1. Install Go 1.25.x di server.
2. Clone/rsync repo ke `/opt/vero-travel-agents`.
3. `cp .env.example .env` lalu set `APP_ENV=production`, `JWT_SECRET` kuat, koneksi DB ke `127.0.0.1`.
4. `go build -o vero-travel-api ./cmd/server`.
5. Pasang systemd unit dari `backend/scripts/vero-travel-api.service`.
6. `systemctl enable --now vero-travel-api`.
7. Untuk production: taruh Nginx/Caddy di depan untuk HTTPS, jangan ekspos `:8080` langsung.

### Frontend (kedua app)
```bash
npm install
npm run build
npm run start    # production
# atau npm run dev untuk development
```
Keduanya default ke port 3000 — jalankan salah satu dengan `--port` berbeda (mis. backoffice di 3001) untuk menghindari bentrok.

## Infrastruktur

```mermaid
flowchart LR
  subgraph client [Browser]
    FE[Customer FE :3000]
    BO[Backoffice FE :3001]
  end
  subgraph server [Server]
    API[vero-travel-api :8080]
    PG[(PostgreSQL 16 :5432)]
    UP[/uploads volume/]
  end
  EXT_AI[AI Provider (OpenAI-compatible)]
  EXT_DOKU[DOKU Payment]
  EXT_N8N[N8N Automation]

  FE -->|"proxy /api/*"| API
  BO -->|"proxy /api/*"| API
  API --> PG
  API --> UP
  API -->|chat completions| EXT_AI
  EXT_DOKU -->|webhook| API
  API -->|trigger| EXT_N8N
```

- **Uploads**: backend menyimpan file ke `./uploads/trips/` dan menyajikannya via `router.Static("/uploads", ...)`. Di server, folder ini perlu persistensi (volume) dan kepemilikan yang benar (`chown www-data` di panduan systemd).
- **CORS**: origin dibaca dari env `CORS_ALLOWED_ORIGINS` (CSV) via `config.go` lalu diteruskan ke `middlewares.CORS(cfg.CORSAllowedOrigins)`. Fallback ke `http://localhost:3000`, `:3001`, `:5173` bila env kosong. Set origin production via env saat deploy (tidak perlu ubah kode lagi — SEC-8).

## Service Pihak Ketiga

| Service | Peran | Titik integrasi |
|---|---|---|
| **PostgreSQL 16** | Database utama | `database/database.go` |
| **AI Provider (OpenAI-compatible)** | Generasi respons chat AI | `ai/ai_client.go`, `services.go` `generateWithAI()` |
| **DOKU** | Payment gateway (QRIS/VA) | `services.go` `PaymentService.Webhook()` + verifikasi HMAC |
| **N8N** | Otomasi workflow pasca-pembayaran | `services.go` `triggerN8N()` |

## Checklist Production

1. `APP_ENV=production`.
2. `JWT_SECRET` panjang dan acak (backend menolak start jika tidak).
3. `DATABASE_PASSWORD` kuat; pertimbangkan `DATABASE_SSLMODE=require`.
4. `DOKU_SECRET` **wajib** diisi di production saat `PAYMENTS_ENABLED=true` (backend menolak start bila kosong); webhook tanpa signature valid ditolak (SEC-4). Default `PAYMENTS_ENABLED=false` untuk flow order manual.
5. HTTPS via reverse proxy; set `JWT_COOKIE_SECURE=true`.
6. Jika frontend beda domain dari API: `JWT_COOKIE_SAME_SITE=None` (otomatis Secure) + tambahkan origin ke CORS.
7. Volume persisten untuk `uploads/`; Docker image berjalan sebagai non-root user `app`.
8. Ganti semua nilai dev placeholder di `.env.example`; jangan menyalin `.env` atau `.env.example` ke image.
9. Set `TRUSTED_PROXIES` ke CIDR reverse proxy yang sah (load balancer/nginx/Caddy) agar `c.ClientIP()` akurat dan rate limit per-IP efektif (SEC-14). Kosongkan hanya jika API langsung terpapar ke internet tanpa proxy.
10. Rotasi password database dan secret setelah deployment pertama.

---

## Strategi Backup & Restore (PRR-P0-2)

Untuk mencegah kehilangan data transaksi, booking, dan media, berikut adalah strategi backup dan restore yang direkomendasikan untuk production.

### 1. Backup PostgreSQL (Database)
Gunakan utilitas `pg_dump` untuk melakukan ekspor basis data secara berkala (misalnya harian).

**Skrip Backup Otomatis (`backup-db.sh`):**
```bash
#!/bin/bash
BACKUP_DIR="/var/backups/postgres"
TIMESTAMP=$(date +%F_%H%M%S)
DATABASE_URL="postgres://vero_user:password_anda@127.0.0.1:5432/vero_travel"

mkdir -p "$BACKUP_DIR"
pg_dump "$DATABASE_URL" | gzip > "$BACKUP_DIR/vero_travel_$TIMESTAMP.sql.gz"

# Hapus backup yang lebih tua dari 30 hari
find "$BACKUP_DIR" -type f -name "*.sql.gz" -mtime +30 -delete
```

Pasang di Cron untuk berjalan setiap hari jam 2 pagi:
```cron
0 2 * * * /opt/vero-travel-agents/scripts/backup-db.sh
```

**Prosedur Restore:**
```bash
# Ekstrak backup
gunzip /var/backups/postgres/vero_travel_xxxx.sql.gz

# Drop database saat ini dan buat ulang (WARNING: Data saat ini hilang)
dropdb -h 127.0.0.1 -U vero_user vero_travel
createdb -h 127.0.0.1 -U vero_user vero_travel

# Restore data
psql -h 127.0.0.1 -U vero_user -d vero_travel -f /var/backups/postgres/vero_travel_xxxx.sql
```

### 2. Backup File Media (`uploads/`)
Gunakan utilitas snapshot atau alat sinkronisasi berkala seperti `restic` atau `rclone` untuk mencadangkan direktori `backend/uploads/` ke Object Storage (seperti AWS S3 atau Cloudflare R2).

**Backup Sederhana dengan Rclone:**
```bash
rclone sync /opt/vero-travel-agents/backend/uploads remote-s3:vero-travel-uploads-backup
```

---

## Panduan Scaling & Horizontal Multi-Instance (PRR-P1-3)

Aplikasi saat ini dirancang untuk berjalan sebagai single-instance. Sebelum melakukan horizontal scaling (misalnya di Kubernetes HPA), beberapa komponen stateful in-memory harus dimigrasi:

1. **Event Bus (Realtime/SSE)**
   - *Masalah*: Event bus `events.Bus` berjalan in-memory. Jika pengguna terhubung ke Instance A, mereka tidak akan menerima event yang dipublikasikan dari Instance B.
   - *Solusi*: Ganti implementasi `Bus` di `backend/internal/events/bus.go` menggunakan Redis Pub/Sub sebagai message broker pusat.
2. **Rate Limiter Per-IP**
   - *Masalah*: Rate limiter disimpan di `sync.Map` in-memory per-instance. Budget rate limit efektif menjadi `N × limit` di mana N adalah jumlah instance.
   - *Solusi*: Ubah middleware rate limiter untuk memeriksa kuota menggunakan Redis (misalnya dengan skrip sliding window Redis).
3. **Cleanup Chat Sessions Ticker**
   - *Masalah*: Fungsi `startChatSessionCleanup` berjalan di internal ticker Go. Saat multi-instance, job pembersihan ini akan berpacu dan membebani database secara redundan.
   - *Solusi*: Matikan internal ticker di `main.go` untuk production. Delegasikan jalannya pembersihan sesi (`CleanupExpiredChatSessions`) ke external scheduler seperti **Kubernetes CronJob** atau Cron systemd server.
4. **Terminasi TLS & Load Balancer**
   - Gunakan Kubernetes Ingress Controller (seperti Nginx Ingress) atau Cloud Load Balancer untuk terminasi SSL/TLS dan mendistribusikan traffic ke pod API secara merata menggunakan algoritma round-robin.

---

## Panduan Deploy Frontend (PRR-P2-3)

Kedua frontend Next.js (`frontend/` dan `backoffice-frontend/`) dapat di-deploy secara reproducible menggunakan Docker standalone build.

### Dockerfile Standalone Next.js (Contoh untuk `frontend`)
```dockerfile
FROM node:20-alpine AS base

# Install dependencies only when needed
FROM base AS deps
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci

# Rebuild the source code only when needed
FROM base AS builder
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY . .
ENV NEXT_TELEMETRY_DISABLED=1
RUN npm run build

# Production image, copy all the files and run next
FROM base AS runner
WORKDIR /app
ENV NODE_ENV=production
ENV NEXT_TELEMETRY_DISABLED=1

RUN addgroup --system --gid 1001 nodejs
RUN adduser --system --uid 1001 nextjs

COPY --from=builder /app/public ./public
COPY --from=builder --chown=nextjs:nodejs /app/.next/standalone ./
COPY --from=builder --chown=nextjs:nodejs /app/.next/static ./.next/static

USER nextjs
EXPOSE 3000
ENV PORT=3000
ENV HOSTNAME="0.0.0.0"

CMD ["node", "server.js"]
```

---

## Observability, Alerting, dan Runbook Insiden (PRR-P1-1, PRR-P3-1)

Sistem memantau kesehatan melalui `/healthz` (liveness), `/readyz` (readiness), dan metrik di `/metrics`.

### 1. Kebijakan Alerting Utama (Recommended Prometheus Alerts)
- **High Error Rate**: Memicu alert jika persentase HTTP Status `5xx` di `/metrics` melebihi `5%` dalam jangka waktu 5 menit.
- **Latency Spike**: Memicu alert jika durasi HTTP Request `p95` melebihi `5 detik` dalam 5 menit (mengindikasikan delay AI provider / DB bottleneck).
- **Database Down**: Memicu alert jika `/readyz` mengembalikan kode selain `200` atau database health check mengembalikan error.

### 2. Runbook Insiden Dasar
- **Kasus `/readyz` Gagal (Database Down)**:
  1. Periksa log backend: `journalctl -u vero-travel-api -f` atau periksa log pod Kubernetes.
  2. Periksa status PostgreSQL: `systemctl status postgresql` atau konektivitas DB.
  3. Periksa apakah pool koneksi database penuh (atur `MaxOpenConns` lebih besar jika beban tinggi).
- **Kasus Latency Spike (Respons AI Lambat)**:
  1. Periksa status upstream AI provider (misalnya OpenAI status page).
  2. Jika upstream AI mengalami overload, sistem akan otomatis mengembalikan respons fallback lokal atau timeout 35s. Administrator dapat mengganti model ke yang lebih ringan melalui variabel env `AI_MODEL`.
