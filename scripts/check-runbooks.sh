#!/bin/sh
set -eu
repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
runbook_root="$repo_root/docs/runbooks"

required="install.md upgrade.md incidents.md clients.md security-boundary.md acceptance-evidence.md"
for name in $required; do
  [ -s "$runbook_root/$name" ] || { echo "missing runbook: $name" >&2; exit 1; }
done

if grep -Rin 'denisonic' "$runbook_root"; then
  echo "old product name found in runbooks" >&2
  exit 1
fi

operator_docs="$repo_root/README.md $runbook_root/install.md $runbook_root/upgrade.md"
for command in './denyra setup' './denyra update' './denyra credentials' './denyra cleanup legacy-lifecycle'; do
  grep -Fq "$command" $operator_docs || { echo "supported command missing from operator docs: $command" >&2; exit 1; }
done

current_surfaces="$repo_root/README.md $runbook_root $repo_root/denyra $repo_root/scripts/manage $repo_root/deploy/compose.yaml"
for forbidden in './denyra backup' './denyra restore' './denyra rollback' 'prior-images.yaml' 'prior-compose.yaml' 'automatic rollback' 'DENYRA_RESTIC_' 'restic/restic:'; do
  if grep -RFqn "$forbidden" $current_surfaces; then
    echo "retired lifecycle surface found: $forbidden" >&2
    exit 1
  fi
done

for removed in "$runbook_root/backup.md" "$runbook_root/restore.md"; do
  [ ! -e "$removed" ] || { echo "retired runbook remains: $removed" >&2; exit 1; }
done

obsolete_dependency='dependencies''.lock.json'
obsolete_images='images''.lock.json'
obsolete_pin_check='verify''-pins'
obsolete_upgrade='DENYRA''_UPGRADE_'
obsolete_address='172''.30.0.'
obsolete_bake='docker buildx ''bake'
for forbidden in "$obsolete_dependency" "$obsolete_images" "$obsolete_pin_check" "$obsolete_bake" "$obsolete_upgrade" "$obsolete_address"; do
  if grep -RFqn "$forbidden" "$repo_root/README.md" "$runbook_root"; then
    echo "obsolete operator guidance found: $forbidden" >&2
    exit 1
  fi
done
if grep -REin 'install (CPython|Python|Node(\.js)?|Go)|build (CPython|Python|Node(\.js)?|Go) from source|manual provenance|update-approval|first-run WebAdmin|create (the )?(first )?(Navidrome|SFTPGo) (admin|user)' "$repo_root/README.md" "$runbook_root"; then
  echo "manual deployment setup found in operator docs" >&2
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

grep -q '0.0.0.0:4003' "$runbook_root/security-boundary.md"
grep -q 'accepted security risk' "$runbook_root/security-boundary.md"
grep -q 'http://localhost:4000' "$runbook_root/clients.md"
grep -q 'http://localhost:4001' "$runbook_root/clients.md"
grep -q 'http://localhost:4002' "$runbook_root/clients.md"
grep -q 'http://localhost:4003' "$runbook_root/clients.md"
grep -q 'http://localhost:4004' "$runbook_root/clients.md"
grep -q 'localhost:4005' "$runbook_root/clients.md"
grep -q '50300/TCP' "$runbook_root/security-boundary.md"
grep -q 'opus-256' "$runbook_root/clients.md"
grep -q 'opus-160' "$runbook_root/clients.md"
grep -qi 'Check selected' "$repo_root/README.md" "$runbook_root/install.md"
grep -qi 'Confirm selected migrations' "$repo_root/README.md" "$runbook_root/install.md"
grep -qi 'Managed.*Unmanaged' "$runbook_root/clients.md"

echo "runbooks verified"
