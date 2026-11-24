# Vendora Backend — E-Commerce & Virtual Shophouse API

Backend REST API untuk platform e-commerce **Vendora** (sewa ruko virtual, katalog produk bervariasi, checkout multi-kurir, rekening bersama **Escrow**, dompet penjual, pelacakan peta, komplain/dispute, tiket bantuan CS, chat real-time polling + media, notifikasi, dan verifikasi OTP Email/WhatsApp).

- **Production API:** `https://vendora-backend-zeta.vercel.app/api`
- **Live Frontend:** `https://www.vendorashop.my.id`
- **Stack:** Go 1.21 · Fiber v2 · MongoDB Atlas (`mongo-driver`) · Bcrypt · Vercel Serverless (`api/index.go`)

---

## Struktur Direktori

```text
backend/
├── api/index.go              # Entrypoint Vercel Serverless (Fiber adaptor + BodyLimit 10 MB + recover)
├── main.go                   # Entrypoint server lokal + AutoCompleteWorker
├── config/db.go              # Koneksi MongoDB, loader .env lokal, dan seed akun demo
├── url/
│   ├── route.go              # Registrasi seluruh rute API, CORS terpusat, dan middleware RBAC
│   └── route_test.go         # Uji whitelist CORS & guard 401 seluruh rute privat
├── model/                    # Struct & skema koleksi MongoDB (User, Shop, Product, Transaction, Dispute, WalletLedger, Message, ChatMedia, Notification, OTPChallenge, Report, SupportTicket)
│   └── product_test.go       # Uji perhitungan harga efektif & promo
├── helper/                   # HTTP response helper, masking PII, generator/hash OTP, pengirim SMTP/Resend/Fonnte/Twilio
│   ├── helper_test.go
│   └── otp_test.go
└── controller/               # Business logic CRUD & pengujian otomatis
    ├── admin.go              # Ekspor CSV transaksi (anti-CSV injection), moderasi produk, laporan pelanggaran
    ├── chat.go               # Chat pembeli ↔ penjual, media foto, inbox & unread counter
    ├── dispute.go            # Komplain pesanan & keputusan refund/release Escrow
    ├── notification.go       # Notifikasi personal & broadcast + sanitasi link anti-XSS
    ├── otp.go                # Login, kirim/verifikasi OTP, dan reset kata sandi
    ├── product.go            # CRUD produk & validasi variasi multi-grup
    ├── session.go            # Token sesi Bearer (SHA-256), middleware AuthOptional/RequireAuth/RequireRole/RequireAdmin
    ├── shop.go               # CRUD ruko, tarif sewa, perpanjangan, persetujuan status & penangguhan
    ├── ticket.go             # Tiket bantuan CS & balasan admin
    ├── transaction.go        # Checkout, pembayaran real-time, transisi status, Escrow, tracking peta, wallet & auto-complete worker
    ├── user.go               # Registrasi, profil, daftar pengguna (PII masked untuk admin), ban user
    ├── scenarios_test.go     # Skenario 01–10: uji unit & keamanan offline (CI-safe)
    └── crud_integration_test.go # Skenario 11–16: uji integrasi CRUD & Escrow end-to-end (DB sementara terisolasi)
```

---

## Ringkasan Endpoint API (CRUD & Layanan Inti)

| Modul | Method & Endpoint | Akses | Fungsi |
|---|---|---|---|
| **Akun & Auth** | `POST /api/register`, `POST /api/login`, `POST /api/logout` | Publik | Registrasi (buyer/seller), login token Bearer, logout |
| | `GET /api/me`, `PUT /api/user/profile` | Auth | Baca & perbarui profil akun |
| | `POST /api/auth/reset/start`, `/otp/send`, `/otp/verify`, `/reset/complete` | Publik | Lupa kata sandi via OTP Email (SMTP/Resend) atau WhatsApp (Fonnte) |
| | `GET /api/users`, `PUT /api/user/:id/ban` | Admin | Daftar pengguna (PII disamarkan) & blokir/cabut sesi |
| **Ruko (Shop)** | `GET /api/shops`, `GET /api/shop/:id`, `GET /api/rent/quote` | Publik | Read daftar/detail ruko & kalkulasi tarif sewa |
| | `GET /api/shop/mine`, `POST /api/shop/rent` | Auth / Seller | Baca ruko milik sendiri & Create pengajuan sewa ruko |
| | `PUT /api/shop/:id`, `PUT /api/shop/:id/renew` | Owner / Admin | Update profil ruko & perpanjang masa sewa |
| | `PUT /api/shop/:id/status`, `DELETE /api/shop/:id` | Admin | Aktivasi/tangguhkan ruko & Delete ruko beserta produknya |
| **Produk** | `GET /api/products`, `GET /api/product/:id`, `GET /api/products/sold` | Publik | Read katalog aktif, detail produk & jumlah terjual |
| | `POST /api/product`, `PUT /api/product/:id`, `DELETE /api/product/:id` | Seller / Admin | Create, Update (harga/promo/variasi/stok), Delete produk |
| | `GET /api/admin/products`, `PUT /api/admin/product/:id/visibility` | Admin | Moderasi & sembunyikan/tampilkan produk |
| **Transaksi & Escrow** | `GET /api/couriers`, `POST /api/checkout` | Publik / Auth | Daftar kurir & Create pesanan + potong stok atomik |
| | `GET /api/transactions`, `GET /api/transaction/:id`, `GET /api/transaction/:id/track` | Auth | Read riwayat/detail pesanan & pelacakan peta kurir |
| | `POST /api/payment/:id/pay`, `GET /api/payment/:id/status` | Auth | Pembayaran token & status pembayaran real-time |
| | `PUT /api/transaction/:id/status`, `POST /api/transaction/:id/confirm-received` | Auth | Update status (pending → paid → shipped → success/cancelled) & cairkan Escrow |
| | `GET /api/wallet/summary`, `POST /api/wallet/withdraw` | Seller / Admin | Ringkasan saldo Escrow/aktif & tarik saldo ke rekening |
| | `GET /api/admin/transactions/export`, `POST /api/admin/transactions/cancel-stale` | Admin | Ekspor CSV (anti-formula injection) & batalkan pesanan macet |
| **Komplain (Dispute)** | `POST /api/disputes`, `GET /api/disputes`, `PUT /api/dispute/:id/reply`, `PUT /api/dispute/:id/resolve` | Auth / Admin | Ajukan komplain, tanggapan penjual, keputusan refund/release Escrow |
| **Tiket CS & Laporan** | `POST /api/tickets`, `GET /api/tickets`, `PUT /api/ticket/:id/reply` | Auth / Admin | Buat & balas tiket bantuan CS |
| | `POST /api/reports`, `GET /api/admin/reports`, `PUT /api/admin/report/:id/resolve` | Publik / Admin | Laporan pelanggaran produk/toko & penyelesaian moderator |
| **Chat & Notifikasi** | `POST /api/chat/send`, `GET /api/chat/history`, `GET /api/chat/inbox`, `GET /api/chat/unread`, `POST /api/chat/read`, `GET /api/chat/media/:id` | Auth / Media | Pesan teks + foto (max 3 MB), inbox & status baca |
| | `GET /api/notifications`, `POST /api/notifications`, `PUT /api/notifications/:id/read`, `PUT /api/notifications/read-all` | Auth | Baca, buat promo/pengumuman, tandai dibaca |

---

## Menjalankan Server Lokal

1. Salin `.env.example` menjadi `.env` dan isi kredensial MongoDB Atlas serta penyedia OTP:
   ```powershell
   Copy-Item .env.example .env
   ```
2. Jalankan server lokal (default `http://localhost:8081` sesuai `PORT` di `.env`):
   ```powershell
   go run .
   ```

---

## Pengujian Otomatis & Laporan Code Coverage (Proyek 2)

Tersedia **16 skenario pengujian otomatis** yang mencakup validasi autentikasi/RBAC, keamanan anti-XSS & anti-exploit harga negatif, matriks transisi Escrow, hingga pengujian integrasi **CRUD end-to-end** terhadap seluruh koleksi utama.

### 1. Mode Offline / CI (Tanpa Database)
Menjalankan Skenario 01–10 dan seluruh unit test (`model`, `helper`, `url`, `controller`):
```powershell
go vet ./...
go test -v -cover ./...
```

### 2. Mode Integrasi CRUD Penuh + Generate Laporan Coverage
Menjalankan seluruh **16 skenario** termasuk Skenario 11–16 pada **database sementara terisolasi (`vdr_test_*`)** yang otomatis dihapus saat tes selesai (dilindungi *safety guard* agar tidak pernah menyentuh database produksi `vendora_db`):
```powershell
$env:VENDORA_INTEGRATION_TEST = "1"
go test -v -count=1 -coverpkg "./controller,./helper,./model,./url" -coverprofile "coverage.out" ./controller ./helper ./model ./url
go tool cover -func coverage.out
go tool cover -html coverage.out -o coverage.html
```

### Hasil Empiris Code Coverage Terakhir
| Package | Statement Tercakup | Persentase |
|---|---|---|
| `vendora/model` | 3 / 3 | **100,0%** |
| `vendora/url` | 94 / 94 | **100,0%** |
| `vendora/controller` | 1.846 / 2.571 | **71,8%** |
| `vendora/helper` | 103 / 182 | **56,6%** *(sisanya pemanggil jaringan eksternal SMTP/Resend/Fonnte/Twilio)* |
| **Total Core Backend** | **2.046 / 2.850** | **71,8%** |
