# Restore and cutover

Denyra restores into a new directory. The restore scripts never overwrite `/data`, remove the current library, or change Compose mounts.

## Prepare

Set an exact Restic snapshot ID, the external repository path, its password file, and a new absolute target. The target must be empty and must not be `/`, the current user's home, the repository workspace, or the live data directory.

```sh
export DENYRA_RESTORE_SNAPSHOT=<snapshot-id>
export DENYRA_RESTIC_REPOSITORY_PATH=/mnt/denyra-restic
export DENYRA_RESTIC_PASSWORD_FILE=/run/secrets/denyra-restic-password
export DENYRA_RESTORE_TARGET=/srv/denyra-restore-20260824
scripts/restore/restore.sh
```

The script checks the Restic repository, restores with content verification, installs the SQLite online-backup copies into the new state tree, and runs the Denyra verifier. The verifier checks every stored file hash, both SQLite databases, migration checksums, the dependency lock, ownership, modes, canonical paths, and the same-filesystem layout.

Review `restore-report.json` and `cutover-report.md` under the restored `workspace/<backup-id>` directory. Run the final check before changing mounts:

```sh
scripts/restore/cutover-check.sh
```

## Manual cutover

Stop the stateful services. Change `DENYRA_DATA_ROOT` or the bind mounts to the verified `source` tree, then start the services and check both readiness endpoints plus Navidrome playback. Keep the previous tree mounted nowhere but otherwise untouched until the new deployment has passed its observation period.

Rollback is a mount change. Stop the services, point the mounts back to the untouched prior tree, and start them again. Do not copy files back over either tree.
