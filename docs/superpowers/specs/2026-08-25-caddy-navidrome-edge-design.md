# Caddy edge proxy for Navidrome

## Purpose

Production needs a public HTTPS endpoint for Navidrome at `denyra.denis.my.id`. Desktop Linux and mobile OpenSubsonic clients will use this endpoint outside the LAN. Direct LAN access to Navidrome on host port `4533` remains available.

The deployment must terminate TLS at Caddy, preserve Denyra's existing network boundaries, and remain reproducible after a Denyra update or container recreation.

## Scope

This change creates a separate Caddy Compose project under `/home/nirwana/caddy` and connects it to Navidrome through a dedicated Docker network. It also records a production-only Denyra Compose override in `/srv/denyra/config/denyra.env` so that future Denyra operations keep Navidrome attached to that network.

The change does not:

- expose the Denyra administration UI, Lidarr, SFTPGo, slskd, or any pipeline endpoint;
- add another authentication layer in front of Navidrome;
- change Navidrome users, passwords, transcoding settings, or its public host port;
- replace the existing Cloudflare Tunnel container;
- change the public DNS record, router port forwarding, or host firewall rules;
- install a Docker socket proxy or grant Caddy access to the Docker socket.

## Current production state

The Denyra checkout is `/home/nirwana/pribadi/denyra`, and its persistent deployment root is `/srv/denyra`. Navidrome publishes TCP port `4533` on the host and is attached to Denyra's playback network. The `DENYRA_COMPOSE_OVERRIDE` value is currently empty.

The A record for `denyra.denis.my.id` resolves to `103.80.81.35`, and the name has no AAAA record. TCP ports 80 and 443 are not currently occupied on the production host. Router forwarding for ports 80 and 443 has been configured by the operator. Navidrome's unauthenticated `/ping` endpoint currently returns HTTP 200.

## Architecture

The deployment uses two Docker networks:

1. Caddy's default Compose network provides outbound connectivity for ACME and normal container DNS.
2. An external bridge network named `caddy_proxy` connects only Caddy and Navidrome. The network is created with Docker's `internal` flag so it cannot provide an alternate internet path.

Caddy does not join any Denyra control, acquisition, import, upload, or playback network. The production-only Denyra override adds `caddy_proxy` to Navidrome with the network alias `navidrome`. Caddy sends upstream traffic to `navidrome:4533` over plain HTTP on this private bridge. Other Denyra service names are not resolvable from Caddy.

This design isolates Docker network membership, not all possible IP reachability. Caddy's egress network can reach the internet for ACME and may also reach addresses routed by the host, including LAN addresses or explicitly published host ports. Caddy receives no host gateway alias or route added by this deployment. Host-level egress filtering is outside scope.

Public request flow:

```text
OpenSubsonic client
  -> denyra.denis.my.id:443
  -> router forwarding
  -> Caddy TLS termination
  -> caddy_proxy network
  -> Navidrome:4533
```

LAN clients may continue using `http://<server-lan-address>:4533`. The public endpoint is `https://denyra.denis.my.id`.

## Production files

`/home/nirwana/caddy` contains:

- `compose.yml`, which defines the Caddy container, ports, volumes, health check, security settings, and networks;
- `Caddyfile`, which defines the public site and Navidrome upstream;
- `denyra.compose.yml`, which attaches only Navidrome to `caddy_proxy`;
- `denyra-compose-override.previous`, which records only the override value that rollback must restore;
- `README.md`, which records validation, start, reload, log, update, and rollback commands.

The directory is mode `0750`. Compose and documentation files are mode `0640`. The Caddyfile is mode `0644` so the capability-restricted container can read its bind mount. No secret is stored in these files.

## Image and platform

Production uses the official Alpine image for Caddy `2.11.4` on `linux/amd64`:

```text
docker.io/library/caddy:2.11.4-alpine@sha256:5f5c8640aae01df9654968d946d8f1a56c497f1dd5c5cda4cf95ab7c14d58648
```

The digest is the official multi-platform manifest resolved on 2026-08-25. Compose also declares `platform: linux/amd64`. There is no floating runtime tag, Watchtower, or automatic image update.

An image update requires resolving a new stable version and digest, validating the Caddyfile with the candidate image, starting the candidate, and repeating the external TLS checks.

## Container configuration

Caddy publishes:

- TCP port 80 for ACME HTTP challenges and HTTP-to-HTTPS redirects;
- TCP port 443 for HTTPS over HTTP/1.1 and HTTP/2;
- UDP port 443 for HTTP/3.

The Compose project has the fixed name `caddy`. The container uses `restart: unless-stopped`, a read-only root filesystem, a temporary filesystem at `/tmp`, `no-new-privileges`, dropped Linux capabilities, and only `NET_BIND_SERVICE` added back. The Caddyfile is mounted read-only. Named volumes with the explicit names `caddy_data` and `caddy_config` persist `/data` and `/config`, including the ACME account, certificates, renewal state, and Caddy autosave state.

Docker rotates container logs with a maximum of three 10 MiB files. The health check reads Caddy's loopback-only administration endpoint from inside the container. The administration endpoint is never published or bound to a non-loopback interface.

The container has no host filesystem mounts other than the read-only Caddyfile and no access to Docker Engine APIs.

## HTTP and TLS behavior

Caddy manages certificate issuance and renewal through its standard automatic HTTPS behavior. Port 80 redirects normal traffic to HTTPS while remaining available for ACME validation. Caddy's current secure TLS defaults remain unchanged; the configuration does not hard-code protocol versions, cipher suites, or an ACME issuer.

The site adds these response headers:

- `Strict-Transport-Security: max-age=31536000`
- `X-Content-Type-Options: nosniff`
- `Referrer-Policy: strict-origin-when-cross-origin`

The `Server` response header is removed. HSTS applies only to `denyra.denis.my.id`; it does not affect direct LAN access by IP. The configuration does not add a content security policy or frame policy because either could break Navidrome's web UI or client integrations without application-specific testing.

Caddy enables gzip and Zstandard response encoding. Already-compressed audio is not transformed. `reverse_proxy` preserves streaming and WebSocket behavior and supplies Caddy's standard forwarding headers. No custom `Host`, `X-Forwarded-For`, or buffering override is needed.

The Navidrome upstream has an active health probe against `/ping`, with a bounded timeout and consecutive-failure threshold. During a Navidrome restart, Caddy returns an upstream availability error instead of routing to another Denyra service.

## Logging and credential safety

Caddy writes structured operational logs to stdout for Docker collection and rotation. HTTP access logging is disabled. OpenSubsonic permits credentials or authentication tokens in query parameters, so recording complete request URIs would create an avoidable credential leak. Troubleshooting may temporarily enable a redacted access log only if its query-filter behavior is tested first.

No log command, verification command, or README example prints Navidrome credentials, ACME account material, private keys, or full certificate storage.

## Denyra integration

`/home/nirwana/caddy/denyra.compose.yml` declares `caddy_proxy` as an external network and adds it to Navidrome. Existing Denyra networks and host port `4533` remain unchanged.

`DENYRA_COMPOSE_OVERRIDE` in `/srv/denyra/config/denyra.env` is set to the absolute override path. Denyra's supported `setup`, `start`, `restart`, `update`, rollback, status, and snapshot commands will therefore render the same network attachment after future container recreation.

The environment file is updated atomically after a timestamped backup is created. The prior `DENYRA_COMPOSE_OVERRIDE` value is also recorded separately so rollback can restore that one setting without overwriting later unrelated changes. The merged Denyra Compose model must validate before Navidrome is recreated.

## Deployment sequence

1. Confirm ports 80 and 443 are free, production is healthy, and the DNS A record resolves as expected.
2. Create the Caddy files with restrictive permissions.
3. Create the external network with `docker network create --driver bridge --internal caddy_proxy` after confirming that a network with that name does not already have incompatible settings.
4. Validate both Compose models and validate the Caddyfile with the pinned image.
5. Back up `/srv/denyra/config/denyra.env`, then atomically set `DENYRA_COMPOSE_OVERRIDE`.
6. From `/home/nirwana/pribadi/denyra`, run `./denyra start` and wait for all Denyra services to become healthy. Compare container IDs before and after; only Navidrome may be recreated.
7. From `/home/nirwana/caddy`, run `docker compose --project-name caddy up -d --wait` and wait for Caddy's health check.
8. Verify HTTP redirect, public certificate, HTTPS response, proxy health, container isolation, and direct LAN port `4533`.

If certificate issuance fails, Caddy remains running and retries according to its automatic HTTPS policy. The deployment is not considered complete until a public client validates the certificate chain and reaches Navidrome through HTTPS.

UDP port 443 forwarding is required for HTTP/3. If the router forwards only TCP 443, HTTP/1.1 and HTTP/2 remain valid and HTTP/3 is treated as unavailable rather than a failed HTTPS deployment.

## Verification

Pre-deployment checks:

- `docker compose --project-name caddy config --quiet` succeeds from `/home/nirwana/caddy`;
- `docker compose --project-name denyra --env-file /srv/denyra/config/denyra.env -f deploy/compose.yaml -f /home/nirwana/caddy/denyra.compose.yml config --quiet` succeeds from the Denyra checkout;
- `docker compose --project-name caddy run --rm --no-deps --entrypoint caddy caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile` accepts the Caddyfile using the pinned image;
- the pinned image resolves to Caddy `2.11.4` for `linux/amd64`;
- no service already listens on TCP 80, TCP 443, or UDP 443;
- `/ping` returns HTTP 200 directly from Navidrome before active health checks are enabled;
- `caddy_proxy` has driver `bridge` and `Internal=true` before either service attaches.

Runtime checks:

- Caddy and all Denyra containers are running and healthy;
- `http://denyra.denis.my.id` redirects to `https://denyra.denis.my.id`;
- DNS still has the expected A record and no AAAA record;
- the public certificate SAN contains `denyra.denis.my.id`, the chain is trusted, and its validity window is current;
- `https://denyra.denis.my.id/ping` reaches Navidrome;
- Navidrome's web UI loads through HTTPS;
- response headers include HSTS, `nosniff`, and the referrer policy without a `Server` header;
- TCP 4533 remains reachable directly on the LAN;
- an external probe outside the production LAN cannot connect to public TCP port 4533;
- Caddy has no Docker socket mount and `caddy_proxy` contains only Caddy and Navidrome;
- Caddy cannot resolve the Docker names of Lidarr, pipeline, gateway, slskd, or SFTPGo;
- Caddy's administration port is not published;
- recent Caddy and Navidrome logs contain no certificate, proxy, or restart-loop errors;
- a synthetic request containing a unique fake authentication marker does not place that marker in Caddy logs;
- `docker compose --project-name caddy down` followed by `up -d --wait`, without `-v`, retains the same named volumes and a working certificate.

An OpenSubsonic client should be configured with `https://denyra.denis.my.id` and tested with authentication, browsing, artwork, seeking, and playback. The test must not print credentials in shell history or logs.

## Rollback

Rollback keeps certificate state intact so a later retry does not create unnecessary ACME orders:

1. From `/home/nirwana/caddy`, run `docker compose --project-name caddy down` without `-v`.
2. Atomically restore only the captured prior value of `DENYRA_COMPOSE_OVERRIDE`; do not replace the whole environment file.
3. From `/home/nirwana/pribadi/denyra`, run `./denyra start` and wait for health checks.
4. Confirm direct LAN access on port `4533`.
5. Remove `caddy_proxy` only after both containers have detached and only if the deployment will not be retried.

Rollback does not remove Navidrome state, users, media, playlists, Caddy certificate volumes, or the production configuration files.

## Acceptance criteria

The change is accepted when:

- public clients reach Navidrome through a trusted HTTPS endpoint on `denyra.denis.my.id`, while an external probe confirms public TCP port `4533` is closed;
- LAN clients retain direct HTTP access on port `4533`;
- Caddy shares an upstream Docker network only with Navidrome and cannot resolve other Denyra service names;
- certificate and Caddy state survive container recreation;
- Denyra updates preserve the Navidrome network attachment;
- configuration validation and rollback commands are documented and tested;
- no credential, Docker socket, control network, or administration endpoint is exposed by the Caddy deployment.
