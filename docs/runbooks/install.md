# Install Denyra

## Requirements

Use a Linux host with Git, Docker Engine, Docker Compose v2, and enough storage for the FLAC library and temporary media work. Deployment does not require host-side Go, Node.js, Python, or compiler setup.

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

Setup asks for the Soulseek username and password when they are not already supplied through the environment. It creates the data layout, secrets, configuration, service accounts, images, and containers. It also reconciles Lidarr, slskd, SFTPGo, and Navidrome without browser setup.

Running setup again is supported. Existing generated secrets and accounts are retained.

## Check the deployment

```sh
./denyra status
./denyra credentials
```

Local URLs:

- Denyra: `http://localhost:8090`
- Navidrome: `http://localhost:4533`
- SFTPGo WebAdmin: `http://localhost:8080`
- SFTP upload: `localhost:2022`

For a private server, replace `localhost` with its LAN address. Use the Navidrome URL in Feishin.

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

Read the backup runbook and create a disaster backup before importing irreplaceable media. Use `./denyra update` for later releases and `./denyra rollback` only when you intend to discard state written after an update.
