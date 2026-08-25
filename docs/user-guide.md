# ShortQ User Guide

ShortQ punya 3 role:

- `superadmin`: kelola user dan domain ALVA, semua link, analytics global.
- `tenant`: kelola customer dalam tenant sendiri, lihat link dan analytics tenant.
- `customer`: kelola link, QR, dan API key dalam department ALVA.

## Akses Aplikasi

1. Buka `http://localhost:8080`.
2. Klik `Open dashboard`.
3. Login pakai akun yang diberikan.

Default superadmin saat pertama run:

- Email: `admin@shortq.local`
- Password: `ChangeMe123!`

Ganti password default sebelum production.

## Mode Admin dan User

Akun superadmin dan admin department memiliki tombol Acting as untuk berpindah mode. Login baru selalu dimulai dalam mode Admin; refresh mempertahankan mode saat ini.

- Mode Admin menampilkan link dan analytics sesuai cakupan admin, serta panel department, domain, dan user.
- Mode User menyembunyikan panel administrasi dan server membatasi semua API dashboard ke role user efektif.
- API key yang dibuat dalam mode User bersifat user-scoped dan tidak dapat memperoleh izin admin. API key lama mempertahankan cakupan account.
- Logout lalu login kembali mengembalikan akun admin ke mode Admin.

## Superadmin Guide

Only superadmins can activate or deactivate deletion access from the Users panel and view the Audit log. The audit log records sanitized administrative changes and denied deletion attempts for 365 days. Passwords, tokens, and raw API keys are never included.

### Login

1. Buka dashboard.
2. Masukkan email dan password superadmin.
3. Setelah login, dashboard menampilkan role `superadmin`.

### Department ALVA

ShortQ memakai satu department tetap bernama ALVA. Semua user SSO baru otomatis dibuat sebagai user ALVA, dan department tambahan tidak dapat dibuat.

### Membuat User Tenant

1. Di bagian `Tenant / Customer users`, isi:
   - `name`: nama user.
   - `email`: email login.
   - `password`: minimal 8 karakter.
   - `role`: pilih `tenant`.
   - `tenant id`: isi ID tenant.
2. Klik `Create user`.
3. User tenant bisa login dan mengelola customer tenant tersebut.

### Membuat Customer

1. Di bagian `Tenant / Customer users`, isi data customer.
2. Pilih role `customer`.
3. Isi `tenant id` sesuai tenant customer.
4. Klik `Create user`.

### Melihat Analytics Global

Bagian stats menampilkan:

- `total links`: semua link di sistem.
- `total clicks`: total klik semua link.
- `today clicks`: klik hari ini.
- `total tenants`: jumlah tenant.
- `total users`: jumlah semua user.

### Mengelola Link

Superadmin bisa melihat dan menghapus link lintas tenant/customer.

1. Lihat daftar di bagian `Links`.
2. Klik `Delete` untuk hapus link.
3. Klik `QR` untuk generate QR dari short URL.

## Tenant Guide

### Login

1. Login pakai akun role `tenant`.
2. Dashboard menampilkan role `tenant` dan tenant ID.

### Membuat Customer Dalam Tenant

1. Di bagian `Tenant / Customer users`, isi:
   - `name`: nama customer.
   - `email`: email customer.
   - `password`: minimal 8 karakter.
   - `role`: biarkan `customer`.
   - `tenant id`: boleh kosong; sistem otomatis memakai tenant milik user tenant.
2. Klik `Create user`.
3. Customer baru masuk tenant yang sama.

### Melihat Customer

Daftar customer hanya berisi customer dalam tenant sendiri.

### Melihat Analytics Tenant

Bagian stats menampilkan data tenant sendiri:

- total link milik tenant.
- total klik link tenant.
- klik hari ini untuk tenant.

### Mengelola Link Tenant

Tenant bisa melihat dan menghapus link customer dalam tenant sendiri.

1. Buka bagian `Links`.
2. Klik `QR` untuk preview QR.
3. Klik `Delete` jika perlu hapus link.

## Customer Guide

### Register Sendiri

1. Klik `Register`.
2. Isi nama, email, password.
3. Klik `Register`.
4. Login memakai email dan password tersebut.

Catatan: register publik membuat customer tanpa tenant. Kalau customer harus masuk tenant, superadmin atau tenant harus membuat user customer dari dashboard.

### Login

1. Klik `Login`.
2. Isi email dan password.
3. Klik `Login`.

### Membuat Short Link

1. Di bagian `Links`, isi:
   - URL target, contoh `https://www.kanezza.com/promo`.
   - custom slug opsional, contoh `promo-agustus`.
   - title opsional.
2. Klik `Create short link`.
3. Short URL muncul di daftar link.

Format short URL:

```text
http://localhost:8080/r/<slug>
```

### Generate QR Code

Ada 2 cara:

1. Klik tombol `QR` di link yang sudah dibuat.
2. Atau isi text/URL di kartu `Try QR`, lalu klik `Generate`.

QR dihasilkan sebagai PNG resolusi tinggi dengan logo resmi ALVA dan error correction tinggi agar tetap mudah dipindai.

QR endpoint publik:

```text
/api/v1/qr?text=<text>
/api/v1/qr?url=<url>
```

### Menghapus Link

1. Cari link di daftar.
2. Klik `Delete`.
3. Link tidak bisa dipakai lagi setelah dihapus. Slug dan analytics tetap disimpan, sehingga slug tidak dapat dipakai ulang.

### Membuat API Key

1. Di bagian `API keys`, isi nama key.
2. Klik `Create key`.
3. Copy key yang muncul.
4. Key hanya ditampilkan sekali.

Contoh penggunaan API key:

```bash
curl -H 'X-API-Key: sq_live_xxx' http://localhost:8080/api/v1/links
```

### Revoke API Key

1. Cari key di daftar API keys.
2. Klik `Revoke`.
3. Key langsung tidak bisa dipakai lagi.

### Change Password

1. Isi `old password`.
2. Isi `new password` minimal 8 karakter.
3. Klik `Change`.

## Forgot Password

1. Klik `Forgot`.
2. Isi email.
3. Klik `Generate reset`.
4. Saat SMTP belum dikonfigurasi, reset token tampil di log server.
5. Masuk dashboard, isi `reset token optional` dan password baru di bagian `Change password`.

## Visibility dan Report

- Link baru bersifat private secara default. Pemilik dapat memilih Shared with ALVA agar user lain dalam department yang sama dapat melihat link, QR, dan report-nya.
- User biasa yang melihat link milik rekan tidak dapat mengedit atau menghapusnya. Admin department dapat mengelola semua link dalam department; superadmin dapat mengelola semua link.
- Klik Report pada baris link untuk membuka report link. Klik My report untuk report semua link milik sendiri. Admin juga dapat membuka report user dari panel Users.
- Report menyediakan filter 7, 30, atau 90 hari dan rentang khusus. Hari dihitung memakai timezone browser.
- KPI mencakup total, klik periode, human, bot, estimasi unique visitor, percobaan expired, rata-rata per hari, peak day, dan perbandingan periode sebelumnya bila data tersedia.
- Chart mencakup trend harian, human/bot, negara, device, browser, OS, referrer, UTM, campaign, route, dan status. Report user juga menampilkan ranking link.
- Download Summary CSV untuk agregat atau Events CSV untuk event aman. Export event tidak menyertakan IP, user-agent mentah, full referrer URL, atau resolved destination URL.
- Detail event dan estimasi unique tersedia untuk 90 hari terakhir. Unique visitor adalah estimasi distinct IP untuk klik human yang berhasil.

## API Docs

- UI docs offline: `http://localhost:8080/docs`
- OpenAPI YAML: `http://localhost:8080/docs/openapi.yaml`

## Role Matrix

| Feature | Superadmin | Tenant | Customer |
|---|---:|---:|---:|
| Create tenant | Yes | No | No |
| List tenants | Yes | No | No |
| Create tenant user | Yes | No | No |
| Create customer | Yes | Yes, own tenant | No |
| List customers | All tenant/customer users | Own tenant customers | No |
| View links | All links | Own tenant links | Own + shared tenant links |
| Edit links | All links | Own tenant links | Own links |
| Delete links | All links | Own tenant links | Own links |
| Create links | Yes | Yes | Yes |
| Detailed link report | All links | Own tenant links | Own + shared tenant links |
| User report | All users | Own tenant users | Self |
| Overview analytics | Global | Own tenant | Own links |
| API keys | Own account | Own account | Own account |
| QR generator | Yes | Yes | Yes |

## Production Checklist

- Ganti `SUPERADMIN_EMAIL` dan `SUPERADMIN_PASSWORD` di `.env`.
- Ganti `JWT_SECRET` minimal 32 karakter acak.
- Pakai HTTPS di reverse proxy.
- Backup volume MySQL.
- Set `APP_BASE_URL` ke domain production.
- Jangan share API key di chat/log.
