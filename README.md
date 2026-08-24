# Denyra

Denyra mengelola akuisisi, validasi, import, dan streaming koleksi FLAC pribadi. Lidarr memiliki library akhir. Navidrome menyediakan playback melalui OpenSubsonic, termasuk artwork dan sidecar lyrics yang sudah masuk ke library.

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
- ruang untuk library, download sementara, processing, dan quarantine
- akun Soulseek

Host tidak perlu memasang Go, Node.js, Python, atau compiler lain untuk deployment. Build container memakai image runtime resmi dan mengambil release binary upstream yang sesuai.

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

## URL lokal

| Fungsi | URL atau alamat |
| --- | --- |
| Denyra | `http://localhost:8090` |
| Navidrome dan Feishin | `http://localhost:4533` |
| SFTPGo WebAdmin | `http://localhost:8080` |
| Upload SFTP | `localhost:2022` |

Untuk server lain di LAN, ganti `localhost` dengan alamat LAN server. Jangan memakai alamat container internal pada client.

## Operasi

```sh
./denyra start
./denyra stop
./denyra restart
./denyra status
./denyra logs
./denyra update
./denyra rollback
./denyra credentials
```

`./denyra update` mengambil fast-forward terbaru dan menyiapkan semua image saat stack lama masih hidup. Setelah build berhasil, command membuat snapshot config dan state, menjalankan kandidat, lalu mengecek semua service. Kandidat yang gagal sebelum sehat otomatis dikembalikan ke state dan image ID lama.

`./denyra rollback` memakai snapshot update sukses terbaru. Command meminta konfirmasi karena perubahan state setelah update akan dibuang. Rollback tidak memindahkan Git worktree ke commit lama.

## Backup

Snapshot update hanya untuk rollback cepat dan tidak menyimpan library. Untuk disaster recovery, siapkan repository Restic di luar `DENYRA_HOME`, lalu jalankan:

```sh
DENYRA_RESTIC_REPOSITORY_PATH=/mnt/denyra-restic ./denyra backup
```

Backup Restic terenkripsi mencakup config, secrets, library, state, incoming, processing, quarantine, dan salinan SQLite yang konsisten. Download mentah, cache, update snapshot, dan workspace backup dikecualikan.

## Pengembangan

Deployment normal tidak menjalankan tool development. Kontributor dapat memakai:

```sh
make verify
go test ./...
```

Lihat [runbook instalasi](docs/runbooks/install.md), [upgrade](docs/runbooks/upgrade.md), [backup](docs/runbooks/backup.md), [restore](docs/runbooks/restore.md), dan [client](docs/runbooks/clients.md) untuk detail operasional.
