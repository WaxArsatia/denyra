# Update and rollback

## Update

Run from a clean checkout on its normal branch:

```sh
./denyra update
```

The command refuses tracked or staged source changes. It records the active commit, rendered Compose model, and exact running image IDs before fetching the branch. Pull and build happen while the old stack remains online.

After every candidate image is ready, the command stops the stack and copies only external config and service state into an update snapshot. Library, downloads, processing media, quarantine, and cache are not copied. The candidate starts with a health wait and the same smoke checks used by setup.

If startup or smoke fails before the candidate is declared healthy, Denyra restores config and state and starts the exact prior image IDs. It does not rebuild an approximation from an old tag. Failed candidate trees remain inside the snapshot for diagnosis.

The two newest update snapshots are retained. Docker images referenced by them are not pruned automatically.

## Manual rollback

```sh
./denyra rollback
```

The command selects the newest successful update snapshot and prints both commits. It then asks:

```text
Rollback will discard service-state writes made after this update. Continue? [y/N]
```

Only `y` or `yes` continues. The same exact-image restore path used by automatic rollback is used here. The current Git worktree stays on its present commit; rollback controls active images, config, and service state.

Rollback cannot proceed if a required prior image has been manually deleted. Keep the snapshot and restore that exact image before retrying.

Use `./denyra status` and `./denyra logs` after either operation. `./denyra credentials` shows the generated login locations without changing them.

Update snapshots are not disaster backups. Configure `./denyra backup` separately when the library and long-lived state need recovery protection.
