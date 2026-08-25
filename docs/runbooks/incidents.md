# Incident handling

Preserve service state, SQLite databases, quarantine content, and logs before changing files. Start with:

```sh
./denyra status
./denyra logs
```

## Failed update

Read the phase and service log command printed by `./denyra update`. A failure before activation leaves the running release and release environment unchanged. A failure during recreation or smoke keeps the selected commit and candidate containers for diagnosis.

Fix the candidate and run `./denyra update` again. The command reconciles an unhealthy stack even when Git HEAD already equals the deployed commit. Do not copy database files, replace service state, or start older images as part of this retry.

## Low disk

Denyra stops new claims, acquisitions, and imports when free space falls below its admission threshold. Remove confirmed disposable downloads or cache. Do not remove active processing work, quarantine evidence, SQLite WAL files, or unresolved acquisition directories.

## Database corruption

Stop stateful services if they still respond. Preserve the database and WAL files. Do not run an ad hoc repair against the only copy. This active-development lifecycle has no automated restore command.

## External provider outage

MusicBrainz, LRCLIB, Soulseek, and fallback provider failures appear as degraded dependencies. They do not make local readiness fail. Leave queued work in place. For a failed manual catalog item, use its Retry button after the provider recovers.

## Interrupted browser upload

Open Incoming and use Retry for the failed file. Denyra resumes the durable session and keeps completed files. If finalization fails, check free space and the incoming/uploading path before retrying. Do not rename `.partial` files by hand.

## Missing artwork or Navidrome release

Check the submitted preview and the final release directory first. For an Unmanaged release, confirm that Navidrome scanned its Unmanaged folder. For a Managed release, check the Lidarr Manual Import result and then rescan Navidrome. A destination collision stays blocked for review and must not be overwritten.

## Stuck media work

Restart the owning Denyra service once, then inspect its logs and durable state. Startup resumes unfinished upload finalization, unmanaged imports, explicit catalog checks, and confirmed migrations. It does not start a new catalog check. Do not move an orphan processing directory until it has been matched to a candidate. Keep ambiguous or partially mutated releases in quarantine.

## Account or secret exposure

Revoke sessions and change affected passwords. If an internal bearer or audit secret may be exposed, stop the custom services, replace that secret, restart, and retain the audit trail. Use `./denyra credentials` only on the trusted host.
