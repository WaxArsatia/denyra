# Update Denyra

## Update

Run from a clean checkout on its normal branch:

```sh
./denyra update
```

The command refuses tracked or staged source changes. It fetches and fast-forwards the current branch, renders Compose, pulls upstream images, and builds Denyra images while the active stack remains online.

After every candidate image is ready, Denyra atomically records the selected commit and image tag. Compose then recreates changed services without stopping the whole project. Health wait and smoke checks cover both custom services, Lidarr, slskd, SFTPGo, Navidrome, and the protected filesystem mounts.

If a failure occurs before activation, the release environment and running containers stay unchanged. If recreation or smoke fails after activation, Denyra keeps the selected commit and candidate containers. The failure output reports `phase`, `affected`, `deployed_commit`, a service log command, and `retry=./denyra update`.

Run the reported log command, fix the selected release, then run `./denyra update` again. An equal Git commit is not treated as complete until the current stack passes health and smoke checks. A successful update removes only unreferenced images labeled `io.denyra.project=denyra`; it never runs a global Docker prune.

## Preservation boundary

Update does not reset, replace, truncate, or delete these paths:

- `library` and `library-unmanaged`
- `state`
- `incoming` and `processing`
- `quarantine`
- unresolved `downloads/slskd` and `downloads/spotiflac`

Pipeline `.migration-backups/<candidateID>` directories remain part of media mutation recovery. They are not lifecycle artifacts.

## One-time legacy cleanup

After the forward-only release passes production acceptance:

```sh
./denyra cleanup legacy-lifecycle
```

The command may list only `${DENYRA_HOME}/updates`, `${DENYRA_DATA_ROOT}/backups`, and `${DENYRA_SECRETS_DIR}/restic_password`. Check the printed paths, then type `DELETE`. Any other input cancels the cleanup. The command does not inspect or remove an external Restic repository.

Use `./denyra status` and `./denyra logs` after an update. `./denyra credentials` shows generated login locations without changing them.
