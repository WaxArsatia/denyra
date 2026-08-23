#!/bin/sh
set -eu
repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
runbook_root="$repo_root/docs/runbooks"

required="install.md backup.md restore.md upgrade.md incidents.md clients.md security-boundary.md"
for name in $required; do
  [ -s "$runbook_root/$name" ] || { echo "missing runbook: $name" >&2; exit 1; }
done

if grep -Rin 'denisonic' "$runbook_root"; then
  echo "old product name found in runbooks" >&2
  exit 1
fi
if grep -REn ':[[:space:]]*latest([@[:space:]]|$)|:nightly([[:space:]`]|$)' "$runbook_root"; then
  echo "floating container tag found in runbooks" >&2
  exit 1
fi
if grep -REn 'Bearer[[:space:]]+[A-Za-z0-9._-]{16,}|(api[_-]?key|password)=[A-Za-z0-9._-]{16,}' "$runbook_root"; then
  echo "credential-like literal found in runbooks" >&2
  exit 1
fi

references=$(grep -RhoE '(scripts|deploy|docs)/[A-Za-z0-9_./-]+|dependencies\.lock\.json' "$runbook_root" | sort -u)
for reference in $references; do
  clean=${reference%[.,:;]}
  [ -e "$repo_root/$clean" ] || { echo "runbook reference does not exist: $clean" >&2; exit 1; }
  case "$clean" in
    scripts/*.sh) [ -x "$repo_root/$clean" ] || { echo "referenced script is not executable: $clean" >&2; exit 1; } ;;
  esac
done

grep -q '0.0.0.0:8090' "$runbook_root/security-boundary.md"
grep -q 'accepted security risk' "$runbook_root/security-boundary.md"
grep -q 'Feishin 1.15.1' "$runbook_root/clients.md"
grep -q 'Tempus 4.25.0' "$runbook_root/clients.md"
grep -q 'opus-256' "$runbook_root/clients.md"
grep -q 'opus-160' "$runbook_root/clients.md"
grep -q 'restore drill' "$runbook_root/backup.md"

echo "runbooks verified"
