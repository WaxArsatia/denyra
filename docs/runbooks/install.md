# Install Denyra

## Requirements

Use a Linux host with Git, Docker Engine, Docker Compose v2, and enough storage for both FLAC libraries and temporary media work. The host does not need Go, Node.js, Python, templ, ffmpeg, flac, or a compiler. Released containers include the runtime media tools.

Prepare a Soulseek account before setup. Keep the deployment on a local machine or private server unless you provide a separate network security design.

## Install

```sh
git clone https://github.com/WaxArsatia/denyra.git
cd denyra
./denyra setup
```

The default deployment root is `/srv/denyra`. Setup may request `sudo` once to create that directory. To use another absolute path:

```sh
DENYRA_HOME=/srv/media/denyra ./denyra setup
```

Setup asks for the Soulseek username and password when they are not already supplied through the environment. It creates the Managed and Unmanaged roots, upload and processing directories, secrets, configuration, service accounts, images, and containers. It also reconciles Lidarr, slskd, SFTPGo, and Navidrome without browser setup or manual database migration.

Running setup again is supported. Existing generated secrets and accounts are retained.

## Check the deployment

```sh
./denyra status
./denyra credentials
```

Local URLs:

- Navidrome: `http://localhost:4000`
- Lidarr: `http://localhost:4001`
- slskd Web UI: `http://localhost:4002`
- Denyra: `http://localhost:4003`
- SFTPGo WebAdmin: `http://localhost:4004`
- SFTP upload: `localhost:4005`
- Soulseek incoming: `localhost:50300/TCP`

For a private server, replace `localhost` with its LAN address. Use the Navidrome URL in Feishin.

## Import an album

Open `http://localhost:4003/incoming`, then drop or select an album folder. An SFTP upload to `localhost:4005` enters the same review flow. Review metadata and artwork, choose Managed or Unmanaged, and submit the release. For an unmanaged release, open Unmanaged, run Check selected, review the durable result, and confirm only an exact match you recognize.

## Normal lifecycle

```sh
./denyra start
./denyra stop
./denyra restart
./denyra status
./denyra logs
```

Use these commands as the operator interface. Compose details remain internal to the deployment scripts.

## Next steps

Use `./denyra update` for later releases. An update builds before cutover and preserves media, service state, unresolved downloads, uploads, processing work, and quarantine content. If a deployment fails after cutover, inspect the reported service logs, fix the candidate, and run the update again.
