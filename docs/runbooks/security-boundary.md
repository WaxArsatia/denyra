# Security boundary

Denyra is designed for a local machine or private server. Its basic deployment boundary is:

- password-protected Denyra, Navidrome, SFTPGo, and slskd accounts
- private Compose networks between services
- secrets stored under the external deployment root, outside Git and image layers
- a read-only Navidrome library mount
- separate write roots for downloads, uploads, processing, and the final Lidarr library

The Denyra Admin UI uses HTTP and binds to `0.0.0.0:8090`. This is an accepted security risk for the intended private deployment. Sessions remain `HttpOnly` and `SameSite=Strict`, and mutations require authentication and CSRF protection.

The repository does not configure TLS, a reverse proxy, host firewall, DNS, VPN access, or public exposure. Add those controls outside Denyra before allowing access from an untrusted network.

Navidrome music users, SFTPGo upload users, and Denyra administrators are separate accounts. Do not reuse passwords. SpotiFLAC does not receive personal streaming-provider credentials.

Upstream services follow supported active release lines instead of promising exact build provenance for every release. Update rollback still records the actual running Docker image IDs so it can restore the previous local deployment exactly while those images remain present.

Secrets must not be committed, copied into config files, added to logs, or pasted into support messages. `./denyra credentials` reads them locally when an operator needs the generated accounts.
