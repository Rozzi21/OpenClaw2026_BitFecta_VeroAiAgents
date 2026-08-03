# Frontend

Dokumen ini mencakup KEDUA aplikasi Next.js di repo: `frontend/` (customer chat) dan `backoffice-frontend/` (operator/admin TravelOS). Keduanya Next.js 14 App Router + React 18 + TypeScript + TailwindCSS, dan sama-sama mem-proxy `/api/*` ke backend `:8080`.

> Untuk endpoint backend yang dipanggil, lihat [api.md](api.md). Untuk arsitektur sistem lihat [architecture.md](architecture.md).

---

## 1. Customer Frontend (`frontend/`)

Antarmuka chat AI untuk tamu. Tidak ada login, tidak ada auth. Efektif hanya dua panggilan backend: chat AI dan detail paket.

### Struktur Halaman & Routing (App Router)

| Route | File | Fungsi |
|---|---|---|
| `/` | `frontend/src/app/page.tsx` | Halaman utama, me-render `ChatInterface` |
| `/trip/[id]` | `frontend/src/app/trip/[id]/page.tsx` | Detail paket trip (memanggil `GET /api/v1/packages/:id`) |
| layout root | `frontend/src/app/layout.tsx` | Layout global, font, metadata |

### Komponen Kunci

| Komponen | Path | Tanggung jawab |
|---|---|---|
| `ChatInterface` | `frontend/src/components/chat/ChatInterface.tsx` | Inti aplikasi: kirim prompt ke `POST /api/v1/chat` (mode streaming SSE, PERF-1), simpan `session_id`, render pesan + caret saat stream + animasi mengetik untuk history, render kartu rekomendasi, panel detail paket |
| `RecommendationCard` | `frontend/src/components/cards/RecommendationCard.tsx` | Kartu paket rekomendasi inline di chat |
| `TripPriceBlock` | `frontend/src/components/pricing/TripPriceBlock.tsx` | Blok harga paket (base/discount/child) |
| `Sidebar` | `frontend/src/components/layout/Sidebar.tsx` | Navigasi kiri (sebagian link masih placeholder `href="#"`) |

### Mekanisme Rekomendasi

Response `POST /api/v1/chat` sekarang mengandung field:
- `show_recommendations` (boolean): menentukan apakah daftar rekomendasi harus ditampilkan.
- `recommendation_reason` (`"initial"` | `"alternative"` | `""`): menjelaskan konteks rekomendasi.
- `recommended_packages` (array, optional): daftar paket hasil tool `search_trips`.

Frontend hanya merender `PackageRecommendations` bila `show_recommendations === true`. Jika user sudah memilih paket atau sedang proses booking, field ini `false` sehingga kartu rekomendasi tidak muncul berulang. Label heading berubah menjadi "Alternatif paket lain dari Vero" ketika `recommendation_reason === "alternative"`.

### Lib / Helper

| File | Fungsi |
|---|---|
| `frontend/src/lib/api.ts` | `apiFetch()` envelope-aware, memeriksa `Content-Type`, menangani respons HTML/proxy error, timeout 35 s via `AbortController`, serta `assetURL()` + tipe `TripPackage`. **`streamChat()` (PERF-1, 3 Agu 2026)** — konsumsi SSE chat streaming via `fetch` + `ReadableStream` reader + parser SSE manual (`parseSSEBlock`), dispatch `delta`/`done`/`error` ke callback; tidak pakai timeout 35s (stream wajar hidup lama, backend kunci via `AI_TIMEOUT_SECONDS` + ctx), `AbortController` tetap membatalkan stream di hulu. Base URL kosong di browser (proxy), `NEXT_PUBLIC_API_BASE_URL` di server |
| `frontend/src/lib/format.ts` | Format harga (`formatIDR`, `getDiscountMeta`, `getTripAdultPrice`/`getTripChildPrice`). `formatIDR` memformat angka termasuk `0` sebagai Rp 0; `"TBD"` hanya untuk `null`/`undefined`/`NaN` |
| `frontend/src/lib/format-trip-pax.ts` | Format jumlah pax (dewasa/anak) |
| `frontend/src/lib/utils.ts` | Util umum (mis. `cn()` untuk className) |

### State Management

Murni React lokal (`useState`/`useEffect`) di `ChatInterface`. Tidak ada Redux/Zustand/Context global. State penting:
- `messages` — array pesan chat
- session identifier tidak disimpan di React/localStorage; browser mengelola cookie HttpOnly `vero_chat_session`
- `recommendedPackages` — dari respons chat

Saat mount, `ChatInterface` memanggil `GET /api/v1/chat/history` dengan credentials browser untuk memulihkan message guest. Request chat mengirim prompt saja; cookie otomatis menjadi ownership proof. Cookie berlaku sliding 7 hari dan session expired memulai percakapan baru.

**Streaming respons (PERF-1, 3 Agu 2026):** `handleSubmit` selalu memakai mode streaming (`stream:true`) lewat `streamChat()` — sisipkan pesan assistant kosong bertanda `streaming`, setiap event `delta` append fragmen teks real-time + caret, event terminal `done` finalisasi packages/recommendation flags + set `completedTyping`. `AbortController` ref memungkinkan cancel. Saat `streaming` true, render text + caret (bukan animasi typing ulang `TypingText`). Efek mengetik (`TypingText`) kini hanya dipakai untuk history yang dimuat dari `GET /chat/history` (non-streaming). Non-stream path (`stream:false`) tetap tersedia via `apiFetch` bila dibutuhkan.

### Fitur yang BELUM aktif (UI placeholder)

- Tombol customer sekarang membuat order manual lewat `POST /api/v1/orders`; tidak membuat payment/session DOKU.
- Teks checkout diganti menjadi manual admin processing; DOKU payment temporarily disabled.
- Link sidebar "Past Journeys", "Saved Places", "Settings", "My Profile" — `href="#"`.
- Tombol "+" di input chat — tanpa handler.

---

## 2. Backoffice Frontend (`backoffice-frontend/`)

Dashboard operator/admin untuk CRUD paket trip. Punya auth penuh (JWT access di localStorage + refresh via cookie HttpOnly).

### Struktur Halaman & Routing

| Route | File | Fungsi |
|---|---|---|
| `/login` | `backoffice-frontend/src/app/login/page.tsx` | Login operator/admin, cek `isBackofficeRole` |
| `/` | `backoffice-frontend/src/app/page.tsx` | Layar utama: list paket / dashboard (panel) |
| `/trips` | `backoffice-frontend/src/app/trips/page.tsx` | Placeholder → render `CurrentTripsScreen` |
| `/trips/[id]` | `backoffice-frontend/src/app/trips/[id]/page.tsx` | Placeholder → render `CurrentTripsScreen` |
| `/orders` | `backoffice-frontend/src/app/orders/page.tsx` | Render `CurrentTripsScreen`; panel Orders menampilkan booking/order dari `/api/v1/bookings` |
| `/settings` | `backoffice-frontend/src/app/settings/page.tsx` | Placeholder → render `CurrentTripsScreen` |
| layout root | `backoffice-frontend/src/app/layout.tsx` | Membungkus seluruh app dengan `AppShell` |

PENTING: `/trips`, `/trips/[id]`, `/orders`, `/settings` masih me-render layar shell yang sama (`CurrentTripsScreen` = re-export `TripsListScreen`). Namun panel `/orders` sekarang menampilkan daftar order dari backend; dashboard/settings/trip detail backoffice masih placeholder.

### Guard Auth: `AppShell`

`backoffice-frontend/src/components/app-shell.tsx` adalah gerbang auth global:
- State `loading | authenticated | unauthenticated`.
- Saat mount: cek token → `fetchCurrentUser()` (`GET /auth/me`) → verifikasi `isBackofficeRole`.
- Jika tidak authenticated dan bukan route publik (`/login`) → redirect ke `/login?redirect=...`.
- Jika authenticated dan di `/login` → redirect ke `/`.
- Memulai `startAuthRefreshScheduler()`.

### Manajemen Paket (fitur inti aktif)

Struktur komponen `trips/`:

| Folder/Komponen | Path | Fungsi |
|---|---|---|
| List screen | `backoffice-frontend/src/components/trips/list/trips-list-screen.tsx` | Layar daftar paket + panel dashboard ("On Development") |
| List hook | `backoffice-frontend/src/components/trips/list/use-trips-list.ts` | Fetch `GET /admin/packages`, cache in-memory 60 detik + deduplikasi request paralel; filter kategori/search berjalan lokal agar interaksi UI tidak memanggil server berulang |
| Trip card | `backoffice-frontend/src/components/trips/list/trip-card.tsx` | Kartu paket di list |
| Form screen | `backoffice-frontend/src/components/trips/form/trip-form-screen.tsx` | Form create/edit paket |
| Form hook | `backoffice-frontend/src/components/trips/form/use-trip-form.ts` | Logika form: create (`POST`), update (`PUT`), upload media (`POST /admin/uploads`) |
| Form sections | `backoffice-frontend/src/components/trips/form/sections/` | 9 seksi: basic-info, summary, pricing, scheduling, itinerary, amenities, highlights, media, reference |
| Form UI | `backoffice-frontend/src/components/trips/form/ui/` | 11 widget reusable: field, label, checkbox, number-stepper, date-range, upload-box, dll |
| Shared | `backoffice-frontend/src/components/trips/shared/` | `backoffice-sidebar.tsx`, `format-trip-pax.ts`, `trip-status-tone.ts`, `format-date-range.ts` |

### Lib / Helper

| File | Fungsi |
|---|---|
| `backoffice-frontend/src/lib/api.ts` | INTI auth: token storage, refresh proaktif + retry 401, BroadcastChannel antar-tab, `apiFetch()` dengan flag `auth`. Lihat [backend.md](backend.md) bagian auth flow detail |
| `backoffice-frontend/src/lib/trip.ts` | Operasi paket: detail (`GET /trips/:id`), update status (`PUT`), delete (`DELETE`) |
| `backoffice-frontend/src/lib/format.ts` | Format harga (`formatIDR`, `getDiscountMeta`). Perilaku `formatIDR` sama dengan customer frontend: angka `0` → Rp 0; `"TBD"` hanya untuk `null`/`undefined`/`NaN` |
| `backoffice-frontend/src/lib/data.ts` | MOCK data (`travelCards`, `orders`, `payments`, `workflowSteps`) — TIDAK terpakai di komponen manapun |
| `backoffice-frontend/src/lib/utils.ts` | Util umum |

### State Management

React lokal + modul-level singletons di `lib/api.ts` untuk auth (token, timer refresh, channel). Tidak ada state library global. Data paket diambil per-screen via hook (`use-trips-list`, `use-trip-form`).

### Auth Token Flow (ringkas)

Detail lengkap di [backend.md](backend.md) dan [api.md](api.md). Ringkasnya:
- Access token + expiry + role disimpan di `localStorage` (key `backoffice_token`, `backoffice_token_expires_at`, `backoffice_user_role`).
- Refresh token HANYA di cookie HttpOnly (tak tersentuh JS).
- Refresh proaktif ~5 menit sebelum expiry (`scheduleProactiveRefresh`), plus retry otomatis saat 401 di `apiFetch`.
- `BroadcastChannel("vero_auth")` menyiarkan token baru ke tab lain (mencegah race rotasi refresh token). Sejak SEC-19, pesan channel divalidasi ketat (type/access_token/expires_at) sebelum diadopsi.
- Sejak SEC-19, kedua `next.config.mjs` mengirim header keamanan (`Content-Security-Policy` dengan `script-src 'self' 'unsafe-inline' 'unsafe-eval'` dan `connect-src` ke backend + WebSocket localhost, tanpa `upgrade-insecure-requests`; serta `X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`, `Permissions-Policy`) di semua route untuk mempersempit permukaan XSS pencurian token sambil tetap kompatibel dengan dev lokal HTTP.

### Fitur yang BELUM aktif (placeholder/mock)

- Dashboard = panel "On Development" (`on-development-panel.tsx`).
- `/settings`, `/trips/[id]` placeholder. `/orders` sudah memiliki layout lengkap (Order Management) melalui `backoffice-frontend/src/components/orders/orders-panel.tsx`: statistik status, pencarian, filter dengan jumlah order, refresh manual, urutan prioritas status, kartu status pembayaran, detail drawer, kontak WhatsApp/email, serta konfirmasi sebelum pembatalan. Mendukung update status manual via `PUT /api/v1/bookings/:id`.
- Mock data di `lib/data.ts` tidak dirender.
- Tidak ada UI pembayaran, tidak ada fetch bookings/logs/analytics.

---

## 3. Pola Bersama Kedua Frontend

| Pola | Implementasi |
|---|---|
| Proxy API | `next.config.mjs` rewrite `/api/:path*` → `http://localhost:8080/api/:path*` |
| Base URL | Kosong di browser (same-origin proxy), `NEXT_PUBLIC_API_BASE_URL` di server |
| Envelope-aware fetch | `apiFetch()` membaca `{ success, message, data }`, melempar `Error(message)` saat gagal |
| Tipe `TripPackage` | Didefinisikan terpisah di tiap `lib/api.ts` (TIDAK di-share antar app) |
| Asset URL | `assetURL()` membangun URL gambar absolut ke backend |
| Styling | TailwindCSS + `clsx`/`tailwind-merge` (`cn()`), ikon `lucide-react` |
| Dependencies npm | `clsx`, `lucide-react` (^1.18), `next`, `react`, `react-dom`, `tailwind-merge` — **tanpa** library animasi eksternal; animasi chat (`TypingText`) murni CSS/React state |

PENTING untuk AI: kedua app adalah codebase TERPISAH. Tidak ada kode yang di-share. Perubahan tipe `TripPackage` di satu app tidak memengaruhi yang lain. `lib/format.ts` juga duplikat per app — jika mengubah `formatIDR`, perbarui **keduanya** agar perilaku harga konsisten.
