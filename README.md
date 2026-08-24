# Denyra

Denyra adalah stack mandiri untuk akuisisi, validasi, pengelolaan, dan streaming
koleksi FLAC pribadi. Lidarr memiliki library akhir. Layanan Go milik Denyra
mengatur akuisisi dan memvalidasi satu release penuh sebelum Lidarr melakukan
import. Navidrome menyediakan library melalui OpenSubsonic tanpa akses tulis ke
file master.

Targetnya adalah pengalaman seperti Spotify pribadi dengan FLAC sebagai format
master. Public exposure, DNS, TLS, reverse proxy, dan firewall berada di luar
scope repository ini.

## Daftar isi

- [Kemampuan](#kemampuan)
- [Batas sistem](#batas-sistem)
- [Arsitektur](#arsitektur)
- [Komponen](#komponen)
- [Kebutuhan sistem](#kebutuhan-sistem)
- [Mencoba demo lokal](#mencoba-demo-lokal)
- [Menjalankan stack lokal lengkap](#menjalankan-stack-lokal-lengkap)
- [Konfigurasi awal](#konfigurasi-awal)
- [Operasi harian](#operasi-harian)
- [Backup, restore, dan upgrade](#backup-restore-dan-upgrade)
- [Model keamanan](#model-keamanan)
- [Pengembangan](#pengembangan)
- [Pengujian](#pengujian)
- [Troubleshooting](#troubleshooting)
- [Dokumentasi proyek](#dokumentasi-proyek)

## Kemampuan

Denyra memiliki tiga jalur ingest yang bertemu di satu media pipeline:

1. Lidarr mencari melalui Lidarr.Plugin.Slskd dan slskd sebagai jalur utama.
2. Acquisition Gateway menjalankan subprocess SpotiFLAC yang dipin jika jalur
   utama memberikan hasil kosong yang sah.
3. SFTPGo menerima upload manual untuk submission dan review eksplisit.

Setiap candidate diproses sebagai satu MusicBrainz release lengkap. Pipeline
memeriksa media, mencocokkan release, menulis tag deterministik yang sudah
disetujui, membuat sidecar lyrics, dan menyerahkan tepat satu pemenang ke Lidarr
Manual Import. Candidate invalid atau ambigu tetap berada di quarantine.

Invariant utama:

- Lidarr adalah satu-satunya proses yang boleh memindahkan, mengubah nama, atau
  mengatur file di `/data/library`.
- Navidrome memasang library akhir secara read-only.
- Downloader hanya menulis ke direktori akuisisi.
- Validasi bersifat release-atomic. Satu track gagal atau hilang menahan seluruh
  release.
- FLAC tetap menjadi format master. Transcoding playback tidak mengubah master.
- SpotiFLAC tidak menerima kredensial streaming-provider pribadi.
- Error operasional tidak pernah diubah menjadi hasil `NO_CANDIDATE` palsu.

## Batas sistem

Denyra mencakup orkestrasi akuisisi, validasi, metadata, lyrics, import,
streaming, administrasi lokal, backup, dan recovery.

Hal berikut memerlukan desain deployment terpisah:

- akses publik dari Internet
- DNS dan TLS
- reverse proxy
- firewall host
- topologi VPN
- penilaian legal, copyright, atau ketentuan provider secara terperinci

Gunakan hanya sumber akuisisi yang boleh Anda akses. Jangan menyimpan kredensial
di Git, log, config snapshot, atau percakapan chat.

## Arsitektur

```text
Lidarr Wanted
    |
    v
Acquisition Gateway
    |
    +--> Lidarr AlbumSearch
    |        |
    |        v
    |    Lidarr.Plugin.Slskd --> slskd --> Soulseek
    |
    +--> SpotiFLAC fallback setelah primary zero-result yang sah
             |
             v
      /data/downloads/*
             |
             v
       Media Pipeline <---- submission manual SFTPGo
             |
             +--> validasi dan pencocokan MusicBrainz release
             +--> tag FLAC deterministik melalui metaflac
             +--> evidence folder.jpg dan sidecar .lrc
             +--> quarantine atau review operator
             |
             v
    /data/processing/approved
             |
             v
      Lidarr Manual Import
             |
             v
       /data/library
             |
             v
 Navidrome --> Feishin / Tempus
```

Gateway dan Pipeline memakai database SQLite terpisah. Keduanya bertukar
candidate ID immutable melalui private HTTP API yang diautentikasi. State bisnis
tidak disinkronkan melalui shared database.

## Komponen

| Komponen                 | Tanggung jawab                                                                                              |
| ------------------------ | ----------------------------------------------------------------------------------------------------------- |
| Acquisition Gateway      | Wanted discovery, primary search, fallback, retry, correlation, arbitration, dan candidate handoff          |
| Media Pipeline           | Claim, validasi teknis, MusicBrainz matching, enrichment, deterministic mutation, review, dan Lidarr import |
| Lidarr nightly           | Wanted state, release policy, final import, naming, dan kepemilikan library akhir                           |
| Lidarr.Plugin.Slskd      | Integrasi native Lidarr dengan slskd                                                                        |
| slskd                    | Soulseek client headless dan downloader utama                                                               |
| SpotiFLAC Module Version | Engine fallback credentialless yang berjalan sebagai subprocess terisolasi                                  |
| SFTPGo CE                | Endpoint upload manual                                                                                      |
| beets                    | Advisory matching evidence untuk manual ingest saja                                                         |
| MusicBrainz              | Metadata release canonical                                                                                  |
| LRCLIB                   | Sumber synchronized lyrics persisten                                                                        |
| Navidrome                | Catalog read-only dan server OpenSubsonic                                                                   |
| Feishin                  | Client Linux yang direkomendasikan                                                                          |
| Tempus                   | Client Android yang direkomendasikan                                                                        |
| Restic                   | Jalur backup dan retention yang didukung                                                                    |

Identitas application, runtime, image, plugin, extension, dan asset tersimpan di
[`dependencies.lock.json`](dependencies.lock.json) serta
[`deploy/images.lock.json`](deploy/images.lock.json). Pin dependency hanya boleh
berubah melalui update eksplisit dan compatibility test.

## Kebutuhan sistem

Gunakan host Linux amd64 dengan:

- Docker Engine dan Docker Compose v2
- Docker Buildx
- Git
- Go untuk development dan test
- ruang penyimpanan untuk library FLAC, download staging, processing, dan
  quarantine
- satu UID/GID numerik yang digunakan bersama oleh media container

Semua path di bawah `/data` harus berada pada filesystem yang sama agar pipeline
dapat memakai atomic rename. Repository Restic harus berada pada disk,
filesystem, atau remote repository lain.

Port lokal:

| Port    | Service        | Fungsi                                |
| ------- | -------------- | ------------------------------------- |
| `8090`  | Media Pipeline | Denyra Admin UI melalui internal HTTP |
| `8686`  | Lidarr         | Setup lokal dan library management    |
| `5030`  | slskd          | Web UI dan konfigurasi lokal          |
| `50300` | slskd          | Soulseek incoming listen port         |
| `8080`  | SFTPGo         | Web administration                    |
| `2022`  | SFTPGo         | Upload SFTP                           |
| `4533`  | Navidrome      | Web UI dan OpenSubsonic API           |

Jika port sudah dipakai, ubah host port melalui environment variable
`DENYRA_*_HOST_PORT` yang sesuai.

## Mencoba demo lokal

Demo lokal menjalankan Gateway, Pipeline, SQLite, Admin UI, dan Navidrome asli.
Lidarr, MusicBrainz, dan LRCLIB diganti oleh local no-result fixture. Demo tidak
menghubungi Soulseek atau provider fallback nyata.

Build dan verifikasi custom image yang sudah dikunci:

```sh
scripts/verify-pins/verify.sh --offline
make generate-provenance
BUILDX_NO_DEFAULT_ATTESTATIONS=1 docker buildx bake \
  -f deploy/docker/docker-bake.hcl gateway pipeline navidrome --load
```

Pilih direktori absolut di luar repository, lalu buat runtime tree dan local
secret:

```sh
export DENYRA_LOCAL_ROOT=/absolute/path/to/denyra-local
export DENYRA_DATA_ROOT="$DENYRA_LOCAL_ROOT/data"
export DENYRA_SECRETS_DIR="$DENYRA_LOCAL_ROOT/secrets"
export DENYRA_MEDIA_UID="$(id -u)"
export DENYRA_MEDIA_GID="$(id -g)"

install -d -m 0750 \
  "$DENYRA_DATA_ROOT"/{downloads/{slskd,spotiflac,other},incoming/manual} \
  "$DENYRA_DATA_ROOT"/{processing/{work,approved},quarantine,library,backups} \
  "$DENYRA_DATA_ROOT"/state/{gateway,pipeline,lidarr,slskd,sftpgo,navidrome} \
  "$DENYRA_DATA_ROOT"/cache/navidrome "$DENYRA_SECRETS_DIR"

for name in internal_bearer audit_key lidarr_api_key soulseek_username soulseek_password restic_password; do
  openssl rand -hex -out "$DENYRA_SECRETS_DIR/$name" 32
done
openssl rand -hex -out "$DENYRA_SECRETS_DIR/bootstrap_admin" 12
chmod 0400 "$DENYRA_SECRETS_DIR"/*
```

Jalankan demo persisten:

```sh
docker compose -p denyra-local \
  -f deploy/compose.yaml \
  -f deploy/compose.acceptance.yaml \
  -f deploy/compose.local.yaml \
  up -d --wait \
  acceptance-fixture media-pipeline acquisition-gateway navidrome
```

Buka:

- Denyra Admin UI: <http://127.0.0.1:8090>
- Navidrome: <http://127.0.0.1:4533>

Username Denyra adalah `admin`. Baca one-time password secara lokal:

```sh
command cat "$DENYRA_SECRETS_DIR/bootstrap_admin"
```

Navidrome meminta Anda membuat administrator pertama. Akun Navidrome terpisah
dari administrator Denyra.

Hentikan demo tanpa menghapus state:

```sh
docker compose -p denyra-local \
  -f deploy/compose.yaml \
  -f deploy/compose.acceptance.yaml \
  -f deploy/compose.local.yaml \
  down
```

Jangan memakai `down --volumes` atau menghapus `DENYRA_DATA_ROOT` kecuali Anda
memang ingin menghapus seluruh state demo.

## Menjalankan stack lokal lengkap

Stack lokal lengkap memakai Lidarr, slskd, SFTPGo, MusicBrainz, LRCLIB, dan
engine SpotiFLAC yang nyata. Stack tetap memakai HTTP lokal dan tidak boleh
diekspos ke jaringan yang tidak dipercaya.

Siapkan:

- akun Soulseek
- library FLAC kosong atau yang sudah ada di data root pilihan
- password berbeda untuk Denyra, SFTPGo, Navidrome, dan slskd
- repository Restic di luar filesystem `/data` jika backup akan diaktifkan

Jangan memasukkan kredensial ke command yang tersimpan di shell history. Simpan
di local secret file atau masukkan melalui setup UI service yang bersangkutan.

Ikuti [`docs/runbooks/install.md`](docs/runbooks/install.md) untuk prosedur
directory, ownership, secret, build, dan verifikasi lengkap. Tambahkan
`deploy/compose.local.yaml` untuk akses lokal:

```sh
docker compose -p denyra-local \
  -f deploy/compose.yaml \
  -f deploy/compose.local.yaml \
  up -d lidarr slskd sftpgo navidrome
```

Selesaikan setup Lidarr, slskd, SFTPGo, dan Navidrome. Masukkan API key Lidarr
ke local secret file `lidarr_api_key`, lalu jalankan service Go milik Denyra:

```sh
docker compose -p denyra-local \
  -f deploy/compose.yaml \
  -f deploy/compose.local.yaml \
  up -d --wait media-pipeline acquisition-gateway
```

Periksa service:

```sh
docker compose -p denyra-local \
  -f deploy/compose.yaml \
  -f deploy/compose.local.yaml \
  ps
```

## Konfigurasi awal

### Lidarr

Buka <http://127.0.0.1:8686> dan selesaikan setup autentikasi.

Pengaturan wajib:

- nonaktifkan automatic Completed Download Handling
- gunakan `/data/library` sebagai final root folder
- izinkan import hanya dari `/data/processing/approved`
- aktifkan Import Extra Files untuk `.lrc`, `.elrc`, dan `.ttml`
- pertahankan `folder.jpg` sebagai nama artwork album
- arahkan Lidarr.Plugin.Slskd yang sudah dibake ke service `slskd`

Salin API key Lidarr ke `DENYRA_SECRETS_DIR/lidarr_api_key` menggunakan editor
lokal, atur mode `0400`, lalu restart Gateway dan Pipeline. Jangan pernah commit
API key.

### Soulseek dan Nicotine+

Soulseek tidak memiliki formulir registrasi akun terpisah. Masukkan username dan
password yang Anda inginkan di Nicotine+, lalu connect. Server membuat akun saat
login pertama berhasil jika username masih tersedia.

Jika muncul `INVALIDPASS`, username tersebut sudah terikat ke password lain.
Pilih username lain, jangan mencoba menebak password. Username maksimal 30
karakter, hanya printable ASCII, dan tidak boleh memiliki spasi di awal atau
akhir.

Soulseek tidak memiliki mekanisme reset password. Simpan username dan password
yang tepat di password manager. Uji bahwa Nicotine+ berstatus connected dan
dapat melakukan search, lalu tutup Nicotine+ sepenuhnya sebelum memakai akun
yang sama di slskd. Login bersamaan dapat membuat salah satu client terputus.

### slskd

Buka <http://127.0.0.1:5030>. Remote configuration diaktifkan oleh local Compose
override.

Konfigurasikan:

- username dan password Soulseek yang sudah diuji melalui Nicotine+
- download directory `/data/downloads/slskd`
- incomplete directory di bawah download tree yang sama
- autentikasi Web UI yang kuat
- read/write API key untuk Lidarr.Plugin.Slskd
- incoming listen port `50300`

Deployment memasang state slskd di `/app`, sehingga konfigurasi tersimpan saat
container dibuat ulang.

### SFTPGo

Buka <http://127.0.0.1:8080>, buat administrator SFTPGo pertama, lalu buat
upload user yang dibatasi ke `/data/incoming/manual`. SFTPGo tidak boleh
memiliki akses ke processing, quarantine, atau library akhir.

### Navidrome

Buka <http://127.0.0.1:4533> dan buat music administrator pertama. Navidrome
memakai `/music:ro`; database, cache, dan transcoding berada di volume writable
terpisah.

Konfigurasikan Feishin atau Tempus dengan URL Navidrome dan akun music user.
Gunakan original FLAC di LAN. Pada koneksi terbatas, minta maximum bitrate
OpenSubsonic yang sesuai, misalnya logical policy `opus-256` atau `opus-160`.

### Denyra Admin UI

Buka <http://127.0.0.1:8090> dan login menggunakan bootstrap username serta
password. Ubah password setelah login pertama, lalu kosongkan bootstrap secret
file. UI menyediakan candidate detail, hasil per track, metadata diff, checksum,
provenance, status artwork dan lyrics, serta action Approve, Reject, dan Retry.

Approval memerlukan MusicBrainz Release ID dan alasan. Mutation memakai
optimistic state revision, proteksi CSRF, dan domain service yang sama dengan
internal API.

## Operasi harian

Command yang sering digunakan:

```sh
docker compose -p denyra-local -f deploy/compose.yaml -f deploy/compose.local.yaml ps
docker compose -p denyra-local -f deploy/compose.yaml -f deploy/compose.local.yaml logs --since 10m acquisition-gateway media-pipeline
docker compose -p denyra-local -f deploy/compose.yaml -f deploy/compose.local.yaml restart acquisition-gateway media-pipeline
```

Lokasi media:

| Path                        | Pemilik atau fungsi                                                 |
| --------------------------- | ------------------------------------------------------------------- |
| `/data/downloads/slskd`     | Download mentah slskd; Pipeline hanya claim job complete dan locked |
| `/data/downloads/spotiflac` | Output fallback Gateway; Pipeline claim completed candidate         |
| `/data/incoming/manual`     | Submission manual dari SFTPGo                                       |
| `/data/processing/work`     | Validasi dan deterministic mutation oleh Pipeline                   |
| `/data/processing/approved` | Batch approved yang terlihat oleh Lidarr                            |
| `/data/quarantine`          | Candidate invalid, ambigu, review-required, atau superseded         |
| `/data/library`             | Library akhir milik Lidarr                                          |

Low storage menghentikan claim, acquisition, dan import baru jika kapasitas
tersedia kurang dari `max(20 GiB, 5%)`. Cleanup, quarantine handling,
reconciliation, backup, recovery, dan administrasi pemulihan kapasitas tetap
diizinkan.

## Backup, restore, dan upgrade

Restic tersedia sebagai optional Compose profile. Repository harus eksplisit dan
tidak boleh berada pada filesystem `/data`. Denyra masuk maintenance mode,
menunggu mutation selesai, membuat online SQLite backup, menghentikan service
third-party yang stateful untuk waktu singkat, lalu memverifikasi snapshot
sebelum keluar dari maintenance.

Baca runbook berikut sebelum mengandalkan deployment:

- [`docs/runbooks/backup.md`](docs/runbooks/backup.md)
- [`docs/runbooks/restore.md`](docs/runbooks/restore.md)
- [`docs/runbooks/upgrade.md`](docs/runbooks/upgrade.md)

Restore selalu menuju directory baru. Cutover tetap manual dan tidak boleh
menimpa live data tree.

## Model keamanan

Admin UI sengaja memakai HTTP pada `0.0.0.0:8090`. Cookie memakai `HttpOnly`,
`SameSite=Strict`, dan tidak memiliki atribut `Secure` karena TLS bukan bagian
dari internal stack. Ini adalah accepted risk. Deployment dan firewall harus
membatasi pihak yang dapat mencapai port tersebut.

Kontrol lain:

- password hash Argon2id
- server-side session dengan opaque CSPRNG token 32 byte
- hanya hash session token yang disimpan di SQLite
- absolute session lifetime 30 hari tanpa idle timeout
- proteksi CSRF pada setiap mutation
- append-only audit evidence dan optimistic state revision
- generic authentication error
- logout, logout-all, password change, dan explicit revocation
- private network antara Gateway dan Pipeline
- constant-time bearer comparison
- redaksi secret dari structured log dan config snapshot

Baca [`docs/runbooks/security-boundary.md`](docs/runbooks/security-boundary.md)
sebelum mengubah port atau network exposure.

## Pengembangan

Custom service memakai Go, `net/http`, `database/sql`, go-sqlite3 dengan CGO,
handwritten repository, embedded migration, templ, dan HTMX yang divendor secara
lokal. Tidak ada Node frontend toolchain.

Command umum:

```sh
make fmt
make vet
make test
make race
make verify-lock
make compose-config
```

Regenerasikan Admin UI setelah mengubah source `.templ`:

```sh
go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate
scripts/verify-ui-source.sh
go run ./scripts/verify-tokens
```

Format dan lint seluruh codebase sebelum commit:

```sh
make fmt
go vet ./...
```

## Pengujian

Jalankan seluruh race suite:

```sh
go test -race -count=1 ./...
```

Jalankan deterministic acceptance test:

```sh
go test -count=1 ./tests/acceptance -run Denyra
```

Jalankan pinned Compose smoke setelah image dikompilasi:

```sh
DENYRA_ACCEPTANCE_COMPOSE=1 \
  go test -count=1 ./tests/acceptance \
  -run TestDenyraPinnedComposeStartsReadyWithLocalAdapters -v
```

Live-provider acceptance tidak masuk CI dan test lokal normal. Profile tersebut
hanya berjalan dengan explicit side-effect acknowledgement yang diwajibkan
Gateway.

Identitas artifact, command, dan hasil verifikasi terakhir tersedia di
[`docs/runbooks/acceptance-evidence.md`](docs/runbooks/acceptance-evidence.md).

## Troubleshooting

Mulai dari service status dan log terbaru:

```sh
docker compose -p denyra-local -f deploy/compose.yaml -f deploy/compose.local.yaml ps
docker compose -p denyra-local -f deploy/compose.yaml -f deploy/compose.local.yaml logs --since 10m
```

Masalah umum:

| Gejala                                 | Pemeriksaan                                                                                         |
| -------------------------------------- | --------------------------------------------------------------------------------------------------- |
| Pipeline tidak ready                   | Ownership, mode, filesystem device identity, binary, config, migration, dan akses secret file       |
| Gateway restart                        | API key dan readiness Lidarr, readiness Pipeline, artifact SpotiFLAC, serta private network address |
| External dependency degraded           | MusicBrainz, LRCLIB, Soulseek, atau fallback provider; local readiness harus tetap sehat            |
| Pekerjaan baru berhenti                | Kapasitas filesystem yang menaungi path `/data` aktual                                              |
| Manual submission kembali menunggu     | Sealed tree fingerprint berubah sebelum claim; periksa dan submit ulang                             |
| Candidate tertahan di review           | Berikan MusicBrainz Release ID pasti dan catat alasan approval                                      |
| Track baru belum terlihat di Navidrome | Periksa watcher log, lalu tunggu recovery scan satu menit                                           |
| Login session hilang                   | Periksa expiry 30 hari, password change, logout-all, atau explicit revocation                       |

Recovery per jenis insiden tersedia di
[`docs/runbooks/incidents.md`](docs/runbooks/incidents.md).

## Dokumentasi proyek

- [Runbook instalasi](docs/runbooks/install.md)
- [Setup client](docs/runbooks/clients.md)
- [Security boundary](docs/runbooks/security-boundary.md)
- [Incident recovery](docs/runbooks/incidents.md)
- [Backup](docs/runbooks/backup.md)
- [Restore](docs/runbooks/restore.md)
- [Upgrade dan rollback](docs/runbooks/upgrade.md)
- [Desain system foundation](docs/superpowers/specs/2026-08-24-system-foundation-design.md)
- [Desain acquisition orchestration](docs/superpowers/specs/2026-08-24-acquisition-orchestration-design.md)
- [Desain controlled media pipeline](docs/superpowers/specs/2026-08-24-controlled-media-pipeline-design.md)
- [Desain operations dan clients](docs/superpowers/specs/2026-08-24-operations-and-clients-design.md)

Repository belum memiliki file license. Perlakukan source sebagai all rights
reserved sampai pemilik menambahkan license eksplisit.
