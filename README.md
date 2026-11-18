# E-Wallet API (Part 2: Microservice)

Backend e-wallet dengan Go (Gin), PostgreSQL, GORM, JWT, dan bcrypt, sesuai `openapi.yaml`.
Fitur: registrasi dan login, cek saldo, top-up, transfer antar user, riwayat transaksi dengan pagination,
dan pembayaran ke merchant yang **kedaluwarsa otomatis dalam 10 menit**.

Part 1 adalah monolith (lihat commit `73cfe2c`). Di Part 2, fitur pembayaran dipisah menjadi **payment-service**
dengan database sendiri, dan semua request masuk lewat satu pintu, yaitu **api-gateway**.

## Arsitektur

```
                         client (Postman, aplikasi)
                                    │
                                    ▼  http://localhost:8080/api/v1
                          ┌───────────────────┐
                          │    api-gateway    │  reverse proxy (stdlib), tanpa secret
                          │       :8080       │  /internal/... → 404
                          └───┬───────────┬───┘
      /api/v1/transactions/pay│           │ /api/v1/... lainnya
      /api/v1/transactions/pay/...        │
                              ▼           ▼
                 ┌─────────────────┐   ┌─────────────────┐
                 │ payment-service │──►│  core-service   │
                 │      :8082      │   │      :8081      │
                 │ + reconciler    │   │                 │
                 └────────┬────────┘   └────────┬────────┘
                          │  HTTP /internal/...  │
                          │  X-Internal-API-Key  │
                   ┌──────▼──────┐        ┌──────▼──────┐
                   │ payment_db  │        │  core_db    │
                   └─────────────┘        └─────────────┘
```

| Service | Isi | Database |
|---|---|---|
| **api-gateway** | meneruskan request ke service yang tepat, memblokir `/internal`, membalas `502`/`504` kalau service mati/lambat | - |
| **core-service** | users, login (membuat JWT), wallet, top-up, transfer, history, ledger, endpoint internal untuk payment | `core_db` |
| **payment-service** | sesi pembayaran, confirm, rekonsiliasi payment kedaluwarsa | `payment_db` |

- **JWT**: dibuat oleh core, diverifikasi sendiri oleh core dan payment (HS256, secret yang sama). Gateway tidak memeriksa JWT.
- **Antar service**: payment memanggil `core-service/internal/...` lewat HTTP dengan header `X-Internal-API-Key`.

## Menjalankan

### A. Full stack dengan Docker (paling mudah)

```bash
cp .env.example .env                        # isi JWT_SECRET dan INTERNAL_API_KEY
docker compose --profile app up -d --build  # 2 database + core + payment + gateway
```

API: `http://localhost:8080/api/v1`. Dari luar, hanya gateway (8080) dan kedua database (5433, 5434, untuk DBeaver)
yang dibuka. core-service dan payment-service tidak punya port publik, sehingga `/internal/...` tidak bisa diakses dari luar.

```bash
docker compose --profile app logs -f payment-service   # lihat log reconciler
docker compose --profile app down                      # berhenti (data tetap ada di volume)
```

### B. Mode development (`go run` per service)

```bash
docker compose up -d                       # hanya 2 database
cd core-service    && go run .             # :8081 (butuh core-service/.env)
cd payment-service && go run .             # :8082 (butuh payment-service/.env)
cd api-gateway     && go run .             # :8080 (tanpa .env, default sudah benar)
```

`JWT_SECRET` dan `INTERNAL_API_KEY` di `core-service/.env` dan `payment-service/.env` **harus sama**.

Postman collection: `postman/E-Wallet.postman_collection.json`. Import ke Postman, lalu jalankan dengan **Run collection**.
Folder 1–5 dan 7 lewat gateway. Folder 6 memanggil endpoint internal core **langsung** di `:8081`, jadi hanya bisa
di mode B (isi variabel `internalApiKey` dulu).

### Konfigurasi

**core-service** (`core-service/.env`)

| Variabel | Default | Keterangan |
|---|---|---|
| `APP_PORT` | `8081` | |
| `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME`, `DB_SSLMODE` | `localhost`, `5433`, `postgres`, -, `core_db`, `disable` | |
| `JWT_SECRET` | (wajib) | penandatangan JWT |
| `TOKEN_TTL` | `24h` | masa berlaku JWT |
| `INTERNAL_API_KEY` | (wajib) | key untuk endpoint `/internal` |

**payment-service** (`payment-service/.env`)

| Variabel | Default | Keterangan |
|---|---|---|
| `APP_PORT` | `8082` | |
| `DB_*` | `localhost`, `5434`, ..., `payment_db` | |
| `JWT_SECRET`, `INTERNAL_API_KEY` | (wajib) | harus sama dengan core |
| `CORE_SERVICE_URL` | `http://localhost:8081` | |
| `CORE_HTTP_TIMEOUT` | `5s` | batas waktu panggilan ke core |
| `PAYMENT_TTL` | `10m` | batas waktu sesi pembayaran |
| `PAYMENT_EXPIRY_INTERVAL` | `30s` | seberapa sering reconciler berjalan |
| `PAYMENT_RECONCILE_GRACE` | `1m` | jeda setelah `expiresAt` sebelum payment boleh dinyatakan `expired`; wajib > `CORE_HTTP_TIMEOUT` |

**api-gateway** (environment variable, tanpa `.env`)

| Variabel | Default |
|---|---|
| `APP_PORT` | `8080` |
| `CORE_SERVICE_URL` | `http://localhost:8081` |
| `PAYMENT_SERVICE_URL` | `http://localhost:8082` |
| `UPSTREAM_TIMEOUT` | `15s` |

## Endpoint

Base URL: `http://localhost:8080/api/v1` (gateway)

| Method | Path | Diteruskan ke | Auth | Sukses | Keterangan |
|---|---|---|---|---|---|
| GET | /health | core | - | 200 | cek core dan database-nya |
| POST | /users/register | core | - | 201 | otomatis membuat wallet dengan saldo 0 |
| POST | /users/login | core | - | 200 | mengembalikan JWT |
| GET | /wallet/balance | core | Bearer | 200 | |
| POST | /transactions/topup | core | Bearer | 200 | 1 – 100.000.000 per transaksi |
| POST | /transactions/transfer | core | Bearer | 202 | |
| GET | /transactions/history | core | Bearer | 200 | `?page=1&limit=10`, maks. `limit=100` |
| POST | /transactions/pay | payment | Bearer | 200 | membuat sesi pembayaran `pending`, saldo **belum** dipotong |
| POST | /transactions/pay/{id}/confirm | payment | Bearer | 200 | **tambahan di luar spec**: memotong saldo kalau sesi masih berlaku |

Gateway sendiri: `GET /healthz` → `{"status":"ok"}`.

Endpoint internal core-service (hanya untuk payment-service, header `X-Internal-API-Key`, **tidak** lewat gateway):

| Method | Path | Keterangan |
|---|---|---|
| GET | /internal/wallets/{userID}/balance | saldo user |
| POST | /internal/wallets/{userID}/debit | potong saldo, idempoten berdasarkan `referenceID` (= ID payment) |
| GET | /internal/debits/{referenceID} | apakah payment ini sudah memotong saldo? (`404 DEBIT_NOT_FOUND` kalau belum) |

Kode error yang dipakai:

| Kode | Kapan |
|---|---|
| 400 | input tidak valid, saldo tidak cukup, transfer ke diri sendiri |
| 401 | token tidak ada / tidak valid / kedaluwarsa, login gagal |
| 404 | penerima transfer atau payment tidak ditemukan (termasuk payment milik user lain), path tidak dikenal gateway |
| 409 | email sudah terdaftar, payment sudah `completed` atau sudah `expired` |
| 500 | error tak terduga (detail hanya dicatat di log server) |
| 502 | gateway: service tujuan mati |
| 503 | payment-service: core-service tidak bisa dihubungi; aman untuk dicoba lagi |
| 504 | gateway: service tujuan tidak menjawab dalam `UPSTREAM_TIMEOUT` |

## Struktur

```
.
├── docker-compose.yaml       # 2 database (default) + 3 service (profile "app")
├── .env.example              # secret untuk compose
├── openapi.yaml
├── postman/
├── api-gateway/              # module api-gateway (hanya stdlib)
│   ├── main.go
│   └── internal/{config,proxy,router}
├── core-service/             # module core-service
│   ├── main.go
│   └── internal/{auth,config,database,dto,handler,middleware,model,repository,router,service}
└── payment-service/          # module payment-service
    ├── main.go
    └── internal/{auth,client,config,database,dto,handler,middleware,model,repository,router,service,worker}
```

Setiap service memakai arsitektur berlapis yang sama: `middleware → handler → service → repository`.
Setiap service punya `Dockerfile` multi-stage (image akhir distroless, user non-root) dan `.dockerignore` yang
membuang `.env`, sehingga secret tidak ikut masuk ke image.

### Tabel

```
core_db:     users ──1:1── wallets
             users ──1:N── transactions   (ledger; payment dicatat dengan reference_id = ID payment, unik)
payment_db:  payments                     (user_id tanpa foreign key: users ada di database lain)
```

## Keputusan desain Part 2

1. **Database per service.** `payments` hanya ada di `payment_db`, sedangkan saldo dan ledger hanya ada di `core_db`.
   payment-service tidak pernah menyentuh tabel wallet; ia hanya meminta core lewat HTTP. Karena itu tidak ada
   satu DB transaction yang mencakup "potong saldo" dan "tandai payment completed". Poin 3–5 menjelaskan cara
   menjaga konsistensinya.

2. **Endpoint internal dilindungi API key, dan tidak pernah dibuka ke luar.**
   `/internal/...` memeriksa `X-Internal-API-Key` dengan `subtle.ConstantTimeCompare`, dan service menolak start
   kalau key kosong. Selain itu ada tiga lapis lain: gateway tidak me-route `/internal` (404), gateway membuang
   header `X-Internal-API-Key` dari client, dan di Docker core-service tidak punya port publik.

3. **Debit idempoten.** Payment mengirim ID payment sebagai `referenceID`. Core mengunci wallet (`FOR UPDATE`),
   mengecek ledger untuk `referenceID` itu, lalu memilih: *replay* (tidak memotong lagi) atau potong. Ada juga unique
   index `(reference_id, type)` sebagai jaring pengaman. Akibatnya, confirm boleh di-retry kapan saja tanpa risiko
   saldo terpotong dua kali.

4. **Hasil "tidak diketahui" dibedakan dari "gagal".** Kalau core mati, timeout, atau membalas 5xx, payment-service
   tidak tahu apakah saldo sudah terpotong. Payment dibiarkan `pending` dan user mendapat `503` ("coba lagi"),
   bukan `400`/`500`.

5. **Rekonsiliasi (Saga sederhana) untuk kasus "debit sukses, payment tidak tahu, user tidak retry".**
   Dulu worker menjalankan `UPDATE ... SET status='expired'` secara massal, sehingga payment yang saldonya **sudah**
   terpotong bisa ikut tercatat `expired`. Sekarang worker (reconciler) memeriksa payment satu per satu:
   - mengambil payment `pending` yang sudah lewat `expiresAt + PAYMENT_RECONCILE_GRACE`;
   - mengunci barisnya dengan `FOR UPDATE SKIP LOCKED` (melewati payment yang sedang di-confirm);
   - bertanya ke core `GET /internal/debits/{paymentID}`;
     - debit ada → `completed` (dengan `paid_at` = waktu debit di core);
     - debit pasti tidak ada → `expired`;
     - core tidak bisa dihubungi → tetap `pending`, lalu dicoba lagi di putaran berikutnya.

   Confirm yang datang setelah lewat batas waktu juga bertanya ke core dulu: kalau ternyata sudah dibayar, jawabannya
   `200 completed`, bukan `409 expired`. Grace period ada karena debit yang jawabannya terlambat (timeout di sisi
   payment) bisa saja masih diproses core; selama grace, jawaban "tidak ada debit" belum dipercaya.

6. **Gateway sederhana dan tanpa secret.** Gateway hanya meneruskan request (`httputil.ReverseProxy`). Ia tidak
   memeriksa JWT dan tidak tahu `INTERNAL_API_KEY`, sehingga kalau gateway bocor, tidak ada secret yang ikut bocor.
   Path yang tidak bersih (`..`, `//`, termasuk bentuk `%2e%2e`) ditolak sebelum routing.

## Keputusan desain Part 1

Poin-poin di bawah ditulis saat monolith dan tetap berlaku di core-service / payment-service.

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
     walaupun worker terlambat (sampai 30 detik) atau mati.
   - *Worker* setiap 30 detik memastikan payment yang **tidak pernah** di-confirm tetap tercatat `expired`.
     Di Part 1 ini berupa satu `UPDATE` massal. Di Part 2 diganti menjadi reconciler yang bertanya ke core dulu
     (lihat keputusan Part 2 no. 5). Query-nya tetap memakai index `(status, expires_at)`.

7. **Tabel `payments` tanpa foreign key ke `users`.**
   Di Part 2, `payments` pindah ke database milik payment-service (*Database per Service*), sedangkan `users` tetap di
   core-service. Foreign key tidak bisa melintasi dua database, jadi sejak awal payment hanya menyimpan `user_id`
   sebagai nilai biasa. Validitasnya dijaga aplikasi: `user_id` selalu diambil dari JWT (poin 9).

8. **Payment mengakses saldo hanya lewat interface `WalletGateway`.**
   `paymentService` tidak pernah memakai repository wallet secara langsung, melainkan interface
   `WalletGateway` (`GetBalance`, `DebitForPayment`). Di Part 1, implementasinya langsung ke database
   (`localWalletGateway`). Di Part 2, implementasinya diganti menjadi HTTP client ke core-service
   (`CoreWalletClient`), dan logika create/confirm tidak berubah. ID payment ikut dikirim saat debit, untuk dipakai sebagai *idempotency key*
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

Unit test:

```bash
cd payment-service && go test ./...   # 10 test: keputusan rekonsiliasi & confirm setelah expired (fake core + fake repo)
cd api-gateway     && go test ./...   # 4 test: routing, /internal diblokir, path traversal, header key dibuang, 502/504
```

End-to-end ke PostgreSQL sungguhan (Postman collection dan skrip curl):

| Area | Skenario | Hasil |
|---|---|---|
| Auth | register, email duplikat (beda kapitalisasi), login gagal | `201` / `409` / `401` dengan pesan identik |
| JWT | tanpa token, token palsu, `alg: none`, token kedaluwarsa, token dari secret lain | semua `401` |
| Top-up | 20 request bersamaan | saldo = jumlah ledger |
| Transfer | 20 transfer bersamaan melebihi saldo | tepat 10 berhasil, 10 ditolak `400` |
| Transfer | 60 transfer dua arah bersamaan | 0 deadlock |
| Payment | 10 confirm bersamaan | tepat 1 berhasil, saldo terpotong sekali |
| Core mati | create / confirm | `503`, payment tetap `pending`, retry berhasil |
| Timeout | core memotong saldo tapi jawabannya terlambat | `503`; retry → `replayed`, tidak dipotong lagi |
| Kasus #5 | timeout, user **tidak** retry | reconciler menandai `completed`, saldo terpotong tepat sekali |
| Expiry | tidak di-confirm / confirm setelah lewat batas | `expired` setelah grace, saldo utuh |
| Gateway | `/internal/...`, `/api/v1/../internal/...`, path tidak dikenal | `404` |
| Gateway | payment-service mati / core-service mati | `502` dari gateway / `503` dari payment |

## Keterbatasan

- Grace period adalah kompromi: debit yang tertahan di core **lebih lama** dari `PAYMENT_RECONCILE_GRACE` setelah
  `expiresAt` masih bisa membuat payment `expired` padahal saldo terpotong. Solusi lengkapnya: core menyediakan
  "void referenceID" supaya debit yang terlambat ditolak setelah payment dinyatakan expired.
- Payment yang sudah terlanjur `expired` sebelum Step 14 (oleh worker lama) tidak diperiksa ulang.
- `merchantID` tidak divalidasi, karena spec tidak menyediakan data merchant.
- History memakai LIMIT/OFFSET (cocok dengan spec `page`/`totalPages`, tapi makin lambat untuk halaman yang sangat jauh).
- Pesan error validasi masih berupa teks mentah dari validator (`Key: 'RegisterRequest.Email' Error:...`).
- Belum ada rate limiting untuk login, sehingga password bisa dicoba berkali-kali (brute force).
- JWT tidak bisa dicabut sebelum kedaluwarsa (belum ada logout di sisi server).
- Skema dibuat dengan GORM `AutoMigrate`. Untuk production sebaiknya memakai migration file berversi.
- Satu secret JWT dipakai bersama (HS256). Kalau jumlah service bertambah, lebih aman RS256: hanya core yang memegang
  private key, service lain cukup public key.
