# Playback clients

Navidrome provides the OpenSubsonic endpoint. Feishin, Tempus, and other clients use Navidrome music accounts, not the Denyra administrator account.

## Local machine

Use these addresses:

- Denyra: `http://localhost:8090`
- Feishin or Navidrome Web UI: `http://localhost:4533`
- SFTPGo WebAdmin: `http://localhost:8080`
- SFTP upload: `localhost:2022`

In Feishin, add a Navidrome or OpenSubsonic server with `http://localhost:4533`, then sign in with the Navidrome account shown by `./denyra credentials`. Use Feishin's music-folder or library filter to show Managed, Unmanaged, or both.

## Private server

Replace `localhost` with the server's LAN address, for example `http://server-lan-address:4533`. Do not enter a Compose service name or container network address in a client running on another machine.

Use original quality on a fast LAN when you want the FLAC master. Lower bitrate or server transcoding remains a client preference and does not modify the master. Synchronized lyrics use the `.lrc` sidecar associated with each imported track. Album and artist artwork come from the files and metadata visible in the Navidrome library.

For remote playback, `opus-256` is the higher-quality preset and `opus-160` uses less bandwidth. Both leave the source FLAC unchanged.

If artwork or lyrics are missing, check the final release directory first. Lidarr owns Managed files. Denyra owns Unmanaged files. Run a Navidrome rescan after confirming the relevant cover and lyric sidecars exist.
