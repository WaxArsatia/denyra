#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_root"

tracked=$(mktemp)
trap 'rm -f "$tracked"' EXIT HUP INT TERM
git ls-files -co --exclude-standard >"$tracked"

retired=$(printf '%s%s' 'deni' 'sonic')
if xargs -r rg -n -i "$retired" <"$tracked" \
    | rg -v '^docs/superpowers/(plans|specs)/' \
    | rg -v '^scripts/check-clean-tree\.sh:'; then
    echo "retired product identifier found" >&2
    exit 1
fi

if xargs -r rg -n 'TODO|FIXME|TBD|panic\("(not implemented|unimplemented)' <"$tracked" \
    | rg -v '^docs/superpowers/(plans|specs)/' \
    | rg -v '^scripts/check-clean-tree\.sh:'; then
    echo "unfinished implementation marker found" >&2
    exit 1
fi

selectors=$(mktemp)
trap 'rm -f "$tracked" "$selectors"' EXIT HUP INT TERM
rg '\.(ya?ml|json|toml|Dockerfile)$|(^|/)Dockerfile$|go\.mod$|requirements.*\.lock$' "$tracked" >"$selectors" || true
if xargs -r rg -n '(:latest|@latest|:[[:space:]]*(main|master)[[:space:]]*$|reference[[:space:]]*=[[:space:]]*"[^"]*:(nightly|latest)")' <"$selectors" \
    | rg -v '^scripts/check-clean-tree\.sh:'; then
    echo "floating dependency selector found" >&2
    exit 1
fi
