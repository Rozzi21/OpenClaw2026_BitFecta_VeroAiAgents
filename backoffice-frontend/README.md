# Vero TravelOS — Backoffice Frontend

Dashboard operator/admin untuk mengelola katalog paket trip ("TravelOS"). Operator login, lalu membuat, mengubah, mempublikasikan, dan menghapus paket trip berikut media gambarnya.

- Framework: Next.js 14 (App Router) + React 18 + TypeScript
- Styling: TailwindCSS, ikon lucide-react
- Port dev: `http://localhost:3000`

## Fitur Aktif

Yang benar-benar tersambung ke backend adalah **auth + manajemen paket (CRUD) + upload media**.

| Fitur | Endpoint | Lokasi |
|---|---|---|
| Login admin/operator | `POST /api/v1/auth/login` | `src/app/login/page.tsx` |
| Refresh access token (proaktif + retry 401) | `POST /api/v1/auth/refresh` | `src/lib/api.ts` |
| Sinkronisasi user/role | `GET /api/v1/auth/me` | `src/lib/api.ts` |
| Logout | `POST /api/v1/auth/logout` | `src/lib/api.ts` |
| List paket (filter kategori + search) | `GET /api/v1/admin/packages?category=&search=` | `src/components/trips/list/use-trips-list.ts` |
| Buat paket | `POST /api/v1/admin/packages` | `src/components/trips/form/use-trip-form.ts` |
| Update paket / ubah status | `PUT /api/v1/admin/packages/:id` | `src/components/trips/form/use-trip-form.ts`, `src/lib/trip.ts` |
| Hapus paket | `DELETE /api/v1/admin/packages/:id` | `src/lib/trip.ts` |
| Detail trip (prefill form edit) | `GET /api/v1/trips/:id` | `src/lib/trip.ts` |
| Upload media gambar (maks 5, FormData) | `POST /api/v1/admin/uploads` | `src/components/trips/form/use-trip-form.ts` |

### Auth & Sesi

- Login memverifikasi peran backoffice (`isBackofficeRole`) lewat `auth/me`.
- Access token di-refresh proaktif (~5 menit sebelum kedaluwarsa) lewat scheduler, plus retry otomatis saat menerima 401 di `apiFetch`.
- Refresh token berada di cookie HttpOnly (request memakai `credentials: include`).

### Form Paket

Form trip berseksi: `basic-info`, `pricing`, `itinerary`, `amenities`, `media`, `scheduling`, dll (lihat `src/components/trips/form/sections/`). Mendukung draft/published dan upload beberapa gambar.

## Yang Belum Aktif (placeholder / mock)

Agar dokumentasi jujur:

- **Dashboard = "On Development".** Panel dashboard menampilkan layar "sedang dalam pengembangan" (`on-development-panel.tsx`) dan tidak memanggil endpoint analytics/dashboard.
- **Route `/orders`, `/settings`, `/trips/[id]`** hanya me-render layar daftar trip (`CurrentTripsScreen`), belum punya implementasi sendiri.
- **Data mock tak terpakai** (dahulu `src/lib/data.ts`) telah dihapus.
- **Tidak ada UI pembayaran aktif** dan **tidak ada pengambilan bookings** dari backend. Ini selaras dengan backend yang sengaja menonaktifkan `create_payment` di workflow chat.
- Endpoint `bookings`, `logs`, dan `analytics/dashboard` **belum dipanggil** dari backoffice.

## Konfigurasi & Proxy API

- Permintaan client memakai path relatif `/api/...`. `next.config.mjs` mem-proxy `/api/:path*` ke `http://localhost:8080/api/:path*`.
- `NEXT_PUBLIC_API_BASE_URL` (default `http://localhost:8080`) dipakai untuk membangun URL aset dan pemanggilan sisi server.

Pastikan backend berjalan di `http://localhost:8080`, dan tersedia akun dengan peran `operator` atau `admin`.

## Menjalankan

```bash
npm install
npm run dev
```

Buka `http://localhost:3000` lalu login di `/login`.

Skrip lain:

```bash
npm run build   # build produksi
npm run start   # jalankan hasil build
npm run lint    # ESLint
```
