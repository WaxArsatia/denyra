#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
views="$root/internal/pipeline/adminui/views"
assets="$root/internal/pipeline/adminui/assets"

if grep -R -n -E '<(script|style)[^>]*>[^<]+' "$views" --include='*.templ'; then
	echo "inline script/style is forbidden" >&2
	exit 1
fi
if grep -R -n -E 'hx-on|js:|javascript:|https?://|allowEval(&quot;|"):true|includeIndicatorStyles(&quot;|"):true' "$views" --include='*.templ'; then
	echo "unsafe or remotely coupled UI source found" >&2
	exit 1
fi
[ "$(sha256sum "$assets/vendor/htmx.min.js" | cut -d ' ' -f 1)" = "71ea67185bfa8c98c39d31717c6fce5d852370fcdfd129db4543774d3145c0de" ]
[ "$(find "$assets/vendor/fonts" -maxdepth 1 -type f -name '*.woff2' | wc -l)" -eq 4 ]

referenced=$(sed -n 's/.*@Icon(shell, "\([^"]*\)").*/\1/p' "$views/layout.templ" | sort -u)
vendored=$(find "$assets/vendor/icons" -maxdepth 1 -type f -name '*.svg' -exec basename {} .svg \; | sort -u)
[ "$referenced" = "$vendored" ] || { echo "icon source/reference mismatch" >&2; diff -u - <<EOF || true
$referenced
EOF
exit 1; }

grep -q 'allowEval&quot;:false' "$views/layout.templ"
grep -q 'includeIndicatorStyles&quot;:false' "$views/layout.templ"
grep -q "script-src 'self'" "$root/internal/pipeline/adminui/handlers/routes.go"
grep -q 'webkitdirectory' "$views/incoming.templ"
grep -q 'webkitRelativePath' "$assets/upload.js"
grep -q 'webkitGetAsEntry' "$assets/upload.js"
grep -q '\.partial' "$assets/upload.js"
grep -q 'dataset.uploadConcurrency' "$assets/upload.js"
echo "UI source and vendored assets verified"
