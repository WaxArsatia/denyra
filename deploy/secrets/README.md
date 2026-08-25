# Denyra deployment secrets

`./denyra setup` creates these files under `$DENYRA_HOME/secrets`:

```text
internal_bearer
audit_key
bootstrap_admin
navidrome_admin
sftpgo_admin
sftpgo_upload
slskd_api_key
slskd_web_password
soulseek_username
soulseek_password
```

The setup command generates values with the host CSPRNG and adopts existing,
nonempty legacy files when present. Lidarr's API key is copied from its
persistent `config.xml` after first startup. Secret values never belong in Git,
TOML, images, command arguments, logs, or audit rows.

The external secret directory uses mode `0700`; files use `0600`. Plain Docker
Compose file-backed secrets preserve host permissions. Never commit generated
deployment state or copy it back into this directory.
