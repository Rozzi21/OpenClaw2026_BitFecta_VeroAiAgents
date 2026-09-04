# Knowledge Base AI — VeroAiTravelAgents

Folder ini adalah **knowledge base permanen** untuk agent AI yang akan bekerja pada project VeroAiTravelAgents. Tujuannya: agent baru bisa memahami sistem dengan cepat tanpa harus membaca seluruh repository dari nol.

Baca dokumen sesuai urutan di bawah sebelum mulai mengerjakan tugas apa pun.

## Apa Itu Project Ini (1 Paragraf)

VeroAiTravelAgents adalah platform travel berbasis AI ("Vero Travel" / "TravelOS"), berupa monorepo dengan tiga aplikasi independen: backend Go (Gin + GORM + PostgreSQL) sebagai orkestrator AI/MCP + REST API + SSE realtime, `frontend/` (Next.js) untuk chat AI pelanggan, dan `backoffice-frontend/` (Next.js) untuk operator/admin mengelola paket trip. Inti AI ada di `backend/internal/services/ai_service.go` (service layer dipecah per-domain dalam package `services`). Tool MCP masih mock, integrasi LLM nyata sudah ada dengan fallback lokal, dan tool `create_payment` sengaja dinonaktifkan di workflow chat.

## Urutan Baca untuk Agent Baru

Baca berurutan. Setiap dokumen dibangun di atas konteks sebelumnya.

| Urutan | File | Baca untuk memahami | Kapan wajib |
|---|---|---|---|
| 1 | [project-map.md](project-map.md) | Peta keseluruhan: struktur folder, entry point, file yang sering disentuh, navigasi cepat | Selalu — baca pertama |
| 2 | [architecture.md](architecture.md) | Arsitektur sistem, alur data utama, design pattern, keputusan arsitektur, entry point | Selalu |
| 3 | [modules.md](modules.md) | Daftar module, tanggung jawab, file penting, dependency antar module | Selalu |
| 4 | [database.md](database.md) | Skema, entity, relasi, ORM (GORM), migrasi, repository penting | Sebelum kerja data/model |
| 5 | [api.md](api.md) | Semua endpoint, request/response, middleware, auth & authorization flow | Sebelum kerja API/integrasi |
| 6 | [backend.md](backend.md) | Service layer, business logic, realtime/SSE, integrasi eksternal (AI, DOKU, N8N) | Sebelum kerja backend |
| 7 | [frontend.md](frontend.md) | Kedua app Next.js: halaman, routing, state, shared components, auth client | Sebelum kerja frontend |
| 8 | [coding-rules.md](coding-rules.md) | Konvensi wajib, pola yang harus diikuti, pola yang dilarang | Sebelum menulis kode |
| 9 | [known-issues.md](known-issues.md) | Technical debt, keterbatasan, bug diketahui, area hati-hati | Sebelum mengubah area sensitif |
| 10 | [deployment.md](deployment.md) | Env vars, proses deploy, infrastruktur, service pihak ketiga, build/release | Sebelum kerja deploy/config |

## Jalur Cepat Berdasarkan Tugas

- **Fix bug backend / tambah endpoint**: 1 → 2 → 3 → 5 → 6 → 8 → 9
- **Kerja database / model**: 1 → 2 → 4 → 8 → 9
- **Kerja chat AI / MCP**: 1 → 2 → 6 (bagian AI/MCP) → 9
- **Kerja frontend customer / backoffice**: 1 → 2 → 7 → 8 → 9
- **Auth / JWT / keamanan**: 1 → 5 (auth flow) → 6 → 9
- **Deploy / konfigurasi env**: 1 → 10 → 9

## Aturan Penting Sebelum Mulai

1. **Selalu baca file aktual sebelum mengklaim perilakunya.** Knowledge base ini akurat per tanggal pembuatan, tapi kode bisa berubah. Verifikasi pada file sumber yang dirujuk.
2. **`create_payment` MCP sengaja dinonaktifkan.** Jangan mengaktifkannya tanpa instruksi eksplisit. Lihat [known-issues.md](known-issues.md) dan [backend.md](backend.md).
3. **Tool MCP masih mock.** Data destinasi/hotel/budget adalah dummy. Lihat [backend.md](backend.md).
4. **Automated test sudah ada.** Backend: `go test ./...` (`ai`, `auth`, `handlers`, `mcp`, `middlewares`, `routes`, `services`, `utils`) — tanpa DB Postgres, tanpa jaringan, tanpa `OPENAI_API_KEY`. Frontend customer: `npm test` (runner bawaan Node). Tetap verifikasi juga dengan `go build ./...` dan `tsc --noEmit`. Lihat [coding-rules.md](coding-rules.md) §5.
5. **Customer frontend punya auth opsional.** Guest chat + paket publik tetap tanpa login, tapi ada login/register/Google + guest order limit; backoffice punya auth sendiri. Lihat [frontend.md](frontend.md).
6. **Patuhi envelope respons API seragam** `{ success, message, data, error }` dan layered architecture (Handler → Service → Repository). Lihat [coding-rules.md](coding-rules.md).

## Memelihara Knowledge Base Ini

Saat membuat perubahan signifikan (endpoint baru, model baru, perubahan arsitektur, integrasi baru), perbarui dokumen terkait di folder ini agar tetap akurat untuk agent berikutnya. Jika menemukan pola berulang baru, tambahkan ke [coding-rules.md](coding-rules.md).
