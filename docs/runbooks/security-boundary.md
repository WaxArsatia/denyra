# Security boundary

The Denyra Admin UI intentionally uses internal HTTP, binds to `0.0.0.0:8090`, and does not set the cookie `Secure` attribute. This is an accepted security risk. The session cookie remains `HttpOnly`, `SameSite=Strict`, and `Path=/`; mutations require CSRF protection and authentication. The deployment and firewall must restrict who can reach port `8090`. Public exposure, TLS termination, reverse proxies, DNS, and firewall design are outside this system.

Gateway and pipeline JSON APIs use a shared bearer secret from a secret file, constant-time comparison, request IDs, idempotency keys, and explicit body limits. They share a dedicated private Compose network that no other container joins. The Admin JSON API is not an external interface; HTMX endpoints stay inside the same admin authentication boundary.

Navidrome music accounts, SFTPGo upload accounts, and Denyra admin accounts are separate. Navidrome mounts `/data/library` read-only. SFTPGo can write only manual incoming files. Downloaders write their own download roots. Pipeline can claim completed downloads and mutate staging candidates, while only Lidarr can rename, move, or import into the final library.

Provider credentials are not passed to SpotiFLAC. Runtime extension installation, registry updates, package installation, and dependency updates are disabled. Every deployment dependency changes through an explicit reviewed lock update and compatibility tests.

Secrets never enter TOML, configuration snapshots, logs, audit events, image layers, or source control. Audit records may store the secret source name and an HMAC fingerprint made with a separate audit key. They never store the secret value or a plain hash of a low-entropy secret.
