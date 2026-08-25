# Caddy Edge Gateway Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create a standalone, modular Caddy Git repository on production and publish Navidrome at `https://denyra.denis.my.id` without losing direct LAN access on TCP port `4533`.

**Architecture:** Caddy runs from `/home/nirwana/caddy` as an independent Compose and Git project. Its configuration imports reusable snippets and one site file per backend; Caddy reaches Navidrome over the dedicated internal network `caddy_navidrome`, while its default network provides ACME egress. Future apex domains, direct subdomains, and unrelated domains use additional site files and isolated backend networks without Docker socket discovery.

**Tech Stack:** Docker Engine 29.7.2, Docker Compose 5.5.0, official Caddy `2.11.4-alpine` image pinned by digest, Caddyfile, POSIX shell, Git, OpenSSL, curl.

**Spec:** `docs/superpowers/specs/2026-08-25-caddy-navidrome-edge-design.md`

## Global Constraints

- Keep all Caddy runtime configuration in the standalone Git repository `/home/nirwana/caddy`; do not add Caddy configuration to the Denyra repository.
- Initialize branch `main`, create local commits, configure no remote, and perform no push; the user will connect and push the separate repository later.
- Publish Navidrome at exactly `denyra.denis.my.id`; preserve direct LAN HTTP access on host TCP port `4533`.
- Support apex domains such as `example.com`, direct subdomains such as `abc.example.com`, and hostnames under unrelated registrable domains; do not require nested names below `denyra.denis.my.id`.
- Use exactly `docker.io/library/caddy:2.11.4-alpine@sha256:5f5c8640aae01df9654968d946d8f1a56c497f1dd5c5cda4cf95ab7c14d58648` on `linux/amd64`.
- Publish TCP 80, TCP 443, and UDP 443; never publish Caddy administration port `2019`.
- Give each backend a dedicated internal Docker network; initial network `caddy_navidrome` may contain only Caddy and Navidrome.
- Do not give Caddy a Docker socket, Docker API access, host gateway alias, writable host bind mount, application secret, or Denyra network membership.
- Keep HTTP access logging disabled. Rotate Docker operational logs at three files of 10 MiB each.
- Persist Caddy state in named volumes `caddy_data` and `caddy_config`; never use `docker compose down -v`.
- Atomically change only `DENYRA_COMPOSE_OVERRIDE` in `/srv/denyra/config/denyra.env`; preserve every unrelated line and retain a timestamped backup.
- Ignore host-specific rollback state in Git and never commit environment files, backups, certificate data, credentials, or generated evidence.
- Treat the deployment as incomplete until public TLS, LAN access, external port boundaries, client playback, isolation, persistence, and rollback are verified.

---

## File Map

| Path | Responsibility |
| --- | --- |
| `/home/nirwana/caddy/compose.yml` | Pinned Caddy runtime, ports, hardening, health check, volumes, logs, and backend networks |
| `/home/nirwana/caddy/config/Caddyfile` | Loopback admin option and ordered imports only |
| `/home/nirwana/caddy/config/snippets/security.caddy` | Reusable security response headers |
| `/home/nirwana/caddy/config/sites/navidrome.caddy` | Navidrome hostname, encoding, upstream, and active health probe |
| `/home/nirwana/caddy/integrations/denyra.compose.yml` | Attaches only Denyra Navidrome to `caddy_navidrome` |
| `/home/nirwana/caddy/scripts/validate.sh` | Fails closed on image, Compose, imports, domain-shape fixture, integration, logging, or syntax drift |
| `/home/nirwana/caddy/scripts/reload.sh` | Validates, then atomically reloads the running Caddy configuration |
| `/home/nirwana/caddy/.gitignore` | Excludes rollback state, backups, generated evidence, and editor artifacts |
| `/home/nirwana/caddy/README.md` | Complete operator runbook, including future service workflow |
| `/home/nirwana/caddy/denyra-compose-override.previous` | Ignored host-specific prior override value used for rollback |
| `/srv/denyra/config/denyra.env` | Existing Denyra environment; only its override setting changes |

## Task 1: Build the standalone repository bundle in bounded staging

**Files:**

- Create temporarily: `/tmp/denyra-caddy-stage/compose.yml`
- Create temporarily: `/tmp/denyra-caddy-stage/config/Caddyfile`
- Create temporarily: `/tmp/denyra-caddy-stage/config/snippets/security.caddy`
- Create temporarily: `/tmp/denyra-caddy-stage/config/sites/navidrome.caddy`
- Create temporarily: `/tmp/denyra-caddy-stage/integrations/denyra.compose.yml`
- Create temporarily: `/tmp/denyra-caddy-stage/scripts/validate.sh`
- Create temporarily: `/tmp/denyra-caddy-stage/scripts/reload.sh`
- Create temporarily: `/tmp/denyra-caddy-stage/.gitignore`
- Create temporarily: `/tmp/denyra-caddy-stage/README.md`

**Interfaces:**

- Consumes: approved spec, Caddy image identity, Denyra Compose service name `navidrome`, and container port `4533`.
- Produces: an exact nine-file portable bundle for Task 2; staging is not a Git repository and is deleted after installation.

- [ ] **Step 1: Establish an empty, bounded staging root**

Run:

```sh
rtk test ! -e /tmp/denyra-caddy-stage
rtk install -d -m 0700 /tmp/denyra-caddy-stage/config/snippets /tmp/denyra-caddy-stage/config/sites /tmp/denyra-caddy-stage/integrations /tmp/denyra-caddy-stage/scripts
```

Expected: both commands exit `0`; no existing directory is reused or overwritten.

- [ ] **Step 2: Create the hardened Compose model**

Use `apply_patch` to create `/tmp/denyra-caddy-stage/compose.yml` with exactly:

```yaml
name: caddy

services:
  caddy:
    image: docker.io/library/caddy:2.11.4-alpine@sha256:5f5c8640aae01df9654968d946d8f1a56c497f1dd5c5cda4cf95ab7c14d58648
    platform: linux/amd64
    restart: unless-stopped
    read_only: true
    tmpfs:
      - /tmp:rw,noexec,nosuid,size=64m
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    cap_add:
      - NET_BIND_SERVICE
    ports:
      - name: http
        target: 80
        published: "80"
        protocol: tcp
      - name: https
        target: 443
        published: "443"
        protocol: tcp
      - name: http3
        target: 443
        published: "443"
        protocol: udp
    volumes:
      - type: bind
        source: ./config
        target: /etc/caddy
        read_only: true
      - type: volume
        source: caddy_data
        target: /data
      - type: volume
        source: caddy_config
        target: /config
    networks:
      default: {}
      caddy_navidrome: {}
    healthcheck:
      test: ["CMD", "wget", "-q", "-O", "/dev/null", "http://127.0.0.1:2019/config/"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 20s
    stop_grace_period: 30s
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"

volumes:
  caddy_data:
    name: caddy_data
  caddy_config:
    name: caddy_config

networks:
  caddy_navidrome:
    name: caddy_navidrome
    external: true
```

- [ ] **Step 3: Create modular Caddy configuration**

Use `apply_patch` to create `/tmp/denyra-caddy-stage/config/Caddyfile`:

```caddyfile
{
	admin 127.0.0.1:2019
}

import snippets/*.caddy
import sites/*.caddy
```

Create `/tmp/denyra-caddy-stage/config/snippets/security.caddy`:

```caddyfile
(common_security) {
	header {
		Strict-Transport-Security "max-age=31536000"
		X-Content-Type-Options "nosniff"
		Referrer-Policy "strict-origin-when-cross-origin"
		-Server
	}
}
```

Create `/tmp/denyra-caddy-stage/config/sites/navidrome.caddy`:

```caddyfile
denyra.denis.my.id {
	import common_security
	encode zstd gzip

	reverse_proxy navidrome:4533 {
		health_uri /ping
		health_interval 30s
		health_timeout 5s
		health_fails 3
		health_passes 2
	}
}
```

Do not add a `log` directive.

- [ ] **Step 4: Create the Denyra integration**

Use `apply_patch` to create `/tmp/denyra-caddy-stage/integrations/denyra.compose.yml`:

```yaml
services:
  navidrome:
    networks:
      caddy_navidrome:
        aliases:
          - navidrome

networks:
  caddy_navidrome:
    name: caddy_navidrome
    external: true
```

- [ ] **Step 5: Create the fail-closed validation script**

Use `apply_patch` to create `/tmp/denyra-caddy-stage/scripts/validate.sh`:

```sh
#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
expected_image='docker.io/library/caddy:2.11.4-alpine@sha256:5f5c8640aae01df9654968d946d8f1a56c497f1dd5c5cda4cf95ab7c14d58648'
denyra_root=/home/nirwana/pribadi/denyra
denyra_env=/srv/denyra/config/denyra.env

cd "$repo_root"

sh -n scripts/validate.sh scripts/reload.sh
docker compose --project-name caddy config --quiet

actual_images=$(docker compose --project-name caddy config --images | sort -u)
[ "$actual_images" = "$expected_image" ] || {
  printf 'unexpected Caddy image: %s\n' "$actual_images" >&2
  exit 1
}

docker buildx imagetools inspect "$expected_image" | grep -F 'linux/amd64' >/dev/null
version=$(docker run --rm --platform linux/amd64 --read-only --cap-drop ALL --entrypoint caddy "$expected_image" version)
case "$version" in
  v2.11.4*) ;;
  *) printf 'unexpected Caddy version: %s\n' "$version" >&2; exit 1 ;;
esac

docker run --rm \
  --platform linux/amd64 \
  --read-only \
  --cap-drop ALL \
  --workdir /etc/caddy \
  --volume "$repo_root/config:/etc/caddy:ro" \
  --entrypoint caddy \
  "$expected_image" \
  validate --config /etc/caddy/Caddyfile --adapter caddyfile

if grep -R -n -E '^[[:space:]]*log([[:space:]]|\{|$)' config; then
  printf 'HTTP access logging must remain disabled\n' >&2
  exit 1
fi

if docker compose --project-name caddy config | grep -F '/var/run/docker.sock'; then
  printf 'Docker socket mount is forbidden\n' >&2
  exit 1
fi

if [ -d "$repo_root/.git" ]; then
  if git ls-files | grep -E '(^|/)(denyra-compose-override\.previous|[^/]*\.env|\.env[^/]*|[^/]*\.pem|[^/]*\.key)$'; then
    printf 'host state or secret-like file is tracked\n' >&2
    exit 1
  fi
  if git grep -n -E 'BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY'; then
    printf 'private key marker found in tracked content\n' >&2
    exit 1
  fi
fi

fixture_dir=$(mktemp -d)
trap 'rm -rf -- "$fixture_dir"' EXIT HUP INT TERM
mkdir -p "$fixture_dir/snippets" "$fixture_dir/sites"
cp config/snippets/security.caddy "$fixture_dir/snippets/security.caddy"
printf '%s\n' \
  '{' \
  '  admin 127.0.0.1:2019' \
  '}' \
  '' \
  'import snippets/*.caddy' \
  'import sites/*.caddy' > "$fixture_dir/Caddyfile"
printf '%s\n' \
  'example.com, abc.example.com {' \
  '  import common_security' \
  '  reverse_proxy example-backend:8080' \
  '}' \
  '' \
  'example.net {' \
  '  import common_security' \
  '  reverse_proxy unrelated-backend:9090' \
  '}' > "$fixture_dir/sites/domain-shapes.caddy"

docker run --rm \
  --platform linux/amd64 \
  --read-only \
  --cap-drop ALL \
  --workdir /etc/caddy \
  --volume "$fixture_dir:/etc/caddy:ro" \
  --entrypoint caddy \
  "$expected_image" \
  validate --config /etc/caddy/Caddyfile --adapter caddyfile

if [ -f "$denyra_root/deploy/compose.yaml" ] && [ -f "$denyra_env" ]; then
  docker compose \
    --project-name denyra \
    --env-file "$denyra_env" \
    --file "$denyra_root/deploy/compose.yaml" \
    --file "$repo_root/integrations/denyra.compose.yml" \
    config --quiet
fi

printf 'Caddy repository validation passed\n'
```

The temporary fixture is validation-only. `caddy validate` parses apex, direct-subdomain, and unrelated-domain shapes without starting a server or requesting certificates.

- [ ] **Step 6: Create the validate-before-reload script**

Use `apply_patch` to create `/tmp/denyra-caddy-stage/scripts/reload.sh`:

```sh
#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_root"

"$repo_root/scripts/validate.sh"

caddy_id=$(docker compose --project-name caddy ps -q caddy)
[ -n "$caddy_id" ] || {
  printf 'Caddy is not running\n' >&2
  exit 1
}

docker compose --project-name caddy exec -T caddy \
  caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile
```

- [ ] **Step 7: Create Git exclusions**

Use `apply_patch` to create `/tmp/denyra-caddy-stage/.gitignore`:

```gitignore
denyra-compose-override.previous
*.bak
*.tmp
*.env
.env*
*.pem
*.key
validation-evidence/
.DS_Store
*~
```

- [ ] **Step 8: Write the complete operator runbook**

Use `apply_patch` to create `/tmp/denyra-caddy-stage/README.md`. It must contain these sections and exact commands:

````markdown
# Caddy edge gateway

This standalone repository publishes services through Caddy. The initial site exposes Navidrome at `https://denyra.denis.my.id`; LAN clients may continue using `http://server-lan-address:4533`.

HTTP access logging is intentionally disabled because request query strings may contain authentication material. Operational logs are available through Docker and are rotated.

## Validate

```sh
cd /home/nirwana/caddy
./scripts/validate.sh
```

## Start and status

```sh
cd /home/nirwana/pribadi/denyra
./denyra start
cd /home/nirwana/caddy
docker compose --project-name caddy up -d --wait
docker compose --project-name caddy ps
```

## Reload site-only changes

```sh
cd /home/nirwana/caddy
./scripts/reload.sh
```

Adding or removing a backend network requires `docker compose --project-name caddy up -d --wait` after validation instead of reload alone.

## Operational logs

```sh
cd /home/nirwana/caddy
docker compose --project-name caddy logs --tail 200 caddy
```

## Add a service

1. Configure and verify its public A record. Add AAAA only when IPv6 reaches this host reliably.
2. Choose either an apex hostname such as `example.com` or a direct subdomain such as `abc.example.com`. Do not add an extra service label below an existing service hostname.
3. Create a dedicated internal Docker network, for example `caddy_example`.
4. Add that external network to the Caddy service in `compose.yml`.
5. Attach only the intended backend through its owning project's Compose override.
6. Add one focused file under `config/sites/`. One file may list both `example.com` and `abc.example.com` when they intentionally serve the same backend.
7. Run `./scripts/validate.sh`, review `git diff`, and commit before activation.
8. Run `docker compose --project-name caddy up -d --wait` when network membership changed; otherwise run `./scripts/reload.sh`.
9. Verify DNS, trusted TLS, redirects, headers, backend health, operational logs, and network membership.

Never attach unrelated backends to one shared proxy network. Never add the Docker socket or use label-based discovery.

## Update Caddy

Resolve a reviewed stable Caddy release and its official multi-platform digest. Replace the exact version and digest in `compose.yml` and the expected identity in `scripts/validate.sh`. Validate, review the diff, commit, reconcile Compose, and repeat public TLS, isolation, credential-safety, persistence, and rollback checks. Never use `latest`, an unpinned major tag, Watchtower, or another automatic updater.

## Roll back a committed Caddy change

```sh
cd /home/nirwana/caddy
git show --stat --oneline HEAD
git revert --no-edit HEAD
./scripts/validate.sh
docker compose --project-name caddy up -d --wait
```

These commands revert the latest reviewed commit. Use `./scripts/reload.sh` instead of Compose reconciliation only when network membership did not change. Do not use `git reset`, manual file replacement, or `docker compose down -v`.

## Roll back the initial Denyra integration

```sh
cd /home/nirwana/caddy
docker compose --project-name caddy down
```

Atomically restore only the value recorded in `denyra-compose-override.previous` to `DENYRA_COMPOSE_OVERRIDE` in `/srv/denyra/config/denyra.env`. Preserve every other line, then run:

```sh
cd /home/nirwana/pribadi/denyra
./denyra start
curl --fail --silent --show-error http://127.0.0.1:4533/ping >/dev/null
```

Keep `caddy_data`, `caddy_config`, and this repository. Remove a dedicated backend network only after both containers detach and no retry is planned.
````

- [ ] **Step 9: Set staging modes and run static checks**

Run:

```sh
rtk chmod 0750 /tmp/denyra-caddy-stage/scripts/validate.sh /tmp/denyra-caddy-stage/scripts/reload.sh
rtk find /tmp/denyra-caddy-stage -type f -print | rtk sort
rtk sh -n /tmp/denyra-caddy-stage/scripts/validate.sh /tmp/denyra-caddy-stage/scripts/reload.sh
rtk docker compose --project-name caddy --file /tmp/denyra-caddy-stage/compose.yml config --quiet
rtk docker compose --project-name denyra --env-file .denyra-home/config/denyra.env --file deploy/compose.yaml --file /tmp/denyra-caddy-stage/integrations/denyra.compose.yml config --quiet
```

Expected: exactly nine files are listed; shell syntax is valid; both Compose models render successfully.

- [ ] **Step 10: Validate the image, imports, and domain-shape fixture locally**

Run:

```sh
rtk /tmp/denyra-caddy-stage/scripts/validate.sh
```

Expected: image manifest includes `linux/amd64`; runtime version begins `v2.11.4`; both real and fixture Caddy configurations are valid; output ends `Caddy repository validation passed`.

No Git commit follows this task because staging is transient and must never join the Denyra repository.

## Task 2: Bootstrap the standalone Git repository on production

**Files:**

- Create: all nine tracked files under `/home/nirwana/caddy`
- Create: `/home/nirwana/caddy/.git/`

**Interfaces:**

- Consumes: exact validated staging bundle from Task 1 and SSH alias `production`.
- Produces: clean standalone branch `main` with one initial commit, no remote, and no runtime change yet.

- [ ] **Step 1: Reconfirm production preconditions without mutation**

Run:

```sh
rtk ssh production 'cd /home/nirwana/pribadi/denyra && ./denyra status'
rtk ssh production 'curl --fail --silent --show-error http://127.0.0.1:4533/ping >/dev/null'
rtk ssh production 'ss -H -lntup "sport = :80 or sport = :443"'
rtk ssh production 'find /home/nirwana/caddy -mindepth 1 -maxdepth 1 -print'
rtk ssh production 'test -n "$(git -C /home/nirwana/pribadi/denyra config --get user.name)" && test -n "$(git -C /home/nirwana/pribadi/denyra config --get user.email)"'
rtk dig +short A denyra.denis.my.id
rtk dig +short AAAA denyra.denis.my.id
```

Expected: Denyra is healthy; direct `/ping` succeeds; ports and Caddy directory checks print nothing; Git identity exists; A prints only `103.80.81.35`; AAAA prints nothing. Stop and reconcile if any assumption changed.

- [ ] **Step 2: Inspect possible conflicting Docker state**

Run:

```sh
rtk ssh production 'docker ps -a --filter label=com.docker.compose.project=caddy --format "{{.Names}} {{.Status}}"'
rtk ssh production 'docker volume ls --filter name=^caddy_data$ --filter name=^caddy_config$ --format "{{.Name}}"'
rtk ssh production 'if docker network inspect caddy_navidrome >/dev/null 2>&1; then docker network inspect caddy_navidrome --format "driver={{.Driver}} internal={{.Internal}} containers={{len .Containers}}"; else printf "%s\n" absent; fi'
```

Expected: no Caddy container or named volume exists; network is absent. An existing network is acceptable only when output is exactly `driver=bridge internal=true containers=0`; otherwise stop without replacing it.

- [ ] **Step 3: Copy the exact staging bundle through a bounded remote directory**

Run:

```sh
rtk ssh production 'test ! -e /tmp/denyra-caddy-stage && install -d -m 0700 /tmp/denyra-caddy-stage/config/snippets /tmp/denyra-caddy-stage/config/sites /tmp/denyra-caddy-stage/integrations /tmp/denyra-caddy-stage/scripts'
rtk scp /tmp/denyra-caddy-stage/compose.yml /tmp/denyra-caddy-stage/.gitignore /tmp/denyra-caddy-stage/README.md production:/tmp/denyra-caddy-stage/
rtk scp /tmp/denyra-caddy-stage/config/Caddyfile production:/tmp/denyra-caddy-stage/config/
rtk scp /tmp/denyra-caddy-stage/config/snippets/security.caddy production:/tmp/denyra-caddy-stage/config/snippets/
rtk scp /tmp/denyra-caddy-stage/config/sites/navidrome.caddy production:/tmp/denyra-caddy-stage/config/sites/
rtk scp /tmp/denyra-caddy-stage/integrations/denyra.compose.yml production:/tmp/denyra-caddy-stage/integrations/
rtk scp /tmp/denyra-caddy-stage/scripts/validate.sh /tmp/denyra-caddy-stage/scripts/reload.sh production:/tmp/denyra-caddy-stage/scripts/
```

Expected: every command exits `0`; no wildcard or unrelated file is transferred.

- [ ] **Step 4: Install the repository tree with bounded permissions**

Run:

```sh
rtk ssh production 'set -eu
install -d -m 0750 /home/nirwana/caddy/config/snippets /home/nirwana/caddy/config/sites /home/nirwana/caddy/integrations /home/nirwana/caddy/scripts
install -m 0640 /tmp/denyra-caddy-stage/compose.yml /home/nirwana/caddy/compose.yml
install -m 0640 /tmp/denyra-caddy-stage/.gitignore /home/nirwana/caddy/.gitignore
install -m 0640 /tmp/denyra-caddy-stage/README.md /home/nirwana/caddy/README.md
install -m 0644 /tmp/denyra-caddy-stage/config/Caddyfile /home/nirwana/caddy/config/Caddyfile
install -m 0644 /tmp/denyra-caddy-stage/config/snippets/security.caddy /home/nirwana/caddy/config/snippets/security.caddy
install -m 0644 /tmp/denyra-caddy-stage/config/sites/navidrome.caddy /home/nirwana/caddy/config/sites/navidrome.caddy
install -m 0640 /tmp/denyra-caddy-stage/integrations/denyra.compose.yml /home/nirwana/caddy/integrations/denyra.compose.yml
install -m 0750 /tmp/denyra-caddy-stage/scripts/validate.sh /home/nirwana/caddy/scripts/validate.sh
install -m 0750 /tmp/denyra-caddy-stage/scripts/reload.sh /home/nirwana/caddy/scripts/reload.sh
diff -qr /tmp/denyra-caddy-stage /home/nirwana/caddy'
```

Expected: exit `0`; installed content exactly matches staging before `.git` exists.

- [ ] **Step 5: Validate production files before initializing Git**

Run:

```sh
rtk ssh production 'cd /home/nirwana/caddy && ./scripts/validate.sh'
```

Expected: output ends `Caddy repository validation passed`. Validation may pull the pinned image but does not start Caddy, create certificates, or bind public ports.

- [ ] **Step 6: Initialize and commit the standalone repository**

Run:

```sh
rtk ssh production 'set -eu
cd /home/nirwana/caddy
git init -b main
git config user.name "$(git -C /home/nirwana/pribadi/denyra config --get user.name)"
git config user.email "$(git -C /home/nirwana/pribadi/denyra config --get user.email)"
git add .gitignore README.md compose.yml config integrations scripts
git diff --cached --check
test "$(git diff --cached --name-only | wc -l)" -eq 9
git commit -m "feat: add modular Caddy edge gateway"
test -z "$(git status --porcelain)"
test "$(git branch --show-current)" = main
test -z "$(git remote)"'
```

Expected: one root commit is created on `main` with the existing Denyra operator identity copied into repository-local Git config; exactly nine paths are tracked; status is clean; no remote exists.

- [ ] **Step 7: Remove only both bounded staging trees**

Run:

```sh
rtk ssh production 'test -d /tmp/denyra-caddy-stage && test "$(find /tmp/denyra-caddy-stage -type f | wc -l)" -eq 9 && rm -rf -- /tmp/denyra-caddy-stage'
rtk test -d /tmp/denyra-caddy-stage
rtk test "$(rtk find /tmp/denyra-caddy-stage -type f | rtk wc -l)" -eq 9
rtk rm -rf -- /tmp/denyra-caddy-stage
```

Expected: only the two exact staging roots are removed. `/home/nirwana/caddy` and its `.git` directory remain.

The root commit in the standalone Caddy repository is this task's commit. Do not commit or push any Caddy file from the Denyra repository.

## Task 3: Integrate Navidrome through its dedicated network

**Files:**

- Create ignored: `/home/nirwana/caddy/denyra-compose-override.previous`
- Modify atomically: `/srv/denyra/config/denyra.env`
- Create: one timestamped `/srv/denyra/config/denyra.env.*.bak`

**Interfaces:**

- Consumes: committed Caddy repository and `integrations/denyra.compose.yml`.
- Produces: internal network `caddy_navidrome`, durable Denyra override, and healthy Navidrome alias `navidrome`.

- [ ] **Step 1: Create or strictly revalidate the backend network**

Run:

```sh
rtk ssh production 'set -eu
if docker network inspect caddy_navidrome >/dev/null 2>&1; then
  test "$(docker network inspect caddy_navidrome --format "{{.Driver}}")" = bridge
  test "$(docker network inspect caddy_navidrome --format "{{.Internal}}")" = true
  test "$(docker network inspect caddy_navidrome --format "{{len .Containers}}")" = 0
else
  docker network create --driver bridge --internal caddy_navidrome >/dev/null
fi
test "$(docker network inspect caddy_navidrome --format "{{.Driver}} {{.Internal}}")" = "bridge true"'
```

Expected: `caddy_navidrome` exists as an empty internal bridge.

- [ ] **Step 2: Revalidate Caddy and the merged Denyra model**

Run:

```sh
rtk ssh production 'cd /home/nirwana/caddy && ./scripts/validate.sh'
```

Expected: exit `0`; the integration override renders without changing runtime state.

- [ ] **Step 3: Capture the prior override value without printing other settings**

Run:

```sh
rtk ssh production 'set -eu
env_file=/srv/denyra/config/denyra.env
previous_file=/home/nirwana/caddy/denyra-compose-override.previous
count=$(sed -n "/^DENYRA_COMPOSE_OVERRIDE=/p" "$env_file" | wc -l)
test "$count" -le 1
sed -n "s/^DENYRA_COMPOSE_OVERRIDE=//p" "$env_file" > "$previous_file"
chmod 0640 "$previous_file"
test "$(wc -l < "$previous_file")" -le 1
cd /home/nirwana/caddy
git check-ignore -q denyra-compose-override.previous
test -z "$(git status --porcelain)"'
```

Expected: the ignored file contains only the previous value, possibly empty; Git remains clean; no environment content is printed.

- [ ] **Step 4: Back up and atomically set the absolute integration path**

Run:

```sh
rtk ssh production 'set -eu
env_file=/srv/denyra/config/denyra.env
new_value=/home/nirwana/caddy/integrations/denyra.compose.yml
stamp=$(date -u +%Y%m%dT%H%M%SZ)
backup="${env_file}.${stamp}.bak"
tmp=$(mktemp --tmpdir=/srv/denyra/config denyra.env.caddy.XXXXXX)
trap "rm -f -- \"$tmp\"" EXIT HUP INT TERM
cp --preserve=mode,ownership,timestamps -- "$env_file" "$backup"
awk -v value="$new_value" '\''
  BEGIN { replaced = 0 }
  /^DENYRA_COMPOSE_OVERRIDE=/ {
    if (!replaced) print "DENYRA_COMPOSE_OVERRIDE=" value
    replaced = 1
    next
  }
  { print }
  END { if (!replaced) print "DENYRA_COMPOSE_OVERRIDE=" value }
'\'' "$env_file" > "$tmp"
chmod --reference="$env_file" "$tmp"
chown --reference="$env_file" "$tmp"
mv -- "$tmp" "$env_file"
trap - EXIT HUP INT TERM
test "$(sed -n "/^DENYRA_COMPOSE_OVERRIDE=\/home\/nirwana\/caddy\/integrations\/denyra.compose.yml$/p" "$env_file" | wc -l)" -eq 1
printf "%s\n" "$backup"'
```

Expected: exactly one backup path is printed; the override occurs once; no unrelated line is printed or replaced.

- [ ] **Step 5: Capture Denyra container identities and reconcile through `./denyra`**

Run:

```sh
rtk ssh production 'docker ps --no-trunc --filter label=com.docker.compose.project=denyra --format "{{.Label \"com.docker.compose.service\"}} {{.ID}}" | sort > /tmp/denyra-caddy-container-ids.before && test -s /tmp/denyra-caddy-container-ids.before'
rtk ssh production 'cd /home/nirwana/pribadi/denyra && ./denyra start'
```

Expected: the identity snapshot is nonempty; Denyra start exits `0` after all health checks pass.

- [ ] **Step 6: Prove only Navidrome was recreated**

Run:

```sh
rtk ssh production 'set -eu
docker ps --no-trunc --filter label=com.docker.compose.project=denyra --format "{{.Label \"com.docker.compose.service\"}} {{.ID}}" | sort > /tmp/denyra-caddy-container-ids.after
awk '\''
  NR == FNR { before[$1] = $2; seen_before[$1] = 1; next }
  {
    seen_after[$1] = 1
    if (!seen_before[$1]) bad = 1
    if ($1 == "navidrome") {
      if (before[$1] == $2) bad = 1
    } else if (before[$1] != $2) {
      bad = 1
    }
  }
  END {
    for (service in seen_before) if (!seen_after[service]) bad = 1
    exit bad
  }
'\'' /tmp/denyra-caddy-container-ids.before /tmp/denyra-caddy-container-ids.after
cd /home/nirwana/pribadi/denyra
./denyra status
curl --fail --silent --show-error http://127.0.0.1:4533/ping >/dev/null'
```

Expected: only Navidrome has a new container ID; all Denyra services are healthy; host port `4533` still answers.

- [ ] **Step 7: Verify the Navidrome-only network state**

Run:

```sh
rtk ssh production 'set -eu
test "$(docker network inspect caddy_navidrome --format "{{len .Containers}}")" -eq 1
docker network inspect caddy_navidrome --format "{{range .Containers}}{{println .Name}}{{end}}"
navidrome_id=$(docker ps --filter label=com.docker.compose.project=denyra --filter label=com.docker.compose.service=navidrome --format "{{.ID}}")
test -n "$navidrome_id"
docker inspect "$navidrome_id" --format "{{json .NetworkSettings.Networks.caddy_navidrome.Aliases}}" | grep -F '"navidrome"' >/dev/null
cd /home/nirwana/caddy
test -z "$(git status --porcelain)"'
```

Expected: only Navidrome is attached at this stage; alias includes `navidrome`; standalone repository remains clean.

No Git commit follows this task because only ignored host state and external runtime state changed.

## Task 4: Start Caddy and verify the public and private boundaries

**Files:**

- Read only: committed standalone Caddy repository

**Interfaces:**

- Consumes: healthy Navidrome on `caddy_navidrome`, correct DNS A record, and router forwarding.
- Produces: trusted public HTTPS, preserved LAN access, and verified container isolation.

- [ ] **Step 1: Start Caddy and wait for health**

Run:

```sh
rtk ssh production 'cd /home/nirwana/caddy && docker compose --project-name caddy up -d --wait && docker compose --project-name caddy ps'
```

Expected: Caddy reports `running` and `healthy`; no restart loop occurs.

- [ ] **Step 2: Verify published ports and hidden administration API**

Run:

```sh
rtk ssh production 'set -eu
cd /home/nirwana/caddy
caddy_id=$(docker compose --project-name caddy ps -q caddy)
test -n "$caddy_id"
docker port "$caddy_id"
if docker inspect "$caddy_id" --format "{{json .NetworkSettings.Ports}}" | grep -F '2019'; then exit 1; fi
ss -H -lnt "sport = :80 or sport = :443"
ss -H -lnu "sport = :443"'
```

Expected: Docker publishes `80/tcp`, `443/tcp`, and `443/udp`; port `2019` is absent.

- [ ] **Step 3: Verify redirect, trusted certificate, SAN, and Navidrome health publicly**

Run outside the production SSH session:

```sh
rtk curl --silent --show-error --max-time 20 --output /dev/null --write-out '%{http_code} %{redirect_url}\n' http://denyra.denis.my.id
rtk curl --fail --silent --show-error --max-time 20 --output /dev/null --write-out '%{http_code}\n' https://denyra.denis.my.id/ping
rtk openssl s_client -connect denyra.denis.my.id:443 -servername denyra.denis.my.id -verify_return_error </dev/null
rtk sh -c 'openssl s_client -connect denyra.denis.my.id:443 -servername denyra.denis.my.id </dev/null 2>/dev/null | openssl x509 -noout -subject -issuer -dates -ext subjectAltName'
```

Expected: HTTP redirects to the HTTPS hostname; `/ping` returns `200`; OpenSSL reports `Verify return code: 0 (ok)`; SAN contains `DNS:denyra.denis.my.id`; validity dates include the current time.

- [ ] **Step 4: Verify shared security headers**

Run:

```sh
rtk curl --silent --show-error --max-time 20 --dump-header - --output /dev/null https://denyra.denis.my.id/
```

Expected: headers include HSTS `max-age=31536000`, `X-Content-Type-Options: nosniff`, and `Referrer-Policy: strict-origin-when-cross-origin`; no `Server` header appears.

- [ ] **Step 5: Prove direct LAN access remains available**

Discover the server's LAN address:

```sh
rtk ssh production 'ip -4 route get 1.1.1.1 | sed -n "s/.* src \([^ ]*\).*/\1/p"'
```

From a different machine on that LAN, run `curl --fail --silent --show-error http://server-lan-address:4533/ping >/dev/null`, replacing `server-lan-address` with the address printed by the preceding command.

Expected: exit `0`. Production-local `http://127.0.0.1:4533/ping` must also remain successful.

- [ ] **Step 6: Prove public TCP `4533` is closed from an external network**

On the external probe machine, run:

```sh
rtk curl --fail --silent --show-error --ipv4 https://api.ipify.org
rtk dig +short A denyra.denis.my.id
rtk nc -vz -w 5 denyra.denis.my.id 4533
```

Expected: the probe public address differs from `103.80.81.35`, and TCP connection fails or times out. If addresses match, repeat through mobile data or another controlled external host; a NAT hairpin result is not acceptance evidence.

- [ ] **Step 7: Prove dedicated network and DNS isolation**

Run:

```sh
rtk ssh production 'set -eu
cd /home/nirwana/caddy
test "$(docker network inspect caddy_navidrome --format "{{len .Containers}}")" -eq 2
docker network inspect caddy_navidrome --format "{{range .Containers}}{{println .Name}}{{end}}" | sort
docker compose --project-name caddy exec -T caddy nslookup navidrome >/dev/null
for name in lidarr acquisition-gateway media-pipeline slskd sftpgo; do
  if docker compose --project-name caddy exec -T caddy nslookup "$name" >/dev/null 2>&1; then
    printf "unexpected DNS visibility: %s\n" "$name" >&2
    exit 1
  fi
done'
```

Expected: exactly Caddy and Navidrome are listed; `navidrome` resolves; every other Denyra name fails to resolve.

- [ ] **Step 8: Verify container hardening and log rotation**

Run:

```sh
rtk ssh production 'set -eu
cd /home/nirwana/caddy
caddy_id=$(docker compose --project-name caddy ps -q caddy)
docker inspect "$caddy_id" --format "readonly={{.HostConfig.ReadonlyRootfs}} caps_add={{json .HostConfig.CapAdd}} caps_drop={{json .HostConfig.CapDrop}} security={{json .HostConfig.SecurityOpt}} log={{.HostConfig.LogConfig.Type}} options={{json .HostConfig.LogConfig.Config}}"
if docker inspect "$caddy_id" --format "{{range .Mounts}}{{println .Source .Destination .RW}}{{end}}" | grep -F docker.sock; then exit 1; fi
docker compose --project-name caddy exec -T caddy sh -eu -c "if touch /denyra-root-write-test 2>/dev/null; then exit 1; fi; touch /tmp/denyra-tmp-write-test; rm /tmp/denyra-tmp-write-test"'
```

Expected: root is read-only; only `NET_BIND_SERVICE` is added; `ALL` is dropped; `no-new-privileges:true` is present; log options are `10m` and `3`; no Docker socket appears; root write fails and `/tmp` write succeeds.

- [ ] **Step 9: Prove HTTP access logging remains disabled at runtime**

Run:

```sh
probe_marker=denyra-fake-auth-marker-20260825
rtk curl --fail --silent --show-error --output /dev/null "https://denyra.denis.my.id/ping?u=${probe_marker}&p=${probe_marker}"
rtk ssh production 'cd /home/nirwana/caddy && docker compose --project-name caddy logs --no-color caddy' | rtk grep -F "$probe_marker"
```

Expected: request succeeds; grep exits `1` and prints nothing. The fixed marker is synthetic and is never used as a real credential.

- [ ] **Step 10: Inspect operational logs and Git state**

Run:

```sh
rtk ssh production 'cd /home/nirwana/caddy && docker compose --project-name caddy logs --no-color --tail 200 caddy'
rtk ssh production 'docker logs --tail 200 "$(docker ps --filter label=com.docker.compose.project=denyra --filter label=com.docker.compose.service=navidrome --format "{{.ID}}")"'
rtk ssh production 'cd /home/nirwana/caddy && test -z "$(git status --porcelain)" && git log -1 --oneline && test -z "$(git remote)"'
```

Expected: no ACME, certificate, health-loop, or restart-loop error remains; repository is clean with one local commit and no remote.

No Git commit follows this task because committed configuration did not change.

## Task 5: Prove persistence and Git-based configuration rollback

**Files:**

- Temporarily modify and restore through Git: `/home/nirwana/caddy/config/sites/navidrome.caddy`

**Interfaces:**

- Consumes: trusted healthy deployment from Task 4.
- Produces: evidence that volumes, certificate identity, atomic reload, and `git revert` preserve service state.

- [ ] **Step 1: Capture named-volume identity and certificate serial**

Run:

```sh
rtk ssh production 'docker volume inspect caddy_data caddy_config --format "{{.Name}} {{.CreatedAt}}" | sort > /tmp/denyra-caddy-volumes.before'
rtk sh -c 'openssl s_client -connect denyra.denis.my.id:443 -servername denyra.denis.my.id </dev/null 2>/dev/null | openssl x509 -noout -serial' > /tmp/denyra-caddy-cert.before
```

Expected: volume snapshot contains exactly two lines; certificate output begins `serial=`.

- [ ] **Step 2: Recreate Caddy without deleting volumes**

Run:

```sh
rtk ssh production 'set -eu
cd /home/nirwana/caddy
docker compose --project-name caddy down
docker volume inspect caddy_data caddy_config >/dev/null
docker compose --project-name caddy up -d --wait'
```

Expected: both volumes exist between down and up; Caddy becomes healthy. The command contains no `-v`.

- [ ] **Step 3: Verify volume and certificate identity after recreation**

Run:

```sh
rtk ssh production 'docker volume inspect caddy_data caddy_config --format "{{.Name}} {{.CreatedAt}}" | sort > /tmp/denyra-caddy-volumes.after && diff -u /tmp/denyra-caddy-volumes.before /tmp/denyra-caddy-volumes.after'
rtk sh -c 'openssl s_client -connect denyra.denis.my.id:443 -servername denyra.denis.my.id </dev/null 2>/dev/null | openssl x509 -noout -serial' > /tmp/denyra-caddy-cert.after
rtk diff -u /tmp/denyra-caddy-cert.before /tmp/denyra-caddy-cert.after
rtk curl --fail --silent --show-error --output /dev/null https://denyra.denis.my.id/ping
```

Expected: diffs are empty; HTTPS succeeds.

- [ ] **Step 4: Commit and activate a harmless configuration comment**

Copy the committed file into a bounded local staging path:

```sh
rtk test ! -e /tmp/denyra-caddy-rollback-test.caddy
rtk scp production:/home/nirwana/caddy/config/sites/navidrome.caddy /tmp/denyra-caddy-rollback-test.caddy
```

Use `apply_patch` locally to add this first line to `/tmp/denyra-caddy-rollback-test.caddy`:

```caddyfile
# Navidrome public edge site.
```

Install only that staged file, validate, commit, activate, and remove the bounded staging copies:

```sh
rtk scp /tmp/denyra-caddy-rollback-test.caddy production:/tmp/denyra-caddy-rollback-test.caddy
rtk ssh production 'install -m 0644 /tmp/denyra-caddy-rollback-test.caddy /home/nirwana/caddy/config/sites/navidrome.caddy'
rtk ssh production 'set -eu
cd /home/nirwana/caddy
./scripts/validate.sh
git add config/sites/navidrome.caddy
git diff --cached --check
git commit -m "test: exercise Caddy config rollback"
./scripts/reload.sh'
rtk curl --fail --silent --show-error --output /dev/null https://denyra.denis.my.id/ping
rtk ssh production 'rm -f -- /tmp/denyra-caddy-rollback-test.caddy'
rtk rm -f -- /tmp/denyra-caddy-rollback-test.caddy
```

Expected: validation, commit, atomic reload, and HTTPS all succeed; only the two exact temporary files are removed.

- [ ] **Step 5: Revert the committed comment and reload atomically**

Run:

```sh
rtk ssh production 'set -eu
cd /home/nirwana/caddy
git revert --no-edit HEAD
./scripts/validate.sh
./scripts/reload.sh
test -z "$(git status --porcelain)"'
rtk curl --fail --silent --show-error --output /dev/null https://denyra.denis.my.id/ping
rtk sh -c 'openssl s_client -connect denyra.denis.my.id:443 -servername denyra.denis.my.id </dev/null 2>/dev/null | openssl x509 -noout -serial' > /tmp/denyra-caddy-cert.reverted
rtk diff -u /tmp/denyra-caddy-cert.before /tmp/denyra-caddy-cert.reverted
```

Expected: `git revert` creates an inverse commit; final file content equals the root commit; Git is clean; HTTPS works; certificate serial is unchanged.

The standalone repository receives the test commit and its revert commit. Do not squash, reset, or push them; they are auditable rollback evidence for user review.

## Task 6: Rehearse the Denyra integration rollback and restore final state

**Files:**

- Read ignored: `/home/nirwana/caddy/denyra-compose-override.previous`
- Modify twice atomically: `/srv/denyra/config/denyra.env`

**Interfaces:**

- Consumes: recorded previous override value and persistent Caddy volumes.
- Produces: proven initial-integration rollback followed by the final accepted deployment.

- [ ] **Step 1: Use one bounded atomic setting function for both directions**

Start one production SSH shell and define:

```sh
set_denyra_override() {
  env_file=/srv/denyra/config/denyra.env
  value=$1
  tmp=$(mktemp --tmpdir=/srv/denyra/config denyra.env.caddy.XXXXXX) || return 1
  awk -v value="$value" '
    BEGIN { replaced = 0 }
    /^DENYRA_COMPOSE_OVERRIDE=/ {
      if (!replaced) print "DENYRA_COMPOSE_OVERRIDE=" value
      replaced = 1
      next
    }
    { print }
    END { if (!replaced) print "DENYRA_COMPOSE_OVERRIDE=" value }
  ' "$env_file" > "$tmp" || { rm -f -- "$tmp"; return 1; }
  chmod --reference="$env_file" "$tmp" || { rm -f -- "$tmp"; return 1; }
  chown --reference="$env_file" "$tmp" || { rm -f -- "$tmp"; return 1; }
  mv -- "$tmp" "$env_file"
}
```

Expected: defining the function prints no environment content.

- [ ] **Step 2: Stop Caddy without deleting state and restore the prior override**

In the same production shell, run:

```sh
cd /home/nirwana/caddy
docker compose --project-name caddy down
docker volume inspect caddy_data caddy_config >/dev/null
previous_value=$(sed -n '1p' denyra-compose-override.previous)
test "$(wc -l < denyra-compose-override.previous)" -le 1
set_denyra_override "$previous_value"
```

Expected: Caddy stops; volumes remain; only the Denyra override value returns to its recorded prior value.

- [ ] **Step 3: Reconcile rollback state and prove LAN continuity**

Run in the same production shell:

```sh
cd /home/nirwana/pribadi/denyra
./denyra start
./denyra status
curl --fail --silent --show-error http://127.0.0.1:4533/ping >/dev/null
test "$(docker network inspect caddy_navidrome --format '{{len .Containers}}')" -eq 0
```

Expected: Denyra is healthy; direct port `4533` works; `caddy_navidrome` is empty.

- [ ] **Step 4: Reapply the approved integration and restore Caddy**

Run in the same production shell:

```sh
set_denyra_override /home/nirwana/caddy/integrations/denyra.compose.yml
cd /home/nirwana/pribadi/denyra
./denyra start
./denyra status
cd /home/nirwana/caddy
./scripts/validate.sh
docker compose --project-name caddy up -d --wait
docker compose --project-name caddy ps
```

Expected: Denyra and Caddy are healthy; the dedicated network again contains only Caddy and Navidrome.

- [ ] **Step 5: Repeat final TLS, port, network, Git, and client acceptance**

Run:

```sh
rtk curl --fail --silent --show-error --output /dev/null https://denyra.denis.my.id/ping
rtk sh -c 'openssl s_client -connect denyra.denis.my.id:443 -servername denyra.denis.my.id </dev/null 2>/dev/null | openssl x509 -noout -serial' > /tmp/denyra-caddy-cert.final
rtk diff -u /tmp/denyra-caddy-cert.before /tmp/denyra-caddy-cert.final
rtk ssh production 'set -eu
test "$(docker network inspect caddy_navidrome --format "{{len .Containers}}")" -eq 2
curl --fail --silent --show-error http://127.0.0.1:4533/ping >/dev/null
cd /home/nirwana/pribadi/denyra
./denyra status
cd /home/nirwana/caddy
docker compose --project-name caddy ps
./scripts/validate.sh
test -z "$(git status --porcelain)"
test -z "$(git remote)"'
```

Repeat the genuine external TCP `4533` test from Task 4. Configure one OpenSubsonic client with base URL `https://denyra.denis.my.id`, then verify authentication, library browsing, artwork, seeking, and playback without printing credentials.

Expected: original certificate serial remains; HTTPS and LAN access work; public TCP `4533` remains closed; every service is healthy; Caddy Git is clean and unpushed; all client actions succeed.

- [ ] **Step 6: Remove only temporary evidence files**

Run:

```sh
rtk ssh production 'rm -f -- /tmp/denyra-caddy-container-ids.before /tmp/denyra-caddy-container-ids.after /tmp/denyra-caddy-volumes.before /tmp/denyra-caddy-volumes.after'
rtk rm -f -- /tmp/denyra-caddy-cert.before /tmp/denyra-caddy-cert.after /tmp/denyra-caddy-cert.reverted /tmp/denyra-caddy-cert.final
```

Expected: only named temporary evidence is removed. Repository, ignored rollback value, env backup, network, volumes, and certificates remain.

- [ ] **Step 7: Run both final repository gates**

Run:

```sh
rtk make verify
rtk git status --short --branch
rtk ssh production 'cd /home/nirwana/caddy && ./scripts/validate.sh && git status --short --branch && git log -3 --oneline && test -z "$(git remote)"'
```

Expected: Denyra verification exits `0`; Denyra contains only its committed design/plan history; Caddy validation exits `0`; Caddy branch is `main`, clean, and has exactly the root, rollback-test, and revert commits; no remote exists.

## Completion Gate

- [ ] Caddy configuration exists only in the standalone `/home/nirwana/caddy` Git repository; Denyra contains only design and implementation-plan documents.
- [ ] Standalone branch `main` is clean, has local commits, contains no ignored host state or secret, and has no remote or push.
- [ ] Modular imports validate the Navidrome site plus apex, direct-subdomain, and unrelated-domain fixture shapes.
- [ ] Public HTTP redirects to trusted HTTPS for `denyra.denis.my.id`; SAN and validity are correct.
- [ ] Navidrome `/ping`, UI, authentication, browsing, artwork, seeking, and playback work over HTTPS.
- [ ] LAN TCP `4533` remains reachable; an external probe shows public TCP `4533` closed.
- [ ] TCP 80, TCP 443, and UDP 443 are published; administration port `2019` is not published.
- [ ] `caddy_navidrome` is an internal bridge containing exactly Caddy and Navidrome.
- [ ] Caddy resolves `navidrome` but cannot resolve Lidarr, gateway, pipeline, slskd, or SFTPGo.
- [ ] Caddy has a read-only root, bounded writable `/tmp`, `no-new-privileges`, only `NET_BIND_SERVICE`, no Docker socket, and rotated operational logs.
- [ ] HTTP access logging remains disabled and the fake authentication marker never appears in Caddy logs.
- [ ] `caddy_data`, `caddy_config`, and certificate identity survive down/up, atomic reload, Git revert, Denyra integration rollback, and final restore.
- [ ] `DENYRA_COMPOSE_OVERRIDE` points exactly to `/home/nirwana/caddy/integrations/denyra.compose.yml`; only the prior value is stored in the ignored rollback file.
- [ ] Both `rtk make verify` and `/home/nirwana/caddy/scripts/validate.sh` pass from the final clean state.
