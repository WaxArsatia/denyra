# Acceptance evidence

This file records the full Denyra acceptance run completed on 2026-08-24. The locked application images were rebuilt before verification, and every required gate exited with status `0` twice from a clean Compose service state.

## Locked identities

- Dependency lock SHA-256: `a207fd601b8ca44c948e39f890085bc4ec8e04b18f7553bc9943ccd22395f730`
- Deployment image lock SHA-256: `55bb14784b7b2af862dae943323a5d67b96d5764492e31c2770e9795fa1751be`
- Acquisition Gateway image: `docker.io/denyra/acquisition-gateway:local@sha256:15de3c7d9fa23f1b46c74e8f3edb0ed331dee74a1b53fe240de65b9043da0ad7`
- Media Pipeline image: `docker.io/denyra/media-pipeline:local@sha256:76995bc1150c6537fd7d9213a4604f812f6c333d4ba5961e12415a7a1f64484d`
- Lidarr derived image: `docker.io/denyra/lidarr:local@sha256:a6a7b287657d71798092aa72821ab903e43a5ddfa2168d61a18bfd2ccb582dbe`
- Navidrome derived image: `docker.io/denyra/navidrome:local@sha256:2abd04221d9848bfe0097836e2a5fdc449bbf37004468a43837a4c3068f2fa40`
- Gateway build provenance SHA-256: `7dcb859f594e2d57a13579a1859d080eb372c284fb3b12a21fb2f97fa22ce26d`
- Pipeline build provenance SHA-256: `50c9acebe4975d2ce88a41e92ce959668334e8fa846a209f2b5a66ccd9a1d510`

## Effective deployment configuration

- Gateway TOML SHA-256: `cb680aef2421f08651fc625f61b46e958838f7aa4216068d6ff22c0afadb0665`
- Pipeline TOML SHA-256: `e9c0ce83618fba668af9dd9897c77dcb3bde6c23755b45310d10b5f05993ccf6`
- Navidrome TOML SHA-256: `f1a3b95f2d46bbb1aa3d0c75c78ce8e6b5903606200b4a7ea807f0a509b908cf`

Jobs and candidates also retain their immutable effective configuration snapshot in the owning SQLite database. These file hashes identify the deployment defaults used by this acceptance run; they do not replace runtime snapshot evidence.

## Commands

```sh
rtk go test -race -count=1 ./...
rtk docker compose -f deploy/compose.yaml -f deploy/compose.acceptance.yaml config --quiet
rtk go test -count=1 ./tests/acceptance -run Denyra
rtk scripts/check-runbooks.sh
rtk scripts/verify-pins/verify.sh --offline
rtk env DENYRA_ACCEPTANCE_COMPOSE=1 go test -count=1 ./tests/acceptance -run TestDenyraPinnedComposeStartsReadyWithLocalAdapters -v
```

The pinned Compose smoke used fake local adapters. The deterministic acceptance suite generated its own non-copyrighted FLAC media for checksum and sidecar checks. The `live-provider-acceptance` profile was intentionally excluded: it is outside automatic acceptance and starts only when `DENYRA_LIVE_PROVIDER_ACCEPTANCE=I_ACCEPT_EXTERNAL_PROVIDER_SIDE_EFFECTS` is supplied explicitly.

## Result

Verification completed at `2026-08-24T04:38:47Z`.

- Race suite, first run: `257` tests passed across `55` packages.
- Race suite, second uncached run: `257` tests passed across `55` packages.
- Deterministic acceptance, both runs: `4` tests passed.
- Pinned Compose smoke, first run: passed in `15.28s`; cleanup completed.
- Pinned Compose smoke, second run: passed in `15.23s`; cleanup completed.
- Compose model, runbook validation, offline pin verification, formatting, `go vet`, shell syntax, templ regeneration, UI asset verification, and token contrast checks: passed.
- Reproducibility audit: two consecutive builds with BuildKit default attestations disabled produced the same Gateway and Pipeline image digests shown above. Verified embedded Denyra build provenance remained identical to the dependency lock.
- Integrated failure matrix covered acquisition retry/no-result separation, correlation, arbitration, handoff, claim, release matching, technical validation, mutation, enrichment, import, admin review, persistence/recovery, admission, backup/restore, and Navidrome behavior.
- Accepted skip: live external providers. This is a deliberate side-effect boundary, not a test failure.

## Plan completion points

- Foundation: `7b527c2` (`ci: verify Denyra foundation invariants`)
- Controlled media pipeline: `0895e47` (`fix(pipeline): execute controlled media workflow`)
- Acquisition orchestration: `d150d54` (`feat(gateway): run recoverable acquisition orchestration`)
- Operations and clients before final acceptance evidence: `f6131b5` (`docs: add Denyra operations and client runbooks`)

The acceptance-evidence commit follows these four completion points and is the final implementation-plan gate.
