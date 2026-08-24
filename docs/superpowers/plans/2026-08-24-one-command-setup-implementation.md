# One-Command Setup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a fresh or existing Denyra deployment reach a healthy, configured stack through `./denyra setup`, with Soulseek credentials as the only required external input.

**Architecture:** A small root POSIX-shell command owns host-side lifecycle, paths, credentials, and Compose invocation. Service-specific reconciliation runs in a one-shot Go binary inside the pipeline image, allowing robust JSON/XML/HTTP handling without requiring Python, Go, Node, or `jq` on the host. Configuration is idempotently rendered under an external deployment root, while existing state and accounts are adopted.

**Tech Stack:** POSIX shell, Go 1.27 standard library, Docker Compose v2, Lidarr API v1, SFTPGo API v2, Navidrome native authentication API.

**Spec:** `plans/001-simplified-local-deployment-design.md`

## Global Constraints

- Require only Git, Docker Engine, and Docker Compose v2 on the host.
- Default `DENYRA_HOME` to `/srv/denyra`; allow explicit absolute `DENYRA_HOME` and legacy-data `DENYRA_DATA_ROOT` overrides.
- Keep the Git checkout separate from generated configuration, secrets, data, and updates.
- Secret directory mode is `0700`, secret files and the credential report are `0600`, and config/data directories are `0750`.
- Never place secret values in command arguments, logs, images, or Git.
- Never overwrite existing nonempty secrets, databases, administrators, or media.
- Use supported configuration files, environment variables, and HTTP APIs; do not automate browser clicks.
- Preserve unrelated Lidarr settings and every existing library/account.
- Use service-name routing; no static container address may be introduced.
- This plan is phase 2 of 3. Complete `2026-08-24-simplified-build-runtime-implementation.md` first and complete this plan before `2026-08-24-simple-update-rollback-backup-implementation.md`.

---

## Confirmed upstream bootstrap interfaces

- slskd accepts YAML at `/app/slskd.yml`, `SLSKD_API_KEY`, `SLSKD_USERNAME`, `SLSKD_PASSWORD`, and Soulseek credentials through environment configuration. The primary API key supports an administrator/read-write role.
- SFTPGo creates the first administrator when `SFTPGO_DATA_PROVIDER__CREATE_DEFAULT_ADMIN=true` and `SFTPGO_DEFAULT_ADMIN_USERNAME`/`SFTPGO_DEFAULT_ADMIN_PASSWORD` are set; this has no effect after an administrator exists. Its admin token endpoint is `GET /api/v2/token` with HTTP Basic authentication, and users are created with `POST /api/v2/users`.
- Navidrome creates its sole first administrator with `POST /auth/createAdmin` and JSON `{"username":"...","password":"..."}`. It returns `403` when any user already exists; that response means adopt the existing account and do not mutate it.
- Lidarr exposes its API key in persistent `/config/config.xml`; required settings use `/api/v1/rootfolder`, `/api/v1/config/downloadclient`, `/api/v1/config/mediamanagement`, `/api/v1/config/naming`, `/api/v1/metadata`, `/api/v1/downloadclient[/schema]`, and `/api/v1/indexer[/schema]`.
- Lidarr.Plugin.Slskd requires both a download client and an indexer against the same slskd instance and API key.

Implementation references:

- slskd configuration and authentication: `https://github.com/slskd/slskd/blob/master/docs/config.md`
- SFTPGo first-admin configuration: `https://docs.sftpgo.com/latest/config-file/`
- SFTPGo REST user creation: `https://docs.sftpgo.com/latest/rest-api/`
- Navidrome authentication routes: `https://github.com/navidrome/navidrome/blob/master/server/server.go` and `https://github.com/navidrome/navidrome/blob/master/server/auth.go`
- Lidarr.Plugin.Slskd settings: `https://github.com/allquiet-hub/Lidarr.Plugin.Slskd/blob/main/README.md`, `src/Lidarr.Plugin.Slskd/Download/Clients/Slskd/SlskdSettings.cs`, and `src/Lidarr.Plugin.Slskd/Indexers/Slskd/SlskdSettings.cs` in that repository

## File map

- `denyra`: stable root command and subcommand dispatcher.
- `scripts/manage/common.sh`: validated deployment context, locking, atomic files, secret generation, and the single Compose wrapper.
- `scripts/manage/setup.sh`: idempotent first-run orchestration.
- `scripts/manage/smoke.sh`: service readiness and endpoint contract checks.
- `scripts/manage/credentials.sh`: owner-only credential report display.
- `deploy/config/slskd.yml`: non-secret slskd paths and webhook contract.
- `deploy/scripts/slskd-secret-entrypoint.sh`: map slskd secret files to supported environment variables.
- `deploy/scripts/sftpgo-secret-entrypoint.sh`: map first-admin secret files to SFTPGo bootstrap variables.
- `cmd/denyra-reconcile/main.go`: one-shot flags, secret loading, HTTP clients, and error reporting.
- `internal/reconcile/lidarr.go`: owned Lidarr settings plus slskd plugin resources.
- `internal/reconcile/sftpgo.go`: restricted manual-upload user creation.
- `internal/reconcile/navidrome.go`: first-admin creation or existing-account adoption.
- `internal/reconcile/reconcile.go`: deterministic service order and aggregate result.
- `tests/integration/operations/management_command_test.go`: fake Git/Docker management-command tests.
- `internal/reconcile/*_test.go`: service API contract tests using `httptest.Server`.

### Task 1: Add the root command and one Compose invocation contract

**Files:**
- Create: `denyra`
- Create: `scripts/manage/common.sh`
- Create: `tests/integration/operations/management_command_test.go`
- Create: `.gitignore`
- Test: `tests/integration/operations/management_command_test.go`

**Interfaces:**
- Consumes: optional `DENYRA_HOME`, optional `DENYRA_DATA_ROOT`, and an ignored `.denyra-home` file containing one absolute deployment-home path.
- Produces: `denyra_context`, `denyra_lock`, `denyra_unlock`, `denyra_atomic_file TARGET MODE`, `denyra_secret NAME [BYTES]`, and `denyra_compose ARGS...`; root commands `setup`, `start`, `stop`, `restart`, `status`, `logs`, `update`, `rollback`, `credentials`, and `backup`.

- [ ] **Step 1: Add command dispatch tests with fake executables**

Create a test helper that writes executable fakes into `t.TempDir()/bin`, prepends it to `PATH`, sets `DENYRA_HOME` to another temporary directory, and records each invocation in `DENYRA_TEST_LOG`. Add tests equivalent to:

```go
func TestStatusUsesOneComposeContext(t *testing.T) {
	fixture := newManagementFixture(t)
	fixture.writeExecutable("docker", `#!/bin/sh
printf '%s\n' "$*" >> "$DENYRA_TEST_LOG"
case "$*" in "compose version"*) exit 0;; esac
`)
	fixture.run("status")
	log := fixture.log()
	for _, want := range []string{"compose", "--project-name denyra", "--env-file", "deploy/compose.yaml", "ps"} {
		if !strings.Contains(log, want) {
			t.Fatalf("compose invocation missing %q: %s", want, log)
		}
	}
}

func TestUnknownCommandReturnsUsage(t *testing.T) {
	fixture := newManagementFixture(t)
	result := fixture.runError("unknown")
	if result.ExitCode() != 2 || !strings.Contains(result.Stderr, "usage:") {
		t.Fatalf("unexpected result: %+v", result)
	}
}
```

Also cover a relative `DENYRA_HOME`, an unresolved `~`/`$`, a deployment root equal to `/`, a relative or root `DENYRA_DATA_ROOT`, first setup before the deployment root exists, and a concurrent operation lock.

- [ ] **Step 2: Run the focused test and verify the command is absent**

Run: `go test ./tests/integration/operations -run 'StatusUsesOneComposeContext|UnknownCommand|DeploymentRoot|OperationLock' -count=1`

Expected: FAIL because `denyra` and `scripts/manage/common.sh` do not exist.

- [ ] **Step 3: Implement shared deployment context and locking**

Start `common.sh` with `set -eu`. Resolve `repo_root` from the sourced file, then use:

```sh
denyra_context() {
  if [ -z "${DENYRA_HOME:-}" ] && [ -f "$repo_root/.denyra-home" ]; then
    IFS= read -r DENYRA_HOME < "$repo_root/.denyra-home"
  fi
  DENYRA_HOME=${DENYRA_HOME:-/srv/denyra}
  case "$DENYRA_HOME" in
    /*) ;;
    *) denyra_die "DENYRA_HOME must be an absolute path" ;;
  esac
  case "$DENYRA_HOME" in /|*'$'*|*'~'*) denyra_die "DENYRA_HOME is unsafe";; esac
  DENYRA_CONFIG_DIR=$DENYRA_HOME/config
  DENYRA_SECRETS_DIR=$DENYRA_HOME/secrets
  if [ -z "${DENYRA_DATA_ROOT:-}" ] && [ -f "$DENYRA_CONFIG_DIR/denyra.env" ]; then
    DENYRA_DATA_ROOT=$(sed -n 's/^DENYRA_DATA_ROOT=//p' "$DENYRA_CONFIG_DIR/denyra.env")
    [ "$(sed -n '/^DENYRA_DATA_ROOT=/p' "$DENYRA_CONFIG_DIR/denyra.env" | wc -l)" -eq 1 ] || denyra_die "denyra.env has an invalid data root"
  fi
  DENYRA_DATA_ROOT=${DENYRA_DATA_ROOT:-$DENYRA_HOME/data}
  case "$DENYRA_DATA_ROOT" in
    /*) ;;
    *) denyra_die "DENYRA_DATA_ROOT must be an absolute path" ;;
  esac
  [ "$DENYRA_DATA_ROOT" != / ] || denyra_die "DENYRA_DATA_ROOT cannot be /"
  DENYRA_UPDATES_DIR=$DENYRA_HOME/updates
  export DENYRA_HOME DENYRA_CONFIG_DIR DENYRA_SECRETS_DIR DENYRA_DATA_ROOT DENYRA_UPDATES_DIR
}

denyra_lock() {
  mkdir "$DENYRA_HOME/.operation-lock" 2>/dev/null || denyra_die "another Denyra operation is running"
  trap 'denyra_unlock' EXIT HUP INT TERM
}

denyra_unlock() {
  rmdir "$DENYRA_HOME/.operation-lock" 2>/dev/null || true
  trap - EXIT HUP INT TERM
}

denyra_compose() {
  docker compose --project-name denyra --env-file "$DENYRA_CONFIG_DIR/denyra.env" -f "$repo_root/deploy/compose.yaml" "$@"
}
```

`denyra_atomic_file TARGET MODE` writes stdin to `TARGET.tmp.$$`, applies the supplied numeric mode, then renames it. `denyra_secret NAME BYTES` reads `/dev/urandom` through `od -An -N"$bytes" -tx1 | tr -d ' \n'` only when the target is absent or empty.

- [ ] **Step 4: Implement the root dispatcher**

Use a stable, source-based dispatcher:

```sh
#!/bin/sh
set -eu
repo_root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$repo_root/scripts/manage/common.sh"
denyra_context
command=${1:-}
[ "$#" -eq 0 ] || shift
case "$command" in
  setup) . "$repo_root/scripts/manage/setup.sh"; denyra_setup "$@" ;;
  update|rollback|backup) denyra_lock; . "$repo_root/scripts/manage/$command.sh"; "denyra_$command" "$@" ;;
  start) denyra_compose up -d --remove-orphans --wait ;;
  stop) denyra_compose stop ;;
  restart) denyra_compose restart; denyra_compose up -d --wait ;;
  status) denyra_compose ps ;;
  logs) denyra_compose logs --tail 200 "$@" ;;
  credentials) . "$repo_root/scripts/manage/credentials.sh"; denyra_credentials ;;
  ""|-h|--help|help) denyra_usage ;;
  *) denyra_usage >&2; exit 2 ;;
esac
```

Until later tasks add lifecycle scripts, make recognized handlers whose files are absent return `command unavailable in this checkout` in tests. `denyra_setup` creates or adopts the deployment root first and then calls `denyra_lock`, so a fresh default `/srv/denyra` does not fail before its one allowed sudo operation. Add `.denyra-home` to `.gitignore`.

- [ ] **Step 5: Run command tests**

Run: `go test ./tests/integration/operations -run 'StatusUsesOneComposeContext|UnknownCommand|DeploymentRoot|OperationLock' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the management command foundation**

```bash
git add denyra scripts/manage/common.sh tests/integration/operations/management_command_test.go .gitignore
git commit -m "feat(deploy): add denyra management command"
```

### Task 2: Generate an external, idempotent deployment root

**Files:**
- Create: `scripts/manage/setup.sh`
- Create: `scripts/manage/credentials.sh`
- Modify: `scripts/bootstrap-data-layout.sh`
- Modify: `deploy/compose.yaml`
- Modify: `deploy/secrets/README.md`
- Modify: `tests/integration/operations/management_command_test.go`
- Test: `tests/integration/operations/management_command_test.go`

**Interfaces:**
- Consumes: `DENYRA_HOME`, optional existing `DENYRA_DATA_ROOT`, numeric current UID/GID, optional `DENYRA_SOULSEEK_USERNAME` and `DENYRA_SOULSEEK_PASSWORD` for noninteractive use.
- Produces: `$DENYRA_HOME/config/denyra.env`, copied service TOML, generated secrets, `$DENYRA_HOME/credentials.txt`, and the full data layout.

- [ ] **Step 1: Add empty, repeated, and partial setup tests**

Fake `git` and `docker` while allowing normal `id`, `mkdir`, `od`, `chmod`, and `cp`. Test that the first run creates these nonempty secrets:

```text
internal_bearer
audit_key
bootstrap_admin
navidrome_admin
sftpgo_admin
sftpgo_upload
slskd_api_key
slskd_web_password
restic_password
soulseek_username
soulseek_password
```

Assert `secrets` is `0700`, each file and `credentials.txt` is `0600`, config/data are `0750`, and `denyra.env` contains absolute paths plus current UID/GID. Record every secret, rerun setup, and assert byte-for-byte equality. Delete one generated config file, rerun, and assert the missing file is restored without changing credentials.

- [ ] **Step 2: Run setup tests and verify they fail**

Run: `go test ./tests/integration/operations -run 'SetupCreates|SetupIsIdempotent|SetupResumes' -count=1`

Expected: FAIL because the setup implementation is absent.

- [ ] **Step 3: Generalize the data-layout helper**

Change `scripts/bootstrap-data-layout.sh` to accept any canonical absolute `--target` other than `/`, reject symlinks, and build the existing relative layout under that target. Preserve same-filesystem directory creation and `0750` ownership. Do not require the target to equal `/data`.

- [ ] **Step 4: Implement directory and secret setup**

`denyra_setup` must:

1. check `git --version`, `docker version`, and `docker compose version`;
2. create `/srv/denyra` with one `sudo install -d` only when the current user cannot create it, then never call sudo again;
3. create config, secrets, the selected data root, and updates with the required modes, then acquire the shared operation lock;
4. resolve UID/GID from `id -u`/`id -g`;
5. prompt with terminal echo disabled only when Soulseek environment values and existing files are both absent;
6. create all secrets through `denyra_secret`, except Soulseek values which are written atomically from input;
7. before generating a known secret, adopt a readable nonempty file of the same name from legacy `deploy/secrets` when present, copying it into the external secret directory with mode `0600`;
8. call the generalized layout helper against `DENYRA_DATA_ROOT`;
9. write a credential report containing service, URL, username, and secret-file path, never inline passwords.

After the deployment root exists, atomically write its absolute path to the ignored checkout file `.denyra-home`. This lets later commands recover a nondefault home without repeated environment exports.

Use usernames `admin` for Denyra/Navidrome/SFTPGo and `upload` for the restricted SFTP account. The report directs the operator to `./denyra credentials`, which reads each current value only on demand.

- [ ] **Step 5: Render Compose inputs outside Git**

Copy the tracked TOML templates and the slskd template introduced in Task 3 only when the destination is absent. Atomically render `denyra.env` with:

```text
DENYRA_HOME=/absolute/home
DENYRA_CONFIG_DIR=/absolute/home/config
DENYRA_SECRETS_DIR=/absolute/home/secrets
DENYRA_DATA_ROOT=/absolute/home/data
DENYRA_MEDIA_UID=<uid>
DENYRA_MEDIA_GID=<gid>
DENYRA_IMAGE_TAG=<short-git-commit>
DENYRA_GIT_COMMIT=<full-git-commit>
```

Persist an explicitly supplied legacy `DENYRA_DATA_ROOT` such as `/data`; all later commands load it from this file, so existing libraries and databases remain in place.

Update Compose `configs.file` entries to `${DENYRA_CONFIG_DIR}/...` and every secret file to `${DENYRA_SECRETS_DIR}/...`. No normal command may depend on `deploy/secrets`.

- [ ] **Step 6: Run setup and Compose-path tests**

Run:

```bash
go test ./tests/integration/operations -run 'SetupCreates|SetupIsIdempotent|SetupResumes' -count=1
DENYRA_CONFIG_DIR=$PWD/deploy/config DENYRA_SECRETS_DIR=$PWD/deploy/secrets DENYRA_DATA_ROOT=/data docker compose -f deploy/compose.yaml config --quiet
```

Expected: PASS.

- [ ] **Step 7: Commit external deployment-root setup**

```bash
git add scripts/manage/setup.sh scripts/manage/credentials.sh scripts/bootstrap-data-layout.sh deploy/compose.yaml deploy/secrets/README.md tests/integration/operations/management_command_test.go
git commit -m "feat(setup): generate external deployment state"
```

### Task 3: Configure slskd and bootstrap SFTPGo from secret files

**Files:**
- Create: `deploy/config/slskd.yml`
- Create: `deploy/scripts/sftpgo-secret-entrypoint.sh`
- Modify: `deploy/scripts/slskd-secret-entrypoint.sh`
- Modify: `deploy/compose.yaml`
- Modify: `tests/integration/compose_config_test.go`
- Modify: `tests/integration/operations/management_command_test.go`
- Test: `tests/integration/compose_config_test.go`

**Interfaces:**
- Consumes: Docker secret files for Soulseek, slskd API/UI, and SFTPGo first-admin credentials.
- Produces: slskd YAML with Denyra webhook and paths; exported supported service environment variables without secret values in Compose.

- [ ] **Step 1: Add secret-wrapper and rendered-config tests**

Execute each entrypoint directly with temporary secret files. For slskd, assert the child receives:

```text
SLSKD_SLSK_USERNAME
SLSKD_SLSK_PASSWORD
SLSKD_API_KEY=role=ReadWrite;<generated-key>
SLSKD_USERNAME=admin
SLSKD_PASSWORD=<generated-web-password>
```

For SFTPGo, assert:

```text
SFTPGO_DATA_PROVIDER__CREATE_DEFAULT_ADMIN=true
SFTPGO_DEFAULT_ADMIN_USERNAME=admin
SFTPGO_DEFAULT_ADMIN_PASSWORD=<generated-password>
```

Compose tests must assert neither password nor API-key value appears in rendered environment, Lidarr/slskd remain unpublished in default Compose, and the slskd config mounts at `/app/slskd.yml`.

- [ ] **Step 2: Run focused tests and verify they fail**

Run: `go test ./tests/integration -run 'Slskd|SFTPGo|Compose' -count=1`

Expected: FAIL because wrappers/config are incomplete.

- [ ] **Step 3: Implement a generic file-to-environment helper in each wrapper**

Use this POSIX pattern without printing values:

```sh
load_secret() {
  target=$1
  source=$2
  [ -r "$source" ] || { echo "required secret is unreadable: $source" >&2; exit 1; }
  value=$(tr -d '\r\n' < "$source")
  [ -n "$value" ] || { echo "required secret is empty: $source" >&2; exit 1; }
  export "$target=$value"
}
```

For the slskd API key, prepend `role=ReadWrite;` after reading. The slskd wrapper ends with `exec "$@"`. The SFTPGo wrapper ends with `exec /entrypoint.sh "$@"`, preserving the official image entrypoint and its existing CMD while injecting only the supported bootstrap variables.

- [ ] **Step 4: Add the non-secret slskd configuration**

Create YAML with download root `/data/downloads/slskd`, incomplete root beneath it, directory/file mode `777` for plugin interoperability, and:

```yaml
integrations:
  webhooks:
    denyra_completion:
      on:
        - DownloadFileComplete
      call:
        url: http://acquisition-gateway:8082/events/slskd
        headers:
          - name: User-Agent
            value: slskd
      timeout: 5000
      retry:
        attempts: 3
```

Do not enable remote configuration; Denyra owns the file.

- [ ] **Step 5: Wire service secrets and bootstrap environment through Compose**

Mount the new slskd config, API/UI secret files, and wrapper. Give SFTPGo its wrapper plus admin secret files and keep `CREATE_DEFAULT_ADMIN` out of the static environment. Existing SFTPGo state makes the upstream bootstrap a no-op.

- [ ] **Step 6: Run wrapper and Compose tests**

Run:

```bash
go test ./tests/integration -run 'Slskd|SFTPGo|Compose' -count=1
docker compose -f deploy/compose.yaml config --quiet
```

Expected: PASS.

- [ ] **Step 7: Commit secret-backed upstream configuration**

```bash
git add deploy/config/slskd.yml deploy/scripts/slskd-secret-entrypoint.sh deploy/scripts/sftpgo-secret-entrypoint.sh deploy/compose.yaml tests/integration/compose_config_test.go tests/integration/operations/management_command_test.go
git commit -m "feat(setup): configure slskd and sftpgo headlessly"
```

### Task 4: Add the service configuration reconciler foundation

**Files:**
- Create: `cmd/denyra-reconcile/main.go`
- Create: `cmd/denyra-reconcile/main_test.go`
- Create: `internal/reconcile/reconcile.go`
- Create: `internal/reconcile/reconcile_test.go`
- Modify: `deploy/docker/pipeline.Dockerfile`
- Modify: `deploy/compose.yaml`
- Test: `internal/reconcile/reconcile_test.go`

**Interfaces:**
- Consumes: HTTP service base URLs and secret file paths.
- Produces: `reconcile.Options`, `reconcile.Service{Name, Apply func(context.Context) (reconcile.Outcome, error)}`, `reconcile.Run(ctx, services) ([]Outcome, error)`, and `/app/denyra-reconcile` in the pipeline image.

- [ ] **Step 1: Add deterministic ordering and error tests**

Use outcomes:

```go
type Outcome struct {
	Service string
	Changed bool
	Message string
}
```

Test that `Run` executes `lidarr`, `sftpgo`, then `navidrome`; stops on the first error; wraps it as `reconcile <service>: ...`; and never includes secret values in the returned error.

- [ ] **Step 2: Run the package test and verify it fails**

Run: `go test ./internal/reconcile ./cmd/denyra-reconcile -count=1`

Expected: FAIL because the packages do not exist.

- [ ] **Step 3: Implement the small orchestration package**

Define:

```go
type Service struct {
	Name  string
	Apply func(context.Context) (Outcome, error)
}

func Run(ctx context.Context, services []Service) ([]Outcome, error) {
	outcomes := make([]Outcome, 0, len(services))
	for _, service := range services {
		outcome, err := service.Apply(ctx)
		if err != nil {
			return outcomes, fmt.Errorf("reconcile %s: %w", service.Name, err)
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes, nil
}
```

The command reads every secret using `os.ReadFile`, trims CR/LF, rejects empty values, and passes values only in memory. Flags are service URLs and file paths; do not accept secret-value flags.

- [ ] **Step 4: Build the reconciler into the pipeline image and add a profile service**

Build `/out/denyra-reconcile ./cmd/denyra-reconcile`, copy it to `/app`, and add:

```yaml
reconciler:
  image: denyra/media-pipeline:${DENYRA_IMAGE_TAG:-local}
  profiles: ["setup"]
  entrypoint: ["/app/denyra-reconcile"]
  networks:
    denyra-acquisition: {}
    denyra-import: {}
    denyra-upload: {}
    denyra-playback: {}
  secrets:
    - lidarr_api_key
    - slskd_api_key
    - sftpgo_admin
    - sftpgo_upload
    - navidrome_admin
```

Pass only URL and `/run/secrets/...` path flags in `command`. Give it no data or library mount.

- [ ] **Step 5: Run unit and Compose tests**

Run:

```bash
go test ./internal/reconcile ./cmd/denyra-reconcile -count=1
docker compose -f deploy/compose.yaml --profile setup config --quiet
```

Expected: PASS.

- [ ] **Step 6: Commit the reconciler foundation**

```bash
git add cmd/denyra-reconcile internal/reconcile deploy/docker/pipeline.Dockerfile deploy/compose.yaml
git commit -m "feat(setup): add service reconciliation runner"
```

### Task 5: Reconcile Lidarr's owned contract and slskd plugin resources

**Files:**
- Create: `internal/reconcile/lidarr.go`
- Create: `internal/reconcile/lidarr_test.go`
- Modify: `cmd/denyra-reconcile/main.go`
- Test: `internal/reconcile/lidarr_test.go`

**Interfaces:**
- Consumes: Lidarr API v1, API key, slskd URL/key, and current resource JSON.
- Produces: `Lidarr{BaseURL, APIKey, SlskdURL, SlskdAPIKey, HTTP}.Apply(ctx) (Outcome, error)`.

- [ ] **Step 1: Add an HTTP fixture that represents both fresh and existing Lidarr**

The fixture must serve GET/POST/PUT for the confirmed endpoints. Add assertions that a fresh run creates `/data/library`, disables Completed Download Handling, enables renaming and `lrc,elrc,ttml`, sets approved naming formats, enables Kodi/Emby artist+album images, and creates both Slskd download-client and indexer resources. Run `Apply` twice and assert the second run emits no POST/PUT.

Add a preservation assertion: an unrelated field such as `recycleBinCleanupDays: 31` and any existing non-Slskd client/indexer must be returned unchanged in update payloads.

- [ ] **Step 2: Run the Lidarr tests and verify they fail**

Run: `go test ./internal/reconcile -run 'Lidarr' -count=1`

Expected: FAIL because `Lidarr.Apply` is absent.

- [ ] **Step 3: Implement a strict, bounded Lidarr client**

Use `X-Api-Key`, `Content-Type: application/json`, a 30-second client timeout, `io.LimitReader(response.Body, 8<<20)`, and status-aware errors. Decode mutable resources as `map[string]any` so unknown upstream fields survive a GET-modify-PUT cycle.

Implement helpers with these exact responsibilities:

```go
func (l Lidarr) ensureRootFolder(ctx context.Context) (bool, error)
func (l Lidarr) reconcileSingleton(ctx context.Context, path string, mutate func(map[string]any) bool) (bool, error)
func (l Lidarr) reconcileMetadata(ctx context.Context) (bool, error)
func (l Lidarr) reconcileSlskdDownloadClient(ctx context.Context) (bool, error)
func (l Lidarr) reconcileSlskdIndexer(ctx context.Context) (bool, error)
```

Singleton PUT paths are `/api/v1/config/downloadclient`, `/api/v1/config/mediamanagement`, and `/api/v1/config/naming`. Metadata updates use `/api/v1/metadata/{id}`. Root-folder creation posts only `{"path":"/data/library"}`.

- [ ] **Step 4: Populate plugin resources from upstream schemas**

Read `/api/v1/downloadclient/schema` and `/api/v1/indexer/schema`; select schemas whose `implementation` equals `Slskd`. Mutate schema fields by their `name`:

- download client: `host=slskd`, `port=5030`, `useSsl=false`, `urlBase=""`, `apiKey=<secret>`;
- download client safety: `repairConfiguration=false`, because Denyra owns slskd YAML and leaves remote configuration disabled;
- indexer: `baseUrl=http://slskd:5030/`, `apiKey=<secret>`, `minimumPeerUploadSpeed=0`, `maximumPeerQueueLength=0`, `allowIncompleteReleases=false`, and `verifyDurations=true`.

Set resource names `Denyra slskd` and `Denyra slskd indexer`, enable them, and retain schema-supplied implementation/config-contract/protocol fields. If a named schema field is absent, fail with `Lidarr Slskd schema lacks field <name>`; do not guess a replacement. Existing resources are detected by implementation and updated only when an owned value differs.

- [ ] **Step 5: Run Lidarr reconciliation tests**

Run: `go test ./internal/reconcile -run 'Lidarr' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit Lidarr reconciliation**

```bash
git add internal/reconcile/lidarr.go internal/reconcile/lidarr_test.go cmd/denyra-reconcile/main.go
git commit -m "feat(setup): reconcile lidarr import and acquisition settings"
```

### Task 6: Bootstrap Navidrome and create the restricted SFTP user

**Files:**
- Create: `internal/reconcile/navidrome.go`
- Create: `internal/reconcile/navidrome_test.go`
- Create: `internal/reconcile/sftpgo.go`
- Create: `internal/reconcile/sftpgo_test.go`
- Modify: `cmd/denyra-reconcile/main.go`
- Test: `internal/reconcile/navidrome_test.go`
- Test: `internal/reconcile/sftpgo_test.go`

**Interfaces:**
- Consumes: generated admin/upload credentials and internal service URLs.
- Produces: `Navidrome.Apply(ctx)` and `SFTPGo.Apply(ctx)` with existing-account adoption.

- [ ] **Step 1: Add Navidrome first-user tests**

Test `POST /auth/createAdmin` receives JSON username `admin` and the generated password. Treat `200` as changed. Treat `403` with any body as unchanged/existing. Treat every other non-2xx response as an actionable error with status and a response body capped at 8 KiB. Assert errors never include the password.

- [ ] **Step 2: Add SFTPGo user tests**

Fixture `GET /api/v2/token` with HTTP Basic admin credentials, `GET /api/v2/users/upload`, and `POST /api/v2/users`. For a missing user, assert the payload is:

```json
{
  "status": 1,
  "username": "upload",
  "password": "generated-value",
  "home_dir": "/data/incoming/manual",
  "permissions": {"/": ["*"]},
  "filesystem": {"provider": 0}
}
```

For an existing user, assert no update occurs. A home directory other than `/data/incoming/manual` must fail rather than silently expand access.

- [ ] **Step 3: Run service tests and verify they fail**

Run: `go test ./internal/reconcile -run 'Navidrome|SFTPGo' -count=1`

Expected: FAIL because both clients are absent.

- [ ] **Step 4: Implement Navidrome bootstrap**

Use a JSON POST with no authorization only for `/auth/createAdmin`. Return `Outcome{Service:"navidrome", Changed:true, Message:"created initial administrator"}` on 2xx and `Changed:false` on 403. Never call its authenticated user API, so existing Navidrome databases and users stay untouched.

- [ ] **Step 5: Implement SFTPGo reconciliation**

Obtain a token with `Authorization: Basic ...`, then use `Authorization: Bearer ...`. On user GET, accept 404 as missing; on 200, decode and require the restricted home directory. Create only the missing `upload` user with the tested payload. Do not modify admin permissions, groups, roles, or unrelated users.

- [ ] **Step 6: Wire both services and run all reconciler tests**

Run: `go test ./internal/reconcile ./cmd/denyra-reconcile -count=1`

Expected: PASS.

- [ ] **Step 7: Commit first-account reconciliation**

```bash
git add internal/reconcile/navidrome.go internal/reconcile/navidrome_test.go internal/reconcile/sftpgo.go internal/reconcile/sftpgo_test.go cmd/denyra-reconcile/main.go
git commit -m "feat(setup): bootstrap playback and upload accounts"
```

### Task 7: Orchestrate setup, extract the Lidarr API key, and prove idempotence

**Files:**
- Create: `scripts/manage/smoke.sh`
- Modify: `scripts/manage/setup.sh`
- Modify: `tests/integration/operations/management_command_test.go`
- Modify: `tests/acceptance/denyra_test.go`
- Modify: `deploy/compose.acceptance.yaml`
- Test: `tests/integration/operations/management_command_test.go`
- Test: `tests/acceptance/denyra_test.go`

**Interfaces:**
- Consumes: phase-1 images and all generated configuration/secrets.
- Produces: a complete `denyra_setup` pipeline and `denyra_smoke` health gate.

- [ ] **Step 1: Add orchestration-order and failure tests**

With fake Docker, assert this order:

```text
compose build --pull
compose up -d --wait lidarr slskd sftpgo navidrome
compose --profile setup run --rm reconciler
compose up -d --wait
compose ps
```

Assert a build failure stops before any `up`, a dependency health failure stops before reconciliation, and a reconciliation failure leaves dependency containers running for diagnosis but never starts custom services. Add a fixture `config.xml` and assert only text inside `<ApiKey>...</ApiKey>` reaches `secrets/lidarr_api_key`.

- [ ] **Step 2: Run management tests and verify they fail**

Run: `go test ./tests/integration/operations -run 'SetupBuildOrder|SetupFailure|LidarrAPIKey' -count=1`

Expected: FAIL because setup does not yet orchestrate services.

- [ ] **Step 3: Complete the setup flow**

After Task 2's rendering, export `DENYRA_RELEASE_REFRESH=setup-$(date -u +%Y%m%dT%H%M%SZ)` and run `denyra_compose build --pull`. Start the four upstream services with `--wait --wait-timeout ${DENYRA_WAIT_SECONDS:-180}`.

Poll `$DENYRA_DATA_ROOT/state/lidarr/config.xml` for at most the same timeout. Extract exactly one API key with a line-oriented `sed` expression, require at least 16 visible characters, and atomically write it only when the secret is absent/empty. If a nonempty existing secret differs, fail with `existing Lidarr API key does not match persistent Lidarr state`.

Run `denyra_compose --profile setup run --rm reconciler`, start the default stack with `up -d --remove-orphans --wait`, then source `smoke.sh` and call `denyra_smoke`.

- [ ] **Step 4: Implement the smoke gate**

`denyra_smoke` runs `denyra_compose ps` and requires all default services to be running and healthy. It then executes custom health commands inside their containers and checks upstream health through each service's existing Docker health status. Print only:

```text
Denyra:     http://<host>:8090
Navidrome:  http://<host>:4533
SFTPGo:     http://<host>:8080
SFTP:       <host>:2022
Credentials: ./denyra credentials
```

Use `${DENYRA_DISPLAY_HOST:-localhost}` for display only.

- [ ] **Step 5: Add acceptance coverage for an empty deployment root**

Extend acceptance Compose with fixture upstream APIs rather than real Internet services. Run setup noninteractively with temporary `DENYRA_HOME`, fake Soulseek values, and real Compose. Assert the default stack becomes healthy, required Lidarr mutations were observed, first accounts were created, rerunning setup creates no extra accounts, and library fixture content remains unchanged.

- [ ] **Step 6: Run the complete phase gate**

Run:

```bash
go test ./internal/reconcile ./cmd/denyra-reconcile ./tests/integration/operations -count=1
go test ./tests/integration -run 'Compose|Slskd|SFTPGo' -count=1
go test ./tests/acceptance -run 'Setup' -count=1
git diff --check
```

Expected: PASS.

- [ ] **Step 7: Commit one-command setup**

```bash
git add scripts/manage/setup.sh scripts/manage/smoke.sh tests/integration/operations/management_command_test.go tests/acceptance/denyra_test.go deploy/compose.acceptance.yaml
git commit -m "feat(setup): deliver idempotent one-command deployment"
```
