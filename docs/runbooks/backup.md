# Backup operations

The supported backup path is the optional Restic Compose profile. The repository must be on another filesystem or host. `/data/backups` is temporary workspace, not the disaster-recovery repository.

Set the data root, external repository, Restic password file, and internal bearer file. Then run:

```sh
export DENYRA_DATA_ROOT=/data
export DENYRA_RESTIC_REPOSITORY_PATH=/mnt/denyra-restic
export DENYRA_RESTIC_PASSWORD_FILE=/run/secrets/denyra-restic-password
export DENYRA_INTERNAL_BEARER_FILE=/run/secrets/denyra-internal-bearer
scripts/backup/backup.sh
```

The script enters maintenance, waits for active leases and unresolved effects to drain, and briefly stops Lidarr, Navidrome, SFTPGo, and slskd. Gateway and pipeline databases are copied through SQLite's online backup API. The script then stores checksums, configuration and build identities, runs Restic verification, applies retention (`7 daily`, `4 weekly`, `12 monthly`), restarts the stopped services, and leaves maintenance.

Raw downloads are excluded. Library, state, incoming, processing, quarantine, and the verified custom database copies are included. A successful run removes its temporary workspace. A failed run preserves the workspace path printed on stderr.

After failure, do not delete that evidence. Check service state, free space, the maintenance responses, and the Restic repository. Correct the cause, confirm no active mutation is stuck, then rerun with a new backup ID.

Run the restore drill in `docs/runbooks/restore.md` after initial setup. Repeat it whenever database migrations, the manifest schema, included paths, ownership policy, or backup tooling changes.
