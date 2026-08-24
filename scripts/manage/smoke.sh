#!/bin/sh

denyra_smoke() {
  denyra_smoke_snapshot=${1:-}
  if [ -n "$denyra_smoke_snapshot" ]; then
    denyra_smoke_compose() { denyra_compose_snapshot "$denyra_smoke_snapshot" "$@"; }
  else
    denyra_smoke_compose() { denyra_compose "$@"; }
  fi
  denyra_smoke_compose ps
  denyra_smoke_compose exec -T acquisition-gateway /app/acquisition-gateway healthcheck --address 127.0.0.1:8081 >/dev/null
  denyra_smoke_compose exec -T media-pipeline /app/media-pipeline healthcheck --address 127.0.0.1:8081 >/dev/null
  denyra_smoke_compose exec -T media-pipeline /bin/sh -ec 'for path in /data/library /data/library-unmanaged /data/incoming /data/processing /data/quarantine; do test -d "$path"; done'
  denyra_smoke_compose exec -T lidarr curl -fsS http://127.0.0.1:8686/ping >/dev/null
  denyra_smoke_compose exec -T slskd wget -q -O /dev/null http://127.0.0.1:5030/health
  denyra_smoke_compose exec -T sftpgo sftpgo ping >/dev/null
  denyra_smoke_compose exec -T navidrome /app/navidrome --version >/dev/null

  denyra_smoke_host=${DENYRA_DISPLAY_HOST:-localhost}
  printf 'Denyra:      http://%s:8090\n' "$denyra_smoke_host"
  printf 'Navidrome:   http://%s:4533\n' "$denyra_smoke_host"
  printf 'SFTPGo:      http://%s:8080\n' "$denyra_smoke_host"
  printf 'SFTP:        %s:2022\n' "$denyra_smoke_host"
  printf 'Credentials: ./denyra credentials\n'
}
