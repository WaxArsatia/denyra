# Acceptance evidence

The deployment workflow is checked through automated integration tests and disposable Compose acceptance stacks.

## Covered operations

- a clean `./denyra setup` creates external config, secrets, data, and service accounts
- a second setup reconciles without resetting accounts or secrets
- update pulls and builds before stopping the active stack
- an unhealthy candidate restores the prior config, state, and six running image IDs
- update snapshots exclude the library and retain failed candidate state
- backup includes the external recovery set and excludes downloads, cache, and temporary data
- restore verifies file checksums, both Denyra databases, migration ledgers, ownership, and filesystem layout

The forced-update acceptance test uses a local temporary Git remote and an acceptance-only Compose health failure. It does not add a production switch that can disable healthchecks.

## Developer verification

```sh
make verify
go test ./tests/integration/operations -count=1
DENYRA_ACCEPTANCE_COMPOSE=1 go test ./tests/acceptance -run 'Setup|FailedUpdateRestoresPriorStateAndImages' -count=1
scripts/check-runbooks.sh
```

External acquisition providers remain outside deterministic acceptance because they create third-party side effects. Local fixtures cover their orchestration boundaries.
