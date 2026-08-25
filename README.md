# Denyra

Denyra mengelola akuisisi, validasi, import, dan streaming koleksi FLAC pribadi. Album yang terdaftar di Lidarr masuk ke library Managed. Album lain tetap dapat diunggah ke library Unmanaged, diputar melalui Navidrome, lalu dipindahkan ke Managed setelah ada kecocokan katalog yang dikonfirmasi.

Stack ini ditujukan untuk mesin lokal atau server privat. Otomasi DNS, TLS, reverse proxy, firewall, dan VPN tidak termasuk.

## Yang dijalankan

- Acquisition Gateway mengatur pencarian utama melalui Lidarr dan slskd, lalu fallback melalui SpotiFLAC bila hasil utama benar-benar kosong.
- Media Pipeline memvalidasi satu release penuh, mencocokkan metadata, menulis tag dan lyrics, lalu menyerahkannya ke Lidarr.
- Lidarr mengatur nama dan memindahkan hasil ke library akhir.
- Navidrome membaca library sebagai read-only dan melayani Feishin atau client OpenSubsonic lain.
- SFTPGo menyediakan jalur upload manual.

FLAC tetap menjadi master. Transcoding playback tidak mengubah file tersebut. Candidate yang rusak atau ambigu masuk quarantine dan tidak diimpor sebagian.

## Kebutuhan

Host Linux memerlukan:

- Git
- Docker Engine
- Docker Compose v2
- ruang untuk library Managed dan Unmanaged, upload, download sementara, processing, dan quarantine
- akun Soulseek

Host tidak perlu memasang Go, Node.js, Python, templ, ffmpeg, flac, atau compiler lain. Semua tool runtime berada di dalam container.

## Instalasi

```sh
git clone https://github.com/WaxArsatia/denyra.git
cd denyra
./denyra setup
```

Default deployment root adalah `/srv/denyra`. Setup dapat meminta `sudo` sekali untuk membuatnya. Untuk lokasi lain:

```sh
DENYRA_HOME=/absolute/path/denyra ./denyra setup
```

Setup meminta username dan password Soulseek bila belum diberikan melalui environment. Ia membuat direktori data, secret, config, akun Navidrome dan SFTPGo, serta konfigurasi Lidarr dan slskd. Setup aman dijalankan ulang.

Lihat akun yang dibuat:

```sh
./denyra credentials
```

## Upload album yang belum ada di Lidarr

1. Buka `http://localhost:4003/incoming`.
2. Drop atau pilih satu folder album. Upload SFTP ke `localhost:4005` juga masuk ke alur review yang sama.
3. Periksa metadata dan cover, lalu pilih Unmanaged dan tekan Submit.
4. Buka Unmanaged, pilih album, lalu tekan Check selected.
5. Tinjau hasilnya. Hanya hasil Exact candidate yang dapat dipilih pada form Confirm selected migrations.

Check selected hanya membaca katalog. File baru dipindahkan ke Lidarr setelah konfirmasi terpisah.

## URL lokal

| Fungsi | URL atau alamat |
| --- | --- |
| Navidrome dan Feishin | `http://localhost:4000` |
| Lidarr | `http://localhost:4001` |
| slskd Web UI | `http://localhost:4002` |
| Denyra | `http://localhost:4003` |
| SFTPGo WebAdmin | `http://localhost:4004` |
| Upload SFTP | `localhost:4005` |
| Soulseek incoming | `localhost:50300/TCP` |

Untuk server lain di LAN, ganti `localhost` dengan alamat LAN server. Jangan memakai alamat container internal pada client.

## Operasi

```sh
./denyra start
./denyra stop
./denyra restart
./denyra status
./denyra logs
./denyra update
./denyra cleanup legacy-lifecycle
./denyra credentials
```

`./denyra update` mengambil fast-forward terbaru, merender Compose, menarik image upstream, dan membangun image Denyra saat stack lama masih hidup. Cutover baru dimulai setelah semua tahap itu berhasil. Update tidak menghentikan seluruh project; Compose mengganti service yang perlu berubah lalu menjalankan health wait dan smoke checks.

Jika update gagal sebelum cutover, release environment dan container aktif tidak berubah. Jika startup atau smoke gagal setelah cutover, commit baru tetap terpilih dan bukti kegagalan tetap tersedia. Perbaiki penyebabnya lalu jalankan `./denyra update` lagi. Output kegagalan mencantumkan phase, service log yang perlu dibaca, commit aktif, dan command retry.

Media, database service, upload yang belum selesai, processing, quarantine, dan download yang belum terselesaikan tidak dihapus oleh update. Setelah deployment forward-only diterima, command berikut dapat menghapus tiga artefak lifecycle lama: direktori `updates`, direktori `backups`, dan secret Restic lokal.

`./denyra cleanup legacy-lifecycle` menampilkan path persis dan meminta token `DELETE`. Command ini tidak dipanggil oleh update dan tidak mencari repository Restic eksternal.

## Pengembangan

Deployment normal tidak menjalankan tool development. Kontributor dapat memakai:

```sh
make verify
go test ./...
```

Lihat [runbook instalasi](docs/runbooks/install.md), [upgrade](docs/runbooks/upgrade.md), [incident](docs/runbooks/incidents.md), dan [client](docs/runbooks/clients.md) untuk detail operasional.
