# E-Wallet API (Part 1: Monolith)

Backend e-wallet dengan Go (Gin), PostgreSQL, GORM, JWT, dan bcrypt, sesuai `openapi.yaml`.
Fitur: registrasi dan login, cek saldo, top-up, transfer antar user, riwayat transaksi dengan pagination,
dan pembayaran ke merchant yang **kedaluwarsa otomatis dalam 10 menit**.

## Menjalankan

```bash
cp .env.example .env        # lalu isi JWT_SECRET
docker compose up -d        # PostgreSQL di localhost:5433
go run .                    # API di http://localhost:8080/api/v1
```

Postman collection: `postman/E-Wallet.postman_collection.json`
(import ke Postman, lalu jalankan dengan **Run collection**; token dan ID tersimpan otomatis antar request).

### Konfigurasi (`.env`)

| Variabel | Default | Keterangan |
|---|---|---|
| `APP_PORT` | `8080` | port HTTP |
| `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME`, `DB_SSLMODE` | `localhost`, `5433`, ... | koneksi PostgreSQL |
| `JWT_SECRET` | (wajib) | secret penandatangan JWT; aplikasi berhenti kalau kosong |
| `TOKEN_TTL` | `24h` | masa berlaku JWT |
| `PAYMENT_TTL` | `10m` | batas waktu sesi pembayaran |
| `PAYMENT_EXPIRY_INTERVAL` | `30s` | seberapa sering worker menandai payment kedaluwarsa |

## Endpoint

Base URL: `http://localhost:8080/api/v1`

| Method | Path | Auth | Sukses | Keterangan |
|---|---|---|---|---|
| GET | /health | - | 200 | cek server dan koneksi database |
| POST | /users/register | - | 201 | otomatis membuat wallet dengan saldo 0 |
| POST | /users/login | - | 200 | mengembalikan JWT |
| GET | /wallet/balance | Bearer | 200 | |
| POST | /transactions/topup | Bearer | 200 | 1 – 100.000.000 per transaksi |
| POST | /transactions/transfer | Bearer | 202 | |
| GET | /transactions/history | Bearer | 200 | `?page=1&limit=10`, maks. `limit=100` |
| POST | /transactions/pay | Bearer | 200 | membuat sesi pembayaran `pending`, saldo **belum** dipotong |
| POST | /transactions/pay/{id}/confirm | Bearer | 200 | **tambahan di luar spec**: memotong saldo kalau sesi masih berlaku |

Kode error yang dipakai:

| Kode | Kapan |
|---|---|
| 400 | input tidak valid, saldo tidak cukup, transfer ke diri sendiri |
| 401 | token tidak ada / tidak valid / kedaluwarsa, login gagal |
| 404 | penerima transfer atau payment tidak ditemukan (termasuk payment milik user lain) |
| 409 | email sudah terdaftar, payment sudah `completed` atau sudah `expired` |
| 500 | error tak terduga (detail hanya dicatat di log server) |

## Struktur

Arsitektur berlapis (layered). Setiap request mengalir satu arah:

```
HTTP request
    │
    ▼
middleware   → cek JWT, simpan user ID ke context
    │
    ▼
handler      → bind & validasi JSON, pilih status code
    │
    ▼
service      → aturan bisnis (cek saldo, expiry, transaksi DB)
    │
    ▼
repository   → query ke PostgreSQL (GORM)
```

Satu proses Go menjalankan dua hal sekaligus: **HTTP server** dan **background worker**
(penanda payment kedaluwarsa). Keduanya berhenti dengan rapi saat menerima Ctrl+C / SIGTERM (graceful shutdown).

```
.
├── main.go                  # wiring dependency, start server + worker, graceful shutdown
├── docker-compose.yaml      # PostgreSQL
├── openapi.yaml             # spesifikasi API
├── postman/                 # Postman collection
└── internal/
    ├── config/              # baca .env
    ├── database/            # koneksi GORM + AutoMigrate
    ├── model/               # struct tabel: user, wallet, transaction, payment
    ├── dto/                 # struct request/response sesuai openapi.yaml
    ├── auth/                # generate & parse JWT
    ├── middleware/          # middleware autentikasi
    ├── router/              # daftar route (public & protected)
    ├── handler/             # user, wallet, transaction, payment, health
    ├── service/             # user, wallet, transaction, payment, wallet_gateway
    ├── repository/          # user, wallet, transaction, payment, transactor
    └── worker/              # payment_expiry (ticker tiap 30 detik)
```

Kode payment sengaja dipisah per file (`payment_handler.go`, `payment_service.go`,
`payment_repository.go`, `worker/`) supaya mudah dipindah ke payment-service di Part 2.

### Tabel

```
users ──1:1── wallets          (foreign key, ON DELETE CASCADE)
users ──1:N── transactions     (ledger per user)
users ──1:N── payments         (tanpa foreign key, lihat keputusan desain no. 7)
```

## Keputusan desain

1. **Uang disimpan sebagai `int64` (rupiah), bukan float.**
   `float` adalah bilangan biner, sehingga banyak angka desimal tidak bisa disimpan dengan tepat
   (contoh: `0.1 + 0.2 = 0.30000000000000004`). Untuk uang, selisih kecil seperti ini menumpuk seiring banyaknya
   transaksi, dan saldo tidak lagi cocok dengan catatannya. Karena itu uang disimpan sebagai bilangan bulat dalam
   satuan terkecil (rupiah), seperti praktik umum sistem pembayaran. Bonusnya, input desimal seperti `1000.5` dan
   angka berbentuk string otomatis ditolak dengan `400`. Database juga punya check constraint `balance >= 0`
   sebagai jaring pengaman terakhir.

2. **Ledger per user.**
   Setiap perubahan saldo dicatat di tabel `transactions` sebagai baris milik user yang saldonya berubah.
   Transfer dicatat dua baris, `transfer_out` untuk pengirim dan `transfer_in` untuk penerima, dihubungkan
   `reference_id` yang sama. Hasilnya:
   - history cukup `WHERE user_id = ?`: pengirim melihat uang keluar, penerima melihat uang masuk, tanpa JOIN;
   - saldo setiap wallet bisa dibuktikan: saldo = total `topup` + `transfer_in` − `transfer_out` − `payment`.
   History memakai composite index `(user_id, created_at)`. Pada 200 ribu baris, query history butuh 0,1 ms
   dengan index dan 6,6 ms tanpa index.

3. **Saldo diubah secara atomik, dan transfer memakai `SELECT ... FOR UPDATE` dengan urutan lock yang konsisten.**
   Tiga masalah konkurensi yang dicegah:
   - *Lost update*: dua top-up bersamaan sama-sama membaca saldo lama lalu saling menimpa, sehingga uang hilang.
     Solusinya, database sendiri yang menghitung (`UPDATE ... SET balance = balance + ?`), bukan Go.
   - *Double spending*: dua transfer bersamaan sama-sama lolos cek "saldo cukup", padahal saldo hanya cukup untuk satu.
     Solusinya, baris wallet dikunci dengan `FOR UPDATE` sampai transaksi selesai, jadi cek dan potong saldo tidak bisa diselip.
   - *Deadlock*: transfer A→B dan B→A bersamaan saling menunggu kunci yang dipegang lawannya.
     Solusinya, kedua wallet selalu dikunci dalam urutan yang sama (UUID terkecil dulu), apa pun arah transfernya.

   Hasil uji: 20 top-up bersamaan → saldo tepat; 20 transfer @10.000 bersamaan dari saldo 100.000 → tepat 10 berhasil;
   60 transfer dua arah bersamaan → semua berhasil. Sebagai pembanding, tanpa urutan lock (dengan jeda buatan 20 ms
   antar-lock) 58 dari 60 transfer gagal karena deadlock.

4. **Transfer membalas `202` dengan `status: "completed"`.**
   Spec mendefinisikan respons `202` dengan contoh `status: "pending"`, yang merupakan pola untuk proses asinkron.
   Implementasi ini memproses transfer secara sinkron: saat respons dikirim, transfer sudah selesai dan tersimpan.
   Kode `202` dipertahankan supaya sesuai kontrak spec, sedangkan `status` diisi dengan kondisi sebenarnya.

5. **Payment memakai sesi + endpoint confirm.**
   Requirement meminta payment diperlakukan seperti sesi QRIS di kasir: harus dikonfirmasi dalam 10 menit, dan kalau
   tidak, gagal tanpa memotong saldo. Spec hanya menyediakan `POST /transactions/pay`, jadi ditambahkan
   `POST /transactions/pay/{id}/confirm`.
   - `POST /pay` membuat payment berstatus `pending` dengan `expiresAt = sekarang + 10 menit`, tanpa memotong saldo.
     Respons tetap berisi field spec (`transactionID`, `message`, `newBalance`), ditambah `status` dan `expiresAt`.
   - `confirm` memotong saldo dan mengubah status menjadi `completed`. Baris payment dikunci (`FOR UPDATE`), sehingga
     confirm ganda tidak bisa memotong saldo dua kali (uji: 10 confirm bersamaan → tepat 1 berhasil).
   - Saldo kurang saat confirm → `400`, dan payment tetap `pending`, sehingga user masih bisa top-up lalu mencoba lagi.

6. **Expiry dijaga dua lapis: cek saat confirm + background worker.**
   - *Cek saat confirm* adalah penjaga yang **menjamin** saldo tidak pernah dipotong setelah lewat batas waktu,
     walaupun worker terlambat (sampai 30 detik) atau mati. Status `expired` di-COMMIT ke database sebelum error dikembalikan.
   - *Worker* menjalankan `UPDATE payments SET status = 'expired' WHERE status = 'pending' AND expires_at <= now`
     setiap 30 detik, supaya payment yang **tidak pernah** di-confirm tetap tercatat `expired`. Query ini memakai index
     `(status, expires_at)` dan bersifat idempotent, sehingga aman walaupun berjalan bersamaan dengan confirm atau
     dengan worker di instance lain.

7. **Tabel `payments` tanpa foreign key ke `users`.**
   Di Part 2, `payments` pindah ke database milik payment-service (*Database per Service*), sedangkan `users` tetap di
   core-service. Foreign key tidak bisa melintasi dua database, jadi sejak awal payment hanya menyimpan `user_id`
   sebagai nilai biasa. Validitasnya dijaga aplikasi: `user_id` selalu diambil dari JWT (poin 9).

8. **Payment mengakses saldo hanya lewat interface `WalletGateway`.**
   `paymentService` tidak pernah memakai repository wallet secara langsung, melainkan interface
   `WalletGateway` (`GetBalance`, `DebitForPayment`). Di Part 1, implementasinya langsung ke database
   (`localWalletGateway`). Di Part 2, cukup implementasinya yang diganti menjadi HTTP client ke core-service,
   dan logika payment tidak berubah. ID payment ikut dikirim saat debit, untuk dipakai sebagai *idempotency key*
   di Part 2 (mencegah saldo terpotong dua kali kalau request di-retry).

9. **`user_id` selalu diambil dari JWT, tidak pernah dari request.**
   Kalau `user_id` diambil dari URL atau body, siapa pun yang punya token bisa mengganti ID untuk melihat saldo atau
   memakai uang user lain (*IDOR, Insecure Direct Object Reference*). Token ditandatangani dengan secret server,
   sehingga isinya tidak bisa dipalsukan. Untuk alasan yang sama, payment milik user lain dibalas `404`, bukan `403`,
   supaya keberadaan payment tersebut tidak bocor.

Keputusan pendukung lainnya:
- Password di-hash dengan bcrypt. Pesan login gagal selalu sama (`invalid email or password`) untuk email yang tidak
  terdaftar maupun password yang salah, supaya endpoint login tidak bisa dipakai menebak email terdaftar.
- Email dinormalisasi (huruf kecil, tanpa spasi), dan duplikasi dicegah oleh unique index di database, bukan dengan
  cek "sudah ada?" sebelum INSERT (yang punya celah race condition).
- User dan wallet dibuat dalam satu DB transaction. Transaksi diteruskan antar repository lewat `context.Context`
  (`Transactor`), sehingga service tidak perlu tahu soal GORM.
- JWT hanya menerima algoritma HS256 (token `alg: none` ditolak) dan wajib punya waktu kedaluwarsa.
- Semua timestamp di respons API dikirim dalam UTC.

## Pengujian

Pengujian dilakukan end-to-end ke PostgreSQL lewat Postman collection dan skrip curl. Ringkasan skenario utama:

| Area | Skenario | Hasil |
|---|---|---|
| Auth | register, email duplikat (beda kapitalisasi), login gagal | `201` / `409` / `401` dengan pesan identik |
| JWT | tanpa token, token palsu, `alg: none`, token kedaluwarsa | semua `401` |
| Top-up | 20 request bersamaan | saldo = jumlah ledger |
| Transfer | 20 transfer bersamaan melebihi saldo | tepat 10 berhasil, 10 ditolak `400` |
| Transfer | 60 transfer dua arah bersamaan | 0 deadlock |
| History | halaman terakhir, di luar batas, user baru | sesuai; daftar kosong dikirim `[]`, bukan `null` |
| Payment | 10 confirm bersamaan | tepat 1 berhasil, saldo terpotong sekali |
| Expiry | confirm setelah lewat batas (worker dimatikan) | `409`, status `expired` tersimpan, saldo utuh |
| Expiry | tidak di-confirm | worker menandai `expired`, saldo utuh |

## Keterbatasan

- `merchantID` tidak divalidasi, karena spec tidak menyediakan data merchant.
- History memakai LIMIT/OFFSET (cocok dengan spec `page`/`totalPages`, tapi makin lambat untuk halaman yang sangat jauh).
- Pesan error validasi masih berupa teks mentah dari validator (`Key: 'RegisterRequest.Email' Error:...`).
- Belum ada rate limiting untuk login, sehingga password bisa dicoba berkali-kali (brute force).
- JWT tidak bisa dicabut sebelum kedaluwarsa (belum ada logout di sisi server).
- Skema dibuat dengan GORM `AutoMigrate`, yang hanya bisa menambah dan tidak bisa memperbaiki struktur yang salah.
  Untuk production sebaiknya memakai migration file berversi.
- Belum ada unit test otomatis; pengujian dilakukan end-to-end secara manual dan lewat Postman.
