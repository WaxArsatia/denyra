# Caddy edge gateway for Navidrome and future services

## Purpose

Production needs a public HTTPS endpoint for Navidrome at `denyra.denis.my.id`. Desktop Linux and mobile OpenSubsonic clients will use this endpoint outside the LAN. Direct LAN access to Navidrome on host port `4533` remains available.

Caddy must also remain practical to operate when additional reverse-proxied services are introduced. Future services may use an apex domain such as `example.com`, a direct subdomain such as `abc.example.com`, or a hostname under a different registrable domain. The design does not require every service to live below `denyra.denis.my.id`.

## Scope

This change creates a standalone Caddy Git repository directly at `/home/nirwana/caddy`. It runs as its own Compose project, contains modular site configuration, and connects Caddy to each backend through a dedicated Docker network.

The initial integration attaches Navidrome to `caddy_navidrome` through a production-only Denyra Compose override. `DENYRA_COMPOSE_OVERRIDE` in `/srv/denyra/config/denyra.env` records the absolute path to that integration file so future Denyra operations preserve the attachment.

The Caddy configuration, scripts, and runbook do not live in the Denyra Git repository. This design document remains in Denyra because it records the approved production integration boundary. The user will configure and push the standalone Caddy repository remote later.

The change does not:

- expose the Denyra administration UI, Lidarr, SFTPGo, slskd, or any pipeline endpoint;
- add another authentication layer in front of Navidrome;
- change Navidrome users, passwords, transcoding settings, or its public host port;
- replace the existing Cloudflare Tunnel container;
- change the current public DNS record, router port forwarding, or host firewall rules;
- install a Docker socket proxy, grant Caddy access to the Docker socket, or enable label-based service discovery;
- use a custom Caddy build or third-party Caddy module;
- create wildcard certificates or nested names such as `service.denyra.denis.my.id`;
- configure future services before their hostnames and backend requirements are known.

## Current production state

The Denyra checkout is `/home/nirwana/pribadi/denyra`, and its persistent deployment root is `/srv/denyra`. Navidrome publishes TCP port `4533` on the host and is attached to Denyra's playback network. The `DENYRA_COMPOSE_OVERRIDE` value is currently empty.

The A record for `denyra.denis.my.id` resolves to `103.80.81.35`, and the name has no AAAA record. TCP ports 80 and 443 are not currently occupied on the production host. Router forwarding for ports 80 and 443 has been configured by the operator. Navidrome's unauthenticated `/ping` endpoint currently returns HTTP 200.

The production directory `/home/nirwana/caddy` exists and is empty. It will become the root of the standalone Git repository and the working directory of the Caddy Compose project.

## Repository structure

The repository has this layout:

```text
/home/nirwana/caddy/
├── compose.yml
├── config/
│   ├── Caddyfile
│   ├── snippets/
│   │   └── security.caddy
│   └── sites/
│       └── navidrome.caddy
├── integrations/
│   └── denyra.compose.yml
├── scripts/
│   ├── validate.sh
│   └── reload.sh
├── .gitignore
└── README.md
```

Each file or directory has one responsibility:

- `compose.yml` defines the Caddy runtime, published ports, persistent volumes, hardening, health check, log rotation, and backend networks.
- `config/Caddyfile` contains only global Caddy options and imports.
- `config/snippets/security.caddy` defines the reusable response-header policy.
- `config/sites/` contains one file per backend service. A service file may declare one or more hostnames when those hostnames intentionally route to the same backend.
- `integrations/` contains Compose overrides used to attach backends managed by other projects. The initial file changes only Denyra's Navidrome service.
- `scripts/validate.sh` validates the complete Compose and Caddy models against the pinned image.
- `scripts/reload.sh` validates first, then atomically reloads a running Caddy instance.
- `README.md` documents DNS prerequisites, adding a backend, validation, start, reload, logs, image update, rollback, and verification.
- `.gitignore` excludes host-specific rollback state, temporary files, editor files, and locally generated evidence.

The host-specific file `denyra-compose-override.previous` is created in the repository root during deployment but is ignored by Git. It contains only the prior override value required by rollback. No secret or environment backup is committed.

The repository uses branch `main`. Initial implementation is committed locally without adding a remote or pushing. All later configuration changes are committed before they are activated in production.

## Domain model

Caddy supports independent hostnames rather than imposing one domain hierarchy. Valid deployment examples include:

- the current Navidrome hostname `denyra.denis.my.id`;
- an apex domain such as `example.com`;
- a direct subdomain such as `abc.example.com`;
- another apex domain or direct subdomain under a different registrable domain.

The phrase "direct subdomain" means one service label immediately below the chosen registrable domain. The design does not create an additional nested label below an existing service hostname. For example, `abc.example.com` is supported, while `api.abc.example.com` is outside the intended naming policy.

This naming policy is reviewed explicitly rather than enforced by counting dots because public suffixes such as `my.id` contain multiple labels. A naive dot-count check would reject valid names or accept invalid nesting.

One site file may list an apex and its direct subdomain when both intentionally serve the same backend:

```caddyfile
example.com, abc.example.com {
	import common_security
	reverse_proxy example-service:8080
}
```

Separate backends use separate site blocks and separate backend networks even when their hostnames share a registrable domain.

Every hostname must have valid public DNS before its site is activated. An A record must point to the forwarded public IPv4 address. An AAAA record may be used only when IPv6 routing, host addressing, and firewall behavior are all verified; a stale or unreachable AAAA record is a deployment failure because some clients will prefer IPv6.

Caddy obtains and renews an individual certificate covering each configured hostname. The deployment does not request a wildcard certificate. A new hostname's DNS or ACME failure must not remove existing certificates or site configuration.

## Modular Caddy configuration

The root `config/Caddyfile` contains the loopback administration address and ordered imports:

```caddyfile
{
	admin 127.0.0.1:2019
}

import snippets/*.caddy
import sites/*.caddy
```

The reusable `common_security` snippet removes Caddy's `Server` response header and adds:

- `Strict-Transport-Security: max-age=31536000`
- `X-Content-Type-Options: nosniff`
- `Referrer-Policy: strict-origin-when-cross-origin`

The snippet does not add a content security policy or frame policy because either could break an application without application-specific testing. A site imports the common policy explicitly, so a reviewer can see whether it receives the standard headers.

The initial Navidrome site file owns only Navidrome behavior:

- hostname `denyra.denis.my.id`;
- the common security snippet;
- gzip and Zstandard response encoding;
- upstream `navidrome:4533`;
- active health probe against `/ping` with bounded timeout and consecutive success/failure thresholds.

Future site files choose their own upstream port, health URI, timeouts, encoding, and application-specific headers. Shared behavior belongs in a named snippet only when it is safe for every consumer. Backend-specific behavior is never hidden in the global file.

HTTP access logging is disabled by omission. OpenSubsonic permits credentials or authentication tokens in query parameters, so recording complete request URIs would create an avoidable credential leak. Caddy writes structured operational logs to stdout for Docker collection and rotation.

## Network architecture

Caddy joins two classes of Docker network:

1. Its default Compose network provides outbound connectivity for ACME, certificate renewal, and normal container DNS.
2. One dedicated internal external bridge exists per backend, starting with `caddy_navidrome`.

The Denyra integration adds only `caddy_navidrome` to Navidrome and gives it the network alias `navidrome`. Caddy sends upstream traffic to `navidrome:4533` over plain HTTP on that private bridge.

Caddy does not join Denyra's control, acquisition, import, upload, or playback networks. Navidrome retains its existing Denyra networks and published host port `4533`. Other Denyra service names are not resolvable from Caddy.

A future backend receives a separate network such as `caddy_files`. The change adds that external network to Caddy's Compose service and attaches only the intended backend through the backend project's integration file. Backends do not share a general-purpose proxy network, which limits lateral connectivity between unrelated services.

Each backend network is created with Docker's `internal` flag. It cannot provide an alternate internet path. The network name, driver, internal flag, and existing attachments must be checked before use; an incompatible network is never silently replaced.

This design isolates Docker network membership, not all possible IP reachability. Caddy's egress network can reach the internet for ACME and may also reach addresses routed by the host, including LAN addresses or explicitly published host ports. Caddy receives no host gateway alias or route added by this deployment. Host-level egress filtering is outside scope.

Public Navidrome request flow:

```text
OpenSubsonic client
  -> denyra.denis.my.id:443
  -> router forwarding
  -> Caddy TLS termination
  -> caddy_navidrome
  -> Navidrome:4533
```

LAN clients may continue using `http://server-lan-address:4533`.

## Image and container configuration

Production uses the official Alpine image for Caddy `2.11.4` on `linux/amd64`:

```text
docker.io/library/caddy:2.11.4-alpine@sha256:5f5c8640aae01df9654968d946d8f1a56c497f1dd5c5cda4cf95ab7c14d58648
```

The digest is the official multi-platform manifest resolved on 2026-08-25. Compose also declares `platform: linux/amd64`. There is no floating runtime tag, Watchtower, or automatic image update.

Caddy publishes TCP port 80, TCP port 443, and UDP port 443. UDP 443 permits HTTP/3; HTTPS over HTTP/1.1 and HTTP/2 remains valid when a router forwards only TCP 443.

The Compose project has the fixed name `caddy`. The container uses `restart: unless-stopped`, a read-only root filesystem, a bounded temporary filesystem at `/tmp`, `no-new-privileges`, dropped Linux capabilities, and only `NET_BIND_SERVICE` added back. The complete `config/` directory is mounted read-only at `/etc/caddy`; the repository's `.git`, scripts, README, and integration files are not visible inside the container.

Named volumes with explicit names `caddy_data` and `caddy_config` persist `/data` and `/config`, including the ACME account, certificates, renewal state, and Caddy autosave state. Docker rotates operational logs with a maximum of three 10 MiB files.

The health check reads Caddy's loopback administration endpoint from inside the container. Port `2019` is never published or bound to a non-loopback interface. The container has no Docker socket, Docker API access, writable host bind mount, application secret, or host gateway alias.

## Validation and reload behavior

`scripts/validate.sh` fails closed. It:

1. confirms the pinned image contains `linux/amd64` and reports Caddy `2.11.4`;
2. renders the Caddy Compose model;
3. validates the complete imported Caddy configuration using the pinned image;
4. renders each tracked integration model against its owning Compose project when that project is present on the production host;
5. confirms no HTTP access-log directive is enabled.

The script never prints environment files, credentials, certificate data, or private keys.

`scripts/reload.sh` runs `validate.sh` before calling `caddy reload` inside the running container. Caddy applies a valid configuration atomically. If validation or reload fails, the existing active configuration keeps serving traffic and the script exits nonzero.

A site-only change can use reload when Caddy already has the required backend network. Adding or removing a backend network requires `docker compose up -d --wait` so Docker can recreate Caddy with the new attachment. Named volumes remain intact during that recreation.

Each backend owns its active health check. An unhealthy backend produces an upstream availability response only for its own site; Caddy does not route the request to another service. One site's backend failure must not stop Caddy or unrelated sites.

## Git workflow

The production directory is initialized as a standalone Git repository before the service starts. The initial commit contains only portable configuration, integration files, scripts, `.gitignore`, and the runbook. It excludes:

- `denyra-compose-override.previous`;
- timestamped Denyra environment backups;
- ACME data and certificates, which live only in Docker volumes;
- generated validation or acceptance evidence;
- editor and operating-system temporary files.

Every later site, network, image, or policy change follows this sequence:

1. make the smallest coherent edit;
2. run repository validation;
3. review the diff for hostnames, ports, networks, logging, and accidental secrets;
4. commit the change locally;
5. activate it through reload or Compose reconciliation;
6. run site-specific and shared acceptance checks.

Rollback uses `git revert` to create an auditable inverse commit, followed by validation and reload or Compose reconciliation. It does not use `git reset`, delete certificate volumes, or overwrite committed files manually. The user will add the remote and push separately after reviewing the repository.

## Denyra integration

`/home/nirwana/caddy/integrations/denyra.compose.yml` declares `caddy_navidrome` as an external network and adds it only to Navidrome. Existing Denyra networks and host port `4533` remain unchanged.

`DENYRA_COMPOSE_OVERRIDE` in `/srv/denyra/config/denyra.env` is set to the absolute integration path. Denyra's supported `setup`, `start`, `restart`, `update`, rollback, status, and snapshot commands will render the same network attachment after future container recreation.

The environment file is updated atomically after a timestamped backup is created. The prior value is recorded separately in the ignored `denyra-compose-override.previous` file. Rollback restores that one setting without replacing the environment file or overwriting later unrelated changes. The merged Denyra Compose model must validate before Navidrome is recreated.

## Adding a future service

A future service is added only after its backend owner, hostname, DNS, upstream port, health behavior, and authentication boundary are known. The operator then:

1. creates and verifies the public DNS A record, plus AAAA only when IPv6 is fully reachable;
2. creates a dedicated internal external Docker network for the backend;
3. attaches Caddy and only that backend to the network;
4. creates one focused file under `config/sites/`;
5. imports the common security snippet and defines explicit backend health behavior;
6. validates the complete repository and owning backend Compose model;
7. commits the change;
8. reconciles Caddy if network membership changed, otherwise reloads atomically;
9. verifies the certificate, redirects, headers, backend behavior, logs, and network isolation.

An apex domain and direct subdomain may share a site file when they intentionally serve the same backend. Different services receive separate site files and networks even when their hostnames share a registrable domain.

## Deployment sequence

1. Confirm ports 80 and 443 are free, production is healthy, the A record resolves as expected, and no AAAA record exists for the current hostname.
2. Initialize the standalone Git repository and create the modular files with restrictive permissions.
3. Validate the repository before creating or changing runtime state.
4. Create `caddy_navidrome` as an internal bridge after confirming no incompatible network exists.
5. Validate both Compose models and the complete imported Caddy configuration with the pinned image.
6. Back up `/srv/denyra/config/denyra.env`, record the prior override value in the ignored rollback file, and atomically set `DENYRA_COMPOSE_OVERRIDE`.
7. From `/home/nirwana/pribadi/denyra`, run `./denyra start` and wait for all Denyra services to become healthy. Compare container IDs before and after; only Navidrome may be recreated.
8. Commit the standalone Caddy repository before starting its service.
9. From `/home/nirwana/caddy`, run `docker compose --project-name caddy up -d --wait`.
10. Verify public HTTPS, direct LAN access, external port boundaries, Docker isolation, credential-safe logs, persistence, and rollback.

If certificate issuance fails, Caddy remains running and retries according to its automatic HTTPS policy. The deployment is not complete until a public client validates the certificate chain and reaches Navidrome through HTTPS.

## Verification

Pre-deployment checks:

- the standalone Git repository contains no tracked secret, rollback value, environment backup, certificate data, or generated evidence;
- `scripts/validate.sh` succeeds;
- the pinned image resolves to Caddy `2.11.4` for `linux/amd64`;
- the root Caddyfile imports both snippets and sites;
- a validation-only fixture proves the configuration pattern accepts `example.com`, `abc.example.com`, and an unrelated domain without starting a server or requesting certificates;
- the merged Denyra model with `integrations/denyra.compose.yml` validates;
- no service already listens on TCP 80, TCP 443, or UDP 443;
- `/ping` returns HTTP 200 directly from Navidrome;
- `caddy_navidrome` uses driver `bridge`, has `Internal=true`, and has no unexpected attachment.

Runtime checks:

- Caddy and all Denyra containers are running and healthy;
- `http://denyra.denis.my.id` redirects to `https://denyra.denis.my.id`;
- DNS still has the expected A record and no AAAA record;
- the public certificate SAN contains `denyra.denis.my.id`, its chain is trusted, and its validity window is current;
- `https://denyra.denis.my.id/ping` reaches Navidrome;
- Navidrome's web UI loads through HTTPS;
- response headers include HSTS, `nosniff`, and the referrer policy without a `Server` header;
- TCP 4533 remains reachable directly on the LAN;
- an external probe outside the production LAN cannot connect to public TCP port 4533;
- Caddy has no Docker socket mount and `caddy_navidrome` contains only Caddy and Navidrome;
- Caddy cannot resolve the Docker names of Lidarr, pipeline, gateway, slskd, or SFTPGo;
- Caddy's administration port is not published;
- recent Caddy and Navidrome logs contain no certificate, proxy, or restart-loop errors;
- a synthetic request containing a unique fake authentication marker does not place that marker in Caddy logs;
- `docker compose --project-name caddy down` followed by `up -d --wait`, without `-v`, retains the same named volumes and working certificate;
- a `git revert` rehearsal preserves existing certificate state and restores the previously committed configuration;
- repository status is clean after the accepted deployment.

An OpenSubsonic client is configured with `https://denyra.denis.my.id` and tested with authentication, browsing, artwork, seeking, and playback. The test must not print credentials in shell history or logs.

## Rollback

Initial Navidrome integration rollback keeps certificate state intact:

1. From `/home/nirwana/caddy`, run `docker compose --project-name caddy down` without `-v`.
2. Atomically restore only the captured prior value of `DENYRA_COMPOSE_OVERRIDE`; do not replace the whole environment file.
3. From `/home/nirwana/pribadi/denyra`, run `./denyra start` and wait for health checks.
4. Confirm direct LAN access on port `4533`.
5. Confirm `caddy_navidrome` has no attachment.
6. Retain the standalone repository, named volumes, and configuration for diagnosis or a later retry.

Rollback of a later committed configuration change uses `git revert`, validation, and either atomic reload or Compose reconciliation according to whether network membership changed. A failed site addition is never repaired by deleting shared certificate volumes or resetting Git history.

The dedicated backend network may be removed only after Caddy and the backend have detached and only when no retry is planned.

## Acceptance criteria

The change is accepted when:

- public clients reach Navidrome through a trusted HTTPS endpoint on `denyra.denis.my.id`, while an external probe confirms public TCP port `4533` is closed;
- LAN clients retain direct HTTP access on port `4533`;
- the standalone Caddy Git repository is clean, contains no secret or host-specific rollback state, and is ready for the user to add a remote and push;
- the modular configuration supports independent apex domains, direct subdomains, and different registrable domains without nested service hostnames;
- configuration for one service can be changed and validated without editing unrelated site files;
- Caddy shares a dedicated upstream Docker network only with Navidrome and cannot resolve other Denyra service names;
- certificate and Caddy state survive container recreation and Git-based rollback;
- Denyra updates preserve the Navidrome network attachment;
- configuration validation, reload, adding a service, image update, and rollback commands are documented and tested;
- no credential, Docker socket, control network, writable host bind mount, or administration endpoint is exposed by the Caddy deployment.
