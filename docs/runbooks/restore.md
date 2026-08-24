# Restore and cutover

Restore always targets a new, empty directory. The scripts do not overwrite the live deployment or change active mounts.

Set an exact Restic snapshot, repository, generated password file, and absolute restore target:

```sh
export DENYRA_RESTORE_SNAPSHOT=<snapshot-id>
export DENYRA_RESTIC_REPOSITORY_PATH=/mnt/denyra-restic
export DENYRA_RESTIC_PASSWORD_FILE=/srv/denyra/secrets/restic_password
export DENYRA_RESTORE_TARGET=/srv/denyra-restore-20260824
scripts/restore/restore.sh
```

The target must be empty and separate from the live deployment. Restore uses content verification and never overwrites existing target files. It installs the consistent SQLite copies into the restored state tree, then verifies:

- every recorded file checksum
- Gateway and Pipeline database integrity and migration ledgers
- Managed and Unmanaged FLAC files plus incomplete uploads and processing state
- the recorded Git commit
- ownership and mode expectations
- canonical paths, no symlinks, and the single-filesystem media layout

Review `restore-report.json` and `cutover-report.md` under `workspace/<backup-id>`. Run the last read-only check before cutover:

```sh
scripts/restore/cutover-check.sh
```

## Manual cutover

Stop the stateful services, point the deployment data root at the verified `source` tree, then start the stack. Check both Navidrome libraries, one upload, unfinished upload sessions, catalog check batches, confirmed migrations, and recent logs. Startup resumes durable nonterminal work but does not create a new catalog check batch. Keep the old tree untouched until the restored deployment has passed its observation period.

To reverse the cutover, stop the stack and point it back to the old tree. Do not copy one tree over the other.
