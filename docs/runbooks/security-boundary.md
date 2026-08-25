# Security boundary

Denyra is designed for a local machine or private server. Its basic deployment boundary is:

- password-protected Denyra, Navidrome, SFTPGo, and slskd accounts
- private Compose networks between services
- secrets stored under the external deployment root, outside Git and image layers
- a read-only Navidrome library mount
- separate write roots for downloads, uploads, processing, and the final Lidarr library

Docker publishes Navidrome, Lidarr, slskd, Denyra, SFTPGo WebAdmin, and SFTP on all host interfaces at ports `4000` through `4005`. The Denyra Admin UI therefore uses HTTP at `0.0.0.0:4003` on IPv4 and the equivalent all-interface binding on IPv6. This is an accepted security risk for the intended private deployment. Sessions remain `HttpOnly` and `SameSite=Strict`, and mutations require authentication and CSRF protection.

Soulseek peer traffic uses public `50300/TCP`. Docker publishes it on all host interfaces, but the operator must separately allow it through any host firewall and forward TCP port `50300` from the router to the server. Do not forward the administrative ports `4001` through `4004` to an untrusted network.

The repository does not configure TLS, a reverse proxy, host firewall, DNS, VPN access, or public exposure. Add those controls outside Denyra before allowing access from an untrusted network.

Navidrome music users, SFTPGo upload users, and Denyra administrators are separate accounts. Do not reuse passwords. SpotiFLAC does not receive personal streaming-provider credentials.

Upstream services follow supported active release lines instead of promising exact build provenance for every release. Updates build before cutover and move forward after activation. There is no operator rollback, snapshot, or restore path.

Secrets must not be committed, copied into config files, added to logs, or pasted into support messages. `./denyra credentials` reads them locally when an operator needs the generated accounts.
