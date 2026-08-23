# Denyra deployment secrets

Create these files before starting Compose:

```text
internal_bearer
audit_key
bootstrap_admin
lidarr_api_key
soulseek_username
soulseek_password
restic_password
```

Secret values never belong in Git, TOML, environment variables, logs, or audit
rows. Generate bearer, audit, API, and password material with a CSPRNG. The
`bootstrap_admin` file is consumed only on first run and is not a permanent
authentication path.

Set every file to mode `0400` when owned by the consuming service UID. Use mode
`0440` only when sharing through the consuming service GID. Plain Docker Compose
file-backed secrets preserve the host file permissions; Compose `uid`, `gid`,
and `mode` fields are not enforced for file sources.
