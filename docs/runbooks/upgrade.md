# Upgrade and rollback

Dependency changes are explicit. Do not change a version, digest, registry commit, extension, runtime, or generated asset without updating `dependencies.lock.json` and running its compatibility tests.

## Verify an update

Keep the previous lock file and create a review record containing `lock_sha256`, `reviewer`, and an RFC 3339 `reviewed_at` timestamp. Point the verifier at an empty evidence directory:

```sh
export DENYRA_UPGRADE_BASE_LOCK=/srv/denyra-evidence/prior-dependencies.lock.json
export DENYRA_UPGRADE_APPROVAL_FILE=/srv/denyra-evidence/update-approval.json
export DENYRA_UPGRADE_EVIDENCE_DIR=/srv/denyra-evidence/update-20260824
scripts/upgrade/verify-update.sh
```

The verifier checks the dependency lock, the hash-locked Python graph, generated templ files, provider compatibility, the complete Go test suite, image labels, platform, and Compose. It builds the custom and derived images, resolves their local immutable digests, and updates `deploy/images.lock.json` plus Compose. Review those generated changes before deployment.

## Deploy

Run a normal Denyra backup and restore it into a new test tree with the candidate images. Set `DENYRA_VERIFIED_BACKUP_DIR` to the backup workspace and `DENYRA_UPGRADE_RESTORE_TARGET` to the verified restored tree. The deployment command refuses to continue if the backup, lock, image, migration, or restore identities differ.

```sh
export DENYRA_VERIFIED_BACKUP_DIR=/data/backups/<backup-id>
export DENYRA_UPGRADE_RESTORE_TARGET=/srv/denyra-upgrade-smoke
scripts/upgrade/deploy.sh
```

The script applies the candidate migrations to the restored test databases first. It then deploys only the digests recorded in `deploy/images.lock.json`, waits for service health, and runs contract and acceptance smoke tests. Preserve the evidence directory, prior image digests, and prior backup.

## Roll back

Set the evidence directory, the prior backup workspace, and the current data root, then run `scripts/upgrade/rollback.sh`. The script compares the complete migration ledger in both custom SQLite databases.

If the ledgers match, it selects `BINARY_ONLY` and starts the exact prior Compose snapshot. If they differ, it selects `RESTORE_DATABASE_TREE`, stops, and directs the operator to the restore runbook. It never writes an older database over the live tree.
