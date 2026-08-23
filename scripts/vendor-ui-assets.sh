#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

download() {
	url=$1
	output=$2
	expected=$3
	curl --fail --location --silent --show-error "$url" --output "$output"
	actual=$(sha256sum "$output" | cut -d ' ' -f 1)
	[ "$actual" = "$expected" ] || {
		echo "checksum mismatch for $output: $actual" >&2
		exit 1
	}
}

download "https://registry.npmjs.org/htmx.org/-/htmx.org-2.0.10.tgz" "$tmp/htmx.tgz" "577ad40c1c94c9de47edb89e0aec78a8353d36024c50017eb53e02992a55e889"
download "https://github.com/vercel/geist-font/releases/download/v1.7.2/geist-font-v1.7.2.zip" "$tmp/geist.zip" "7fc800d2ac6b92844895196e5041aca55d814c15db70c44f79b3b83ab82b04e2"
download "https://registry.npmjs.org/@phosphor-icons/core/-/core-2.0.8.tgz" "$tmp/phosphor.tgz" "c4d7eca2a776229c2e33c6749e09dbea32f5f3a83171c7502b3bc52f887a3551"

assets="$root/internal/pipeline/adminui/assets/vendor"
mkdir -p "$assets/fonts" "$assets/icons"
find "$assets/icons" -maxdepth 1 -type f -name '*.svg' -delete
tar -xOzf "$tmp/htmx.tgz" package/dist/htmx.min.js > "$assets/htmx.min.js"
[ "$(sha256sum "$assets/htmx.min.js" | cut -d ' ' -f 1)" = "71ea67185bfa8c98c39d31717c6fce5d852370fcdfd129db4543774d3145c0de" ]

unzip -p "$tmp/geist.zip" geist-font/Geist/webfonts/Geist-Regular.woff2 > "$assets/fonts/geist-regular.woff2"
unzip -p "$tmp/geist.zip" geist-font/Geist/webfonts/Geist-Medium.woff2 > "$assets/fonts/geist-medium.woff2"
unzip -p "$tmp/geist.zip" geist-font/Geist/webfonts/Geist-SemiBold.woff2 > "$assets/fonts/geist-semibold.woff2"
unzip -p "$tmp/geist.zip" geist-font/GeistMono/webfonts/GeistMono-Regular.woff2 > "$assets/fonts/geist-mono-regular.woff2"

for icon in tray warning-circle clock-counter-clockwise users-three shield-check; do
	tar -xOzf "$tmp/phosphor.tgz" "package/assets/regular/$icon.svg" > "$assets/icons/$icon.svg"
done

echo "verified and vendored pinned UI assets"
