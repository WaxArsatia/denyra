# Incident handling

Preserve service state, SQLite databases, quarantine content, and logs before changing files. Start with:

```sh
./denyra status
./denyra logs
```

## Failed update

An unhealthy candidate should roll back automatically before `./denyra update` exits. Check the newest directory under the deployment's `updates` directory. A rolled-back snapshot contains prior config and state plus `failed-config` and `failed-state` from the candidate.

Do not prune the prior image IDs referenced by that snapshot. If automatic rollback reports a missing image, recover that exact image before retrying.

## Low disk

Denyra stops new claims, acquisitions, and imports when free space falls below its admission threshold. Remove confirmed disposable downloads or cache. Do not remove active processing work, quarantine evidence, SQLite WAL files, update snapshots needed for rollback, or unverified backup workspaces.

## Database corruption

Stop stateful services if they still respond. Preserve the database and WAL files and follow the restore runbook into a new directory. Do not run an ad hoc repair against the only copy.

## External provider outage

MusicBrainz, LRCLIB, Soulseek, and fallback provider failures appear as degraded dependencies. They do not make local readiness fail. Leave queued work in place and retry after the provider recovers.

## Stuck media work

Restart the owning Denyra service once, then inspect its logs and durable state. Do not move an orphan processing directory until it has been matched to a candidate. Keep ambiguous or partially mutated releases in quarantine.

## Account or secret exposure

Revoke sessions and change affected passwords. If an internal bearer or audit secret may be exposed, stop the custom services, replace that secret, restart, and retain the audit trail. Use `./denyra credentials` only on the trusted host.
