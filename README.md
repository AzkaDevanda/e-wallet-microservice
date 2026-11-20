# E-Wallet API (Part 3: RabbitMQ)

Backend e-wallet dengan Go (Gin), PostgreSQL, GORM, JWT, bcrypt, dan RabbitMQ, sesuai `openapi.yaml`.
Fitur: registrasi dan login, cek saldo, top-up, transfer antar user, riwayat transaksi dengan pagination,
dan pembayaran ke merchant yang diproses **asinkron** lewat message queue, serta **kedaluwarsa otomatis setelah 10 menit**.

Riwayat:
- **Part 1** adalah monolith (lihat commit `73cfe2c`).
- **Part 2** memisahkan fitur pembayaran menjadi **payment-service** dengan database sendiri, dan semua request masuk lewat **api-gateway**.
- **Part 3** (sekarang) mengganti komunikasi pembayaran menjadi asinkron. **core-service** menerima `POST /transactions/pay`
  lalu mengirim pesan ke **RabbitMQ**. **payment-service** menjadi *consumer* yang memproses pesan dan mengirim hasilnya
  kembali lewat `status.queue`.

## Arsitektur

```
                       client (Postman, aplikasi)
                                  │
                                  ▼  http://localhost:8080/api/v1
                        ┌───────────────────┐
                        │    api-gateway    │  semua /api/v1/... → core
                        │       :8080       │  /internal/... → 404
                        └─────────┬─────────┘
                                  ▼
                        ┌───────────────────┐  publish (confirm)  ┌──────────────────────────────┐
                        │   core-service    │ ──────────────────► │ RabbitMQ  payment.exchange   │
                        │      :8081        │                     │ (direct)                     │
                        │ + status consumer │ ◄────────────────── │  payment.request → payment.queue
                        └───┬───────────▲───┘   status.queue      │  payment.status  → status.queue
                            │           │                         └──────────────┬───────────────┘
                     ┌──────▼─────┐     │ HTTP /internal/... (debit)             │ consume (manual ack)
                     │  core_db   │     │ X-Internal-API-Key            ┌────────▼────────┐
                     └────────────┘     └───────────────────────────────│ payment-service │
                                                                        │ consumer :8082  │
                                                                        └────────┬────────┘
                                                                          ┌──────▼──────┐
                                                                          │ payment_db  │
                                                                          └─────────────┘
```

| Service | Isi | Database |
|---|---|---|
| **api-gateway** | meneruskan semua `/api/v1/...` ke core, memblokir `/internal`, membalas `502`/`504` kalau core mati/lambat | - |
| **core-service** | users, login (membuat JWT), wallet, top-up, transfer, history, ledger, **publisher** `payment.queue`, **consumer** `status.queue`, endpoint internal untuk payment | `core_db` |
| **payment-service** | **consumer** `payment.queue`: cek expired, simulasi proses, potong saldo lewat core, simpan hasil, **publish** ke `status.queue`. HTTP hanya `/api/v1/health`. | `payment_db` |
| **RabbitMQ** | message broker. Dashboard: http://localhost:15672 (`guest` / `guest`) | volume `rabbitmq-data` |

### Topologi RabbitMQ

Dideklarasikan oleh **kedua** service saat start. Deklarasi bersifat idempoten, jadi urutan start tidak masalah.

| Nama | Jenis | Binding key | Producer → Consumer |
|---|---|---|---|
| `payment.exchange` | direct, durable | - | - |
| `payment.queue` | durable | `payment.request` | core → payment-service |
| `status.queue` | durable | `payment.status` | payment-service → core |

Isi pesan `payment.queue` (JSON, `created_at` dalam RFC3339):

```json
{
  "payment_id": "7b3c55ef-3755-4f0b-92e0-3acd6cf63e0f",
  "user_id": "1f0e9a52-1d0c-4c5e-9d55-0b6a0f1c2d3e",
  "merchant_id": "merchant-123",
  "amount": 50000,
  "created_at": "2026-11-20T07:30:00Z"
}
```

`merchant_id` ditambahkan di luar yang diminta, karena debit di core membutuhkannya.

Isi pesan `status.queue`:

```json
{ "payment_id": "7b3c55ef-...", "status": "completed", "processed_at": "2026-11-20T07:30:02Z" }
{ "payment_id": "...", "status": "failed", "reason": "insufficient balance", "processed_at": "..." }
```

### Alur pembayaran

1. Client → `POST /api/v1/transactions/pay` (lewat gateway) → core.
2. Core memvalidasi JWT, body, dan saldo (saldo kurang → `400`, tidak ada pesan yang dikirim).
3. Core menyimpan `payment_requests` berstatus `pending`, lalu publish ke `payment.exchange` dan menunggu *publisher confirm*.
4. Core membalas **`202`** `{"status":"pending"}`. RabbitMQ tidak bisa dihubungi → `503`, dan payment ditandai `failed`.
5. payment-service mengambil pesan dari `payment.queue`:
   - umur pesan (dari `created_at`) lebih dari 10 menit → tanya core apakah debit sudah ada. Ada → `completed`, tidak ada → `expired`;
   - selain itu → `sleep(PAYMENT_PROCESSING_DELAY)` sebagai simulasi, lalu potong saldo lewat `POST /internal/wallets/{userID}/debit`;
   - hasilnya disimpan di `payment_db`, di-publish ke `status.queue`, **baru** pesan di-`ack`.
6. Core mendengarkan `status.queue` dan meng-update `payment_requests`.
7. Client → `GET /api/v1/transactions/pay/{id}` → `completed` / `failed` / `expired`.

- **JWT**: dibuat dan diverifikasi oleh core (HS256). Gateway tidak memeriksa JWT. payment-service tidak menerima request user, jadi tidak butuh `JWT_SECRET`.
- **Antar service**: payment memanggil `core-service/internal/...` lewat HTTP dengan header `X-Internal-API-Key`.

## Menjalankan

### A. Full stack dengan Docker (paling mudah)

```bash
cp .env.example .env                        # isi JWT_SECRET dan INTERNAL_API_KEY
docker compose --profile app up -d --build  # 2 database + RabbitMQ + core + payment + gateway
```

API: `http://localhost:8080/api/v1`. Dari luar, yang dibuka hanya gateway (8080), kedua database (5433, 5434, untuk DBeaver),
dan RabbitMQ (5672 AMQP, 15672 dashboard). core-service dan payment-service tidak punya port publik, sehingga `/internal/...`
tidak bisa diakses dari luar.

```bash
docker compose --profile app logs -f payment-service core-service   # lihat pesan diproses
docker compose --profile app down                                   # berhenti (data dan pesan tetap ada di volume)
```

### B. Mode development (`go run` per service)

```bash
docker compose up -d                       # 2 database + RabbitMQ
cd core-service    && go run .             # :8081 (butuh core-service/.env)
cd payment-service && go run .             # :8082 (butuh payment-service/.env)
cd api-gateway     && go run .             # :8080 (tanpa .env, default sudah benar)
```

`INTERNAL_API_KEY` di `core-service/.env` dan `payment-service/.env` **harus sama**.

### Postman

Collection: `postman/E-Wallet.postman_collection.json`. Import ke Postman, lalu jalankan dengan **Run collection**
(matikan "Stop run if an error occurs").

- Folder 1–5 dan 7 lewat gateway (`baseUrl` = `http://localhost:8080/api/v1`).
- Folder 5 menguji alur asinkron: `202 pending` → tunggu `paymentWaitMs` → `completed` → saldo terpotong sekali.
- Folder 6 memanggil core (`:8081`) dan payment-service (`:8082`) **langsung**, jadi hanya bisa di mode B. Folder ini
  otomatis di-skip selama variabel `goRunMode` = `false`. Untuk menjalankannya, set `goRunMode` = `true` dan isi `internalApiKey`.

### Konfigurasi

**core-service** (`core-service/.env`)

| Variabel | Default | Keterangan |
|---|---|---|
| `APP_PORT` | `8081` | |
| `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME`, `DB_SSLMODE` | `localhost`, `5433`, `postgres`, -, `core_db`, `disable` | |
| `JWT_SECRET` | (wajib) | penandatangan JWT |
| `TOKEN_TTL` | `24h` | masa berlaku JWT |
| `INTERNAL_API_KEY` | (wajib) | key untuk endpoint `/internal` |
| `RABBITMQ_URL` | `amqp://guest:guest@localhost:5672/` | |

**payment-service** (`payment-service/.env`)

| Variabel | Default | Keterangan |
|---|---|---|
| `APP_PORT` | `8082` | hanya untuk `/api/v1/health` |
| `DB_*` | `localhost`, `5434`, ..., `payment_db` | |
| `INTERNAL_API_KEY` | (wajib) | harus sama dengan core |
| `CORE_SERVICE_URL` | `http://localhost:8081` | |
| `CORE_HTTP_TIMEOUT` | `5s` | batas waktu panggilan ke core |
| `RABBITMQ_URL` | `amqp://guest:guest@localhost:5672/` | |
| `PAYMENT_TTL` | `10m` | pesan yang lebih tua dari ini (dihitung dari `created_at`) dinyatakan `expired` |
| `PAYMENT_PROCESSING_DELAY` | `2s` | lama simulasi pemrosesan pembayaran |
| `PAYMENT_RETRY_DELAY` | `10s` | jeda sebelum pesan dikembalikan ke antrian kalau core tidak bisa dihubungi; wajib > `CORE_HTTP_TIMEOUT` |

**api-gateway** (environment variable, tanpa `.env`)

| Variabel | Default |
|---|---|
| `APP_PORT` | `8080` |
| `CORE_SERVICE_URL` | `http://localhost:8081` |
| `UPSTREAM_TIMEOUT` | `15s` |

## Endpoint

Base URL: `http://localhost:8080/api/v1` (gateway). Semua diteruskan ke core-service.

| Method | Path | Auth | Sukses | Keterangan |
|---|---|---|---|---|
| GET | /health | - | 200 | cek core dan database-nya |
| POST | /users/register | - | 201 | otomatis membuat wallet dengan saldo 0 |
| POST | /users/login | - | 200 | mengembalikan JWT |
| GET | /wallet/balance | Bearer | 200 | |
| POST | /transactions/topup | Bearer | 200 | 1 – 100.000.000 per transaksi |
| POST | /transactions/transfer | Bearer | 202 | |
| GET | /transactions/history | Bearer | 200 | `?page=1&limit=10`, maks. `limit=100` |
| POST | /transactions/pay | Bearer | 202 | divalidasi, dikirim ke `payment.queue`, langsung dibalas `pending` |
| GET | /transactions/pay/{id} | Bearer | 200 | **tambahan di luar spec**: status terkini `pending` / `completed` / `expired` / `failed` (+ `reason`) |

Gateway sendiri: `GET /healthz` → `{"status":"ok"}`.

Endpoint internal core-service (hanya untuk payment-service, header `X-Internal-API-Key`, **tidak** lewat gateway):

| Method | Path | Keterangan |
|---|---|---|
| GET | /internal/wallets/{userID}/balance | saldo user |
| POST | /internal/wallets/{userID}/debit | potong saldo, idempoten berdasarkan `referenceID` (= `payment_id`) |
| GET | /internal/debits/{referenceID} | apakah payment ini sudah memotong saldo? (`404 DEBIT_NOT_FOUND` kalau belum) |

Kode error yang dipakai:

| Kode | Kapan |
|---|---|
| 400 | input tidak valid, saldo tidak cukup, transfer ke diri sendiri, ID payment bukan UUID |
| 401 | token tidak ada / tidak valid / kedaluwarsa, login gagal |
| 404 | penerima transfer atau payment tidak ditemukan (termasuk payment milik user lain), path tidak dikenal gateway |
| 409 | email sudah terdaftar |
| 500 | error tak terduga (detail hanya dicatat di log server) |
| 502 | gateway: core-service mati |
| 503 | core: RabbitMQ tidak bisa dihubungi saat `POST /transactions/pay`; aman untuk dicoba lagi |
| 504 | gateway: core-service tidak menjawab dalam `UPSTREAM_TIMEOUT` |

Status hasil pembayaran (`GET /transactions/pay/{id}`):

| Status | Arti |
|---|---|
| `pending` | pesan sudah di antrian, belum selesai diproses |
| `completed` | saldo sudah terpotong |
| `failed` | gagal diproses (misalnya `insufficient balance`); saldo tidak terpotong |
| `expired` | pesan baru diproses setelah lebih dari 10 menit; saldo tidak terpotong |

## Struktur

```
.
├── docker-compose.yaml       # 2 database + RabbitMQ (default) + 3 service (profile "app")
├── .env.example              # secret untuk compose
├── openapi.yaml
├── postman/
├── api-gateway/              # module api-gateway (hanya stdlib)
│   ├── main.go
│   └── internal/{config,proxy,router}
├── core-service/             # module core-service
│   ├── main.go
│   └── internal/{auth,config,database,dto,handler,messaging,middleware,model,repository,router,service}
└── payment-service/          # module payment-service
    ├── main.go
    └── internal/{client,config,database,handler,messaging,model,repository,router,service}
```

- `internal/messaging` berisi koneksi RabbitMQ, deklarasi topologi, struct pesan, publisher, dan consumer.
  Kodenya sengaja diduplikasi di kedua service (tanpa shared module), supaya tiap service tetap bisa di-build dan di-deploy sendiri.
- Setiap service memakai arsitektur berlapis: `handler/consumer → service → repository`.
- Setiap service punya `Dockerfile` multi-stage (image akhir distroless, user non-root) dan `.dockerignore` yang
  membuang `.env`, sehingga secret tidak ikut masuk ke image.

### Tabel

```
core_db:     users ──1:1── wallets
             users ──1:N── transactions       (ledger; payment dicatat dengan reference_id = ID payment, unik)
             users ──1:N── payment_requests   (status pembayaran yang dilihat user)
payment_db:  payments                         (hasil pemrosesan; user_id tanpa foreign key: users ada di database lain)
```

## Keputusan desain Part 3

1. **Komunikasi asinkron lewat RabbitMQ.** Core tidak lagi menunggu pembayaran selesai. Core memvalidasi (JWT, body,
   saldo awal), menyimpan `payment_requests` berstatus `pending`, mengirim pesan ke `payment.exchange` (direct) dengan
   routing key `payment.request`, lalu membalas `202`. Kalau payment-service mati, pesan menunggu di `payment.queue` dan
   diproses saat service menyala lagi.

2. **Pesan tidak hilang.**
   - Exchange dan queue `durable`, pesan `persistent`, dan RabbitMQ memakai volume. Pesan tetap ada setelah RabbitMQ restart.
   - Publisher memakai *publisher confirm*: core baru membalas `202` setelah RabbitMQ mengonfirmasi pesan sudah diterima.
   - Consumer memakai *manual ack*. Pesan baru di-`ack` setelah hasilnya tersimpan **dan** status sudah di-publish.
     Kalau service mati di tengah proses, pesan dikirim ulang.
   - Channel publish dan consume dipisah, dan channel publish dilindungi mutex (channel AMQP tidak aman dipakai banyak goroutine).

3. **Idempoten di setiap langkah**, karena RabbitMQ menjamin *at-least-once* (pesan bisa datang dua kali):
   - payment-service: payment yang sudah final tidak diproses ulang, cukup kirim ulang statusnya;
   - debit di core: `referenceID = payment_id` (dari Part 2), saldo tidak terpotong dua kali;
   - status di core: hanya baris `pending`/`failed` yang berubah. Status `completed`/`expired` tidak bisa ditimpa.

4. **Expired 10 menit dihitung dari `created_at` di pesan** (waktu user membayar, diisi core dalam UTC), bukan dari
   waktu pesan diproses. Sebelum menyatakan `expired`, payment-service bertanya ke core (`GET /internal/debits/{id}`)
   apakah debit sudah terjadi, supaya user tidak melihat `expired` padahal saldonya terpotong (misalnya pesan yang
   dikirim ulang setelah debit sukses tapi sebelum ack).

5. **Retry dan pesan rusak.**
   - Core tidak bisa dihubungi → tunggu `PAYMENT_RETRY_DELAY`, lalu pesan dikembalikan ke antrian (`Nack requeue`).
     Retry otomatis berhenti setelah 10 menit, karena pesan menjadi `expired`. Jedanya wajib lebih besar dari
     `CORE_HTTP_TIMEOUT`, supaya debit yang masih berjalan di core sempat selesai sebelum dicoba lagi.
   - JSON rusak atau data tidak valid → `Nack` tanpa requeue (dibuang dan dicatat di log), supaya tidak berputar terus.
   - `Qos(1)`: satu instance hanya memegang satu pesan yang belum di-ack.

6. **Hasil dikirim balik lewat `status.queue`** (extra credit). Core mendengarkan dan meng-update status, sehingga
   core tidak perlu membaca database payment-service (tetap *database per service*), dan user cukup bertanya ke core.

7. **Payment-service tidak punya endpoint publik lagi.** Gateway hanya mengenal core. Flow sesi + confirm dan
   reconciler dari Part 2 dihapus, dan payment-service tidak butuh `JWT_SECRET`.

## Keputusan desain Part 2

Ditulis saat Part 2. Poin 1–3 dan 6 tetap berlaku. Poin 4–5 **digantikan di Part 3** (lihat keputusan Part 3 no. 4–5).

1. **Database per service.** `payments` hanya ada di `payment_db`, sedangkan saldo dan ledger hanya ada di `core_db`.
   payment-service tidak pernah menyentuh tabel wallet; ia hanya meminta core lewat HTTP.

2. **Endpoint internal dilindungi API key, dan tidak pernah dibuka ke luar.**
   `/internal/...` memeriksa `X-Internal-API-Key` dengan `subtle.ConstantTimeCompare`, dan service menolak start
   kalau key kosong. Selain itu ada tiga lapis lain: gateway tidak me-route `/internal` (404), gateway membuang
   header `X-Internal-API-Key` dari client, dan di Docker core-service tidak punya port publik.

3. **Debit idempoten.** Payment mengirim ID payment sebagai `referenceID`. Core mengunci wallet (`FOR UPDATE`),
   mengecek ledger untuk `referenceID` itu, lalu memilih: *replay* (tidak memotong lagi) atau potong. Ada juga unique
   index `(reference_id, type)` sebagai jaring pengaman. Ini yang membuat pemrosesan ulang pesan di Part 3 aman.

4. *(Digantikan)* Hasil "tidak diketahui" (core mati/timeout) dibedakan dari "gagal": payment dibiarkan `pending`
   dan user mendapat `503`. Di Part 3, kasus ini ditangani dengan requeue pesan.

5. *(Digantikan)* Reconciler berkala memeriksa payment `pending` yang lewat batas waktu dan bertanya ke core apakah
   debit sudah ada sebelum menandai `expired`. Di Part 3, pengecekan yang sama dilakukan saat pesan diproses.

6. **Gateway sederhana dan tanpa secret.** Gateway hanya meneruskan request (`httputil.ReverseProxy`). Ia tidak
   memeriksa JWT dan tidak tahu `INTERNAL_API_KEY`, sehingga kalau gateway bocor, tidak ada secret yang ikut bocor.
   Path yang tidak bersih (`..`, `//`, termasuk bentuk `%2e%2e`) ditolak sebelum routing.

## Keputusan desain Part 1

Poin-poin di bawah ditulis saat monolith dan tetap berlaku di core-service, kecuali poin 5–6 yang digantikan di Part 3.

1. **Uang disimpan sebagai `int64` (rupiah), bukan float.**
   `float` adalah bilangan biner, sehingga banyak angka desimal tidak bisa disimpan dengan tepat
   (contoh: `0.1 + 0.2 = 0.30000000000000004`). Untuk uang, selisih kecil seperti ini menumpuk seiring banyaknya
   transaksi. Karena itu uang disimpan sebagai bilangan bulat dalam satuan terkecil (rupiah). Bonusnya, input desimal
   seperti `1000.5` dan angka berbentuk string otomatis ditolak dengan `400`. Database juga punya check constraint
   `balance >= 0` sebagai jaring pengaman terakhir.

2. **Ledger per user.**
   Setiap perubahan saldo dicatat di tabel `transactions` sebagai baris milik user yang saldonya berubah.
   Transfer dicatat dua baris, `transfer_out` untuk pengirim dan `transfer_in` untuk penerima, dihubungkan
   `reference_id` yang sama. Hasilnya:
   - history cukup `WHERE user_id = ?`, tanpa JOIN;
   - saldo setiap wallet bisa dibuktikan: saldo = total `topup` + `transfer_in` − `transfer_out` − `payment`.
   History memakai composite index `(user_id, created_at)`. Pada 200 ribu baris, query history butuh 0,1 ms
   dengan index dan 6,6 ms tanpa index.

3. **Saldo diubah secara atomik, dan transfer memakai `SELECT ... FOR UPDATE` dengan urutan lock yang konsisten.**
   - *Lost update* dicegah dengan `UPDATE ... SET balance = balance + ?` (database yang menghitung, bukan Go).
   - *Double spending* dicegah dengan mengunci baris wallet (`FOR UPDATE`) sampai transaksi selesai.
   - *Deadlock* dicegah dengan selalu mengunci kedua wallet dalam urutan yang sama (UUID terkecil dulu).

   Hasil uji: 20 top-up bersamaan → saldo tepat; 20 transfer @10.000 bersamaan dari saldo 100.000 → tepat 10 berhasil;
   60 transfer dua arah bersamaan → semua berhasil (tanpa urutan lock, 58 dari 60 gagal karena deadlock).

4. **Transfer membalas `202` dengan `status: "completed"`.**
   Spec mendefinisikan respons `202`, tapi transfer diproses sinkron. Kode `202` dipertahankan supaya sesuai kontrak
   spec, sedangkan `status` diisi dengan kondisi sebenarnya. (Di Part 3, `POST /transactions/pay` benar-benar asinkron,
   jadi `202 pending` di sana berarti apa adanya.)

5. *(Digantikan di Part 3)* Payment memakai sesi + endpoint `POST /transactions/pay/{id}/confirm`.

6. *(Digantikan di Part 3)* Expiry dijaga oleh cek saat confirm + background worker.

7. **Tabel `payments` tanpa foreign key ke `users`.**
   `payments` ada di database milik payment-service, sedangkan `users` di core-service. Foreign key tidak bisa
   melintasi dua database, jadi payment hanya menyimpan `user_id` sebagai nilai biasa.

8. **Payment mengakses saldo hanya lewat interface `WalletGateway`.**
   Implementasinya HTTP client ke core-service (`CoreWalletClient`). Di Part 3 interface yang sama dipakai ulang oleh
   consumer, tanpa perubahan.

9. **`user_id` selalu diambil dari JWT, tidak pernah dari request.**
   Mencegah *IDOR (Insecure Direct Object Reference)*. Untuk alasan yang sama, payment milik user lain dibalas `404`,
   bukan `403`, supaya keberadaan payment tersebut tidak bocor. `user_id` di pesan RabbitMQ juga diisi core dari JWT.

Keputusan pendukung lainnya:
- Password di-hash dengan bcrypt. Pesan login gagal selalu sama (`invalid email or password`).
- Email dinormalisasi (huruf kecil, tanpa spasi), dan duplikasi dicegah oleh unique index di database.
- User dan wallet dibuat dalam satu DB transaction (`Transactor` lewat `context.Context`).
- JWT hanya menerima algoritma HS256 (token `alg: none` ditolak) dan wajib punya waktu kedaluwarsa.
- Semua timestamp di respons API dan di pesan RabbitMQ dikirim dalam UTC.

## Pengujian

Unit test:

```bash
cd payment-service && go test ./...   # 8 test: completed, failed, expired (+ cek debit di core), duplikat, pesan tidak valid, core mati
cd api-gateway     && go test ./...   # 4 test: routing ke core, /internal diblokir, path traversal, header key dibuang, 502/504
```

End-to-end (Docker, PostgreSQL dan RabbitMQ sungguhan, lewat Postman collection dan skrip curl):

| Area | Skenario | Hasil |
|---|---|---|
| Auth | register, email duplikat, login gagal | `201` / `409` / `401` dengan pesan identik |
| JWT | tanpa token, token palsu, `alg: none`, token kedaluwarsa | semua `401` |
| Top-up / transfer | request bersamaan | saldo = jumlah ledger, 0 deadlock |
| Pay | saldo cukup | `202 pending` → beberapa detik kemudian `completed`, saldo terpotong sekali |
| Pay | saldo kurang saat request | `400`, tidak ada pesan dikirim |
| Pay | dua payment, saldo hanya cukup untuk satu | pertama `completed`, kedua `failed` (`insufficient balance`) |
| Pay | RabbitMQ mati | `503`, payment `failed` |
| Pay | payment-service mati, lalu nyala lagi | pesan menunggu di `payment.queue`, lalu diproses |
| Expiry | pesan berumur > 10 menit | `expired`, saldo utuh |
| Redelivery | pesan yang sama dikirim dua kali | diproses sekali, saldo terpotong sekali |
| Status | core mati saat payment-service mengirim status | status menunggu di `status.queue`, diterapkan saat core nyala |
| Status | status palsu untuk payment `completed`, status tidak dikenal, JSON rusak | diabaikan / dibuang, data tidak rusak |
| Gateway | `/internal/...`, `/api/v1/../internal/...`, path tidak dikenal | `404` |
| Gateway | core-service mati | `502` |

## Keterbatasan

- *Dual write* di core: `INSERT payment_requests` dan publish ke RabbitMQ bukan satu transaksi. Kalau publish gagal,
  payment ditandai `failed` dan user mendapat `503`. Kalau publish timeout tapi pesan ternyata sampai, status `failed`
  akan ditimpa hasil dari payment-service. Solusi lengkapnya adalah pola *transactional outbox*.
- Belum ada *dead-letter queue*: pesan rusak langsung dibuang (hanya dicatat di log).
- Tidak ada reconnect otomatis: kalau koneksi RabbitMQ putus, service berhenti lalu dinyalakan ulang oleh Docker
  (`restart: unless-stopped`). Di mode `go run`, service harus dijalankan ulang manual.
- Pesan diproses satu per satu per instance (`Qos 1`). Untuk throughput lebih besar, jalankan beberapa instance
  payment-service (`docker compose --profile app up -d --scale payment-service=3`).
- Cek saldo di core saat `POST /pay` hanya validasi awal; saldo bisa berubah sebelum pesan diproses, sehingga hasil
  akhirnya tetap ditentukan saat debit (`failed` kalau saldo tidak cukup).
- `merchantID` tidak divalidasi, karena spec tidak menyediakan data merchant.
- History memakai LIMIT/OFFSET (cocok dengan spec `page`/`totalPages`, tapi makin lambat untuk halaman yang sangat jauh).
- Belum ada rate limiting untuk login, dan JWT tidak bisa dicabut sebelum kedaluwarsa.
- Skema dibuat dengan GORM `AutoMigrate`. Untuk production sebaiknya memakai migration file berversi.
- Kredensial RabbitMQ memakai `guest`/`guest` bawaan image. Untuk production, buat user sendiri dan simpan di secret.
